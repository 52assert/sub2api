package service

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

const (
	SubscriberResetVoteThreshold = 3
	SubscriberResetVoteWindow    = 24 * time.Hour

	subscriberVoteStaleExecutingTimeout = 3 * time.Minute
	subscriberVoteExecTimeout           = 90 * time.Second

	SubscriberResetVoteStatusNone      = "none"
	SubscriberResetVoteStatusOpen      = "open"
	SubscriberResetVoteStatusExecuting = "executing"
	SubscriberResetVoteStatusSucceeded = "succeeded"
	SubscriberResetVoteStatusFailed    = "failed"

	SubscriberResetVoteIneligibleQuotaGroupOnly = "quota_group_only"
	SubscriberResetVoteIneligibleNotSubscribed  = "not_subscribed"
	SubscriberResetVoteIneligibleUnsupported    = "unsupported_account"
)

type subscriberVoteQuotaClient interface {
	QueryUsage(ctx context.Context, accountID int64) (*OpenAIQuotaUsage, error)
	ResetCreditTargeted(ctx context.Context, accountID int64, creditID, redeemRequestID string) (*OpenAIQuotaResetResult, error)
	ResetCreditWithRequestID(ctx context.Context, accountID int64, redeemRequestID string) (*OpenAIQuotaResetResult, error)
	CachePostResetSnapshot(ctx context.Context, accountID int64, usage *OpenAIQuotaUsage) error
}

type subscriberVoteResetApplier interface {
	ResetByVote(ctx context.Context, accountID int64, operationID string, actorUserID int64) (int, error)
}

// SubscriberResetVoteState is the subscriber-facing, sanitized view of an account's reset vote state.
type SubscriberResetVoteState struct {
	Supported               bool       `json:"supported"`
	Eligible                bool       `json:"eligible"`
	CanVote                 bool       `json:"can_vote"`
	HasVoted                bool       `json:"has_voted"`
	IneligibleReason        string     `json:"ineligible_reason,omitempty"`
	RoundID                 int64      `json:"round_id,omitempty"`
	Status                  string     `json:"status"`
	VoteCount               int        `json:"vote_count"`
	Threshold               int        `json:"threshold"`
	InitiatedAt             *time.Time `json:"initiated_at,omitempty"`
	ExpiresAt               *time.Time `json:"expires_at,omitempty"`
	TriggeredAt             *time.Time `json:"triggered_at,omitempty"`
	CompletedAt             *time.Time `json:"completed_at,omitempty"`
	CardStatus              string     `json:"card_status,omitempty"`
	SubscriptionResetStatus string     `json:"subscription_reset_status,omitempty"`
	SubscriptionResetCount  int        `json:"subscription_reset_count,omitempty"`
	CooldownActive          bool       `json:"cooldown_active"`
	CanInitiate             bool       `json:"can_initiate"`
	ErrorCode               string     `json:"error_code,omitempty"`
	ErrorMessage            string     `json:"error_message,omitempty"`
	WarningCode             string     `json:"warning_code,omitempty"`
}

// SubscriberResetVoteCastResult summarizes the outcome of casting a subscriber reset vote.
type SubscriberResetVoteCastResult struct {
	State          *SubscriberResetVoteState `json:"state"`
	VoteRecorded   bool                      `json:"vote_recorded"`
	TriggeredReset bool                      `json:"triggered_reset"`
	ResetSucceeded bool                      `json:"reset_succeeded"`
}

type subscriberResetVoteRoundRow struct {
	ID                      int64
	AccountID               int64
	Threshold               int
	Status                  string
	RequestID               string
	InitiatedAt             time.Time
	ExpiresAt               time.Time
	TriggeredAt             *time.Time
	CompletedAt             *time.Time
	CardStatus              string
	SubscriptionResetStatus string
	SubscriptionResetCount  int
	ErrorCode               string
	ErrorMessage            string
	WarningCode             string
	VoteCount               int
	HasVoted                bool
}

