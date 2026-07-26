package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func newCodexModelsTestAccount() *Account {
	return &Account{
		ID:          1,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"access_token":       "test-access-token",
			"chatgpt_account_id": "acc-123",
		},
	}
}

func useCodexModelsTestURL(t *testing.T, rawURL string) {
	t.Helper()
	original := chatgptCodexModelsURL
	chatgptCodexModelsURL = rawURL
	t.Cleanup(func() { chatgptCodexModelsURL = original })
}

func TestFetchCodexModelsManifestPassthrough(t *testing.T) {
	manifestBody := `{"models":[{"slug":"gpt-5.6-sol","display_name":"GPT-5.6 Sol"}]}`

	var gotAuth, gotAccountID, gotOriginator, gotVersion, gotUserAgent, gotClientVersion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccountID = r.Header.Get("chatgpt-account-id")
		gotOriginator = r.Header.Get("Originator")
		gotVersion = r.Header.Get("Version")
		gotUserAgent = r.Header.Get("User-Agent")
		gotClientVersion = r.URL.Query().Get("client_version")
		w.Header().Set("ETag", `W/"abc123"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(manifestBody))
	}))
	defer server.Close()
	useCodexModelsTestURL(t, server.URL)

	manifest, err := (&OpenAIGatewayService{}).FetchCodexModelsManifest(context.Background(), newCodexModelsTestAccount(), " 0.137.0 ", "")
	require.NoError(t, err)
	require.Equal(t, manifestBody, string(manifest.Body))
	require.Equal(t, `W/"abc123"`, manifest.ETag)
	require.Equal(t, "Bearer test-access-token", gotAuth)
	require.Equal(t, "acc-123", gotAccountID)
	require.Equal(t, "codex_cli_rs", gotOriginator)
	require.Equal(t, "0.137.0", gotVersion)
	require.Equal(t, codexCLIUserAgent, gotUserAgent)
	require.Equal(t, "0.137.0", gotClientVersion)
	require.NotContains(t, string(manifest.Body), "test-access-token")
}

func TestFetchCodexModelsManifestDefaultClientVersion(t *testing.T) {
	var gotClientVersion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClientVersion = r.URL.Query().Get("client_version")
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer server.Close()
	useCodexModelsTestURL(t, server.URL)

	_, err := (&OpenAIGatewayService{}).FetchCodexModelsManifest(context.Background(), newCodexModelsTestAccount(), "", "")
	require.NoError(t, err)
	require.Equal(t, openAICodexProbeVersion, gotClientVersion)
}

func TestFetchCodexModelsManifestNotModified(t *testing.T) {
	var gotIfNoneMatch string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIfNoneMatch = r.Header.Get("If-None-Match")
		w.Header().Set("ETag", `W/"abc123"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()
	useCodexModelsTestURL(t, server.URL)

	manifest, err := (&OpenAIGatewayService{}).FetchCodexModelsManifest(context.Background(), newCodexModelsTestAccount(), "0.137.0", ` W/"abc123" `)
	require.NoError(t, err)
	require.True(t, manifest.NotModified)
	require.Empty(t, manifest.Body)
	require.Equal(t, `W/"abc123"`, manifest.ETag)
	require.Equal(t, `W/"abc123"`, gotIfNoneMatch)
}

func TestFetchCodexModelsManifestUpstreamErrorDoesNotLeakCredentials(t *testing.T) {
	const token = "test-access-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"reflected `+token+`"}`, http.StatusInternalServerError)
	}))
	defer server.Close()
	useCodexModelsTestURL(t, server.URL)

	_, err := (&OpenAIGatewayService{}).FetchCodexModelsManifest(context.Background(), newCodexModelsTestAccount(), "0.137.0", "")
	require.Error(t, err)
	require.Equal(t, http.StatusBadGateway, infraerrors.Code(err))
	require.NotContains(t, infraerrors.Message(err), token)
	require.NotContains(t, err.Error(), token)
}

func TestFetchCodexModelsManifestRejectsMissingOrNonOAuthCredentials(t *testing.T) {
	t.Run("missing token", func(t *testing.T) {
		account := newCodexModelsTestAccount()
		delete(account.Credentials, "access_token")
		_, err := (&OpenAIGatewayService{}).FetchCodexModelsManifest(context.Background(), account, "0.137.0", "")
		require.Error(t, err)
		require.Equal(t, http.StatusBadGateway, infraerrors.Code(err))
	})

	t.Run("API key account", func(t *testing.T) {
		account := newCodexModelsTestAccount()
		account.Type = AccountTypeAPIKey
		account.Credentials = map[string]any{"api_key": "sk-must-not-be-used"}
		_, err := (&OpenAIGatewayService{}).FetchCodexModelsManifest(context.Background(), account, "0.137.0", "")
		require.Error(t, err)
		require.Equal(t, http.StatusBadGateway, infraerrors.Code(err))
		require.NotContains(t, err.Error(), "sk-must-not-be-used")
	})
}

func TestFetchCodexModelsManifestRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", int(codexModelsManifestBodyLimit)+1)))
	}))
	defer server.Close()
	useCodexModelsTestURL(t, server.URL)

	_, err := (&OpenAIGatewayService{}).FetchCodexModelsManifest(context.Background(), newCodexModelsTestAccount(), "0.137.0", "")
	require.Error(t, err)
	require.Equal(t, "OPENAI_CODEX_MODELS_RESPONSE_TOO_LARGE", infraerrors.Reason(err))
}

type codexModelsAccountRepoStub struct {
	AccountRepository
	accounts []Account
}

