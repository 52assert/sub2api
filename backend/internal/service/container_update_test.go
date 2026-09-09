//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type containerUpdateRoundTripper func(*http.Request) (*http.Response, error)

func (f containerUpdateRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func customServiceForTest(t *testing.T, release ContainerRelease, onPost func(*http.Request)) *UpdateService {
	t.Helper()
	previous := CustomBuildRevision
	CustomBuildRevision = strings.Repeat("a", 40)
	t.Cleanup(func() { CustomBuildRevision = previous })
	svc := NewUpdateService(nil, nil, "0.2.3", "custom")
	svc.containerUpdater = &containerUpdateClient{client: &http.Client{
		Transport: containerUpdateRoundTripper(func(req *http.Request) (*http.Response, error) {
			writer := httptest.NewRecorder()
			if req.Method == http.MethodPost {
				require.Equal(t, "/v1/jobs", req.URL.Path)
				if onPost != nil {
					onPost(req)
				}
				writer.WriteHeader(http.StatusAccepted)
				require.NoError(t, json.NewEncoder(writer).Encode(ContainerUpdateJob{ID: "task", Stage: "queued"}))
			} else if req.URL.Path == "/v1/jobs/latest" {
				require.NoError(t, json.NewEncoder(writer).Encode(ContainerUpdateJob{ID: "task", Stage: "succeeded"}))
			} else {
				require.Equal(t, "/v1/release", req.URL.Path)
				require.NoError(t, json.NewEncoder(writer).Encode(release))
			}
			return writer.Result(), nil
		}),
	}}
	return svc
}

func TestContainerUpdateDetectsNewBuildWithoutChangingVisibleVersion(t *testing.T) {
	release := ContainerRelease{Version: "0.2.3", Revision: strings.Repeat("b", 40), Image: "ghcr.io/52assert/sub2api@sha256:" + strings.Repeat("c", 64)}
	svc := customServiceForTest(t, release, nil)
	info, err := svc.CheckUpdate(context.Background(), true)
	require.NoError(t, err)
	require.True(t, info.HasUpdate)
	require.Equal(t, "0.2.3", info.CurrentVersion)
	require.Equal(t, "0.2.3", info.LatestVersion)
	require.Equal(t, "container", info.UpdateMode)
	require.Equal(t, release.Image, info.ContainerImage)
}

func TestContainerUpdateDoesNotDowngradeOrReinstallCurrentBuild(t *testing.T) {
	for _, release := range []ContainerRelease{
		{Version: "0.2.3", Revision: strings.Repeat("a", 40)},
		{Version: "0.2.2", Revision: strings.Repeat("b", 40)},
	} {
		t.Run(release.Version, func(t *testing.T) {
			svc := customServiceForTest(t, release, nil)
			info, err := svc.CheckUpdate(context.Background(), false)
			require.NoError(t, err)
			require.False(t, info.HasUpdate)
		})
	}
}

func TestContainerUpdateOnlyEnqueuesReviewedImageAndReportsDurableStatus(t *testing.T) {
	release := ContainerRelease{Version: "0.2.4", Revision: strings.Repeat("b", 40), Image: "ghcr.io/52assert/sub2api@sha256:" + strings.Repeat("c", 64)}
	posts := 0
	svc := customServiceForTest(t, release, func(req *http.Request) {
		posts++
		var body map[string]string
		require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
		require.Equal(t, map[string]string{"image": release.Image}, body)
	})
	_, err := svc.StartContainerUpdate(context.Background(), "different-image")
	require.Error(t, err)
	require.Zero(t, posts)
	job, err := svc.StartContainerUpdate(context.Background(), release.Image)
	require.NoError(t, err)
	require.Equal(t, "queued", job.Stage)
	require.Equal(t, 1, posts)
	job, err = svc.ContainerUpdateStatus(context.Background())
	require.NoError(t, err)
	require.Equal(t, "succeeded", job.Stage)
	// The old binary replacement entry point stays disabled for custom builds.
	require.ErrorIs(t, svc.PerformUpdate(context.Background()), ErrCustomBuildUpdateDisabled)
}

func TestContainerUpdaterUnavailableNeverFallsBackToOfficialRelease(t *testing.T) {
	t.Setenv("SUB2API_UPDATER_SOCKET", "")
	svc := NewUpdateService(nil, nil, "0.2.3", "custom")
	info, err := svc.CheckUpdate(context.Background(), true)
	require.NoError(t, err)
	require.False(t, info.HasUpdate)
	require.NotEmpty(t, info.Warning)
	_, err = svc.StartContainerUpdate(context.Background(), "anything")
	require.Error(t, err)
}