// CustomSubscriberResetVoteService coordinates 24-hour reset votes initiated by active
// subscription-group members of shared OpenAI OAuth parent accounts.
type CustomSubscriberResetVoteService struct {
	db        *sql.DB
	accounts  AccountRepository
	quota     subscriberVoteQuotaClient
	recoverer openAIQuotaResetWorkflowRecoverer
	resets    subscriberVoteResetApplier
	now       func() time.Time
	newUUID   func() string
}

// ProvideCustomSubscriberResetVoteService wires the subscriber reset vote service for dependency injection.
func ProvideCustomSubscriberResetVoteService(
	db *sql.DB,
	accounts AccountRepository,
	quota *OpenAIQuotaService,
	rateLimit *RateLimitService,
	resets *CustomCodexResetService,
) *CustomSubscriberResetVoteService {
	var q subscriberVoteQuotaClient
	if quota != nil {
		q = quota
	}
	var rec openAIQuotaResetWorkflowRecoverer
	if rateLimit != nil {
		rec = rateLimit
	}
	var r subscriberVoteResetApplier
	if resets != nil {
		r = resets
	}
	return newCustomSubscriberResetVoteServiceWithDeps(db, accounts, q, rec, r)
}

func newCustomSubscriberResetVoteServiceWithDeps(
	db *sql.DB,
	accounts AccountRepository,
	quota subscriberVoteQuotaClient,
	recoverer openAIQuotaResetWorkflowRecoverer,
	resets subscriberVoteResetApplier,
) *CustomSubscriberResetVoteService {
	return &CustomSubscriberResetVoteService{
		db:        db,
		accounts:  accounts,
		quota:     quota,
		recoverer: recoverer,
		resets:    resets,
		now:       time.Now,
		newUUID:   uuid.NewString,
	}
}

func isVoteSupportedOpenAIAccount(acc *Account) bool {
	if acc == nil {
		return false
	}
	if acc.Platform != PlatformOpenAI || acc.Type != AccountTypeOAuth {
		return false
	}
	if acc.IsShadow() || acc.IsCredentialShadow() || acc.IsSyntheticUITest() {
		return false
	}
	return IsAccountSubscriberVisible(acc)
}

func unsupportedVoteState() *SubscriberResetVoteState {
	return &SubscriberResetVoteState{
		Supported:        false,
		Eligible:         false,
		CanVote:          false,
		IneligibleReason: SubscriberResetVoteIneligibleUnsupported,
		Status:           SubscriberResetVoteStatusNone,
		Threshold:        SubscriberResetVoteThreshold,
	}
}

// GetStatesForAccounts returns batch-projected reset vote states for a list of accounts.
func (s *CustomSubscriberResetVoteService) GetStatesForAccounts(
	ctx context.Context,
	userID int64,
	accounts []*Account,
) (map[int64]*SubscriberResetVoteState, error) {
	out := make(map[int64]*SubscriberResetVoteState, len(accounts))
	if len(accounts) == 0 {
		return out, nil
	}

	supportedIDs := make([]int64, 0, len(accounts))
	seen := make(map[int64]struct{}, len(accounts))
	for _, acc := range accounts {
		if acc == nil || acc.ID <= 0 {
			continue
		}
		if _, dup := seen[acc.ID]; dup {
			continue
		}
		seen[acc.ID] = struct{}{}
		if !isVoteSupportedOpenAIAccount(acc) {
			out[acc.ID] = unsupportedVoteState()
			continue
		}
		supportedIDs = append(supportedIDs, acc.ID)
	}
	if len(supportedIDs) == 0 {
		return out, nil
	}
	if s == nil || s.db == nil {
		for _, accID := range supportedIDs {
			out[accID] = unsupportedVoteState()
		}
		return out, nil
	}

	now := s.now().UTC()
	hasSubGroup, err := s.queryAccountsWithSubscriptionGroups(ctx, supportedIDs)
	if err != nil {
		return nil, err
	}

	eligibleSet := make(map[int64]bool)
	if userID > 0 {
		eligibleSet, err = s.queryEligibleAccountsForUser(ctx, userID, supportedIDs, now)
		if err != nil {
			return nil, err
		}
	}

	roundsByAccount, err := s.queryLatestVoteRounds(ctx, userID, supportedIDs)
	if err != nil {
		return nil, err
	}

	for _, accID := range supportedIDs {
		eligible := eligibleSet[accID]
		ineligibleReason := ""
		if !eligible {
			if !hasSubGroup[accID] {
				ineligibleReason = SubscriberResetVoteIneligibleQuotaGroupOnly
			} else {
				ineligibleReason = SubscriberResetVoteIneligibleNotSubscribed
			}
		}
		out[accID] = s.projectVoteRoundState(eligible, ineligibleReason, roundsByAccount[accID], now)
	}
	return out, nil
}

