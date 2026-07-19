package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
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
	xaiOAuthDefaultClientID    = "b1a00492-073a-47ea-816f-4c329264a828"
	xaiOAuthDefaultUserAgent   = "new-api-grok-oauth/1.0"
	xaiOAuthDefaultRedirectURI = "http://127.0.0.1:56121/callback"
	xaiOAuthDefaultScope       = "openid profile email offline_access grok-cli:access api:access"
	xaiOAuthSessionTTL         = 30 * time.Minute
	xaiOAuthMaxTokenBodyBytes  = 1 << 20
)

// xaiOAuthDefaultExpiresIn is the fallback access-token lifetime used when the
// X.AI token response omits expires_in. The X.AI access token default lifetime is
// 6 hours.
const xaiOAuthDefaultExpiresIn = int64(6 * 60 * 60)

// xaiOAuthTokenURL is the X.AI OAuth2 token endpoint. It is a package-level var
// (not const) so tests can override it with an httptest server URL, mirroring the
// gemini/claude OAuth test seams.
var (
	xaiOAuthAuthorizeURL = "https://auth.x.ai/oauth2/authorize"
	xaiOAuthTokenURL     = "https://auth.x.ai/oauth2/token"
)

type XAIOAuthAuthorizationInput struct {
	Code          string
	State         string
	RequiresState bool
}

type XAIOAuthAuthorization struct {
	AuthURL   string `json:"auth_url"`
	SessionID string `json:"session_id"`
	State     string `json:"state"`
}

type xaiOAuthSession struct {
	State        string
	CodeVerifier string
	ClientID     string
	ProxyURL     string
	RedirectURI  string
	CreatedAt    time.Time
}

type XAIOAuthSessionStore struct {
	mu       sync.Mutex
	sessions map[string]xaiOAuthSession
	ttl      time.Duration
	now      func() time.Time
}

func NewXAIOAuthSessionStore() *XAIOAuthSessionStore {
	return newXAIOAuthSessionStore(xaiOAuthSessionTTL, time.Now)
}

func newXAIOAuthSessionStore(ttl time.Duration, now func() time.Time) *XAIOAuthSessionStore {
	if ttl <= 0 {
		ttl = xaiOAuthSessionTTL
	}
	if now == nil {
		now = time.Now
	}
	return &XAIOAuthSessionStore{
		sessions: make(map[string]xaiOAuthSession),
		ttl:      ttl,
		now:      now,
	}
}

func (s *XAIOAuthSessionStore) set(sessionID string, session xaiOAuthSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, stored := range s.sessions {
		if now.Sub(stored.CreatedAt) > s.ttl {
			delete(s.sessions, id)
		}
	}
	s.sessions[sessionID] = session
}

func (s *XAIOAuthSessionStore) get(sessionID string) (xaiOAuthSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return xaiOAuthSession{}, false
	}
	if s.now().Sub(session.CreatedAt) > s.ttl {
		delete(s.sessions, sessionID)
		return xaiOAuthSession{}, false
	}
	return session, true
}

func (s *XAIOAuthSessionStore) consume(sessionID string) (xaiOAuthSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	delete(s.sessions, sessionID)
	if !ok || s.now().Sub(session.CreatedAt) > s.ttl {
		return xaiOAuthSession{}, false
	}
	return session, true
}

type XAIOAuthTokenInfo struct {
	AccessToken       string `json:"access_token"`
	RefreshToken      string `json:"refresh_token,omitempty"`
	IDToken           string `json:"id_token,omitempty"`
	TokenType         string `json:"token_type,omitempty"`
	ExpiresIn         int64  `json:"expires_in"`
	ExpiresAt         int64  `json:"expires_at"`
	ClientID          string `json:"client_id,omitempty"`
	Scope             string `json:"scope,omitempty"`
	Email             string `json:"email,omitempty"`
	Subject           string `json:"sub,omitempty"`
	TeamID            string `json:"team_id,omitempty"`
	SubscriptionTier  string `json:"subscription_tier,omitempty"`
	EntitlementStatus string `json:"entitlement_status,omitempty"`
}

func (i XAIOAuthTokenInfo) AccountPoolCredential() AccountPoolCredentialConfig {
	return AccountPoolCredentialConfig{
		Type:              AccountPoolCredentialTypeOAuth,
		Email:             strings.TrimSpace(i.Email),
		RefreshToken:      strings.TrimSpace(i.RefreshToken),
		IDToken:           strings.TrimSpace(i.IDToken),
		ClientID:          strings.TrimSpace(i.ClientID),
		Scope:             strings.TrimSpace(i.Scope),
		TokenType:         strings.TrimSpace(i.TokenType),
		Subject:           strings.TrimSpace(i.Subject),
		TeamID:            strings.TrimSpace(i.TeamID),
		SubscriptionTier:  strings.TrimSpace(i.SubscriptionTier),
		EntitlementStatus: strings.TrimSpace(i.EntitlementStatus),
	}
}

