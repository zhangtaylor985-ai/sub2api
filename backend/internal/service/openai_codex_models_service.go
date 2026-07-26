package service

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

// chatgptCodexModelsURL is a variable so tests can replace the fixed upstream
// endpoint with a local HTTP server.
var chatgptCodexModelsURL = "https://chatgpt.com/backend-api/codex/models"

const codexModelsManifestBodyLimit int64 = 8 << 20

// CodexModelsManifest carries the unmodified upstream manifest and the cache
// metadata required by Codex clients.
type CodexModelsManifest struct {
	Body        []byte
	ETag        string
	NotModified bool
}

// SelectAccountForCodexModelsManifest selects a schedulable OpenAI OAuth
// account. OpenAI API-key accounts cannot access the ChatGPT Codex manifest
// endpoint and therefore must not win this selection.
func (s *OpenAIGatewayService) SelectAccountForCodexModelsManifest(ctx context.Context, groupID *int64) (*Account, error) {
	return s.SelectAccountForCodexModelsManifestWithExclusions(ctx, groupID, nil)
}

// SelectAccountForCodexModelsManifestWithExclusions selects a schedulable
// OpenAI OAuth account while skipping accounts that already failed during the
// current manifest request.
func (s *OpenAIGatewayService) SelectAccountForCodexModelsManifestWithExclusions(ctx context.Context, groupID *int64, excludedIDs map[int64]struct{}) (*Account, error) {
	accounts, err := s.listSchedulableAccounts(ctx, groupID)
	if err != nil {
		return nil, err
	}
	oauthAccounts := make([]Account, 0, len(accounts))
	for i := range accounts {
		if accounts[i].IsOpenAIOAuth() {
			oauthAccounts = append(oauthAccounts, accounts[i])
		}
	}
	selected, _ := s.selectBestAccount(ctx, groupID, oauthAccounts, "", excludedIDs, false)
	if selected == nil {
		return nil, infraerrors.New(http.StatusServiceUnavailable, "OPENAI_CODEX_MODELS_ACCOUNT_UNAVAILABLE", "no available OpenAI OAuth accounts")
	}
	return s.hydrateSelectedAccount(ctx, selected)
}

// FetchCodexModelsManifestWithFailover fetches the manifest with bounded
// account failover. Each failed account is excluded before selecting the next
// candidate, so a broken OAuth account cannot monopolize model discovery for
// the whole group.
func (s *OpenAIGatewayService) FetchCodexModelsManifestWithFailover(ctx context.Context, groupID *int64, clientVersion, ifNoneMatch string, maxAccountSwitches int) (*CodexModelsManifest, error) {
	if maxAccountSwitches <= 0 {
		maxAccountSwitches = 3
	}

	excludedIDs := make(map[int64]struct{})
	var lastErr error
	switchesUsed := 0

	for {
		account, err := s.SelectAccountForCodexModelsManifestWithExclusions(ctx, groupID, excludedIDs)
		if err != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, infraerrors.New(http.StatusServiceUnavailable, "OPENAI_CODEX_MODELS_ACCOUNT_UNAVAILABLE", "No available OpenAI OAuth accounts")
		}

		manifest, err := s.FetchCodexModelsManifest(ctx, account, clientVersion, ifNoneMatch)
		if err == nil {
			return manifest, nil
		}

		lastErr = err
		excludedIDs[account.ID] = struct{}{}
		if switchesUsed >= maxAccountSwitches {
			return nil, lastErr
		}
		switchesUsed++
	}
}

// FetchCodexModelsManifest fetches the live Codex models manifest with an
// account's OAuth credentials. The manifest schema intentionally remains
// opaque so new upstream fields are passed through without gateway changes.
func (s *OpenAIGatewayService) FetchCodexModelsManifest(ctx context.Context, account *Account, clientVersion, ifNoneMatch string) (*CodexModelsManifest, error) {
	if account == nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_CODEX_MODELS_ACCOUNT_REQUIRED", "account is required")
	}
	if !account.IsOpenAIOAuth() {
		return nil, infraerrors.New(http.StatusBadGateway, "OPENAI_CODEX_MODELS_OAUTH_REQUIRED", "Codex models manifest requires an OpenAI OAuth account")
	}

	accessToken, tokenType, err := s.GetAccessToken(ctx, account)
	if err != nil || tokenType != "oauth" || strings.TrimSpace(accessToken) == "" {
		return nil, infraerrors.New(http.StatusBadGateway, "OPENAI_CODEX_MODELS_TOKEN_MISSING", "Codex backend credentials are unavailable")
	}
	accessToken = strings.TrimSpace(accessToken)

	clientVersion = strings.TrimSpace(clientVersion)
	if clientVersion == "" {
		clientVersion = openAICodexProbeVersion
	}
	requestURL := chatgptCodexModelsURL + "?client_version=" + url.QueryEscape(clientVersion)

	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_CODEX_MODELS_REQUEST_FAILED", "failed to create Codex models manifest request")
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Originator", "codex_cli_rs")
	req.Header.Set("Version", clientVersion)
	req.Header.Set("User-Agent", codexCLIUserAgent)
	if ifNoneMatch = strings.TrimSpace(ifNoneMatch); ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	if chatGPTAccountID := strings.TrimSpace(account.GetChatGPTAccountID()); chatGPTAccountID != "" {
		req.Header.Set("chatgpt-account-id", chatGPTAccountID)
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	client, err := httpclient.GetClient(httpclient.Options{
		ProxyURL:              proxyURL,
		Timeout:               15 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	})
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_CODEX_MODELS_PROXY_INVALID", "Codex models manifest proxy configuration is invalid")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, infraerrors.New(http.StatusBadGateway, "OPENAI_CODEX_MODELS_UPSTREAM_FAILED", "Codex models manifest request failed")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return &CodexModelsManifest{ETag: resp.Header.Get("ETag"), NotModified: true}, nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_MODELS_UPSTREAM_FAILED", "Codex models manifest upstream returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, codexModelsManifestBodyLimit+1))
	if err != nil {
		return nil, infraerrors.New(http.StatusBadGateway, "OPENAI_CODEX_MODELS_UPSTREAM_FAILED", "failed to read Codex models manifest response")
	}
	if int64(len(body)) > codexModelsManifestBodyLimit {
		return nil, infraerrors.New(http.StatusBadGateway, "OPENAI_CODEX_MODELS_RESPONSE_TOO_LARGE", "Codex models manifest response is too large")
	}
	return &CodexModelsManifest{Body: body, ETag: resp.Header.Get("ETag")}, nil
}