// CastVote initiates a 24-hour vote round or records a subscriber's vote in the active round,
// automatically consuming a reset card and resetting bound subscription groups when reaching threshold.
func (s *CustomSubscriberResetVoteService) CastVote(
	ctx context.Context,
	userID, accountID int64,
) (*SubscriberResetVoteCastResult, error) {
	if s == nil || s.db == nil || s.accounts == nil {
		return nil, infraerrors.ServiceUnavailable("RESET_VOTE_UNAVAILABLE", "Reset voting service is unavailable")
	}
	if userID <= 0 || accountID <= 0 {
		return nil, infraerrors.BadRequest("RESET_VOTE_INVALID_REQUEST", "Valid user and account IDs are required")
	}

	acc, err := s.accounts.GetByID(ctx, accountID)
	if err != nil || acc == nil || !IsAccountSubscriberVisible(acc) {
		return nil, infraerrors.NotFound("ACCOUNT_NOT_FOUND", "Account not found")
	}

	now := s.now().UTC()
	round, voteCount, triggered, err := s.recordVotePhase(ctx, userID, accountID, acc, now)
	if err != nil {
		return nil, err
	}

	if !triggered {
		round.VoteCount = voteCount
		round.HasVoted = true
		state := s.projectVoteRoundState(true, "", round, now)
		return &SubscriberResetVoteCastResult{
			State:          state,
			VoteRecorded:   true,
			TriggeredReset: false,
			ResetSucceeded: false,
		}, nil
	}

	execCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), subscriberVoteExecTimeout)
	defer cancel()

	round = s.executeTriggeredRound(execCtx, round, userID)
	round.VoteCount = voteCount
	round.HasVoted = true
	state := s.projectVoteRoundState(true, "", round, s.now().UTC())
	return &SubscriberResetVoteCastResult{
		State:          state,
		VoteRecorded:   true,
		TriggeredReset: true,
		ResetSucceeded: state.Status == SubscriberResetVoteStatusSucceeded,
	}, nil
}

