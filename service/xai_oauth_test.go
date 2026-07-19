package service

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// overrideXAIOAuthTokenURLForTest replaces xaiOAuthTokenURL with the given URL for
// the duration of the test, restoring the original on cleanup. Mirrors the
// gemini/claude OAuth test seams.
func overrideXAIOAuthTokenURLForTest(t *testing.T, serverURL string) {
	t.Helper()
	old := xaiOAuthTokenURL
	xaiOAuthTokenURL = serverURL
	t.Cleanup(func() { xaiOAuthTokenURL = old })
}

// TestRefreshXAIOAuthTokenSuccess verifies that a successful refresh carries the
// public client_id, grant_type=refresh_token, the refresh token, and NO
// client_secret, and that the result populates CodexOAuthTokenResult correctly.
func TestRefreshXAIOAuthTokenSuccess(t *testing.T) {
	var gotForm map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.NoError(t, r.ParseForm())
		gotForm = map[string][]string(r.Form)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		b, _ := common.Marshal(map[string]any{
			"access_token":  "xai-access-new",
			"refresh_token": "xai-refresh-new",
			"expires_in":    3600,
		})
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	overrideXAIOAuthTokenURLForTest(t, srv.URL)

	before := time.Now()
	result, err := RefreshXAIOAuthTokenWithProxy(context.Background(), "my-refresh-token", "")
	after := time.Now()

	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, []string{"refresh_token"}, gotForm["grant_type"])
	assert.Equal(t, []string{"my-refresh-token"}, gotForm["refresh_token"])
	assert.Equal(t, []string{xaiOAuthDefaultClientID}, gotForm["client_id"], "public Grok client_id must be sent")
	_, hasSecret := gotForm["client_secret"]
	assert.False(t, hasSecret, "X.AI is a public PKCE client: client_secret must NOT be sent")

	assert.Equal(t, "xai-access-new", result.AccessToken)
	assert.Equal(t, "xai-refresh-new", result.RefreshToken)
	assert.True(t, result.ExpiresAt.After(before.Add(3599*time.Second)))
	assert.True(t, result.ExpiresAt.Before(after.Add(3601*time.Second)))
}

// TestRefreshXAIOAuthTokenDefaultsExpiry verifies that a response omitting
// expires_in falls back to the 6-hour default lifetime.
func TestRefreshXAIOAuthTokenDefaultsExpiry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		b, _ := common.Marshal(map[string]any{
			"access_token":  "xai-access",
			"refresh_token": "xai-refresh",
		})
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	overrideXAIOAuthTokenURLForTest(t, srv.URL)

	before := time.Now()
	result, err := RefreshXAIOAuthTokenWithProxy(context.Background(), "rt", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	// Default 6h lifetime: ExpiresAt should be roughly now+21600s.
	assert.True(t, result.ExpiresAt.After(before.Add(time.Duration(xaiOAuthDefaultExpiresIn-1)*time.Second)))
}

// TestRefreshXAIOAuthTokenPreservesRefreshTokenWhenOmitted verifies that when the
// response omits refresh_token, the function returns an empty RefreshToken (the
// token provider then preserves the prior refresh token).
func TestRefreshXAIOAuthTokenPreservesRefreshTokenWhenOmitted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		b, _ := common.Marshal(map[string]any{
			"access_token": "xai-access-only",
			"expires_in":   3600,
		})
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	overrideXAIOAuthTokenURLForTest(t, srv.URL)

	result, err := RefreshXAIOAuthTokenWithProxy(context.Background(), "rt", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "xai-access-only", result.AccessToken)
	assert.Empty(t, result.RefreshToken, "omitted refresh_token returns empty; provider preserves the old one")
}

// TestRefreshXAIOAuthTokenNon2xxReturnsError verifies that a non-2xx response
// returns an error including the status code.
func TestRefreshXAIOAuthTokenNon2xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	overrideXAIOAuthTokenURLForTest(t, srv.URL)

	result, err := RefreshXAIOAuthTokenWithProxy(context.Background(), "bad-token", "")
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "xai oauth refresh failed")
	assert.Contains(t, err.Error(), "401")
}

