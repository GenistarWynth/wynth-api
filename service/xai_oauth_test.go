package service

import (
	"context"
	"net/http"
	"net/http/httptest"
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