func (i XAIOAuthTokenInfo) AccountPoolTokenState() AccountPoolTokenState {
	return AccountPoolTokenState{
		AccessToken:  strings.TrimSpace(i.AccessToken),
		RefreshToken: strings.TrimSpace(i.RefreshToken),
		ExpiresAt:    i.ExpiresAt,
		Version:      1,
	}
}

func ParseXAIOAuthAuthorizationInput(raw string) XAIOAuthAuthorizationInput {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return XAIOAuthAuthorizationInput{}
	}
	if parsed, err := url.Parse(trimmed); err == nil {
		if code := strings.TrimSpace(parsed.Query().Get("code")); code != "" {
			return XAIOAuthAuthorizationInput{
				Code:          code,
				State:         strings.TrimSpace(parsed.Query().Get("state")),
				RequiresState: true,
			}
		}
	}
	queryCandidate := strings.TrimPrefix(trimmed, "?")
	if strings.Contains(queryCandidate, "=") {
		if values, err := url.ParseQuery(queryCandidate); err == nil {
			if code := strings.TrimSpace(values.Get("code")); code != "" {
				return XAIOAuthAuthorizationInput{
					Code:          code,
					State:         strings.TrimSpace(values.Get("state")),
					RequiresState: true,
				}
			}
		}
	}
	return XAIOAuthAuthorizationInput{Code: trimmed}
}

func ValidateXAIOAuthRedirectURI(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = xaiOAuthDefaultRedirectURI
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return "", errors.New("invalid xai oauth redirect_uri")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("xai oauth redirect_uri must not include userinfo or fragment")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return trimmed, nil
	case "http":
		host := strings.ToLower(parsed.Hostname())
		ip := net.ParseIP(host)
		if host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return trimmed, nil
		}
	}
	return "", errors.New("xai oauth redirect_uri must use https or loopback http")
}

func GenerateXAIOAuthCodeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

func GenerateXAIOAuthAuthorization(store *XAIOAuthSessionStore, proxyURL string, redirectURI string) (XAIOAuthAuthorization, error) {
	if store == nil {
		return XAIOAuthAuthorization{}, errors.New("xai oauth session store is required")
	}
	redirectURI, err := ValidateXAIOAuthRedirectURI(redirectURI)
	if err != nil {
		return XAIOAuthAuthorization{}, err
	}
	authorizeURL, err := validateXAIOAuthEndpointURL(xaiOAuthAuthorizeURL)
	if err != nil {
		return XAIOAuthAuthorization{}, err
	}
	state, err := randomXAIOAuthHex(32)
	if err != nil {
		return XAIOAuthAuthorization{}, err
	}
	nonce, err := randomXAIOAuthHex(16)
	if err != nil {
		return XAIOAuthAuthorization{}, err
	}
	sessionID, err := randomXAIOAuthHex(16)
	if err != nil {
		return XAIOAuthAuthorization{}, err
	}
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return XAIOAuthAuthorization{}, err
	}
	codeVerifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	clientID := common.GetEnvOrDefaultString("XAI_OAUTH_CLIENT_ID", xaiOAuthDefaultClientID)
	values := url.Values{}
	values.Set("response_type", "code")
	values.Set("client_id", clientID)
	values.Set("redirect_uri", redirectURI)
	values.Set("scope", xaiOAuthDefaultScope)
	values.Set("state", state)
	values.Set("nonce", nonce)
	values.Set("code_challenge", GenerateXAIOAuthCodeChallenge(codeVerifier))
	values.Set("code_challenge_method", "S256")
	values.Set("plan", "generic")
	values.Set("referrer", "new-api")
	store.set(sessionID, xaiOAuthSession{
		State:        state,
		CodeVerifier: codeVerifier,
		ClientID:     clientID,
		ProxyURL:     strings.TrimSpace(proxyURL),
		RedirectURI:  redirectURI,
		CreatedAt:    store.now(),
	})
	return XAIOAuthAuthorization{
		AuthURL:   authorizeURL + "?" + values.Encode(),
		SessionID: sessionID,
		State:     state,
	}, nil
}

func ExchangeXAIOAuthCode(ctx context.Context, store *XAIOAuthSessionStore, sessionID string, authorizationInput string, state string) (*XAIOAuthTokenInfo, error) {
	if store == nil {
		return nil, errors.New("xai oauth session store is required")
	}
	session, ok := store.consume(strings.TrimSpace(sessionID))
	if !ok {
		return nil, errors.New("xai oauth session not found or expired")
	}
	parsed := ParseXAIOAuthAuthorizationInput(authorizationInput)
	if parsed.Code == "" {
		return nil, errors.New("xai oauth authorization code is required")
	}
	providedState := strings.TrimSpace(state)
	if providedState == "" {
		providedState = parsed.State
	}
	if parsed.RequiresState && providedState == "" {
		return nil, errors.New("xai oauth state is required for callback URLs")
	}
	if providedState != "" && subtle.ConstantTimeCompare([]byte(providedState), []byte(session.State)) != 1 {
		return nil, errors.New("invalid xai oauth state")
	}
	client, err := getXAIOAuthHTTPClient(session.ProxyURL)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", session.ClientID)
	form.Set("code", parsed.Code)
	form.Set("redirect_uri", session.RedirectURI)
	form.Set("code_verifier", session.CodeVerifier)
	return requestXAIOAuthToken(ctx, client, xaiOAuthTokenURL, form, session.ClientID)
}

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

