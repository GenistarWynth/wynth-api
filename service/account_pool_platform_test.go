package service

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── normalizeAccountPoolPlatform ──────────────────────────────────────────────

func TestNormalizeAccountPoolPlatformOpenAI(t *testing.T) {
	got, err := normalizeAccountPoolPlatform("openai")
	require.NoError(t, err)
	assert.Equal(t, model.AccountPoolPlatformOpenAI, got)
}

func TestNormalizeAccountPoolPlatformEmptyDefaultsToOpenAI(t *testing.T) {
	got, err := normalizeAccountPoolPlatform("")
	require.NoError(t, err)
	assert.Equal(t, model.AccountPoolPlatformOpenAI, got)
}

func TestNormalizeAccountPoolPlatformAnthropic(t *testing.T) {
	got, err := normalizeAccountPoolPlatform("anthropic")
	require.NoError(t, err)
	assert.Equal(t, model.AccountPoolPlatformAnthropic, got)
}

func TestNormalizeAccountPoolPlatformUnknownRejected(t *testing.T) {
	_, err := normalizeAccountPoolPlatform("bedrock")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported")
}

func TestNormalizeAccountPoolPlatformGemini(t *testing.T) {
	got, err := normalizeAccountPoolPlatform("gemini")
	require.NoError(t, err)
	assert.Equal(t, model.AccountPoolPlatformGemini, got)
}

// ── validateAccountPoolRuntimeChannel ────────────────────────────────────────

func TestValidateAccountPoolRuntimeChannelAllowsAnthropic(t *testing.T) {
	ch := model.Channel{Type: constant.ChannelTypeAnthropic}
	require.NoError(t, validateAccountPoolRuntimeChannel(ch))
}

func TestValidateAccountPoolRuntimeChannelAllowsOpenAI(t *testing.T) {
	ch := model.Channel{Type: constant.ChannelTypeOpenAI}
	require.NoError(t, validateAccountPoolRuntimeChannel(ch))
}

func TestValidateAccountPoolRuntimeChannelAllowsCodex(t *testing.T) {
	ch := model.Channel{Type: constant.ChannelTypeCodex}
	require.NoError(t, validateAccountPoolRuntimeChannel(ch))
}

func TestValidateAccountPoolRuntimeChannelRejectsOther(t *testing.T) {
	ch := model.Channel{Type: 99} // arbitrary unsupported channel type
	err := validateAccountPoolRuntimeChannel(ch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account pool runtime")
}

func TestValidateAccountPoolRuntimeChannelAllowsGemini(t *testing.T) {
	ch := model.Channel{Type: constant.ChannelTypeGemini}
	require.NoError(t, validateAccountPoolRuntimeChannel(ch))
}

// ── account identifier seam ───────────────────────────────────────────────────

func TestAccountPoolRuntimeAccountIdentifierAnthropicUsesDirectIdentifier(t *testing.T) {
	selection := AccountPoolSelectionResult{
		Platform:          model.AccountPoolPlatformAnthropic,
		AccountIdentifier: "claude-user-123",
	}
	got := accountPoolRuntimeAccountIdentifier(selection, "some-bearer-token")
	assert.Equal(t, "claude-user-123", got)
}

func TestAccountPoolRuntimeAccountIdentifierAnthropicNoJWTParsing(t *testing.T) {
	// Anthropic account with no AccountIdentifier → returns "" (no JWT extraction attempt)
	selection := AccountPoolSelectionResult{
		Platform:          model.AccountPoolPlatformAnthropic,
		AccountIdentifier: "",
	}
	// Craft a fake JWT that would parse as an OpenAI account_id if ExtractCodexAccountIDFromJWT were called.
	fakeJWT := buildFakeJWTWithCodexAccountID(t, "should-not-appear")
	got := accountPoolRuntimeAccountIdentifier(selection, fakeJWT)
	assert.Equal(t, "", got)
}

func TestAccountPoolRuntimeAccountIdentifierOpenAIFallsBackToJWT(t *testing.T) {
	// OpenAI account with no AccountIdentifier → extracts from JWT
	selection := AccountPoolSelectionResult{
		Platform:          model.AccountPoolPlatformOpenAI,
		AccountIdentifier: "",
	}
	fakeJWT := buildFakeJWTWithCodexAccountID(t, "openai-account-xyz")
	got := accountPoolRuntimeAccountIdentifier(selection, fakeJWT)
	assert.Equal(t, "openai-account-xyz", got)
}

// ── refresh dispatch ──────────────────────────────────────────────────────────

func TestAccountPoolRefreshDispatchUsesClaudeRefreshForAnthropic(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name: "anthropic-dispatch",
		Credential: AccountPoolCredentialConfig{
			Type:         AccountPoolCredentialTypeOAuth,
			RefreshToken: "claude-refresh-token",
		},
		TokenState: AccountPoolTokenState{
			ExpiresAt: 900,
			Version:   1,
		},
	})

	var claudeCalled bool
	var codexCalled bool

	oldClaude := accountPoolClaudeOAuthRefresh
	accountPoolClaudeOAuthRefresh = func(_ context.Context, refreshToken string, _ string) (*CodexOAuthTokenResult, error) {
		claudeCalled = true
		assert.Equal(t, "claude-refresh-token", refreshToken)
		return &CodexOAuthTokenResult{
			AccessToken:  "claude-access-new",
			RefreshToken: "claude-refresh-new",
			ExpiresAt:    time.Unix(9999, 0),
		}, nil
	}
	t.Cleanup(func() { accountPoolClaudeOAuthRefresh = oldClaude })

	setAccountPoolOAuthRefreshForTest(t, func(_ context.Context, _ string, _ string) (*CodexOAuthTokenResult, error) {
		codexCalled = true
		return nil, errors.New("codex refresh must not be called for anthropic")
	})

	token, err := ResolveAccountPoolRuntimeCredential(context.Background(), AccountPoolRuntimeCredentialRequest{
		AccountID: account.Id,
		Credential: AccountPoolCredentialConfig{
			Type:         AccountPoolCredentialTypeOAuth,
			RefreshToken: "claude-refresh-token",
		},
		TokenState: AccountPoolTokenState{
			ExpiresAt: 900,
			Version:   1,
		},
		Platform: model.AccountPoolPlatformAnthropic,
		Now:      1000,
	})

	require.NoError(t, err)
	assert.Equal(t, "claude-access-new", token)
	assert.True(t, claudeCalled, "claude refresh seam must have been called")
	assert.False(t, codexCalled, "codex refresh seam must NOT have been called for anthropic")
}