func (s *CustomSubscriberResetVoteService) recordVotePhase(
	ctx context.Context,
	userID, accountID int64,
	acc *Account,
	now time.Time,
) (*subscriberResetVoteRoundRow, int, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, false, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('custom-subscriber-reset-vote:' || $1::text, 0))`, accountID); err != nil {
		return nil, 0, false, err
	}

	eligible, err := s.isUserEligibleInTx(ctx, tx, userID, accountID, now)
	if err != nil {
		return nil, 0, false, err
	}
	if !eligible {
		return nil, 0, false, infraerrors.Forbidden("RESET_VOTE_SUBSCRIPTION_REQUIRED", "Only active members of this account's subscription groups can vote to reset")
	}
	if !isVoteSupportedOpenAIAccount(acc) {
		return nil, 0, false, infraerrors.BadRequest("RESET_VOTE_UNSUPPORTED_ACCOUNT", "Only OpenAI OAuth parent accounts support reset voting")
	}

	latest, err := s.loadLatestRoundForUpdate(ctx, tx, accountID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, 0, false, err
	}

	var round *subscriberResetVoteRoundRow
	var voteCount int
	triggered := false

	if errors.Is(err, sql.ErrNoRows) || !latest.ExpiresAt.After(now) {
		requestID := s.newUUID()
		expiresAt := now.Add(SubscriberResetVoteWindow)
		round = &subscriberResetVoteRoundRow{
			AccountID:               accountID,
			Threshold:               SubscriberResetVoteThreshold,
			Status:                  SubscriberResetVoteStatusOpen,
			RequestID:               requestID,
			InitiatedAt:             now,
			ExpiresAt:               expiresAt,
			CardStatus:              "pending",
			SubscriptionResetStatus: "pending",
		}
		err = tx.QueryRowContext(
			ctx,
			`INSERT INTO custom_subscriber_reset_vote_rounds
			 (account_id, initiator_user_id, threshold, status, request_id, initiated_at, expires_at, card_status, subscription_reset_status, created_at, updated_at)
			 VALUES ($1, $2, $3, 'open', $4, $5, $6, 'pending', 'pending', $5, $5)
			 RETURNING id`,
			accountID, userID, SubscriberResetVoteThreshold, requestID, now, expiresAt,
		).Scan(&round.ID)
		if err != nil {
			return nil, 0, false, err
		}
		if _, err = tx.ExecContext(
			ctx,
			`INSERT INTO custom_subscriber_reset_vote_voters (round_id, user_id, created_at) VALUES ($1, $2, $3)`,
			round.ID, userID, now,
		); err != nil {
			return nil, 0, false, err
		}
		voteCount = 1
	} else {
		round = latest
		if round.Status == SubscriberResetVoteStatusExecuting &&
			round.TriggeredAt != nil &&
			now.Sub(*round.TriggeredAt) > subscriberVoteStaleExecutingTimeout &&
			round.CardStatus == "pending" {
			errCode := "VOTE_EXECUTION_TIMED_OUT"
			errMsg := "Previous reset attempt timed out before upstream confirmation"
			if _, updErr := tx.ExecContext(
				ctx,
				`UPDATE custom_subscriber_reset_vote_rounds
				 SET status = 'failed', card_status = 'unknown', error_code = $2, error_message = $3, completed_at = $4, updated_at = $4
				 WHERE id = $1 AND status = 'executing'`,
				round.ID, errCode, errMsg, now,
			); updErr != nil {
				return nil, 0, false, updErr
			}
			round.Status = SubscriberResetVoteStatusFailed
			round.CardStatus = "unknown"
			round.ErrorCode = errCode
			round.ErrorMessage = errMsg
			round.CompletedAt = &now
		}

		switch round.Status {
		case SubscriberResetVoteStatusExecuting:
			return nil, 0, false, infraerrors.Conflict("RESET_VOTE_EXECUTING", "Reset card execution is currently in progress")
		case SubscriberResetVoteStatusSucceeded, SubscriberResetVoteStatusFailed:
			return nil, 0, false, infraerrors.Conflict("RESET_VOTE_COOLDOWN_ACTIVE", "A reset vote round already completed within the last 24 hours; please wait until the 24-hour window expires")
		case SubscriberResetVoteStatusOpen:
			var voterID int64
			err = tx.QueryRowContext(
				ctx,
				`INSERT INTO custom_subscriber_reset_vote_voters (round_id, user_id, created_at)
				 VALUES ($1, $2, $3)
				 ON CONFLICT (round_id, user_id) DO NOTHING
				 RETURNING id`,
				round.ID, userID, now,
			).Scan(&voterID)
			if errors.Is(err, sql.ErrNoRows) {
				return nil, 0, false, infraerrors.Conflict("RESET_VOTE_ALREADY_VOTED", "You have already voted in this 24-hour reset round")
			}
			if err != nil {
				return nil, 0, false, err
			}

			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM custom_subscriber_reset_vote_voters WHERE round_id = $1`, round.ID).Scan(&voteCount); err != nil {
				return nil, 0, false, err
			}
			if voteCount >= round.Threshold {
				if _, err = tx.ExecContext(
					ctx,
					`UPDATE custom_subscriber_reset_vote_rounds
					 SET status = 'executing', triggered_at = $2, updated_at = $2
					 WHERE id = $1 AND status = 'open'`,
					round.ID, now,
				); err != nil {
					return nil, 0, false, err
				}
				round.Status = SubscriberResetVoteStatusExecuting
				round.TriggeredAt = &now
				triggered = true
			}
		default:
			return nil, 0, false, infraerrors.InternalServer("RESET_VOTE_INVALID_STATE", "Invalid vote round state")
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, 0, false, err
	}
	return round, voteCount, triggered, nil
}

