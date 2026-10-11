package service

import (
	"context"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSetSubscriptionExpiryPreservesUsageAndRestoresOnlyExpiredStatus(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	target := time.Date(2026, 11, 10, 20, 15, 30, 123000000, time.FixedZone("UTC+8", 8*60*60))
	windowStart := now.Add(-24 * time.Hour)
	for _, status := range []string{SubscriptionStatusActive, SubscriptionStatusExpired, SubscriptionStatusSuspended} {
		t.Run(status, func(t *testing.T) {
			original := UserSubscription{
				ID: 1, UserID: 2, GroupID: 3, StartsAt: now.AddDate(0, 0, -30), ExpiresAt: now.AddDate(0, 0, 60),
				Status: status, Notes: "original", DailyUsageUSD: 1, WeeklyUsageUSD: 2, MonthlyUsageUSD: 3,
				DailyWindowStart: &windowStart, WeeklyWindowStart: &windowStart, MonthlyWindowStart: &windowStart,
			}
			if status != SubscriptionStatusActive {
				original.ExpiresAt = now.Add(-time.Hour)
			}
			repo := &lockingRenewalRepo{current: original}
			cfg := &config.Config{SubscriptionCache: config.SubscriptionCacheConfig{L1Size: 100, L1TTLSeconds: 60}}
			svc := NewSubscriptionService(nil, repo, nil, nil, cfg)
			t.Cleanup(svc.Stop)
			svc.now = func() time.Time { return now }
			cacheKey := subCacheKey(original.UserID, original.GroupID)
			require.True(t, svc.subCacheL1.Set(cacheKey, &original, 1))
			svc.subCacheL1.Wait()
			_, cached := svc.subCacheL1.Get(cacheKey)
			require.True(t, cached)

			updated, err := svc.SetSubscriptionExpiry(context.Background(), original.ID, target)

			require.NoError(t, err)
			require.Equal(t, 1, repo.lockReads)
			expected := original
			expected.ExpiresAt = target.UTC()
			if status == SubscriptionStatusExpired {
				expected.Status = SubscriptionStatusActive
			}
			require.Equal(t, expected, *updated)
			svc.subCacheL1.Wait()
			_, cached = svc.subCacheL1.Get(cacheKey)
			require.False(t, cached, "absolute expiration changes must invalidate authentication caches")
		})
	}
}

func TestSetSubscriptionExpiryRejectsPastAndOutOfRangeWithoutMutation(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	original := UserSubscription{ID: 1, ExpiresAt: now.AddDate(0, 0, 30), Status: SubscriptionStatusActive}
	for _, target := range []time.Time{{}, now, now.Add(-time.Second), MaxExpiresAt.Add(time.Nanosecond)} {
		t.Run(target.String(), func(t *testing.T) {
			repo := &lockingRenewalRepo{current: original}
			svc := NewSubscriptionService(nil, repo, nil, nil, nil)
			t.Cleanup(svc.Stop)
			svc.now = func() time.Time { return now }

			_, err := svc.SetSubscriptionExpiry(context.Background(), original.ID, target)

			require.Error(t, err)
			require.Equal(t, original, repo.current)
		})
	}
	// The limit itself remains a valid target.
	repo := &lockingRenewalRepo{current: original}
	svc := NewSubscriptionService(nil, repo, nil, nil, nil)
	t.Cleanup(svc.Stop)
	svc.now = func() time.Time { return now }
	updated, err := svc.SetSubscriptionExpiry(context.Background(), original.ID, MaxExpiresAt)
	require.NoError(t, err)
	require.Equal(t, MaxExpiresAt, updated.ExpiresAt)
}

func TestSetSubscriptionExpiryRollsBackExpiryWhenStatusRestoreFails(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, sqlDB)))
	t.Cleanup(func() { _ = client.Close() })
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	original := UserSubscription{ID: 1, ExpiresAt: now.Add(-time.Hour), Status: SubscriptionStatusExpired}
	repo := &transactionalBulkSubscriptionRepo{
		committed: original, pending: make(map[*dbent.Tx]*UserSubscription), reads: make(map[*dbent.Tx]int), statusFailure: true,
	}
	svc := NewSubscriptionService(nil, repo, nil, client, nil)
	t.Cleanup(svc.Stop)
	svc.now = func() time.Time { return now }
	mock.ExpectBegin()
	mock.ExpectRollback()

	_, err = svc.SetSubscriptionExpiry(context.Background(), original.ID, now.AddDate(0, 0, 30))

	require.ErrorContains(t, err, "status update failed")
	require.Equal(t, original, repo.committed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExtendSubscriptionKeepsRelativeDaySemantics(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		expiry  time.Time
		days    int
		want    time.Time
		wantErr bool
	}{
		{"active extension", now.AddDate(0, 0, 10), 30, now.AddDate(0, 0, 40), false},
		{"active shortening", now.AddDate(0, 0, 10), -5, now.AddDate(0, 0, 5), false},
		{"expired renewal", now.AddDate(0, 0, -10), 30, now.AddDate(0, 0, 30), false},
		{"expired shortening", now.AddDate(0, 0, -10), -5, time.Time{}, true},
		{"would expire", now.AddDate(0, 0, 10), -10, time.Time{}, true},
		{"maximum expiry clamp", now.AddDate(0, 0, 10), MaxValidityDays, MaxExpiresAt, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &lockingRenewalRepo{current: UserSubscription{ID: 1, ExpiresAt: tc.expiry, Status: SubscriptionStatusActive}}
			svc := NewSubscriptionService(nil, repo, nil, nil, nil)
			t.Cleanup(svc.Stop)
			svc.now = func() time.Time { return now }
			updated, err := svc.ExtendSubscription(context.Background(), 1, tc.days)
			if tc.wantErr {
				require.Error(t, err)
				require.Equal(t, tc.expiry, repo.current.ExpiresAt)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, updated.ExpiresAt)
			}
		})
	}
}