func TestAccountPoolRefreshDispatchUsesCodexRefreshForOpenAI(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name: "openai-dispatch",
		Credential: AccountPoolCredentialConfig{
			Type:         AccountPoolCredentialTypeOAuth,
			RefreshToken: "openai-refresh-token",
		},
		TokenState: AccountPoolTokenState{
			ExpiresAt: 900,
			Version:   1,
		},
	})

	var claudeCalled bool
	oldClaude := accountPoolClaudeOAuthRefresh
	accountPoolClaudeOAuthRefresh = func(_ context.Context, _ string, _ string) (*CodexOAuthTokenResult, error) {
		claudeCalled = true
		return nil, errors.New("claude refresh must not be called for openai")
	}
	t.Cleanup(func() { accountPoolClaudeOAuthRefresh = oldClaude })

	setAccountPoolOAuthRefreshForTest(t, func(_ context.Context, refreshToken string, _ string) (*CodexOAuthTokenResult, error) {
		assert.Equal(t, "openai-refresh-token", refreshToken)
		return &CodexOAuthTokenResult{
			AccessToken:  "codex-access-new",
			RefreshToken: "codex-refresh-new",
			ExpiresAt:    time.Unix(9999, 0),
		}, nil
	})

	token, err := ResolveAccountPoolRuntimeCredential(context.Background(), AccountPoolRuntimeCredentialRequest{
		AccountID: account.Id,
		Credential: AccountPoolCredentialConfig{
			Type:         AccountPoolCredentialTypeOAuth,
			RefreshToken: "openai-refresh-token",
		},
		TokenState: AccountPoolTokenState{
			ExpiresAt: 900,
			Version:   1,
		},
		Platform: model.AccountPoolPlatformOpenAI,
		Now:      1000,
	})

	require.NoError(t, err)
	assert.Equal(t, "codex-access-new", token)
	assert.False(t, claudeCalled, "claude refresh seam must NOT have been called for openai")
}

// ── Claude OAuth refresh (httptest) ──────────────────────────────────────────

func TestRefreshClaudeOAuthTokenWithProxyPostsCorrectRequest(t *testing.T) {
	var gotMethod, gotPath, gotContentType, gotUserAgent string
	var gotForm map[string][]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotUserAgent = r.Header.Get("User-Agent")
		require.NoError(t, r.ParseForm())
		gotForm = map[string][]string(r.Form)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-claude","refresh_token":"rt-claude","expires_in":3600}`))
	}))
	defer srv.Close()

	// Use the internal function with a test server URL.
	client := &http.Client{Timeout: 5 * time.Second}
	result, err := refreshClaudeOAuthToken(context.Background(), client, srv.URL+"/v1/oauth/token", claudeOAuthClientID, "rt-old")

	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/v1/oauth/token", gotPath)
	assert.Contains(t, gotContentType, "application/x-www-form-urlencoded")
	assert.Equal(t, "axios/1.13.6", gotUserAgent)
	assert.Equal(t, []string{"9d1c250a-e61b-44d9-88ed-5944d1962f5e"}, gotForm["client_id"])
	assert.Equal(t, []string{"refresh_token"}, gotForm["grant_type"])
	assert.Equal(t, []string{"rt-old"}, gotForm["refresh_token"])

	assert.Equal(t, "at-claude", result.AccessToken)
	assert.Equal(t, "rt-claude", result.RefreshToken)
	assert.True(t, result.ExpiresAt.After(time.Now()))
}

func TestRefreshClaudeOAuthTokenRejectsNonSuccessStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`<html>Unauthorized</html>`)) // non-JSON body
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	_, err := refreshClaudeOAuthToken(context.Background(), client, srv.URL+"/v1/oauth/token", claudeOAuthClientID, "bad-token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "claude oauth refresh failed")
}

func TestRefreshClaudeOAuthTokenRejectsEmptyRefreshToken(t *testing.T) {
	_, err := RefreshClaudeOAuthTokenWithProxy(context.Background(), "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty refresh_token")
}

// ── helpers ──────────────────────────────────────────────────────────────────

// buildFakeJWTWithCodexAccountID builds a minimal 3-part JWT with the given
// chatgpt_account_id embedded in the https://api.openai.com/auth claim,
// suitable for testing ExtractCodexAccountIDFromJWT.
func buildFakeJWTWithCodexAccountID(t *testing.T, accountID string) string {
	t.Helper()
	payload := `{"https://api.openai.com/auth":{"chatgpt_account_id":"` + accountID + `"}}`
	encoded := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return "eyJhbGciOiJSUzI1NiJ9." + encoded + ".sig"
}
