package service

import (
	"context"
	"errors"
	"net/http"
	"time"
)

type customResetProbeKey struct{}
type customResetProbeCapture struct{ snapshot customQuotaSnapshot }

func customResetProbe(ctx context.Context) *customResetProbeCapture {
	v, _ := ctx.Value(customResetProbeKey{}).(*customResetProbeCapture)
	return v
}
func (s *AccountTestService) probeCustomCodexReset(ctx context.Context, id int64) (customQuotaSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	capture := &customResetProbeCapture{}
	ctx = context.WithValue(ctx, customResetProbeKey{}, capture)
	result, err := s.RunTestBackground(ctx, id, "gpt-6-astra")
	if err != nil {
		return customQuotaSnapshot{}, err
	}
	if result.Status != "success" {
		return customQuotaSnapshot{}, errors.New("gpt-6-astra medium probe failed")
	}
	if capture.snapshot.At.IsZero() {
		return customQuotaSnapshot{}, errors.New("probe did not return quota headers")
	}
	return capture.snapshot, nil
}
func captureCustomResetHeaders(ctx context.Context, header http.Header, account *Account) {
	if capture := customResetProbe(ctx); capture != nil {
		if snap := ParseCodexRateLimitHeaders(header); snap != nil {
			capture.snapshot = customSnapshot(buildCodexUsageExtraUpdates(snap, time.Now()))
			capture.snapshot.Identity = customResetIdentity(account)
		}
	}
}