func TestRefreshXAIOAuthTokenClassifiesPermanentCredentialFailureWithoutLeakingBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_grant","message":"refresh token super-secret-token was revoked"}}`))
	}))
	defer srv.Close()
	overrideXAIOAuthTokenURLForTest(t, srv.URL)

	result, err := RefreshXAIOAuthTokenWithProxy(context.Background(), "super-secret-token", "")
	require.Error(t, err)
	assert.Nil(t, result)
	var tokenErr *XAIOAuthTokenError
	require.True(t, errors.As(err, &tokenErr))
	assert.Equal(t, http.StatusUnauthorized, tokenErr.StatusCode)
	assert.Equal(t, "invalid_grant", tokenErr.Code)
	assert.True(t, IsXAIOAuthPermanentCredentialError(err))
	assert.NotContains(t, err.Error(), "super-secret-token")
	assert.NotContains(t, err.Error(), "was revoked")
}

func TestRefreshXAIOAuthTokenTreatsServerFailureAsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"temporarily_unavailable"}`))
	}))
	defer srv.Close()
	overrideXAIOAuthTokenURLForTest(t, srv.URL)

	_, err := RefreshXAIOAuthTokenWithProxy(context.Background(), "refresh-token", "")
	require.Error(t, err)
	var tokenErr *XAIOAuthTokenError
	require.True(t, errors.As(err, &tokenErr))
	assert.Equal(t, "temporarily_unavailable", tokenErr.Code)
	assert.False(t, IsXAIOAuthPermanentCredentialError(err))
}

// TestRefreshXAIOAuthTokenMissingAccessToken verifies that a 200 response with an
// empty access_token returns an error.
func TestRefreshXAIOAuthTokenMissingAccessToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		b, _ := common.Marshal(map[string]any{
			"access_token": "",
			"expires_in":   3600,
		})
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	overrideXAIOAuthTokenURLForTest(t, srv.URL)

	result, err := RefreshXAIOAuthTokenWithProxy(context.Background(), "rt", "")
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "access_token")
}

// TestRefreshXAIOAuthTokenEmptyRefreshToken verifies that an empty refresh_token
// returns an error immediately without making any HTTP request.
func TestRefreshXAIOAuthTokenEmptyRefreshToken(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	overrideXAIOAuthTokenURLForTest(t, srv.URL)

	result, err := RefreshXAIOAuthTokenWithProxy(context.Background(), "   ", "")
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "empty refresh_token")
	assert.False(t, called, "no HTTP request should be made for empty refresh_token")
}

func TestParseXAIOAuthAuthorizationInput(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantCode      string
		wantState     string
		requiresState bool
	}{
		{name: "callback URL", input: "http://127.0.0.1:56121/callback?code=abc&state=state-1", wantCode: "abc", wantState: "state-1", requiresState: true},
		{name: "query string", input: "?code=query-code&state=query-state", wantCode: "query-code", wantState: "query-state", requiresState: true},
		{name: "bare code", input: " bare-code ", wantCode: "bare-code"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := ParseXAIOAuthAuthorizationInput(tt.input)
			assert.Equal(t, tt.wantCode, parsed.Code)
			assert.Equal(t, tt.wantState, parsed.State)
			assert.Equal(t, tt.requiresState, parsed.RequiresState)
		})
	}
}

func TestValidateXAIOAuthRedirectURI(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "default loopback", value: "http://127.0.0.1:56121/callback"},
		{name: "localhost loopback", value: "http://localhost:56121/callback"},
		{name: "https callback", value: "https://admin.example.com/oauth/xai/callback"},
		{name: "non loopback plain http", value: "http://admin.example.com/oauth/xai/callback", wantErr: true},
		{name: "userinfo", value: "https://user:pass@admin.example.com/callback", wantErr: true},
		{name: "fragment", value: "https://admin.example.com/callback#token", wantErr: true},
		{name: "javascript", value: "javascript:alert(1)", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateXAIOAuthRedirectURI(tt.value)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.value, got)
		})
	}
}

func TestGenerateXAIOAuthAuthorizationStoresPKCESession(t *testing.T) {
	store := NewXAIOAuthSessionStore()
	result, err := GenerateXAIOAuthAuthorization(store, "http://proxy.internal:8080", "")
	require.NoError(t, err)
	require.NotEmpty(t, result.SessionID)
	require.NotEmpty(t, result.State)

	parsed, err := url.Parse(result.AuthURL)
	require.NoError(t, err)
	assert.Equal(t, "https", parsed.Scheme)
	assert.Equal(t, "auth.x.ai", parsed.Host)
	assert.Equal(t, "/oauth2/authorize", parsed.Path)
	assert.Equal(t, "code", parsed.Query().Get("response_type"))
	assert.Equal(t, xaiOAuthDefaultClientID, parsed.Query().Get("client_id"))
	assert.Equal(t, xaiOAuthDefaultRedirectURI, parsed.Query().Get("redirect_uri"))
	assert.Equal(t, result.State, parsed.Query().Get("state"))
	assert.Equal(t, "S256", parsed.Query().Get("code_challenge_method"))
	assert.NotEmpty(t, parsed.Query().Get("code_challenge"))
	assert.NotEmpty(t, parsed.Query().Get("nonce"))

	session, ok := store.get(result.SessionID)
	require.True(t, ok)
	assert.Equal(t, result.State, session.State)
	assert.Equal(t, "http://proxy.internal:8080", session.ProxyURL)
	assert.Equal(t, xaiOAuthDefaultRedirectURI, session.RedirectURI)
	assert.Equal(t, parsed.Query().Get("code_challenge"), GenerateXAIOAuthCodeChallenge(session.CodeVerifier))
}

func TestExchangeXAIOAuthCodeValidatesStateAndConsumesSession(t *testing.T) {
	var exchangeCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exchangeCalls++
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "authorization_code", r.Form.Get("grant_type"))
		assert.Equal(t, "oauth-code", r.Form.Get("code"))
		assert.Equal(t, xaiOAuthDefaultClientID, r.Form.Get("client_id"))
		assert.Equal(t, xaiOAuthDefaultRedirectURI, r.Form.Get("redirect_uri"))
		assert.NotEmpty(t, r.Form.Get("code_verifier"))
		_, _ = w.Write([]byte(`{"access_token":"access","refresh_token":"refresh","expires_in":3600}`))
	}))
	defer srv.Close()
	overrideXAIOAuthTokenURLForTest(t, srv.URL)

	store := NewXAIOAuthSessionStore()
	auth, err := GenerateXAIOAuthAuthorization(store, "", "")
	require.NoError(t, err)

	_, err = ExchangeXAIOAuthCode(context.Background(), store, auth.SessionID, "?code=oauth-code&state=wrong", "")
	require.ErrorContains(t, err, "state")
	assert.Equal(t, 0, exchangeCalls)

	_, err = ExchangeXAIOAuthCode(context.Background(), store, auth.SessionID, "oauth-code", auth.State)
	require.ErrorContains(t, err, "session")
	assert.Equal(t, 0, exchangeCalls)
}

func TestExchangeXAIOAuthCodeReturnsAccountPoolPayload(t *testing.T) {
	idToken := fakeXAIOAuthJWT(t, map[string]any{
		"email":   "admin@example.com",
		"sub":     "subject-1",
		"team_id": "team-1",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access","refresh_token":"refresh","id_token":"` + idToken + `","token_type":"Bearer","scope":"openid offline_access","expires_in":3600}`))
	}))
	defer srv.Close()
	overrideXAIOAuthTokenURLForTest(t, srv.URL)

	store := NewXAIOAuthSessionStore()
	auth, err := GenerateXAIOAuthAuthorization(store, "", "")
	require.NoError(t, err)
	before := time.Now().Unix()
	info, err := ExchangeXAIOAuthCode(context.Background(), store, auth.SessionID, "oauth-code", auth.State)
	require.NoError(t, err)

	assert.Equal(t, "admin@example.com", info.Email)
	assert.Equal(t, "subject-1", info.Subject)
	assert.Equal(t, "team-1", info.TeamID)
	assert.GreaterOrEqual(t, info.ExpiresAt, before+3599)
	assert.Equal(t, AccountPoolCredentialConfig{
		Type:         AccountPoolCredentialTypeOAuth,
		Email:        "admin@example.com",
		RefreshToken: "refresh",
		IDToken:      idToken,
		ClientID:     xaiOAuthDefaultClientID,
		Scope:        "openid offline_access",
		TokenType:    "Bearer",
		Subject:      "subject-1",
		TeamID:       "team-1",
	}, info.AccountPoolCredential())
	assert.Equal(t, AccountPoolTokenState{
		AccessToken:  "access",
		RefreshToken: "refresh",
		ExpiresAt:    info.ExpiresAt,
		Version:      1,
	}, info.AccountPoolTokenState())

	_, err = ExchangeXAIOAuthCode(context.Background(), store, auth.SessionID, "oauth-code", auth.State)
	require.ErrorContains(t, err, "session")
}

