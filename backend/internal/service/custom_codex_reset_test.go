package service

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCustomCodexResetFeed(t *testing.T) {
	now := time.Date(2026, 9, 27, 1, 0, 0, 0, time.UTC)
	record, err := parseCustomResetRecord(strings.NewReader(`{"ok":true,"data":{"id":"e1","kind":"reset_completed","resetType":"global","announcedAt":"2026-09-26T18:17:54.000Z","scope":{"plans":["all"],"windows":["unknown"]}}}`), now)
	require.NoError(t, err)
	sh := time.FixedZone("Asia/Shanghai", 8*60*60)
	require.Equal(t, "2026-09-27 02:17:54", record.AnnouncedAt.In(sh).Format("2006-01-02 15:04:05"))
	record, err = parseCustomResetRecord(strings.NewReader(`{"ok":true,"data":null}`), now)
	require.NoError(t, err)
	require.Nil(t, record)
	for _, body := range []string{`{"ok":true,"data":{"id":"e1","kind":"reset_completed","resetType":"global"}}`, `{"ok":true,"data":{"id":"e1","kind":"reset_completed","resetType":"global","announcedAt":"2026-09-26T18:17:54Z","scope":{"plans":["all"],"windows":["5h"]}}}`} {
		record, err = parseCustomResetRecord(strings.NewReader(body), now)
		require.NoError(t, err)
		require.Nil(t, record)
	}
	for _, body := range []string{`{"ok":false}`, `{"ok":true,"data":{"id":"e1","kind":"reset_completed","resetType":"global","announcedAt":"2026-09-28T00:00:00Z"}}`, strings.Repeat("x", 128*1024+1)} {
		_, err = parseCustomResetRecord(strings.NewReader(body), now)
		require.Error(t, err)
	}
	record, err = parseCustomResetRecord(strings.NewReader(`{"ok":true,"data":{"id":"e1","kind":"reset_completed","resetType":"global","announcedAt":"2026-09-26T18:17:54Z","scope":{"plans":["pro"]}}}`), now)
	require.NoError(t, err)
	require.Nil(t, record)
}
func TestCustomCodexResetEvidence(t *testing.T) {
	now := time.Now()
	announced := now.Add(-time.Minute)
	high, low := 83.0, 0.5
	before := customQuotaSnapshot{Identity: "same-account", At: announced.Add(-time.Hour), Weekly: &high, WeeklyMinutes: 10080, WeeklyReset: now.Add(48 * time.Hour)}
	after := customQuotaSnapshot{Identity: "same-account", At: now, Weekly: &low, WeeklyMinutes: 10080}
	require.Equal(t, "confirmed", customResetEvidence(before, after, announced, now))
	cases := []struct {
		name, want string
		alter      func(*customQuotaSnapshot, *customQuotaSnapshot)
	}{
		{"changed upstream account", "account_identity_changed", func(_, a *customQuotaSnapshot) { a.Identity = "different-account" }},
		{"pre-event snapshot", "stale_snapshot", func(_, a *customQuotaSnapshot) { a.At = announced }},
		{"stale snapshot", "stale_snapshot", func(_, a *customQuotaSnapshot) { a.At = now.Add(-6 * time.Minute) }},
		{"missing weekly", "missing_weekly_window", func(_, a *customQuotaSnapshot) { a.Weekly = nil }},
		{"zero-length weekly", "missing_weekly_window", func(_, a *customQuotaSnapshot) { a.WeeklyMinutes = 0 }},
		{"high usage", "usage_not_near_zero", func(_, a *customQuotaSnapshot) { a.Weekly = &high }},
		{"unknown baseline", "missing_baseline", func(b, _ *customQuotaSnapshot) { b.At = time.Time{} }},
		{"unknown baseline window", "missing_baseline", func(b, _ *customQuotaSnapshot) { b.WeeklyMinutes = 0 }},
		{"no drop", "no_observed_drop", func(b, _ *customQuotaSnapshot) { b.Weekly = &low }},
		{"natural expiry", "natural_reset_possible", func(b, _ *customQuotaSnapshot) { b.WeeklyReset = now.Add(-time.Second) }},
		{"5h not reset", "usage_not_near_zero", func(_, a *customQuotaSnapshot) { a.FiveHourMinutes = 300; a.FiveHour = &high }},
		{"absent secondary is not evidence", "confirmed", func(_, a *customQuotaSnapshot) { a.FiveHourMinutes = 0; a.FiveHour = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, a := before, after
			tc.alter(&b, &a)
			require.Equal(t, tc.want, customResetEvidence(b, a, announced, now))
		})
	}
}
func TestCustomCodexResetBackoff(t *testing.T) {
	now := time.Now()
	require.Equal(t, time.Minute, customCodexResetInterval)
	require.Equal(t, 30*time.Minute, customResetRetryDelay(20, "", now))
	require.Equal(t, 2*time.Hour, customResetRetryDelay(1, "7200", now))
	require.GreaterOrEqual(t, customResetRetryDelay(1, now.Add(time.Hour).UTC().Format(http.TimeFormat), now), 59*time.Minute)
}