func (s *CustomSubscriberResetVoteService) executeTriggeredRound(
	ctx context.Context,
	round *subscriberResetVoteRoundRow,
	triggeringUserID int64,
) *subscriberResetVoteRoundRow {
	failRound := func(cardStatus, subResetStatus, errCode, errMsg, warningCode string, resetCount int) *subscriberResetVoteRoundRow {
		now := s.now().UTC()
		round.Status = SubscriberResetVoteStatusFailed
		round.CardStatus = cardStatus
		round.SubscriptionResetStatus = subResetStatus
		round.SubscriptionResetCount = resetCount
		round.ErrorCode = errCode
		round.ErrorMessage = errMsg
		round.WarningCode = warningCode
		round.CompletedAt = &now
		if dbErr := s.persistRoundOutcome(ctx, round); dbErr != nil {
			slog.Warn("subscriber_reset_vote_persist_failed", "round_id", round.ID, "account_id", round.AccountID, "error", dbErr)
		}
		return round
	}

	if s.quota == nil || s.resets == nil {
		return failRound("failed", "pending", "RESET_SERVICE_UNAVAILABLE", "Reset card or subscription reset service is unavailable", "", 0)
	}

	acc, err := s.accounts.GetByID(ctx, round.AccountID)
	if err != nil || !isVoteSupportedOpenAIAccount(acc) {
		return failRound("failed", "pending", "RESET_VOTE_UNSUPPORTED_ACCOUNT", "Account is no longer eligible for vote reset", "", 0)
	}

	usage, err := s.quota.QueryUsage(ctx, round.AccountID)
	if err != nil {
		return failRound("failed", "pending", "QUOTA_QUERY_FAILED", "Failed to check available reset cards", "", 0)
	}
	if usage == nil || usage.RateLimitResetCredits == nil || usage.RateLimitResetCredits.AvailableCount <= 0 {
		return failRound("no_credit", "pending", "NO_RESET_CARD", "No reset card is available for this account", "", 0)
	}

	var resetRes *OpenAIQuotaResetResult
	var resetErr error
	if len(usage.autoResetCandidates) > 0 {
		candidate, cardErr := selectCustomAccountScheduleCard(usage, s.now().UTC())
		if cardErr != nil {
			return failRound("no_credit", "pending", "NO_RESET_CARD", cardErr.Error(), "", 0)
		}
		resetRes, resetErr = s.quota.ResetCreditTargeted(ctx, round.AccountID, candidate.ID, round.RequestID)
	} else {
		resetRes, resetErr = s.quota.ResetCreditWithRequestID(ctx, round.AccountID, round.RequestID)
	}

	if resetErr != nil {
		code := infraerrors.Code(resetErr)
		reason := infraerrors.Reason(resetErr)
		if reason == "OPENAI_QUOTA_RESET_REQUEST_FAILED" || code >= http.StatusInternalServerError || code == http.StatusRequestTimeout || errors.Is(resetErr, context.Canceled) || errors.Is(resetErr, context.DeadlineExceeded) {
			return failRound("unknown", "pending", "RESET_CARD_UNKNOWN", "Reset card outcome is unknown; please verify account quota before retrying", "", 0)
		}
		return failRound("failed", "pending", safeCustomAccountScheduleReason(reason), "Reset card consumption failed", "", 0)
	}
	if resetRes == nil {
		return failRound("unknown", "pending", "RESET_CARD_UNKNOWN", "Reset card outcome is unknown; please verify account quota before retrying", "", 0)
	}

	code := strings.ToLower(strings.TrimSpace(resetRes.Code))
	if code == "no_credit" {
		return failRound("no_credit", "pending", "NO_RESET_CARD", "No reset card is available for this account", "", 0)
	}
	if code != "ok" && code != "success" {
		return failRound("unknown", "pending", "RESET_CARD_UNKNOWN", "Reset card outcome is unknown; please verify account quota before retrying", "", 0)
	}

	// Durable checkpoint: upstream card was consumed, never re-charge this card.
	if err := s.markCardSucceeded(ctx, round.ID); err != nil {
		slog.Warn("subscriber_reset_vote_card_checkpoint_failed", "round_id", round.ID, "account_id", round.AccountID, "error", err)
	}
	round.CardStatus = "succeeded"

	post := RunOpenAIQuotaResetPostProcess(ctx, round.AccountID, s.quota, s.recoverer, func(loadCtx context.Context, id int64) (*Account, error) {
		if s.accounts == nil {
			return nil, errors.New("account repository unavailable")
		}
		return s.accounts.GetByID(loadCtx, id)
	})
	warningCode := post.WarningCode

	resetCount, subErr := s.resets.ResetByVote(ctx, round.AccountID, round.RequestID, triggeringUserID)
	if subErr != nil {
		if errors.Is(subErr, ErrScheduledSubscriptionCacheRefresh) {
			warningCode = "subscription_cache_refresh_failed"
		} else {
			return failRound("succeeded", "failed", safeCustomAccountScheduleReason(infraerrors.Reason(subErr)), "Reset card succeeded, but resetting subscription group usage failed", warningCode, resetCount)
		}
	}

	now := s.now().UTC()
	round.Status = SubscriberResetVoteStatusSucceeded
	round.CardStatus = "succeeded"
	round.SubscriptionResetStatus = "succeeded"
	round.SubscriptionResetCount = resetCount
	round.ErrorCode = ""
	round.ErrorMessage = ""
	round.WarningCode = warningCode
	round.CompletedAt = &now
	if dbErr := s.persistRoundOutcome(ctx, round); dbErr != nil {
		slog.Warn("subscriber_reset_vote_persist_succeeded_failed", "round_id", round.ID, "account_id", round.AccountID, "error", dbErr)
	}
	return round
}

