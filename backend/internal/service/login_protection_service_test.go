//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newLoginProtectionTestService(t *testing.T) (*LoginProtectionService, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	server.SetTime(time.Date(2026, time.October, 11, 12, 0, 0, 123000000, time.UTC))
	client := redis.NewClient(&redis.Options{
		Addr:         server.Addr(),
		MaxRetries:   -1,
		DialTimeout:  100 * time.Millisecond,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})
	t.Cleanup(func() { _ = client.Close() })
	return NewLoginProtectionService(client), server
}

func requireLoginProtectionBan(t *testing.T, svc *LoginProtectionService, ip string) *LoginIPBan {
	t.Helper()
	var ban *LoginIPBan
	for i := 1; i <= LoginFailureThreshold; i++ {
		var count int
		var err error
		ban, count, err = svc.RecordFailure(context.Background(), ip)
		require.NoError(t, err)
		require.Equal(t, i, count)
	}
	require.NotNil(t, ban)
	return ban
}

func TestLoginProtection_ThirdFailurePermanentlyBansIP(t *testing.T) {
	svc, server := newLoginProtectionTestService(t)
	ctx := context.Background()
	ip := "192.0.2.1"

	ban, err := svc.Check(ctx, ip)
	require.NoError(t, err)
	require.Nil(t, ban)
	for expected := 1; expected < LoginFailureThreshold; expected++ {
		ban, count, err := svc.RecordFailure(ctx, ip)
		require.NoError(t, err)
		require.Nil(t, ban)
		require.Equal(t, expected, count)
		ban, err = svc.Check(ctx, ip)
		require.NoError(t, err)
		require.Nil(t, ban)
	}
	ban, count, err := svc.RecordFailure(ctx, ip)
	require.NoError(t, err)
	require.Equal(t, LoginFailureThreshold, count)
	require.NotNil(t, ban)
	require.Equal(t, ip, ban.ClientIP)
	require.Equal(t, LoginFailureThreshold, ban.FailureCount)
	require.Equal(t, time.Date(2026, time.October, 11, 12, 0, 0, 123000000, time.UTC), ban.BlockedAt)
	require.Nil(t, ban.BlockedUntil)

	encoded, err := json.Marshal(ban)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "blocked_until")

	// Bans remain active after the failure window and do not move on further requests.
	server.FastForward(7 * 24 * time.Hour)
	server.SetTime(ban.BlockedAt.Add(7 * 24 * time.Hour))
	checked, err := svc.Check(ctx, ip)
	require.NoError(t, err)
	require.Equal(t, ban, checked)
	repeated, count, err := svc.RecordFailure(ctx, ip)
	require.NoError(t, err)
	require.Equal(t, LoginFailureThreshold, count)
	require.Equal(t, ban, repeated)
	require.Equal(t, time.Duration(0), server.TTL(loginBanKeyPrefix+ip))

	// A second process sees the same shared ban.
	secondService := NewLoginProtectionService(svc.client)
	checked, err = secondService.Check(ctx, ip)
	require.NoError(t, err)
	require.Equal(t, ban, checked)
}

func TestLoginProtection_NormalizesIPAndSeparatesClients(t *testing.T) {
	svc, _ := newLoginProtectionTestService(t)
	ctx := context.Background()
	for _, ip := range []string{"192.0.2.1", "::ffff:192.0.2.1", " 192.0.2.1 "} {
		_, _, err := svc.RecordFailure(ctx, ip)
		require.NoError(t, err)
	}
	ban, err := svc.Check(ctx, "::ffff:c000:201")
	require.NoError(t, err)
	require.NotNil(t, ban)
	require.Equal(t, "192.0.2.1", ban.ClientIP)
	ban, count, err := svc.RecordFailure(ctx, "192.0.2.2")
	require.NoError(t, err)
	require.Nil(t, ban)
	require.Equal(t, 1, count)

	for _, ip := range []string{"2001:db8::1", "2001:0db8:0000:0000:0000:0000:0000:0001", "2001:DB8::1"} {
		_, _, err := svc.RecordFailure(ctx, ip)
		require.NoError(t, err)
	}
	ban, err = svc.Check(ctx, "2001:db8::1")
	require.NoError(t, err)
	require.NotNil(t, ban)
	require.Equal(t, "2001:db8::1", ban.ClientIP)
	ban, err = svc.Check(ctx, "2001:db8::2")
	require.NoError(t, err)
	require.Nil(t, ban)
}

