//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoginProtectionService_MissingStoreFailsClosed(t *testing.T) {
	for _, svc := range []*LoginProtectionService{nil, NewLoginProtectionService(nil)} {
		requireLoginProtectionStoreError(t, svc, nil)
	}
}

func TestLoginProtectionService_StoreErrorsFailClosed(t *testing.T) {
	failure := errors.New("storage unavailable")
	svc := NewLoginProtectionService(unavailableLoginProtectionStore{err: failure})
	requireLoginProtectionStoreError(t, svc, failure)
}

func requireLoginProtectionStoreError(t *testing.T, svc *LoginProtectionService, expected error) {
	t.Helper()
	ctx := context.Background()
	ban, err := svc.Check(ctx, "192.0.2.1")
	require.Nil(t, ban)
	require.Error(t, err)
	if expected != nil {
		require.ErrorIs(t, err, expected)
	}
	ban, count, err := svc.RecordFailure(ctx, "192.0.2.1")
	require.Nil(t, ban)
	require.Zero(t, count)
	require.Error(t, err)
	if expected != nil {
		require.ErrorIs(t, err, expected)
	}
	err = svc.RecordSuccess(ctx, "192.0.2.1")
	require.Error(t, err)
	if expected != nil {
		require.ErrorIs(t, err, expected)
	}
	bans, err := svc.ListBans(ctx, 1, 20)
	require.Nil(t, bans)
	require.Error(t, err)
	if expected != nil {
		require.ErrorIs(t, err, expected)
	}
	err = svc.Unban(ctx, "192.0.2.1")
	require.Error(t, err)
	if expected != nil {
		require.ErrorIs(t, err, expected)
	}
}

type unavailableLoginProtectionStore struct {
	err error
}

func (s unavailableLoginProtectionStore) Check(context.Context, string) (*LoginIPBan, error) {
	return nil, s.err
}

func (s unavailableLoginProtectionStore) RecordFailure(context.Context, string) (*LoginIPBan, int, error) {
	return nil, 0, s.err
}

func (s unavailableLoginProtectionStore) RecordSuccess(context.Context, string) error {
	return s.err
}

func (s unavailableLoginProtectionStore) ListBans(context.Context, int, int) (*LoginIPBanList, error) {
	return nil, s.err
}

func (s unavailableLoginProtectionStore) Unban(context.Context, string) error {
	return s.err
}