func (s *CustomSubscriberResetVoteService) queryAccountsWithSubscriptionGroups(ctx context.Context, accountIDs []int64) (map[int64]bool, error) {
	result := make(map[int64]bool, len(accountIDs))
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT DISTINCT ag.account_id
		 FROM account_groups ag
		 JOIN groups g ON g.id = ag.group_id
		 WHERE ag.account_id = ANY($1)
		   AND g.deleted_at IS NULL
		   AND g.subscription_type = $2`,
		pq.Array(accountIDs),
		SubscriptionTypeSubscription,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var accID int64
		if scanErr := rows.Scan(&accID); scanErr != nil {
			return nil, scanErr
		}
		result[accID] = true
	}
	return result, rows.Err()
}

func (s *CustomSubscriberResetVoteService) queryEligibleAccountsForUser(ctx context.Context, userID int64, accountIDs []int64, now time.Time) (map[int64]bool, error) {
	result := make(map[int64]bool, len(accountIDs))
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT DISTINCT ag.account_id
		 FROM user_subscriptions us
		 JOIN account_groups ag ON ag.group_id = us.group_id
		 JOIN groups g ON g.id = us.group_id
		 WHERE us.user_id = $1
		   AND ag.account_id = ANY($2)
		   AND us.deleted_at IS NULL
		   AND g.deleted_at IS NULL
		   AND us.status = $3
		   AND us.starts_at <= $4
		   AND us.expires_at > $4
		   AND g.subscription_type = $5`,
		userID,
		pq.Array(accountIDs),
		StatusActive,
		now,
		SubscriptionTypeSubscription,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var accID int64
		if scanErr := rows.Scan(&accID); scanErr != nil {
			return nil, scanErr
		}
		result[accID] = true
	}
	return result, rows.Err()
}