func TestRefreshXAIOAuthTokenInfoPreservesRefreshToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "stored-client", r.Form.Get("client_id"))
		assert.Equal(t, "old-refresh", r.Form.Get("refresh_token"))
		_, _ = w.Write([]byte(`{"access_token":"new-access","expires_in":3600}`))
	}))
	defer srv.Close()
	overrideXAIOAuthTokenURLForTest(t, srv.URL)

	info, err := RefreshXAIOAuthTokenInfoWithProxy(context.Background(), "old-refresh", "", "stored-client")
	require.NoError(t, err)
	assert.Equal(t, "new-access", info.AccessToken)
	assert.Equal(t, "old-refresh", info.RefreshToken)
	assert.Equal(t, "stored-client", info.ClientID)
}

func TestXAIOAuthSessionExpires(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store := newXAIOAuthSessionStore(time.Minute, func() time.Time { return now })
	auth, err := GenerateXAIOAuthAuthorization(store, "", "")
	require.NoError(t, err)

	now = now.Add(time.Minute + time.Second)
	_, err = ExchangeXAIOAuthCode(context.Background(), store, auth.SessionID, "code", auth.State)
	require.ErrorContains(t, err, "session")
}

func TestMergeAccountPoolCredentialUpdateKeepsAndRotatesXAIMetadata(t *testing.T) {
	existing := AccountPoolCredentialConfig{
		Type:         AccountPoolCredentialTypeOAuth,
		RefreshToken: "stored-refresh",
		IDToken:      "old-id-token",
		ClientID:     "stored-client",
		Scope:        "old-scope",
		Subject:      "subject-1",
	}
	incoming := AccountPoolCredentialConfig{
		Type:      AccountPoolCredentialTypeOAuth,
		IDToken:   "new-id-token",
		Scope:     "new-scope",
		TokenType: "Bearer",
	}

	merged := mergeAccountPoolCredentialUpdate(existing, incoming)
	assert.Equal(t, "stored-refresh", merged.RefreshToken)
	assert.Equal(t, "new-id-token", merged.IDToken)
	assert.Equal(t, "stored-client", merged.ClientID)
	assert.Equal(t, "new-scope", merged.Scope)
	assert.Equal(t, "Bearer", merged.TokenType)
	assert.Equal(t, "subject-1", merged.Subject)
}

func fakeXAIOAuthJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := common.Marshal(claims)
	require.NoError(t, err)
	return strings.Join([]string{
		base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)),
		base64.RawURLEncoding.EncodeToString(payload),
		"signature",
	}, ".")
}
