//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCustomBuildCannotInstallOfficialBinaries(t *testing.T) {
	// Nil dependencies also verify that custom builds never contact GitHub or
	// consult cached official release information, even for forced checks.
	svc := NewUpdateService(nil, nil, "0.1.0-custom.abc123", "custom")
	ctx := context.Background()
	for _, force := range []bool{false, true} {
		info, err := svc.CheckUpdate(ctx, force)
		require.NoError(t, err)
		require.False(t, info.HasUpdate)
		require.Equal(t, "custom", info.BuildType)
		require.Equal(t, info.CurrentVersion, info.LatestVersion)
		require.Nil(t, info.ReleaseInfo)
	}
	versions, err := svc.ListRollbackVersions(ctx)
	require.NoError(t, err)
	require.Empty(t, versions)
	require.ErrorIs(t, svc.PerformUpdate(ctx), ErrCustomBuildUpdateDisabled)
	require.ErrorIs(t, svc.RollbackToVersion(ctx, "0.0.1"), ErrCustomBuildUpdateDisabled)
	require.ErrorIs(t, svc.Rollback(), ErrCustomBuildUpdateDisabled)
}
