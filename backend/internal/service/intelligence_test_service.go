package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

const (
	IntelligenceTestRetention     = 10
	IntelligenceTestTimeout       = 15 * time.Minute
	DefaultIntelligenceTestPrompt = "生成 html，内容是 svg 绘制鹈鹕骑自行车 2D 动画，不用进行测试"
	IntelligenceTestRunnerHTTP    = "http"
	IntelligenceTestRunnerCodex   = "codex_cli"
)

var (
	ErrIntelligenceTestQueueFull = infraerrors.TooManyRequests("INTELLIGENCE_TEST_QUEUE_FULL", "The test queue is full. Please try again later.")
	ErrIntelligenceTestNotFound  = infraerrors.NotFound("INTELLIGENCE_TEST_NOT_FOUND", "Test record not found.")
)

// IntelligenceTest shares the account's display name at submission time without
// publishing account identifiers or credentials.
type IntelligenceTest struct {
	ID              int64      `json:"id"`
	AccountID       int64      `json:"-"`
	AccountName     string     `json:"account_name"`
	Platform        string     `json:"platform"`
	Model           string     `json:"model"`
	ReasoningEffort string     `json:"reasoning_effort"`
	Prompt          string     `json:"prompt"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	DurationMS      int64      `json:"duration_ms"`
	Output          string     `json:"output,omitempty"`
	Error           string     `json:"error,omitempty"`
	Runner          string     `json:"runner"`
	RunnerVersion   string     `json:"runner_version"`
	EffectiveModel  string     `json:"effective_model"`
	ArtifactName    string     `json:"artifact_name"`
	FinalMessage    string     `json:"final_message"`
}

type IntelligenceTestRepository interface {
	Create(context.Context, *IntelligenceTest) error
	ClaimNext(context.Context) (*IntelligenceTest, error)
	Finish(context.Context, *IntelligenceTest) error
	List(context.Context) ([]*IntelligenceTest, error)
	GetByID(context.Context, int64) (*IntelligenceTest, error)
	RecoverInterrupted(context.Context, time.Time) error
}

type intelligenceTestGenerator interface {
	RunIntelligenceTest(context.Context, int64, string, string, string) (string, error)
	ValidateIntelligenceTest(*Account, string, string) error
}

// Optional detailed generation preserves the original HTTP generator contract.
type intelligenceTestDetailedGenerator interface {
	ValidateIntelligenceTestRunner(context.Context, *Account, string, string, string) error
	RunIntelligenceTestDetailed(context.Context, int64, string, string, string, string) (IntelligenceTestGenerationResult, error)
}

type IntelligenceTestGenerationResult struct {
	Output         string
	RunnerVersion  string
	EffectiveModel string
	ArtifactName   string
	FinalMessage   string
}

// IntelligenceTestService drains a durable queue independently of HTTP request lifetimes.
type IntelligenceTestService struct {
	repo      IntelligenceTestRepository
	accounts  AccountRepository
	generator intelligenceTestGenerator
	ctx       context.Context
	cancel    context.CancelFunc
	wake      chan struct{}
	wg        sync.WaitGroup
	startOnce sync.Once
	stopOnce  sync.Once
}

func NewIntelligenceTestService(repo IntelligenceTestRepository, accounts AccountRepository, generator *AccountTestService) *IntelligenceTestService {
	ctx, cancel := context.WithCancel(context.Background())
	return &IntelligenceTestService{repo: repo, accounts: accounts, generator: generator, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 2)}
}

func (s *IntelligenceTestService) Create(ctx context.Context, accountID int64, model, effort, prompt string, runners ...string) (*IntelligenceTest, error) {
	runner := IntelligenceTestRunnerHTTP
	if len(runners) > 0 && strings.TrimSpace(runners[0]) != "" {
		runner = strings.TrimSpace(runners[0])
	}
	if runner != IntelligenceTestRunnerHTTP && runner != IntelligenceTestRunnerCodex {
		return nil, infraerrors.BadRequest("INVALID_TEST_RUNNER", "Select a valid test runner.")
	}
	model = strings.TrimSpace(model)
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" {
		effort = "default"
	}
	if model == "" || len(model) > 200 || strings.ContainsAny(model, "\r\n\x00") {
		return nil, infraerrors.BadRequest("INVALID_TEST_MODEL", "Select a valid text model.")
	}
	switch effort {
	case "default", "none", "minimal", "low", "medium", "high", "xhigh", "max":
	default:
		return nil, infraerrors.BadRequest("INVALID_REASONING_EFFORT", "Invalid thinking level.")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = DefaultIntelligenceTestPrompt
	}
	if len(prompt) > 32*1024 {
		return nil, infraerrors.BadRequest("TEST_PROMPT_TOO_LONG", "Prompt must be at most 32 KiB.")
	}
	account, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if detailed, ok := s.generator.(intelligenceTestDetailedGenerator); ok {
		if err := detailed.ValidateIntelligenceTestRunner(ctx, account, model, effort, runner); err != nil {
			return nil, err
		}
	} else {
		if runner != IntelligenceTestRunnerHTTP {
			return nil, infraerrors.BadRequest("CODEX_CLI_UNAVAILABLE", "Codex CLI runner is unavailable.")
		}
		if err := s.generator.ValidateIntelligenceTest(account, model, effort); err != nil {
			return nil, err
		}
	}
	record := &IntelligenceTest{AccountID: accountID, AccountName: account.Name, Platform: account.Platform, Model: model, ReasoningEffort: effort, Prompt: prompt, Status: "queued", Runner: runner}
	if err := s.repo.Create(ctx, record); err != nil {
		return nil, err
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return record, nil
}

func (s *IntelligenceTestService) List(ctx context.Context) ([]*IntelligenceTest, error) {
	return s.repo.List(ctx)
}
func (s *IntelligenceTestService) GetByID(ctx context.Context, id int64) (*IntelligenceTest, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *IntelligenceTestService) Start() {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		for i := 0; i < 2; i++ {
			s.wg.Add(1)
			go s.worker()
		}
		s.wg.Add(1)
		go s.recoverLoop()
	})
}

func (s *IntelligenceTestService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { s.cancel(); s.wg.Wait() })
}

func (s *IntelligenceTestService) worker() {
	defer s.wg.Done()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		if s.ctx.Err() != nil {
			return
		}
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		record, err := s.repo.ClaimNext(ctx)
		cancel()
		if err != nil {
			s.logError("claim", err)
		}
		if err == nil && record != nil {
			s.run(record)
			continue
		}
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
	}
}

func (s *IntelligenceTestService) recoverLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		err := s.repo.RecoverInterrupted(ctx, time.Now().Add(-IntelligenceTestTimeout-2*time.Minute))
		cancel()
		if err != nil {
			s.logError("recover", err)
		}
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *IntelligenceTestService) run(record *IntelligenceTest) {
	ctx, cancel := context.WithTimeout(s.ctx, IntelligenceTestTimeout)
	defer cancel()
	output, err := s.generate(ctx, record)
	if err == nil {
		err = ctx.Err()
	}
	completed := time.Now()
	record.CompletedAt = &completed
	if record.StartedAt != nil {
		record.DurationMS = completed.Sub(*record.StartedAt).Milliseconds()
		if record.DurationMS < 0 {
			record.DurationMS = 0
		}
	}
	record.Status = "succeeded"
	record.Output = output
	if err != nil {
		record.Status = "failed"
		record.Output = ""
		record.FinalMessage = ""
		// Upstream errors can contain endpoints, credential fragments and provider account identity.
		// Only bounded, fixed messages belong in the result shared with every user.
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			record.Error = "Test timed out after 15 minutes."
		case errors.Is(err, context.Canceled):
			record.Error = "Test interrupted by a server restart. Please run it again."
		default:
			record.Error = "Generation failed. Check the account, model and thinking level, then try again."
		}
		s.logError(fmt.Sprintf("generate record=%d", record.ID), err)
	}
	// Persist even if the upstream request timed out or the server is shutting down.
	for attempt := 0; attempt < 3; attempt++ {
		finishCtx, finishCancel := context.WithTimeout(context.Background(), 2*time.Second)
		finishErr := s.repo.Finish(finishCtx, record)
		finishCancel()
		if finishErr == nil || errors.Is(finishErr, ErrIntelligenceTestNotFound) {
			return
		}
		s.logError("finish", finishErr)
	}
}

func (s *IntelligenceTestService) generate(ctx context.Context, record *IntelligenceTest) (output string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			output = ""
			err = fmt.Errorf("generation panic: %v", recovered)
		}
	}()
	if detailed, ok := s.generator.(intelligenceTestDetailedGenerator); ok {
		result, generationErr := detailed.RunIntelligenceTestDetailed(ctx, record.AccountID, record.Model, record.Prompt, record.ReasoningEffort, record.Runner)
		record.RunnerVersion = result.RunnerVersion
		record.EffectiveModel = result.EffectiveModel
		record.ArtifactName = result.ArtifactName
		if generationErr == nil {
			record.FinalMessage = result.FinalMessage
		}
		return result.Output, generationErr
	}
	return s.generator.RunIntelligenceTest(ctx, record.AccountID, record.Model, record.Prompt, record.ReasoningEffort)
}

func (s *IntelligenceTestService) logError(operation string, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	logger.LegacyPrintf("service.intelligence_test", "[IntelligenceTest] %s: %s", operation, logredact.RedactText(err.Error()))
}
