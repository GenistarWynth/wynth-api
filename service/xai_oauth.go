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

// X.AI (Grok) OAuth public client credentials.
//
// X.AI uses a public OAuth client (PKCE, NO client_secret). The default client_id
// is the public value shipped with the Grok CLI. Override at runtime via the
// XAI_OAUTH_CLIENT_ID env var.
//
// Cross-checked against the sub2api reference
// (.codex/external/sub2api-src/backend/internal/pkg/xai/oauth.go and
// repository/grok_oauth_client.go): the refresh grant POSTs form fields
// {grant_type=refresh_token, client_id, refresh_token} with NO client_secret.
const (
	xaiOAuthDefaultClientID  = "b1a00492-073a-47ea-816f-4c329264a828"
	xaiOAuthDefaultUserAgent = "new-api-grok-oauth/1.0"
)

// xaiOAuthDefaultExpiresIn is the fallback access-token lifetime used when the
// X.AI token response omits expires_in. The X.AI access token default lifetime is
// 6 hours.
const xaiOAuthDefaultExpiresIn = int64(6 * 60 * 60)

// xaiOAuthTokenURL is the X.AI OAuth2 token endpoint. It is a package-level var
// (not const) so tests can override it with an httptest server URL, mirroring the
// gemini/claude OAuth test seams.
var xaiOAuthTokenURL = "https://auth.x.ai/oauth2/token"

// RefreshXAIOAuthTokenWithProxy refreshes an X.AI (Grok) OAuth refresh token and
// returns a new token result using the same CodexOAuthTokenResult type used by the
// Codex, Claude, and Gemini refresh paths.
//
// The function POSTs form-urlencoded to the X.AI OAuth2 token endpoint using the
// public Grok client_id (overridable via XAI_OAUTH_CLIENT_ID). NO client_secret is
// sent (X.AI is a public PKCE client). A non-empty proxyURL routes the request
// through the given proxy (SOCKS5/HTTP/HTTPS).
func RefreshXAIOAuthTokenWithProxy(ctx context.Context, refreshToken string, proxyURL string) (*CodexOAuthTokenResult, error) {
	clientID := common.GetEnvOrDefaultString("XAI_OAUTH_CLIENT_ID", xaiOAuthDefaultClientID)

	client, err := getXAIOAuthHTTPClient(proxyURL)
	if err != nil {
		return nil, err
	}
	return refreshXAIOAuthToken(ctx, client, xaiOAuthTokenURL, clientID, refreshToken)
}

func refreshXAIOAuthToken(
	ctx context.Context,
	client *http.Client,
	tokenURL string,
	clientID string,
	refreshToken string,
) (*CodexOAuthTokenResult, error) {
	rt := strings.TrimSpace(refreshToken)
	if rt == "" {
		return nil, fmt.Errorf("empty refresh_token")
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", clientID)
	form.Set("refresh_token", rt)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", xaiOAuthDefaultUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("xai oauth refresh failed: status=%d", resp.StatusCode)
	}

	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}

	if err := common.DecodeJson(resp.Body, &payload); err != nil {
		return nil, err
	}

	if strings.TrimSpace(payload.AccessToken) == "" {
		return nil, fmt.Errorf("xai oauth refresh response missing access_token")
	}

	expiresIn := payload.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = xaiOAuthDefaultExpiresIn
	}

	return &CodexOAuthTokenResult{
		AccessToken:  strings.TrimSpace(payload.AccessToken),
		RefreshToken: strings.TrimSpace(payload.RefreshToken),
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second),
	}, nil
}

func getXAIOAuthHTTPClient(proxyURL string) (*http.Client, error) {
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