func (s *codexModelsAccountRepoStub) ListSchedulableByGroupIDAndPlatform(_ context.Context, _ int64, platform string) ([]Account, error) {
	result := make([]Account, 0, len(s.accounts))
	for _, account := range s.accounts {
		if account.Platform == platform {
			result = append(result, account)
		}
	}
	return result, nil
}

func TestSelectAccountForCodexModelsManifestSkipsAPIKeyAccounts(t *testing.T) {
	apiKeyAccount := *newCodexModelsTestAccount()
	apiKeyAccount.ID = 1
	apiKeyAccount.Type = AccountTypeAPIKey
	apiKeyAccount.Priority = 0
	apiKeyAccount.Credentials = map[string]any{"api_key": "sk-test"}

	oauthAccount := *newCodexModelsTestAccount()
	oauthAccount.ID = 2
	oauthAccount.Priority = 1

	service := &OpenAIGatewayService{accountRepo: &codexModelsAccountRepoStub{accounts: []Account{apiKeyAccount, oauthAccount}}}
	groupID := int64(9)
	selected, err := service.SelectAccountForCodexModelsManifest(context.Background(), &groupID)
	require.NoError(t, err)
	require.Equal(t, oauthAccount.ID, selected.ID)
}

func TestFetchCodexModelsManifestWithFailoverUsesSecondAccount(t *testing.T) {
	tests := []struct {
		name      string
		failFirst func(http.ResponseWriter)
	}{
		{
			name: "upstream HTTP 500",
			failFirst: func(w http.ResponseWriter) {
				http.Error(w, `{"detail":"must not leak"}`, http.StatusInternalServerError)
			},
		},
		{
			name: "network failure",
			failFirst: func(w http.ResponseWriter) {
				hijacker, ok := w.(http.Hijacker)
				if !ok {
					http.Error(w, "hijacking unavailable", http.StatusInternalServerError)
					return
				}
				conn, _, err := hijacker.Hijack()
				if err != nil {
					return
				}
				_ = conn.Close()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				if r.Header.Get("Authorization") == "Bearer first-token" {
					tt.failFirst(w)
					return
				}
				w.Header().Set("ETag", `W/"second"`)
				_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-5.6-sol"}]}`))
			}))
			defer server.Close()
			useCodexModelsTestURL(t, server.URL)

			first := *newCodexModelsTestAccount()
			first.ID = 1
			first.Priority = 0
			first.Credentials["access_token"] = "first-token"
			second := *newCodexModelsTestAccount()
			second.ID = 2
			second.Priority = 1
			second.Credentials["access_token"] = "second-token"

			svc := &OpenAIGatewayService{accountRepo: &codexModelsAccountRepoStub{accounts: []Account{first, second}}}
			groupID := int64(9)
			manifest, err := svc.FetchCodexModelsManifestWithFailover(context.Background(), &groupID, "0.137.0", "", 1)
			require.NoError(t, err)
			require.Equal(t, int32(2), attempts.Load())
			require.Equal(t, `W/"second"`, manifest.ETag)
			require.JSONEq(t, `{"models":[{"slug":"gpt-5.6-sol"}]}`, string(manifest.Body))
		})
	}
}

func TestFetchCodexModelsManifestWithFailoverPreservesNotModified(t *testing.T) {
	var attempts atomic.Int32
	var gotIfNoneMatch atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if r.Header.Get("Authorization") == "Bearer first-token" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		gotIfNoneMatch.Store(r.Header.Get("If-None-Match"))
		w.Header().Set("ETag", `W/"cached"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()
	useCodexModelsTestURL(t, server.URL)

	first := *newCodexModelsTestAccount()
	first.ID = 1
	first.Priority = 0
	first.Credentials["access_token"] = "first-token"
	second := *newCodexModelsTestAccount()
	second.ID = 2
	second.Priority = 1
	second.Credentials["access_token"] = "second-token"

	svc := &OpenAIGatewayService{accountRepo: &codexModelsAccountRepoStub{accounts: []Account{first, second}}}
	groupID := int64(9)
	manifest, err := svc.FetchCodexModelsManifestWithFailover(context.Background(), &groupID, "0.137.0", `W/"cached"`, 1)
	require.NoError(t, err)
	require.Equal(t, int32(2), attempts.Load())
	require.Equal(t, `W/"cached"`, gotIfNoneMatch.Load())
	require.True(t, manifest.NotModified)
	require.Equal(t, `W/"cached"`, manifest.ETag)
}

func TestFetchCodexModelsManifestWithFailoverIsBoundedAndSanitized(t *testing.T) {
	const secret = "credential-must-not-leak"
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, `{"detail":"`+secret+`"}`, http.StatusInternalServerError)
	}))
	defer server.Close()
	useCodexModelsTestURL(t, server.URL)

	accounts := make([]Account, 0, 5)
	for i := int64(1); i <= 5; i++ {
		account := *newCodexModelsTestAccount()
		account.ID = i
		account.Priority = int(i)
		account.Credentials["access_token"] = secret + string(rune('a'+i))
		accounts = append(accounts, account)
	}

	svc := &OpenAIGatewayService{accountRepo: &codexModelsAccountRepoStub{accounts: accounts}}
	groupID := int64(9)
	_, err := svc.FetchCodexModelsManifestWithFailover(context.Background(), &groupID, "0.137.0", "", 0)
	require.Error(t, err)
	require.Equal(t, int32(4), attempts.Load(), "default must allow three switches after the first attempt")
	require.NotContains(t, err.Error(), secret)
	require.Equal(t, "Codex models manifest upstream returned HTTP 500", infraerrors.Message(err))
}
