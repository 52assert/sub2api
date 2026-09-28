package service

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
)

const customCodexResetProbeModel = "gpt-6-astra"
const customCodexResetProbePrompt = "Reply with exactly OK"

// Natural weekly rollover needs no announcement and never authorizes an
// exceptional subscription refund. It only starts the next upstream window.
func (s *CustomCodexResetService) probeNaturalWindows(ctx context.Context) error {
	if s.probe == nil {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT account_id FROM custom_codex_reset_policy WHERE natural_probe_enabled ORDER BY account_id`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.probeNaturalWindow(ctx, id); err != nil {
			slog.Warn("custom_codex_natural_window_probe_failed", "account_id", id, "error", err)
		}
	}
	return nil
}

func customCodexWindowProbeBoundary(a *Account, now time.Time) (string, *time.Time) {
	if a == nil || a.Status != "active" || customResetIdentity(a) == "" {
		return "", nil
	}
	snapshot := customAccountSnapshot(a)
	if snapshot.WeeklyMinutes != 10080 || snapshot.At.IsZero() || !snapshot.WeeklyReset.After(snapshot.At) {
		// Absence of a window is unknown, not evidence that a reset happened.
		// The opt-in permits one initialization attempt per activation instead.
		return "bootstrap", nil
	}
	if snapshot.WeeklyReset.After(now) {
		return "", nil
	}
	return "weekly", &snapshot.WeeklyReset
}

func (s *CustomCodexResetService) probeNaturalWindow(ctx context.Context, id int64) error {
	a, err := s.eligible(ctx, id)
	if err != nil {
		return err
	}
	if kind, _ := customCodexWindowProbeBoundary(a, time.Now()); kind == "" {
		return nil
	}
	conn, release, err := customResetAccountLock(ctx, s.db, id)
	if err != nil {
		return err
	}
	defer release()
	// Re-read after locking: traffic or another worker may have opened a window.
	a, err = s.eligible(ctx, id)
	if err != nil {
		return err
	}
	kind, boundary := customCodexWindowProbeBoundary(a, time.Now())
	if kind == "" {
		return nil
	}
	identity := customResetIdentity(a)
	// Header-derived reset timestamps can drift by a few seconds. The account
	// lock and five-minute tolerance deduplicate the same boundary across polls,
	// failures, restarts and instances, while allowing the next weekly boundary.
	var resetAt time.Time
	err = conn.QueryRowContext(ctx, `INSERT INTO custom_codex_window_probes(account_id,identity,kind,reset_at)
 SELECT $1,$2,$3,COALESCE($4::timestamptz,p.natural_probe_enabled_at)
 FROM custom_codex_reset_policy p WHERE p.account_id=$1 AND p.natural_probe_enabled AND p.natural_probe_enabled_at IS NOT NULL
 AND ($3='bootstrap' OR p.natural_probe_enabled_at<=$4)
 AND NOT EXISTS(SELECT 1 FROM custom_codex_window_probes w WHERE w.account_id=$1 AND w.identity=$2 AND w.kind=$3
 AND (($3='bootstrap' AND w.reset_at=p.natural_probe_enabled_at) OR ($3='weekly' AND w.reset_at>=$4::timestamptz-INTERVAL '5 minutes')))
 ON CONFLICT DO NOTHING RETURNING reset_at`, id, identity, kind, boundary).Scan(&resetAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	updates, probeErr := s.probe(ctx, a)
	status := "succeeded"
	if probeErr != nil {
		status = "failed"
	} else {
		mergeAccountExtra(a, updates)
	}
	// Preserve the outcome even when the upstream timeout consumed the batch.
	stateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = conn.ExecContext(stateCtx, `UPDATE custom_codex_window_probes SET status=$5 WHERE account_id=$1 AND identity=$2 AND kind=$3 AND reset_at=$4`, id, identity, kind, resetAt, status)
	if probeErr != nil {
		return probeErr
	}
	return err
}

func (s *AccountUsageService) probeCustomCodexReset(ctx context.Context, account *Account) (map[string]any, error) {
	payload := createOpenAITestPayload(customCodexResetProbeModel, true)
	payload["instructions"] = customCodexResetProbePrompt
	payload["input"] = []map[string]any{{
		"role":    "user",
		"content": []map[string]any{{"type": "input_text", "text": customCodexResetProbePrompt}},
	}}
	return s.probeOpenAICodexSnapshotWithPayload(ctx, account, payload, true)
}

// Wait for the terminal event, but never retain or log generated content.
func consumeCustomCodexResetProbe(body io.Reader) error {
	scanner := bufio.NewScanner(io.LimitReader(body, 1024*1024))
	scanner.Buffer(make([]byte, 4096), 256*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var event struct {
			Type     string `json:"type"`
			Response struct {
				Status string `json:"status"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return errors.New("invalid codex reset probe stream")
		}
		switch event.Type {
		case "response.completed", "response.done":
			if event.Response.Status == "" || event.Response.Status == "completed" {
				return nil
			}
			return errors.New("codex reset probe did not complete")
		case "error", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
			return errors.New("codex reset probe failed")
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read codex reset probe: %w", err)
	}
	return errors.New("codex reset probe ended without completion")
}