func TestLoginProtection_FailureWindowDoesNotExtendAndSuccessResetsCounter(t *testing.T) {
	svc, server := newLoginProtectionTestService(t)
	ctx := context.Background()
	ip := "192.0.2.1"
	_, count, err := svc.RecordFailure(ctx, ip)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	server.FastForward(LoginFailureWindow - time.Hour)
	_, count, err = svc.RecordFailure(ctx, ip)
	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.Equal(t, time.Hour, server.TTL(loginFailureKeyPrefix+ip))
	server.FastForward(time.Hour)
	ban, count, err := svc.RecordFailure(ctx, ip)
	require.NoError(t, err)
	require.Nil(t, ban)
	require.Equal(t, 1, count)
	_, count, err = svc.RecordFailure(ctx, ip)
	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.NoError(t, svc.RecordSuccess(ctx, ip))
	ban, count, err = svc.RecordFailure(ctx, ip)
	require.NoError(t, err)
	require.Nil(t, ban)
	require.Equal(t, 1, count)
}

func TestLoginProtection_ConcurrentFailuresCreateOneBan(t *testing.T) {
	svc, _ := newLoginProtectionTestService(t)
	ctx := context.Background()
	ip := "192.0.2.1"
	const workers = 48
	type result struct {
		ban   *LoginIPBan
		count int
		err   error
	}
	results := make(chan result, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ban, count, err := svc.RecordFailure(ctx, ip)
			results <- result{ban: ban, count: count, err: err}
		}()
	}
	wg.Wait()
	close(results)
	var firstBan *LoginIPBan
	unblocked := 0
	for result := range results {
		require.NoError(t, result.err)
		if result.ban == nil {
			unblocked++
			require.Less(t, result.count, LoginFailureThreshold)
			continue
		}
		require.Equal(t, LoginFailureThreshold, result.count)
		if firstBan == nil {
			firstBan = result.ban
		}
		require.Equal(t, firstBan, result.ban)
	}
	require.Equal(t, LoginFailureThreshold-1, unblocked)
	require.NotNil(t, firstBan)
	listed, err := svc.ListBans(ctx, 1, 20)
	require.NoError(t, err)
	require.Equal(t, int64(1), listed.Total)
	require.Equal(t, []*LoginIPBan{firstBan}, listed.Items)
}

func TestLoginProtection_ActiveBanSurvivesConcurrentSuccesses(t *testing.T) {
	svc, _ := newLoginProtectionTestService(t)
	ctx := context.Background()
	ip := "192.0.2.1"
	ban := requireLoginProtectionBan(t, svc, ip)
	const workers = 32
	errors := make(chan error, workers*2)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			errors <- svc.RecordSuccess(ctx, ip)
		}()
		go func() {
			defer wg.Done()
			_, _, err := svc.RecordFailure(ctx, ip)
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	checked, err := svc.Check(ctx, ip)
	require.NoError(t, err)
	require.Equal(t, ban, checked)
}

func TestLoginProtection_SuccessRacingThirdFailureCannotRemoveBan(t *testing.T) {
	svc, _ := newLoginProtectionTestService(t)
	ctx := context.Background()
	for i := 1; i <= 24; i++ {
		ip := fmt.Sprintf("192.0.2.%d", i)
		for j := 0; j < LoginFailureThreshold-1; j++ {
			_, _, err := svc.RecordFailure(ctx, ip)
			require.NoError(t, err)
		}
		start := make(chan struct{})
		successError := make(chan error, 1)
		go func() {
			<-start
			successError <- svc.RecordSuccess(ctx, ip)
		}()
		close(start)
		ban, count, err := svc.RecordFailure(ctx, ip)
		require.NoError(t, err)
		require.NoError(t, <-successError)
		checked, err := svc.Check(ctx, ip)
		require.NoError(t, err)
		if ban != nil {
			// If the third failure won, the concurrent success preserves its ban.
			require.Equal(t, LoginFailureThreshold, count)
			require.Equal(t, ban, checked)
		} else {
			// If success won, the following failure starts a new counter.
			require.Equal(t, 1, count)
			require.Nil(t, checked)
		}
	}
}

