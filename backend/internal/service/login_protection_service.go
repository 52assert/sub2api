package service

import (
	"context"
	"errors"
	"time"
)

const (
	LoginFailureThreshold = 3
	LoginFailureWindow    = 24 * time.Hour
	// LoginIPBanDuration is zero because bans require an administrator to remove them.
	LoginIPBanDuration = time.Duration(0)
)

type LoginIPBan struct {
	ClientIP     string     `json:"client_ip"`
	FailureCount int        `json:"failure_count"`
	BlockedAt    time.Time  `json:"blocked_at"`
	BlockedUntil *time.Time `json:"blocked_until,omitempty"`
}

type LoginIPBanList struct {
	Items                []*LoginIPBan `json:"items"`
	Total                int64         `json:"total"`
	Page                 int           `json:"page"`
	PageSize             int           `json:"page_size"`
	Threshold            int           `json:"threshold"`
	FailureWindowSeconds int64         `json:"failure_window_seconds"`
	BanDurationSeconds   int64         `json:"ban_duration_seconds"`
}

// LoginProtectionStore shares login failures and bans across all server instances.
// Implementations must make threshold checks, updates, and unbanning atomic.
type LoginProtectionStore interface {
	Check(ctx context.Context, ip string) (*LoginIPBan, error)
	// RecordFailure creates a ban atomically when the failure threshold is reached.
	RecordFailure(ctx context.Context, ip string) (*LoginIPBan, int, error)
	// RecordSuccess resets failures but must preserve any existing ban.
	RecordSuccess(ctx context.Context, ip string) error
	ListBans(ctx context.Context, page, pageSize int) (*LoginIPBanList, error)
	// Unban removes both the ban and its failure counter atomically.
	Unban(ctx context.Context, ip string) error
}

// LoginProtectionService returns storage failures to callers so login can fail closed.
type LoginProtectionService struct {
	store LoginProtectionStore
}

func NewLoginProtectionService(store LoginProtectionStore) *LoginProtectionService {
	return &LoginProtectionService{store: store}
}

func (s *LoginProtectionService) Check(ctx context.Context, ip string) (*LoginIPBan, error) {
	if err := s.checkStore(); err != nil {
		return nil, err
	}
	return s.store.Check(ctx, ip)
}

func (s *LoginProtectionService) RecordFailure(ctx context.Context, ip string) (*LoginIPBan, int, error) {
	if err := s.checkStore(); err != nil {
		return nil, 0, err
	}
	return s.store.RecordFailure(ctx, ip)
}

func (s *LoginProtectionService) RecordSuccess(ctx context.Context, ip string) error {
	if err := s.checkStore(); err != nil {
		return err
	}
	return s.store.RecordSuccess(ctx, ip)
}

func (s *LoginProtectionService) ListBans(ctx context.Context, page, pageSize int) (*LoginIPBanList, error) {
	if err := s.checkStore(); err != nil {
		return nil, err
	}
	return s.store.ListBans(ctx, page, pageSize)
}

func (s *LoginProtectionService) Unban(ctx context.Context, ip string) error {
	if err := s.checkStore(); err != nil {
		return err
	}
	return s.store.Unban(ctx, ip)
}

func (s *LoginProtectionService) checkStore() error {
	if s == nil || s.store == nil {
		return errors.New("login protection store unavailable")
	}
	return nil
}
