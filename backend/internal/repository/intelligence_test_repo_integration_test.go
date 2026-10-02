//go:build integration

package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func cleanIntelligenceTestTable(t *testing.T) {
	t.Helper()
	_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM intelligence_tests`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := integrationDB.ExecContext(context.Background(), `DELETE FROM intelligence_tests`)
		require.NoError(t, cleanupErr)
	})
}

func TestIntelligenceTestRepositoryConcurrentAdmissionAndClaims(t *testing.T) {
	cleanIntelligenceTestTable(t)
	ctx := context.Background()
	repo := NewIntelligenceTestRepository(integrationDB)
	const submissions = 30
	createErrors := make([]error, submissions)
	var wg sync.WaitGroup
	for i := range submissions {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			createErrors[index] = repo.Create(ctx, &service.IntelligenceTest{
				Platform: "openai", Model: "test-model", Prompt: "prompt",
			})
		}(i)
	}
	wg.Wait()
	accepted := 0
	for _, err := range createErrors {
		if err == nil {
			accepted++
		} else {
			require.ErrorIs(t, err, service.ErrIntelligenceTestQueueFull)
		}
	}
	require.Equal(t, intelligenceTestQueueLimit, accepted)

	claimed := make([]*service.IntelligenceTest, submissions)
	claimErrors := make([]error, submissions)
	for i := range submissions {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			claimed[index], claimErrors[index] = repo.ClaimNext(ctx)
		}(i)
	}
	wg.Wait()
	ids := make(map[int64]bool)
	for i, item := range claimed {
		require.NoError(t, claimErrors[i])
		if item != nil {
			require.False(t, ids[item.ID], "the same job cannot be claimed twice")
			ids[item.ID] = true
			require.Equal(t, "running", item.Status)
			require.NotNil(t, item.StartedAt)
		}
	}
	require.Len(t, ids, intelligenceTestRunningLimit)
	for _, item := range claimed {
		if item != nil {
			item.Status = "succeeded"
			require.NoError(t, repo.Finish(ctx, item))
		}
	}
	item, err := repo.ClaimNext(ctx)
	require.NoError(t, err)
	require.NotNil(t, item, "finishing jobs must reopen the global worker capacity")
}

func TestIntelligenceTestRepositoryRetentionAndInterruptedRecovery(t *testing.T) {
	cleanIntelligenceTestTable(t)
	ctx := context.Background()
	repo := NewIntelligenceTestRepository(integrationDB)
	var firstID, newestID int64
	for i := range 12 {
		item := &service.IntelligenceTest{Platform: "openai", Model: "test-model", Prompt: "prompt"}
		require.NoError(t, repo.Create(ctx, item))
		if i == 0 {
			firstID = item.ID
		}
		newestID = item.ID
		claimed, err := repo.ClaimNext(ctx)
		require.NoError(t, err)
		require.NotNil(t, claimed)
		claimed.Status = "succeeded"
		claimed.Output = "<svg></svg>"
		claimed.DurationMS = 123
		require.NoError(t, repo.Finish(ctx, claimed))
	}
	items, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, items, intelligenceTestRetention)
	require.Equal(t, newestID, items[0].ID)
	for _, item := range items {
		require.Empty(t, item.Output)
		require.Zero(t, item.AccountID)
	}
	_, err = repo.GetByID(ctx, firstID)
	require.ErrorIs(t, err, service.ErrIntelligenceTestNotFound)
	detail, err := repo.GetByID(ctx, newestID)
	require.NoError(t, err)
	require.Equal(t, "<svg></svg>", detail.Output)
	require.NotNil(t, detail.CompletedAt)

	queued := &service.IntelligenceTest{Platform: "openai", Model: "test-model", Prompt: "prompt"}
	require.NoError(t, repo.Create(ctx, queued))
	interrupted, err := repo.ClaimNext(ctx)
	require.NoError(t, err)
	require.NotNil(t, interrupted)
	old := time.Now().UTC().Add(-2 * time.Hour)
	_, err = integrationDB.ExecContext(ctx, `UPDATE intelligence_tests SET started_at = $2 WHERE id = $1`, interrupted.ID, old)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, &service.IntelligenceTest{Platform: "openai", Model: "test-model", Prompt: "prompt"}))
	require.NoError(t, repo.RecoverInterrupted(ctx, time.Now().UTC().Add(-time.Hour)))
	failed, err := repo.GetByID(ctx, interrupted.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", failed.Status)
	require.Empty(t, failed.Output)
	require.NotEmpty(t, failed.Error)
	require.NotNil(t, failed.CompletedAt)
	interrupted.Status = "succeeded"
	require.ErrorIs(t, repo.Finish(ctx, interrupted), service.ErrIntelligenceTestNotFound)
	items, err = repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, items, intelligenceTestRetention+1, "retention must preserve unfinished jobs")
}
