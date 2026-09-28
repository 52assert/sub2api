package service

import (
	"bufio"
	"context"
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

func customCodexNaturalWindowDue(a *Account, now time.Time) (time.Time, bool) {
	if a == nil || a.Status != "active" || customResetIdentity(a) == "" {
		return time.Time{}, false
	}
	snapshot := customAccountSnapshot(a)
	return snapshot.WeeklyReset, snapshot.WeeklyMinutes == 10080 &&
		!snapshot.At.IsZero() && snapshot.WeeklyReset.After(snapshot.At) &&
		!snapshot.WeeklyReset.After(now)
}

func (s *CustomCodexResetService) probeNaturalWindow(ctx context.Context, id int64) error {
	a, err := s.eligible(ctx, id)
	if err != nil {
		return err
	}
	if _, due := customCodexNaturalWindowDue(a, time.Now()); !due {
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
	resetAt, due := customCodexNaturalWindowDue(a, time.Now())
	if !due {
		return nil
	}
	identity := customResetIdentity(a)
	// Header-derived reset timestamps can drift by a few seconds. The account
	// lock and five-minute tolerance deduplicate the same boundary across polls,
	// failures, restarts and instances, while allowing the next weekly boundary.
	result, err := conn.ExecContext(ctx, `INSERT INTO custom_codex_window_probes(account_id,identity,reset_at)
 SELECT $1,$2,$3 WHERE EXISTS(SELECT 1 FROM custom_codex_reset_policy WHERE account_id=$1 AND natural_probe_enabled AND natural_probe_enabled_at<=$3)
 AND NOT EXISTS(SELECT 1 FROM custom_codex_window_probes WHERE account_id=$1 AND identity=$2 AND reset_at>=$3::timestamptz-INTERVAL '5 minutes')
 ON CONFLICT DO NOTHING`, id, identity, resetAt)
	if err != nil {
		return err
	}
	claimed, err := result.RowsAffected()
	if err != nil || claimed == 0 {
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
	_, err = conn.ExecContext(stateCtx, `UPDATE custom_codex_window_probes SET status=$4 WHERE account_id=$1 AND identity=$2 AND reset_at=$3`, id, identity, resetAt, status)
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
