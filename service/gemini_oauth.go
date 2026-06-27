package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// Gemini OAuth public installed-app client credentials (Gemini CLI).
// These are public values shipped with the Gemini CLI tool.
// Override at runtime via GEMINI_OAUTH_CLIENT_ID / GEMINI_OAUTH_CLIENT_SECRET env vars.
const (
	geminiOAuthDefaultClientID     = "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"
	geminiOAuthDefaultClientSecret = "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"
	geminiOAuthDefaultUserAgent    = "GeminiCLI/0.1.5 (Windows; AMD64)"
)

// geminiOAuthTokenURL is the Google OAuth2 token endpoint.
// It is a package-level var (not const) so tests can override it with an httptest server URL.
//
// NOTE: This implementation performs a standard Google OAuth2 token refresh using the
// Gemini CLI public client. The Code Assist (cloudcode-pa.googleapis.com) routing path
// for code_assist-scoped tokens (GCP project detection, etc.) is a documented FOLLOW-UP
// and is NOT part of this slice.
var geminiOAuthTokenURL = "https://oauth2.googleapis.com/token"

// RefreshGeminiOAuthTokenWithProxy refreshes a Gemini OAuth refresh token and
// returns a new token result using the same CodexOAuthTokenResult type used by
// the Codex and Claude refresh paths.
//
// The function POSTs form-urlencoded to the Google OAuth2 token endpoint using the
// Gemini CLI public client credentials (overridable via GEMINI_OAUTH_CLIENT_ID /
// GEMINI_OAUTH_CLIENT_SECRET env vars). A non-empty proxyURL routes the request
// through the given proxy (SOCKS5/HTTP/HTTPS).
func RefreshGeminiOAuthTokenWithProxy(ctx context.Context, refreshToken string, proxyURL string) (*CodexOAuthTokenResult, error) {
	clientID := common.GetEnvOrDefaultString("GEMINI_OAUTH_CLIENT_ID", geminiOAuthDefaultClientID)
	clientSecret := common.GetEnvOrDefaultString("GEMINI_OAUTH_CLIENT_SECRET", geminiOAuthDefaultClientSecret)

	client, err := getGeminiOAuthHTTPClient(proxyURL)
	if err != nil {
		return nil, err
	}
	return refreshGeminiOAuthToken(ctx, client, geminiOAuthTokenURL, clientID, clientSecret, refreshToken)
}

func refreshGeminiOAuthToken(
	ctx context.Context,
	client *http.Client,
	tokenURL string,
	clientID string,
	clientSecret string,
	refreshToken string,
) (*CodexOAuthTokenResult, error) {
	rt := strings.TrimSpace(refreshToken)
	if rt == "" {
		return nil, fmt.Errorf("empty refresh_token")
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", rt)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", geminiOAuthDefaultUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gemini oauth refresh failed: status=%d", resp.StatusCode)
	}

	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}

	if err := common.DecodeJson(resp.Body, &payload); err != nil {
		return nil, err
	}

	if strings.TrimSpace(payload.AccessToken) == "" {
		return nil, fmt.Errorf("gemini oauth refresh response missing access_token")
	}
	if payload.ExpiresIn <= 0 {
		return nil, fmt.Errorf("gemini oauth refresh response missing expires_in")
	}

	return &CodexOAuthTokenResult{
		AccessToken:  strings.TrimSpace(payload.AccessToken),
		RefreshToken: strings.TrimSpace(payload.RefreshToken),
		ExpiresAt:    time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second),
	}, nil
}

func getGeminiOAuthHTTPClient(proxyURL string) (*http.Client, error) {
	baseClient, err := GetHttpClientWithProxy(strings.TrimSpace(proxyURL))
	if err != nil {
		return nil, err
	}
	if baseClient == nil {
		return &http.Client{Timeout: defaultHTTPTimeout}, nil
	}
	clientCopy := *baseClient
	clientCopy.Timeout = defaultHTTPTimeout
	return &clientCopy, nil
}