func RefreshXAIOAuthTokenForClientWithProxy(ctx context.Context, refreshToken string, proxyURL string, clientID string) (*CodexOAuthTokenResult, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		clientID = common.GetEnvOrDefaultString("XAI_OAUTH_CLIENT_ID", xaiOAuthDefaultClientID)
	}
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
	info, err := requestXAIOAuthToken(ctx, client, tokenURL, form, clientID)
	if err != nil {
		return nil, fmt.Errorf("xai oauth refresh failed: %w", err)
	}
	return &CodexOAuthTokenResult{
		AccessToken:  info.AccessToken,
		RefreshToken: info.RefreshToken,
		ExpiresAt:    time.Unix(info.ExpiresAt, 0),
	}, nil
}

func RefreshXAIOAuthTokenInfoWithProxy(ctx context.Context, refreshToken string, proxyURL string, clientID string) (*XAIOAuthTokenInfo, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil, errors.New("empty refresh_token")
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		clientID = common.GetEnvOrDefaultString("XAI_OAUTH_CLIENT_ID", xaiOAuthDefaultClientID)
	}
	client, err := getXAIOAuthHTTPClient(proxyURL)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", clientID)
	form.Set("refresh_token", refreshToken)
	info, err := requestXAIOAuthToken(ctx, client, xaiOAuthTokenURL, form, clientID)
	if err != nil {
		return nil, err
	}
	if info.RefreshToken == "" {
		info.RefreshToken = refreshToken
	}
	return info, nil
}

func requestXAIOAuthToken(ctx context.Context, client *http.Client, tokenURL string, form url.Values, clientID string) (*XAIOAuthTokenInfo, error) {
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
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("xai oauth token request failed: status=%d", resp.StatusCode)
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := common.DecodeJson(io.LimitReader(resp.Body, xaiOAuthMaxTokenBodyBytes), &payload); err != nil {
		return nil, err
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return nil, errors.New("xai oauth token response missing access_token")
	}
	if payload.ExpiresIn <= 0 {
		payload.ExpiresIn = xaiOAuthDefaultExpiresIn
	}
	if strings.TrimSpace(payload.TokenType) == "" {
		payload.TokenType = "Bearer"
	}
	info := &XAIOAuthTokenInfo{
		AccessToken:  strings.TrimSpace(payload.AccessToken),
		RefreshToken: strings.TrimSpace(payload.RefreshToken),
		IDToken:      strings.TrimSpace(payload.IDToken),
		TokenType:    strings.TrimSpace(payload.TokenType),
		ExpiresIn:    payload.ExpiresIn,
		ExpiresAt:    time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second).Unix(),
		ClientID:     strings.TrimSpace(clientID),
		Scope:        strings.TrimSpace(payload.Scope),
	}
	applyXAIOAuthClaims(info, info.IDToken)
	applyXAIOAuthClaims(info, info.AccessToken)
	return info, nil
}

func applyXAIOAuthClaims(info *XAIOAuthTokenInfo, token string) {
	if info == nil || strings.TrimSpace(token) == "" {
		return
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return
	}
	claims := map[string]any{}
	if err := common.Unmarshal(payload, &claims); err != nil {
		return
	}
	if info.Email == "" {
		info.Email = xaiOAuthClaimString(claims, "email")
	}
	if info.Subject == "" {
		info.Subject = xaiOAuthClaimString(claims, "sub")
	}
	if info.TeamID == "" {
		info.TeamID = xaiOAuthClaimString(claims, "team_id")
	}
	if info.SubscriptionTier == "" {
		info.SubscriptionTier = xaiOAuthClaimString(claims, "subscription_tier")
	}
	if info.EntitlementStatus == "" {
		info.EntitlementStatus = xaiOAuthClaimString(claims, "entitlement_status")
	}
}

func xaiOAuthClaimString(claims map[string]any, key string) string {
	value, _ := claims[key].(string)
	return strings.TrimSpace(value)
}

func validateXAIOAuthEndpointURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", errors.New("invalid xai oauth endpoint")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("xai oauth endpoint must not include userinfo, query, or fragment")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "x.ai" && !strings.HasSuffix(host, ".x.ai") {
		return "", errors.New("xai oauth endpoint host is not trusted")
	}
	return parsed.String(), nil
}

func randomXAIOAuthHex(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
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