func TestLoginProtection_ListPaginationAndUnban(t *testing.T) {
	svc, server := newLoginProtectionTestService(t)
	ctx := context.Background()
	bans := make([]*LoginIPBan, 0, 3)
	for i := 1; i <= 3; i++ {
		server.SetTime(time.Date(2026, time.October, 11, 12, 0, i, 0, time.UTC))
		bans = append(bans, requireLoginProtectionBan(t, svc, fmt.Sprintf("192.0.2.%d", i)))
	}
	listed, err := svc.ListBans(ctx, 1, 2)
	require.NoError(t, err)
	require.Equal(t, int64(3), listed.Total)
	require.Equal(t, []*LoginIPBan{bans[2], bans[1]}, listed.Items)
	require.Equal(t, LoginFailureThreshold, listed.Threshold)
	require.Equal(t, int64(86400), listed.FailureWindowSeconds)
	require.Zero(t, listed.BanDurationSeconds)
	listed, err = svc.ListBans(ctx, 2, 2)
	require.NoError(t, err)
	require.Equal(t, []*LoginIPBan{bans[0]}, listed.Items)
	listed, err = svc.ListBans(ctx, 3, 2)
	require.NoError(t, err)
	require.Empty(t, listed.Items)
	require.NotNil(t, listed.Items)
	require.Equal(t, int64(3), listed.Total)
	listed, err = svc.ListBans(ctx, 0, 0)
	require.NoError(t, err)
	require.Equal(t, 1, listed.Page)
	require.Equal(t, 20, listed.PageSize)
	listed, err = svc.ListBans(ctx, 1, 1000)
	require.NoError(t, err)
	require.Equal(t, 100, listed.PageSize)

	require.NoError(t, svc.Unban(ctx, "::ffff:192.0.2.2"))
	require.NoError(t, svc.Unban(ctx, "192.0.2.2")) // Unban is idempotent.
	checked, err := svc.Check(ctx, "192.0.2.2")
	require.NoError(t, err)
	require.Nil(t, checked)
	listed, err = svc.ListBans(ctx, 1, 20)
	require.NoError(t, err)
	require.Equal(t, int64(2), listed.Total)
	require.Equal(t, []*LoginIPBan{bans[2], bans[0]}, listed.Items)
	ban, count, err := svc.RecordFailure(ctx, "192.0.2.2")
	require.NoError(t, err)
	require.Nil(t, ban)
	require.Equal(t, 1, count)
	_, _, err = svc.RecordFailure(ctx, "192.0.2.2")
	require.NoError(t, err)
	// Administrators can also reset failures before an IP reaches the threshold.
	require.NoError(t, svc.Unban(ctx, "192.0.2.2"))
	ban, count, err = svc.RecordFailure(ctx, "192.0.2.2")
	require.NoError(t, err)
	require.Nil(t, ban)
	require.Equal(t, 1, count)
}

func TestLoginProtection_ReturnsStoreAndInputErrors(t *testing.T) {
	ctx := context.Background()
	svc, server := newLoginProtectionTestService(t)
	for _, ip := range []string{"", "bad-ip", "192.0.2.1:443"} {
		_, err := svc.Check(ctx, ip)
		require.Error(t, err)
		_, _, err = svc.RecordFailure(ctx, ip)
		require.Error(t, err)
		require.Error(t, svc.RecordSuccess(ctx, ip))
		require.Error(t, svc.Unban(ctx, ip))
	}
	require.Empty(t, server.Keys())
	for _, unavailable := range []*LoginProtectionService{nil, NewLoginProtectionService(nil)} {
		_, err := unavailable.Check(ctx, "192.0.2.1")
		require.Error(t, err)
		_, _, err = unavailable.RecordFailure(ctx, "192.0.2.1")
		require.Error(t, err)
		require.Error(t, unavailable.RecordSuccess(ctx, "192.0.2.1"))
		_, err = unavailable.ListBans(ctx, 1, 20)
		require.Error(t, err)
		require.Error(t, unavailable.Unban(ctx, "192.0.2.1"))
	}
	server.Close()
	_, err := svc.Check(ctx, "192.0.2.1")
	require.Error(t, err)
	_, _, err = svc.RecordFailure(ctx, "192.0.2.1")
	require.Error(t, err)
	require.Error(t, svc.RecordSuccess(ctx, "192.0.2.1"))
	_, err = svc.ListBans(ctx, 1, 20)
	require.Error(t, err)
	require.Error(t, svc.Unban(ctx, "192.0.2.1"))
}

func TestLoginProtection_CorruptBanFailsClosed(t *testing.T) {
	svc, server := newLoginProtectionTestService(t)
	ctx := context.Background()
	ip := "192.0.2.1"
	server.HSet(loginBanKeyPrefix+ip, "failure_count", "3")
	_, err := svc.client.ZAdd(ctx, loginBanIndexKey, redis.Z{Score: 1, Member: ip}).Result()
	require.NoError(t, err)
	_, err = svc.Check(ctx, ip)
	require.Error(t, err)
	_, _, err = svc.RecordFailure(ctx, ip)
	require.Error(t, err)
	_, err = svc.ListBans(ctx, 1, 20)
	require.Error(t, err)
	require.NoError(t, svc.RecordSuccess(ctx, ip))
	require.True(t, server.Exists(loginBanKeyPrefix+ip))
	require.NoError(t, svc.Unban(ctx, ip))
	ban, err := svc.Check(ctx, ip)
	require.NoError(t, err)
	require.Nil(t, ban)
}