func (s *CustomSubscriberResetVoteService) isUserEligibleInTx(ctx context.Context, tx *sql.Tx, userID, accountID int64, now time.Time) (bool, error) {
	var eligible bool
	err := tx.QueryRowContext(
		ctx,
		`SELECT EXISTS (
			SELECT 1
			FROM user_subscriptions us
			JOIN account_groups ag ON ag.group_id = us.group_id
			JOIN groups g ON g.id = us.group_id
			WHERE us.user_id = $1
			  AND ag.account_id = $2
			  AND us.deleted_at IS NULL
			  AND g.deleted_at IS NULL
			  AND us.status = $3
			  AND us.starts_at <= $4
			  AND us.expires_at > $4
			  AND g.subscription_type = $5
		)`,
		userID,
		accountID,
		StatusActive,
		now,
		SubscriptionTypeSubscription,
	).Scan(&eligible)
	return eligible, err
}

func (s *CustomSubscriberResetVoteService) queryLatestVoteRounds(ctx context.Context, userID int64, accountIDs []int64) (map[int64]*subscriberResetVoteRoundRow, error) {
	result := make(map[int64]*subscriberResetVoteRoundRow, len(accountIDs))
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT DISTINCT ON (r.account_id)
			r.id,
			r.account_id,
			r.threshold,
			r.status,
			r.request_id,
			r.initiated_at,
			r.expires_at,
			r.triggered_at,
			r.completed_at,
			r.card_status,
			r.subscription_reset_status,
			r.subscription_reset_count,
			r.error_code,
			r.error_message,
			r.warning_code,
			(SELECT COUNT(*) FROM custom_subscriber_reset_vote_voters v WHERE v.round_id = r.id) AS vote_count,
			CASE WHEN $2 > 0 THEN EXISTS (SELECT 1 FROM custom_subscriber_reset_vote_voters v WHERE v.round_id = r.id AND v.user_id = $2) ELSE FALSE END AS has_voted
		 FROM custom_subscriber_reset_vote_rounds r
		 WHERE r.account_id = ANY($1)
		 ORDER BY r.account_id, r.id DESC`,
		pq.Array(accountIDs),
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var row subscriberResetVoteRoundRow
		var triggeredAt, completedAt sql.NullTime
		if scanErr := rows.Scan(
			&row.ID,
			&row.AccountID,
			&row.Threshold,
			&row.Status,
			&row.RequestID,
			&row.InitiatedAt,
			&row.ExpiresAt,
			&triggeredAt,
			&completedAt,
			&row.CardStatus,
			&row.SubscriptionResetStatus,
			&row.SubscriptionResetCount,
			&row.ErrorCode,
			&row.ErrorMessage,
			&row.WarningCode,
			&row.VoteCount,
			&row.HasVoted,
		); scanErr != nil {
			return nil, scanErr
		}
		if triggeredAt.Valid {
			t := triggeredAt.Time
			row.TriggeredAt = &t
		}
		if completedAt.Valid {
			t := completedAt.Time
			row.CompletedAt = &t
		}
		result[row.AccountID] = &row
	}
	return result, rows.Err()
}

func (s *CustomSubscriberResetVoteService) loadLatestRoundForUpdate(ctx context.Context, tx *sql.Tx, accountID int64) (*subscriberResetVoteRoundRow, error) {
	var row subscriberResetVoteRoundRow
	var triggeredAt, completedAt sql.NullTime
	err := tx.QueryRowContext(
		ctx,
		`SELECT id, account_id, threshold, status, request_id, initiated_at, expires_at,
		        triggered_at, completed_at, card_status, subscription_reset_status,
		        subscription_reset_count, error_code, error_message, warning_code
		 FROM custom_subscriber_reset_vote_rounds
		 WHERE account_id = $1
		 ORDER BY id DESC
		 LIMIT 1
		 FOR UPDATE`,
		accountID,
	).Scan(
		&row.ID,
		&row.AccountID,
		&row.Threshold,
		&row.Status,
		&row.RequestID,
		&row.InitiatedAt,
		&row.ExpiresAt,
		&triggeredAt,
		&completedAt,
		&row.CardStatus,
		&row.SubscriptionResetStatus,
		&row.SubscriptionResetCount,
		&row.ErrorCode,
		&row.ErrorMessage,
		&row.WarningCode,
	)
	if err != nil {
		return nil, err
	}
	if triggeredAt.Valid {
		t := triggeredAt.Time
		row.TriggeredAt = &t
	}
	if completedAt.Valid {
		t := completedAt.Time
		row.CompletedAt = &t
	}
	return &row, nil
}

func (s *CustomSubscriberResetVoteService) markCardSucceeded(ctx context.Context, roundID int64) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE custom_subscriber_reset_vote_rounds
		 SET card_status = 'succeeded', updated_at = $2
		 WHERE id = $1 AND status = 'executing'`,
		roundID,
		s.now().UTC(),
	)
	return err
}

