//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type intelligenceTestMemoryRepo struct {
	mu       sync.Mutex
	records  []*IntelligenceTest
	finished chan *IntelligenceTest
}

func (r *intelligenceTestMemoryRepo) Create(_ context.Context, record *IntelligenceTest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	record.ID = int64(len(r.records) + 1)
	record.CreatedAt = time.Now()
	copy := *record
	r.records = append(r.records, &copy)
	return nil
}
func (r *intelligenceTestMemoryRepo) ClaimNext(context.Context) (*IntelligenceTest, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, record := range r.records {
		if record.Status == "queued" {
			now := time.Now()
			record.StartedAt = &now
			record.Status = "running"
			copy := *record
			return &copy, nil
		}
	}
	return nil, nil
}
func (r *intelligenceTestMemoryRepo) Finish(_ context.Context, record *IntelligenceTest) error {
	copy := *record
	r.finished <- &copy
	return nil
}
func (r *intelligenceTestMemoryRepo) List(context.Context) ([]*IntelligenceTest, error) {
	return nil, nil
}
func (r *intelligenceTestMemoryRepo) GetByID(context.Context, int64) (*IntelligenceTest, error) {
	return nil, ErrIntelligenceTestNotFound
}
func (r *intelligenceTestMemoryRepo) RecoverInterrupted(context.Context, time.Time) error { return nil }

type intelligenceTestAccounts struct{ AccountRepository }

func (intelligenceTestAccounts) GetByID(context.Context, int64) (*Account, error) {
	return &Account{ID: 1, Platform: PlatformOpenAI}, nil
}

type intelligenceTestGeneratorStub struct {
	started chan struct{}
	finish  chan struct{}
	err     error
	panic   bool
}

func (*intelligenceTestGeneratorStub) ValidateIntelligenceTest(*Account, string, string) error {
	return nil
}
func (g *intelligenceTestGeneratorStub) RunIntelligenceTest(ctx context.Context, _ int64, _, _, _ string) (string, error) {
	if g.panic {
		panic("provider panic")
	}
	if g.started != nil {
		close(g.started)
	}
	if g.finish != nil {
		select {
		case <-g.finish:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return "<svg></svg>", g.err
}

func intelligenceServiceForTest(t *testing.T, generator *intelligenceTestGeneratorStub) (*IntelligenceTestService, *intelligenceTestMemoryRepo) {
	t.Helper()
	repo := &intelligenceTestMemoryRepo{finished: make(chan *IntelligenceTest, 4)}
	svc := NewIntelligenceTestService(repo, intelligenceTestAccounts{}, nil)
	svc.generator = generator
	t.Cleanup(svc.Stop)
	return svc, repo
}

func awaitIntelligenceResult(t *testing.T, repo *intelligenceTestMemoryRepo) *IntelligenceTest {
	t.Helper()
	select {
	case result := <-repo.finished:
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("background test did not finish")
		return nil
	}
}

func TestIntelligenceTestContinuesAfterRequestEnds(t *testing.T) {
	generator := &intelligenceTestGeneratorStub{started: make(chan struct{}), finish: make(chan struct{})}
	svc, repo := intelligenceServiceForTest(t, generator)
	ctx, cancel := context.WithCancel(context.Background())
	record, err := svc.Create(ctx, 1, "gpt-test", "high", "generate animation")
	require.NoError(t, err)
	require.Equal(t, "queued", record.Status)
	cancel() // Browser closes the request before the worker even starts.
	svc.Start()
	select {
	case <-generator.started:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not start")
	}
	close(generator.finish)
	result := awaitIntelligenceResult(t, repo)
	require.Equal(t, "succeeded", result.Status)
	require.Equal(t, "<svg></svg>", result.Output)
	require.NotNil(t, result.StartedAt)
	require.NotNil(t, result.CompletedAt)
	require.Equal(t, "high", result.ReasoningEffort)
}

func TestIntelligenceTestShutdownPersistsFailure(t *testing.T) {
	generator := &intelligenceTestGeneratorStub{started: make(chan struct{}), finish: make(chan struct{})}
	svc, repo := intelligenceServiceForTest(t, generator)
	_, err := svc.Create(context.Background(), 1, "gpt-test", "default", "")
	require.NoError(t, err)
	svc.Start()
	select {
	case <-generator.started:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not start")
	}
	svc.Stop()
	result := awaitIntelligenceResult(t, repo)
	require.Equal(t, "failed", result.Status)
	require.Contains(t, result.Error, "restart")
	require.Empty(t, result.Output)
}

func TestIntelligenceTestFailureDoesNotPublishUpstreamSecrets(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		panic bool
	}{
		{name: "upstream", err: errors.New("api_key=secret-value private-account@example.com")},
		{name: "panic", panic: true},
		{name: "deadline", err: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := intelligenceServiceForTest(t, &intelligenceTestGeneratorStub{err: tc.err, panic: tc.panic})
			_, err := svc.Create(context.Background(), 1, "gpt-test", "", "")
			require.NoError(t, err)
			svc.Start()
			result := awaitIntelligenceResult(t, repo)
			require.Equal(t, "failed", result.Status)
			require.Empty(t, result.Output)
			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "account_id")
			require.NotContains(t, string(encoded), "secret-value")
			require.NotContains(t, string(encoded), "private-account")
		})
	}
}

func TestIntelligenceTestInputValidationAndDefaultPrompt(t *testing.T) {
	svc, _ := intelligenceServiceForTest(t, &intelligenceTestGeneratorStub{})
	record, err := svc.Create(context.Background(), 1, "gpt-test", "", " ")
	require.NoError(t, err)
	require.Equal(t, DefaultIntelligenceTestPrompt, record.Prompt)
	require.Equal(t, "default", record.ReasoningEffort)
	_, err = svc.Create(context.Background(), 1, "", "default", "")
	require.Error(t, err)
	_, err = svc.Create(context.Background(), 1, "gpt-test", "unexpected", "")
	require.Error(t, err)
}
