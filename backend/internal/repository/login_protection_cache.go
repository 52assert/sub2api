package repository

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	loginFailureKeyPrefix = "auth:login_protection:failures:"
	loginBanKeyPrefix     = "auth:login_protection:ban:"
	loginBanIndexKey      = "auth:login_protection:bans"
)

// LoginProtectionCache shares counters and permanent bans across all instances
// and login methods. Redis failures are returned to callers so they can fail closed.
type LoginProtectionCache struct {
	client *redis.Client
}

func NewLoginProtectionCache(client *redis.Client) service.LoginProtectionStore {
	return &LoginProtectionCache{client: client}
}

// Every read of a ban validates its data instead of treating a damaged record as
// an unblocked IP. The sorted set supports pagination without scanning Redis keys.
const loginProtectionReadBanLua = `
local function readBan(key, index, ip)
    local fields = redis.call('HMGET', key, 'failure_count', 'blocked_at')
    if not fields[1] and not fields[2] then
        if redis.call('EXISTS', key) == 1 then
            error('invalid login protection ban')
        end
        redis.call('ZREM', index, ip)
        return nil
    end
    local count = tonumber(fields[1])
    local blockedAt = tonumber(fields[2])
    if not count or count ~= 3 or not blockedAt or blockedAt <= 0 then
        error('invalid login protection ban')
    end
    return {count, blockedAt}
end
`

var loginProtectionCheckScript = redis.NewScript(loginProtectionReadBanLua + `
local ban = readBan(KEYS[1], KEYS[2], ARGV[1])
if ban then return ban end
return {0, 0}
`)

var loginProtectionFailureScript = redis.NewScript(loginProtectionReadBanLua + `
local ban = readBan(KEYS[2], KEYS[3], ARGV[1])
if ban then return ban end

local count = redis.call('INCR', KEYS[1])
if count == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[3]) end
if count < tonumber(ARGV[2]) then return {count, 0} end

local clock = redis.call('TIME')
local blockedAt = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
redis.call('HSET', KEYS[2], 'failure_count', count, 'blocked_at', blockedAt)
redis.call('ZADD', KEYS[3], blockedAt, ARGV[1])
redis.call('DEL', KEYS[1])
return {count, blockedAt}
`)

var loginProtectionSuccessScript = redis.NewScript(`
-- A login that started before the third failure must not remove a new ban.
if redis.call('EXISTS', KEYS[2]) == 0 then
    redis.call('DEL', KEYS[1])
end
return 1
`)

var loginProtectionListScript = redis.NewScript(loginProtectionReadBanLua + `
local offset = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local items = {}
while #items < limit * 3 do
    local remaining = limit - #items / 3
    local ips = redis.call('ZREVRANGE', KEYS[1], offset, offset + remaining - 1)
    if #ips == 0 then break end
    for _, ip in ipairs(ips) do
        local ban = readBan(ARGV[3] .. ip, KEYS[1], ip)
        if ban then
            table.insert(items, ip)
            table.insert(items, ban[1])
            table.insert(items, ban[2])
            offset = offset + 1
        end
    end
end
local result = {redis.call('ZCARD', KEYS[1])}
for _, value in ipairs(items) do table.insert(result, value) end
return result
`)

var loginProtectionUnbanScript = redis.NewScript(`
redis.call('DEL', KEYS[1], KEYS[2])
redis.call('ZREM', KEYS[3], ARGV[1])
return 1
`)

func (s *LoginProtectionCache) Check(ctx context.Context, ip string) (*service.LoginIPBan, error) {
	ip, err := s.normalizeIP(ip)
	if err != nil {
		return nil, err
	}
	values, err := loginProtectionCheckScript.Run(ctx, s.client, []string{loginBanKeyPrefix + ip, loginBanIndexKey}, ip).Slice()
	if err != nil {
		return nil, fmt.Errorf("check login IP ban: %w", err)
	}
	ban, _, err := parseLoginProtectionResult(ip, values)
	return ban, err
}

// RecordFailure counts failures in a fixed window starting with the first
// failure. The third failure creates a permanent ban in the same Redis operation.
func (s *LoginProtectionCache) RecordFailure(ctx context.Context, ip string) (*service.LoginIPBan, int, error) {
	ip, err := s.normalizeIP(ip)
	if err != nil {
		return nil, 0, err
	}
	values, err := loginProtectionFailureScript.Run(ctx, s.client, s.keys(ip), ip, service.LoginFailureThreshold, service.LoginFailureWindow.Milliseconds()).Slice()
	if err != nil {
		return nil, 0, fmt.Errorf("record login failure: %w", err)
	}
	return parseLoginProtectionResult(ip, values)
}

