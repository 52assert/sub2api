package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// CustomBuildRevision is set by the custom image build without changing the
// user-visible upstream version (for example, 0.2.3).
var CustomBuildRevision = ""

type containerUpdateClient struct {
	client *http.Client
}

type ContainerRelease struct {
	Version     string `json:"version"`
	Revision    string `json:"revision"`
	Image       string `json:"image"`
	ReleaseURL  string `json:"release_url"`
	PublishedAt string `json:"published_at"`
	Notes       string `json:"notes"`
}

type ContainerUpdateJob struct {
	ID        string `json:"id"`
	Stage     string `json:"stage"`
	Message   string `json:"message"`
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	UpdatedAt int64  `json:"updated_at"`
}

func newContainerUpdateClient() *containerUpdateClient {
	path := strings.TrimSpace(os.Getenv("SUB2API_UPDATER_SOCKET"))
	if path == "" {
		return nil
	}
	return &containerUpdateClient{client: &http.Client{
		Timeout: 75 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", path)
			},
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     30 * time.Second,
		},
	}}
}

func (u *containerUpdateClient) request(ctx context.Context, method, path string, input, output any) error {
	if u == nil {
		return infraerrors.ServiceUnavailable("CONTAINER_UPDATER_UNAVAILABLE", "尚未连接容器更新服务")
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://updater"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := u.client.Do(req)
	if err != nil {
		return infraerrors.ServiceUnavailable("CONTAINER_UPDATER_UNAVAILABLE", "无法连接容器更新服务，请稍后重试")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return infraerrors.ServiceUnavailable("CONTAINER_UPDATE_REJECTED", "更新服务暂时无法处理请求，请刷新版本信息或检查之前的任务")
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 3*1024*1024)).Decode(output); err != nil {
		return fmt.Errorf("decode container update response: %w", err)
	}
	return nil
}

func (s *UpdateService) IsContainerBuild() bool {
	return s.buildType == "custom"
}

func (s *UpdateService) checkContainerUpdate(ctx context.Context, force bool) (*UpdateInfo, error) {
	info := &UpdateInfo{
		CurrentVersion: s.currentVersion,
		LatestVersion:  s.currentVersion,
		BuildType:      s.buildType,
		UpdateMode:     "container",
		Revision:       CustomBuildRevision,
	}
	path := "/v1/release"
	if force {
		path += "?force=true"
	}
	var release ContainerRelease
	if err := s.containerUpdater.request(ctx, http.MethodGet, path, nil, &release); err != nil {
		info.Warning = err.Error()
		return info, nil
	}
	info.LatestVersion = release.Version
	info.LatestRevision = release.Revision
	info.ContainerImage = release.Image
	comparison := compareVersions(s.currentVersion, release.Version)
	info.HasUpdate = comparison < 0 || (comparison == 0 && release.Revision != CustomBuildRevision)
	info.ReleaseInfo = &ReleaseInfo{
		Name:        "Sub2API " + release.Version,
		Body:        release.Notes,
		HTMLURL:     release.ReleaseURL,
		PublishedAt: release.PublishedAt,
	}
	return info, nil
}

func (s *UpdateService) StartContainerUpdate(ctx context.Context, image string) (*ContainerUpdateJob, error) {
	if !s.IsContainerBuild() {
		return nil, ErrCustomBuildUpdateDisabled
	}
	info, err := s.checkContainerUpdate(ctx, true)
	if err != nil {
		return nil, err
	}
	if info.Warning != "" {
		return nil, infraerrors.ServiceUnavailable("CONTAINER_UPDATER_UNAVAILABLE", info.Warning)
	}
	if !info.HasUpdate {
		return nil, ErrNoUpdateAvailable
	}
	if image == "" || image != info.ContainerImage {
		return nil, infraerrors.Conflict("CONTAINER_RELEASE_CHANGED", "可用版本已变化，请刷新后重新更新")
	}
	var job ContainerUpdateJob
	if err := s.containerUpdater.request(ctx, http.MethodPost, "/v1/jobs", map[string]string{"image": image}, &job); err != nil {
		return nil, err
	}
	return &job, nil
}

func (s *UpdateService) ContainerUpdateStatus(ctx context.Context) (*ContainerUpdateJob, error) {
	if !s.IsContainerBuild() {
		return nil, ErrCustomBuildUpdateDisabled
	}
	var job *ContainerUpdateJob
	err := s.containerUpdater.request(ctx, http.MethodGet, "/v1/jobs/latest", nil, &job)
	return job, err
}