func (s *CustomSubscriberResetVoteService) persistRoundOutcome(ctx context.Context, round *subscriberResetVoteRoundRow) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE custom_subscriber_reset_vote_rounds
		 SET status = $2,
		     card_status = $3,
		     subscription_reset_status = $4,
		     subscription_reset_count = $5,
		     error_code = $6,
		     error_message = $7,
		     warning_code = $8,
		     completed_at = $9,
		     updated_at = $9
		 WHERE id = $1 AND status = 'executing'`,
		round.ID,
		round.Status,
		round.CardStatus,
		round.SubscriptionResetStatus,
		round.SubscriptionResetCount,
		round.ErrorCode,
		round.ErrorMessage,
		round.WarningCode,
		round.CompletedAt,
	)
	return err
}

func (s *CustomSubscriberResetVoteService) projectVoteRoundState(
	eligible bool,
	ineligibleReason string,
	round *subscriberResetVoteRoundRow,
	now time.Time,
) *SubscriberResetVoteState {
	if round == nil || !round.ExpiresAt.After(now) {
		return &SubscriberResetVoteState{
			Supported:        true,
			Eligible:         eligible,
			CanVote:          eligible,
			HasVoted:         false,
			IneligibleReason: ineligibleReason,
			Status:           SubscriberResetVoteStatusNone,
			VoteCount:        0,
			Threshold:        SubscriberResetVoteThreshold,
			CooldownActive:   false,
			CanInitiate:      eligible,
		}
	}

	threshold := round.Threshold
	if threshold <= 0 {
		threshold = SubscriberResetVoteThreshold
	}
	status := round.Status
	cardStatus := round.CardStatus
	errorCode := round.ErrorCode
	errorMessage := round.ErrorMessage
	if status == SubscriberResetVoteStatusExecuting &&
		round.TriggeredAt != nil &&
		now.Sub(*round.TriggeredAt) > subscriberVoteStaleExecutingTimeout &&
		cardStatus == "pending" {
		status = SubscriberResetVoteStatusFailed
		cardStatus = "unknown"
		errorCode = "VOTE_EXECUTION_TIMED_OUT"
		errorMessage = "Previous reset attempt timed out before upstream confirmation"
	}

	initiatedAt := round.InitiatedAt
	expiresAt := round.ExpiresAt
	cooldownActive := status != SubscriberResetVoteStatusOpen
	canVote := eligible && status == SubscriberResetVoteStatusOpen && !round.HasVoted

	return &SubscriberResetVoteState{
		Supported:               true,
		Eligible:                eligible,
		CanVote:                 canVote,
		HasVoted:                round.HasVoted,
		IneligibleReason:        ineligibleReason,
		RoundID:                 round.ID,
		Status:                  status,
		VoteCount:               round.VoteCount,
		Threshold:               threshold,
		InitiatedAt:             &initiatedAt,
		ExpiresAt:               &expiresAt,
		TriggeredAt:             round.TriggeredAt,
		CompletedAt:             round.CompletedAt,
		CardStatus:              cardStatus,
		SubscriptionResetStatus: round.SubscriptionResetStatus,
		SubscriptionResetCount:  round.SubscriptionResetCount,
		CooldownActive:          cooldownActive,
		CanInitiate:             false,
		ErrorCode:               errorCode,
		ErrorMessage:            errorMessage,
		WarningCode:             round.WarningCode,
	}
}