func (s *LoginProtectionCache) RecordSuccess(ctx context.Context, ip string) error {
	ip, err := s.normalizeIP(ip)
	if err != nil {
		return err
	}
	if err := loginProtectionSuccessScript.Run(ctx, s.client, s.keys(ip)[:2]).Err(); err != nil {
		return fmt.Errorf("record login success: %w", err)
	}
	return nil
}

func (s *LoginProtectionCache) ListBans(ctx context.Context, page, pageSize int) (*service.LoginIPBanList, error) {
	if err := s.checkClient(); err != nil {
		return nil, err
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	// Convert before multiplication, and reject overflow from untrusted pagination.
	offset := int64(page - 1)
	if offset > int64(^uint64(0)>>1)/int64(pageSize) {
		return nil, errors.New("login IP ban page is too large")
	}
	offset *= int64(pageSize)
	values, err := loginProtectionListScript.Run(ctx, s.client, []string{loginBanIndexKey}, offset, pageSize, loginBanKeyPrefix).Slice()
	if err != nil {
		return nil, fmt.Errorf("list login IP bans: %w", err)
	}
	if len(values) == 0 || (len(values)-1)%3 != 0 {
		return nil, errors.New("invalid login IP ban list result")
	}
	total, err := loginProtectionInt(values[0])
	if err != nil {
		return nil, err
	}
	result := &service.LoginIPBanList{
		Items:                make([]*service.LoginIPBan, 0, (len(values)-1)/3),
		Total:                total,
		Page:                 page,
		PageSize:             pageSize,
		Threshold:            service.LoginFailureThreshold,
		FailureWindowSeconds: int64(service.LoginFailureWindow / time.Second),
		BanDurationSeconds:   int64(service.LoginIPBanDuration / time.Second),
	}
	for i := 1; i < len(values); i += 3 {
		ip, ok := values[i].(string)
		if !ok {
			return nil, errors.New("invalid login IP ban address result")
		}
		ban, _, err := parseLoginProtectionResult(ip, values[i+1:i+3])
		if err != nil {
			return nil, err
		}
		if ban == nil {
			return nil, errors.New("invalid login IP ban list entry")
		}
		result.Items = append(result.Items, ban)
	}
	return result, nil
}

// Unban atomically removes both the ban and any prior failure counter.
func (s *LoginProtectionCache) Unban(ctx context.Context, ip string) error {
	ip, err := s.normalizeIP(ip)
	if err != nil {
		return err
	}
	if err := loginProtectionUnbanScript.Run(ctx, s.client, s.keys(ip), ip).Err(); err != nil {
		return fmt.Errorf("unban login IP: %w", err)
	}
	return nil
}

func (s *LoginProtectionCache) checkClient() error {
	if s == nil || s.client == nil {
		return errors.New("login protection store unavailable")
	}
	return nil
}

func (s *LoginProtectionCache) normalizeIP(ip string) (string, error) {
	if err := s.checkClient(); err != nil {
		return "", err
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return "", fmt.Errorf("invalid login client IP: %w", err)
	}
	return addr.Unmap().WithZone("").String(), nil
}

func (s *LoginProtectionCache) keys(ip string) []string {
	return []string{loginFailureKeyPrefix + ip, loginBanKeyPrefix + ip, loginBanIndexKey}
}

func parseLoginProtectionResult(ip string, values []any) (*service.LoginIPBan, int, error) {
	if len(values) != 2 {
		return nil, 0, errors.New("invalid login protection result")
	}
	count, err := loginProtectionInt(values[0])
	if err != nil {
		return nil, 0, err
	}
	blockedAt, err := loginProtectionInt(values[1])
	if err != nil {
		return nil, 0, err
	}
	if count < 0 || count > service.LoginFailureThreshold || blockedAt < 0 || (blockedAt > 0 && count != service.LoginFailureThreshold) {
		return nil, 0, errors.New("invalid login protection count or timestamp")
	}
	if blockedAt == 0 {
		return nil, int(count), nil
	}
	return &service.LoginIPBan{
		ClientIP:     ip,
		FailureCount: int(count),
		BlockedAt:    time.UnixMilli(blockedAt).UTC(),
	}, int(count), nil
}

func loginProtectionInt(value any) (int64, error) {
	switch value := value.(type) {
	case int64:
		return value, nil
	case string:
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err == nil {
			return parsed, nil
		}
	}
	return 0, errors.New("invalid numeric login protection result")
}
