package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// overrideGeminiOAuthTokenURLForTest replaces geminiOAuthTokenURL with the given
// URL for the duration of the test, restoring the original on cleanup.
// This mirrors the exact mechanism used by the Claude OAuth test seam.
func overrideGeminiOAuthTokenURLForTest(t *testing.T, serverURL string) {
	t.Helper()
	old := geminiOAuthTokenURL
	geminiOAuthTokenURL = serverURL
	t.Cleanup(func() { geminiOAuthTokenURL = old })
}

// TestRefreshGeminiOAuthTokenSuccess verifies that a successful refresh response
// populates all CodexOAuthTokenResult fields correctly.
func TestRefreshGeminiOAuthTokenSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "refresh_token", r.FormValue("grant_type"))
		assert.Equal(t, "my-refresh-token", r.FormValue("refresh_token"))
		// client_id and client_secret must be present (exact values come from env/defaults)
		assert.NotEmpty(t, r.FormValue("client_id"))
		assert.NotEmpty(t, r.FormValue("client_secret"))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "ya29.new-access-token",
			"refresh_token": "1//new-refresh-token",
			"expires_in":    3600,
		})
	}))
	defer srv.Close()
	overrideGeminiOAuthTokenURLForTest(t, srv.URL)

	before := time.Now()
	result, err := RefreshGeminiOAuthTokenWithProxy(context.Background(), "my-refresh-token", "")
	after := time.Now()

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "ya29.new-access-token", result.AccessToken)
	assert.Equal(t, "1//new-refresh-token", result.RefreshToken)
	assert.True(t, result.ExpiresAt.After(before.Add(3599*time.Second)),
		"ExpiresAt should be roughly now+3600s")
	assert.True(t, result.ExpiresAt.Before(after.Add(3601*time.Second)),
		"ExpiresAt should not be far in the future")
}

// TestRefreshGeminiOAuthTokenForTypeSelectsClient verifies that the OAuth client_id
// sent in the refresh form is selected by oauth_type: antigravity uses the public
// antigravity client, while code_assist / ai_studio / google_one / empty use the
// Gemini CLI client. This is the load-bearing per-type contract for slice 6a.
func TestRefreshGeminiOAuthTokenForTypeSelectsClient(t *testing.T) {
	tests := []struct {
		name           string
		oauthType      string
		wantClientID   string
		wantClientSecr string
	}{
		{
			name:           "antigravity selects the antigravity client",
			oauthType:      AccountPoolGeminiOAuthTypeAntigravity,
			wantClientID:   geminiAntigravityDefaultClientID,
			wantClientSecr: geminiAntigravityDefaultClientSecret,
		},
		{
			name:           "code_assist selects the gemini-cli client",
			oauthType:      AccountPoolGeminiOAuthTypeCodeAssist,
			wantClientID:   geminiOAuthDefaultClientID,
			wantClientSecr: geminiOAuthDefaultClientSecret,
		},
		{
			name:           "google_one selects the gemini-cli client",
			oauthType:      AccountPoolGeminiOAuthTypeGoogleOne,
			wantClientID:   geminiOAuthDefaultClientID,
			wantClientSecr: geminiOAuthDefaultClientSecret,
		},
		{
			name:           "empty oauth_type selects the gemini-cli client",
			oauthType:      "",
			wantClientID:   geminiOAuthDefaultClientID,
			wantClientSecr: geminiOAuthDefaultClientSecret,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotClientID, gotClientSecret string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.NoError(t, r.ParseForm())
				gotClientID = r.FormValue("client_id")
				gotClientSecret = r.FormValue("client_secret")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token": "ya29.new-access-token",
					"expires_in":   3600,
				})
			}))
			defer srv.Close()
			overrideGeminiOAuthTokenURLForTest(t, srv.URL)

			result, err := RefreshGeminiOAuthTokenForType(context.Background(), tc.oauthType, "my-refresh-token", "")
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tc.wantClientID, gotClientID, "client_id must be selected by oauth_type")
			assert.Equal(t, tc.wantClientSecr, gotClientSecret, "client_secret must be selected by oauth_type")
		})
	}
}

// TestRefreshGeminiOAuthTokenNon2xxReturnsError verifies that a non-2xx response
// returns an error without attempting to decode the body.
func TestRefreshGeminiOAuthTokenNon2xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	overrideGeminiOAuthTokenURLForTest(t, srv.URL)

	result, err := RefreshGeminiOAuthTokenWithProxy(context.Background(), "bad-token", "")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "gemini oauth refresh failed")
	assert.Contains(t, err.Error(), "401")
}

// TestRefreshGeminiOAuthTokenMissingAccessToken verifies that a 200 response
// with an empty access_token field returns an error.
func TestRefreshGeminiOAuthTokenMissingAccessToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()
	overrideGeminiOAuthTokenURLForTest(t, srv.URL)

	result, err := RefreshGeminiOAuthTokenWithProxy(context.Background(), "some-refresh-token", "")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "access_token")
}

// TestRefreshGeminiOAuthTokenEmptyRefreshToken verifies that an empty refresh_token
// returns an error immediately without making any HTTP request.
func TestRefreshGeminiOAuthTokenEmptyRefreshToken(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	overrideGeminiOAuthTokenURLForTest(t, srv.URL)

	result, err := RefreshGeminiOAuthTokenWithProxy(context.Background(), "   ", "")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "empty refresh_token")
	assert.False(t, called, "no HTTP request should be made for empty refresh_token")
}
