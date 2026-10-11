package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/require"
)

func TestNormalizePasskeyName(t *testing.T) {
	require.Equal(t, defaultPasskeyName, normalizePasskeyName("   "))
	require.Equal(t, "Laptop", normalizePasskeyName("  Laptop  "))

	longName := strings.Repeat("密", maxPasskeyNameLength+10)
	require.Len(t, []rune(normalizePasskeyName(longName)), maxPasskeyNameLength)
}

func TestPasskeySummaryReportsCurrentBackupState(t *testing.T) {
	record := &PasskeyCredentialRecord{
		Credential: webauthn.Credential{
			Flags: webauthn.CredentialFlags{BackupEligible: true},
		},
	}
	require.False(t, passkeySummary(record).Backup)

	record.Credential.Flags.BackupState = true
	require.True(t, passkeySummary(record).Backup)
}

// 桩仅实现测试所需方法；未桩方法调用即 panic（嵌入 nil 接口）。
type passkeyPwUserRepoStub struct {
	UserRepository
	user *User
}

func (s *passkeyPwUserRepoStub) GetByID(context.Context, int64) (*User, error) {
	return s.user, nil
}

type passkeyPwRepoStub struct {
	PasskeyRepository
	handleCalled bool
	deleteCalled bool
}

func (s *passkeyPwRepoStub) EnsureUserHandle(_ context.Context, _ int64, candidate []byte) ([]byte, error) {
	s.handleCalled = true
	return candidate, nil
}

func (s *passkeyPwRepoStub) ListByUserID(context.Context, int64) ([]PasskeyCredentialRecord, error) {
	return nil, nil
}

func (s *passkeyPwRepoStub) Delete(context.Context, int64, int64) error {
	s.deleteCalled = true
	return nil
}

type passkeyPwSessionStoreStub struct {
	PasskeySessionStore
}

func (s *passkeyPwSessionStoreStub) Store(context.Context, *PasskeySession, time.Duration) (string, error) {
	return "session-token", nil
}

func newPasskeyPwService(t *testing.T, user *User) (*PasskeyService, *passkeyPwRepoStub) {
	t.Helper()
	repo := &passkeyPwRepoStub{}
	svc, err := NewPasskeyService(&config.Config{WebAuthn: config.WebAuthnConfig{
		Enabled:       true,
		RPDisplayName: "Sub2API",
		RPID:          "sub2api.example.com",
		RPOrigins:     []string{"https://sub2api.example.com"},
	}}, repo, &passkeyPwSessionStoreStub{}, &passkeyPwUserRepoStub{user: user})
	require.NoError(t, err)
	return svc, repo
}

// 注册与吊销必须验证账号密码：被窃会话不得静默添加/移除凭据。
// 用密码而非 TOTP step-up，保证未配置 TOTP 加密密钥的部署同样受保护。
func TestPasskeyEnrollmentAndRevocationRequireAccountPassword(t *testing.T) {
	user := &User{ID: 7, Email: "user@example.com", Status: StatusActive}
	require.NoError(t, user.SetPassword("correct-password"))
	svc, repo := newPasskeyPwService(t, user)

	_, _, err := svc.BeginRegistration(context.Background(), user.ID, "")
	require.ErrorIs(t, err, ErrPasswordRequired)
	_, _, err = svc.BeginRegistration(context.Background(), user.ID, "wrong-password")
	require.ErrorIs(t, err, ErrPasswordIncorrect)
	require.False(t, repo.handleCalled)

	creation, token, err := svc.BeginRegistration(context.Background(), user.ID, "correct-password")
	require.NoError(t, err)
	require.NotNil(t, creation)
	require.Equal(t, "session-token", token)
	require.True(t, repo.handleCalled)

	err = svc.Delete(context.Background(), user.ID, 1, "")
	require.ErrorIs(t, err, ErrPasswordRequired)
	err = svc.Delete(context.Background(), user.ID, 1, "wrong-password")
	require.ErrorIs(t, err, ErrPasswordIncorrect)
	require.False(t, repo.deleteCalled)

	require.NoError(t, svc.Delete(context.Background(), user.ID, 1, "correct-password"))
	require.True(t, repo.deleteCalled)
}

type passkeyLoginFailureRepoStub struct {
	PasskeyRepository
	credential    *PasskeyCredentialRecord
	credentialErr error
	listErr       error
}

func (s *passkeyLoginFailureRepoStub) GetByCredentialID(context.Context, []byte) (*PasskeyCredentialRecord, error) {
	return s.credential, s.credentialErr
}

func (s *passkeyLoginFailureRepoStub) ListByUserID(context.Context, int64) ([]PasskeyCredentialRecord, error) {
	return nil, s.listErr
}

type passkeyLoginFailureUserRepoStub struct {
	UserRepository
	user *User
	err  error
}

func (s *passkeyLoginFailureUserRepoStub) GetByID(context.Context, int64) (*User, error) {
	return s.user, s.err
}

type passkeyLoginFailureSessionStub struct {
	PasskeySessionStore
}

func (*passkeyLoginFailureSessionStub) Consume(context.Context, string) (*PasskeySession, error) {
	return &PasskeySession{Kind: passkeySessionLogin}, nil
}

func TestPasskeyLoginDoesNotTreatStorageOutagesAsInvalidCredentials(t *testing.T) {
	storageErr := errors.New("database unavailable")
	for _, test := range []struct {
		name          string
		credentialErr error
		userErr       error
		listErr       error
		want          error
	}{
		{name: "credential lookup outage", credentialErr: storageErr, want: storageErr},
		{name: "user lookup outage", userErr: storageErr, want: storageErr},
		{name: "credential list outage", listErr: storageErr, want: storageErr},
		{name: "unknown credential", credentialErr: ErrPasskeyNotFound, want: ErrPasskeyVerify},
		{name: "unknown user", userErr: ErrUserNotFound, want: ErrPasskeyVerify},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &passkeyLoginFailureRepoStub{
				credential:    &PasskeyCredentialRecord{UserID: 7, UserHandle: []byte{4, 5, 6}},
				credentialErr: test.credentialErr,
				listErr:       test.listErr,
			}
			svc, err := NewPasskeyService(&config.Config{WebAuthn: config.WebAuthnConfig{
				Enabled:       true,
				RPDisplayName: "Sub2API",
				RPID:          "sub2api.example.com",
				RPOrigins:     []string{"https://sub2api.example.com"},
			}}, repo, &passkeyLoginFailureSessionStub{}, &passkeyLoginFailureUserRepoStub{
				user: &User{ID: 7, Email: "user@example.com", Status: StatusActive},
				err:  test.userErr,
			})
			require.NoError(t, err)

			// A parseable assertion reaches the repository lookup before
			// signature validation, allowing failures at every storage step.
			body, err := json.Marshal(map[string]any{
				"id": "AQID", "rawId": "AQID", "type": "public-key",
				"response": map[string]string{
					"authenticatorData": base64.RawURLEncoding.EncodeToString(make([]byte, 37)),
					"clientDataJSON": base64.RawURLEncoding.EncodeToString([]byte(
						`{"type":"webauthn.get","challenge":"test","origin":"https://sub2api.example.com"}`)),
					"signature": "Bw", "userHandle": "BAUG",
				},
			})
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(string(body)))
			user, err := svc.FinishLogin(context.Background(), "session-token", req)
			require.Nil(t, user)
			require.ErrorIs(t, err, test.want)
			if test.want == storageErr {
				require.NotErrorIs(t, err, ErrPasskeyVerify)
			}
		})
	}
}
