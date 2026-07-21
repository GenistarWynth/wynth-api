package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

var ErrUpstreamSource2FARequired = errors.New("upstream source requires 2FA")

const sub2APIResponseBodyLimitBytes int64 = 1 << 20
const defaultSub2APIRequestTimeout = 30 * time.Second

// sub2APIAccessTokenRefreshSkewSeconds is the safety margin subtracted from
// an access token's expiry when deciding whether it's still usable, so a
// token that is about to expire mid-request is proactively renewed instead
// of used right up to the wire.
const sub2APIAccessTokenRefreshSkewSeconds = int64(60)

type Sub2APIAdapter struct {
	Client *http.Client
}

type sub2APIEnvelope[T any] struct {
	// Code is decoded as raw JSON rather than int because some sub2api
	// gateways send it as a numeric string (e.g. "0", "40001") instead of a
	// JSON number, which broke common.Unmarshal against a hardcoded int
	// field. See sub2APICodeIndicatesError for the flexible interpretation.
	Code    json.RawMessage `json:"code"`
	Message string          `json:"message"`
	Data    T               `json:"data"`
}

// sub2APICodeIndicatesError treats only a NONZERO number (int or numeric
// string) as an error. Non-numeric strings ("success") and empty/absent
// codes are not errors by themselves -- HTTP status is the primary success
// signal. Gateways vary (some send code as int 0, some as string "0" or
// "success").
func sub2APICodeIndicatesError(raw json.RawMessage) bool {
	s := sub2APINormalizeCode(raw)
	if s == "" || s == "null" {
		return false
	}
	// ParseFloat (rather than Atoi) so a numeric-but-non-integer code like
	// "5.0" still classifies as a nonzero error code instead of falling
	// through to "not error".
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return n != 0
	}
	return false
}

func sub2APICodeString(raw json.RawMessage) string {
	return sub2APINormalizeCode(raw)
}

// sub2APINormalizeCode strips surrounding whitespace and a JSON string's
// quotes from a raw envelope "code" value, leaving a bare token suitable for
// numeric classification or display.
func sub2APINormalizeCode(raw json.RawMessage) string {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	return strings.TrimSpace(s)
}

type sub2APIAuthConfig struct {
	Email         string `json:"email"`
	Password      string `json:"password"`
	AccessToken   string `json:"access_token"`
	RefreshToken  string `json:"refresh_token"`
	ExpiresAt     int64  `json:"expires_at"`
	SessionSource string `json:"session_source,omitempty"`
}

type sub2APILoginData struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	ExpiresAt    int64  `json:"expires_at"`
	Requires2FA  bool   `json:"requires_2fa"`
}

type sub2APIGroup struct {
	ID             *int64   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Platform       string   `json:"platform"`
	Status         string   `json:"status"`
	RateMultiplier *float64 `json:"rate_multiplier"`
}

type sub2APIKey struct {
	ID            *int64 `json:"id"`
	Key           string `json:"key"`
	Name          string `json:"name"`
	GroupID       *int64 `json:"group_id"`
	ConfigVersion *int64 `json:"config_version"`
}

type sub2APIListKeysData struct {
	Items    []sub2APIKey `json:"items"`
	Total    int          `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
}

func (a Sub2APIAdapter) DiscoverGroups(ctx context.Context, source *model.UpstreamSource) ([]UpstreamGroup, error) {
	token, err := a.ensureAccessToken(ctx, source)
	if err != nil {
		return nil, err
	}

	groups, err := sub2APIRequest[[]sub2APIGroup](ctx, &a, source, http.MethodGet, "/groups/available", nil, nil, token)
	if err != nil {
		return nil, err
	}
	rates, err := sub2APIRequest[map[string]float64](ctx, &a, source, http.MethodGet, "/groups/rates", nil, nil, token)
	if err != nil {
		return nil, err
	}

	result := make([]UpstreamGroup, 0, len(groups))
	for _, group := range groups {
		id := formatNullableIntID(group.ID)
		var effective *float64
		if rate, ok := rates[id]; ok {
			rateCopy := rate
			effective = &rateCopy
		} else if group.RateMultiplier != nil {
			rateCopy := *group.RateMultiplier
			effective = &rateCopy
		}
		result = append(result, UpstreamGroup{
			ID:                      id,
			Name:                    group.Name,
			Description:             group.Description,
			Platform:                group.Platform,
			Status:                  group.Status,
			RateMultiplier:          group.RateMultiplier,
			EffectiveRateMultiplier: effective,
		})
	}
	return result, nil
}

func (a Sub2APIAdapter) CreateKey(ctx context.Context, source *model.UpstreamSource, groupID string, name string) (UpstreamKey, error) {
	token, err := a.ensureAccessToken(ctx, source)
	if err != nil {
		return UpstreamKey{}, err
	}
	groupIDValue, err := parseSub2APIGroupIDPayload(groupID)
	if err != nil {
		return UpstreamKey{}, err
	}
	payload := map[string]any{
		"group_id": groupIDValue,
		"name":     name,
	}
	key, err := sub2APIRequest[sub2APIKey](ctx, &a, source, http.MethodPost, "/keys", nil, payload, token)
	if err != nil {
		return UpstreamKey{}, err
	}
	return normalizeSub2APIKey(key), nil
}

func (a Sub2APIAdapter) UpdateKey(ctx context.Context, source *model.UpstreamSource, keyID string, groupID string, name string) (UpstreamKey, error) {
	token, err := a.ensureAccessToken(ctx, source)
	if err != nil {
		return UpstreamKey{}, err
	}
	groupIDValue, err := parseSub2APIGroupIDPayload(groupID)
	if err != nil {
		return UpstreamKey{}, err
	}
	payload := map[string]any{
		"group_id": groupIDValue,
		"name":     name,
	}
	key, err := sub2APIRequest[sub2APIKey](ctx, &a, source, http.MethodPut, "/keys/"+url.PathEscape(keyID), nil, payload, token)
	if err != nil {
		errorText := strings.ToLower(err.Error())
		if !strings.Contains(errorText, "config_version") || !strings.Contains(errorText, "required") {
			return UpstreamKey{}, err
		}
		current, getErr := sub2APIRequest[sub2APIKey](ctx, &a, source, http.MethodGet, "/keys/"+url.PathEscape(keyID), nil, nil, token)
		if getErr != nil {
			return UpstreamKey{}, getErr
		}
		configVersion := int64(0)
		if current.ConfigVersion != nil {
			configVersion = *current.ConfigVersion
		}
		payload["config_version"] = configVersion
		key, err = sub2APIRequest[sub2APIKey](ctx, &a, source, http.MethodPut, "/keys/"+url.PathEscape(keyID), nil, payload, token)
		if err != nil {
			return UpstreamKey{}, err
		}
	}
	normalized := normalizeSub2APIKey(key)
	if normalized.ID == "" {
		normalized.ID = keyID
	}
	if normalized.GroupID == "" {
		normalized.GroupID = groupID
	}
	if normalized.Name == "" {
		normalized.Name = name
	}
	return normalized, nil
}

func (a Sub2APIAdapter) ListKeys(ctx context.Context, source *model.UpstreamSource, groupID string) ([]UpstreamKey, error) {
	token, err := a.ensureAccessToken(ctx, source)
	if err != nil {
		return nil, err
	}

	keys := make([]UpstreamKey, 0)
	page := 1
	for {
		query := url.Values{}
		query.Set("group_id", groupID)
		query.Set("page", strconv.Itoa(page))
		query.Set("page_size", "100")

		data, err := sub2APIRequest[sub2APIListKeysData](ctx, &a, source, http.MethodGet, "/keys", query, nil, token)
		if err != nil {
			return nil, err
		}
		for _, item := range data.Items {
			keys = append(keys, normalizeSub2APIKey(item))
		}

		if len(data.Items) == 0 {
			break
		}
		if data.Total > 0 && len(keys) >= data.Total {
			break
		}
		if data.PageSize > 0 && len(data.Items) < data.PageSize {
			break
		}
		page++
	}
	return keys, nil
}

func (a Sub2APIAdapter) ensureAccessToken(ctx context.Context, source *model.UpstreamSource) (string, error) {
	authConfig, err := parseSub2APIAuthConfig(source)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	if authConfig.AccessToken != "" && (authConfig.ExpiresAt == 0 || authConfig.ExpiresAt-sub2APIAccessTokenRefreshSkewSeconds > now) {
		return authConfig.AccessToken, nil
	}
	// Access token missing/expired: try refresh-token renewal before
	// browser/password login.
	if authConfig.RefreshToken != "" {
		refreshed, rErr := refreshSub2APIAccessToken(ctx, &a, source, authConfig)
		if rErr == nil && refreshed.AccessToken != "" {
			if marshaled := mustMarshalSub2APIAuthConfig(refreshed); marshaled != "" {
				source.AuthConfig = marshaled
			}
			return refreshed.AccessToken, nil
		}
		if rErr != nil {
			common.SysLog("sub2api refresh-token renewal failed, falling back: " + SanitizeUpstreamSourceError(rErr))
		}
	}
	// Headless browser first (per chosen strategy) when configured.
	if upstreamBrowserEnabled() {
		acquired, bErr := acquireSub2APISessionViaBrowser(ctx, source, authConfig)
		if bErr == nil && acquired.AccessToken != "" {
			if marshaled := mustMarshalSub2APIAuthConfig(acquired); marshaled != "" {
				source.AuthConfig = marshaled
			}
			return acquired.AccessToken, nil
		}
		if bErr != nil {
			common.SysLog("upstream source headless browser login failed, falling back to password login: " + SanitizeUpstreamSourceError(bErr))
		}
	}
	if authConfig.Email == "" || authConfig.Password == "" {
		return "", errors.New("sub2api email and password are required")
	}

	payload := map[string]string{
		"email":    authConfig.Email,
		"password": authConfig.Password,
	}
	data, err := sub2APIRequest[sub2APILoginData](ctx, &a, source, http.MethodPost, "/auth/login", nil, payload, "")
	if err != nil {
		if isUpstreamSourceTurnstileError(err) {
			return "", ErrUpstreamSourceTurnstileRequired
		}
		return "", err
	}
	if data.Requires2FA {
		return "", ErrUpstreamSource2FARequired
	}
	if data.AccessToken == "" {
		return "", errors.New("sub2api login response missing access token")
	}

	authConfig.AccessToken = data.AccessToken
	authConfig.RefreshToken = data.RefreshToken
	authConfig.ExpiresAt = sub2APIResolveExpiresAt(data.AccessToken, data.ExpiresAt, data.ExpiresIn)
	updated, err := common.Marshal(authConfig)
	if err != nil {
		return "", err
	}
	source.AuthConfig = string(updated)
	return authConfig.AccessToken, nil
}

// sub2APIJWTExp decodes a JWT's exp (unix seconds) claim WITHOUT verifying
// its signature -- it is only used as a best-effort expiry hint when the
// gateway response/import request did not supply one explicitly. Returns 0
// if the token is not a well-formed JWT or carries no exp claim.
func sub2APIJWTExp(token string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		if payload, err = base64.URLEncoding.DecodeString(parts[1]); err != nil {
			return 0
		}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := common.Unmarshal(payload, &claims); err != nil {
		return 0
	}
	return claims.Exp
}

// sub2APIResolveExpiresAt prefers an explicit expiresAt, then an
// expiresIn-from-now duration, then falls back to the access token's JWT exp
// claim. 0 means "never expires".
func sub2APIResolveExpiresAt(accessToken string, expiresAt int64, expiresIn int64) int64 {
	if expiresAt > 0 {
		return expiresAt
	}
	if expiresIn > 0 {
		return time.Now().Unix() + expiresIn
	}
	return sub2APIJWTExp(accessToken)
}

func mustMarshalSub2APIAuthConfig(cfg sub2APIAuthConfig) string {
	data, err := common.Marshal(cfg)
	if err != nil {
		return ""
	}
	return string(data)
}

func parseSub2APIAuthConfig(source *model.UpstreamSource) (sub2APIAuthConfig, error) {
	if source == nil {
		return sub2APIAuthConfig{}, errors.New("upstream source is required")
	}
	raw, err := ReadUpstreamSourceAuthConfig(source.AuthConfig)
	if err != nil {
		return sub2APIAuthConfig{}, err
	}
	if strings.TrimSpace(raw) == "" {
		return sub2APIAuthConfig{}, nil
	}
	var authConfig sub2APIAuthConfig
	if err := common.UnmarshalJsonStr(raw, &authConfig); err != nil {
		return sub2APIAuthConfig{}, err
	}
	return authConfig, nil
}

// refreshSub2APIAccessToken exchanges the stored refresh token for a fresh
// access token. ASSUMPTION: POST {AdminAPIBasePath}/auth/refresh
// {"refresh_token": ...} returns the same shape as /auth/login. If a given
// sub2api build's refresh endpoint differs, this errors and the caller falls
// back to browser/password login (non-breaking).
func refreshSub2APIAccessToken(ctx context.Context, a *Sub2APIAdapter, source *model.UpstreamSource, authConfig sub2APIAuthConfig) (sub2APIAuthConfig, error) {
	if strings.TrimSpace(authConfig.RefreshToken) == "" {
		return authConfig, errors.New("no refresh token")
	}
	payload := map[string]string{"refresh_token": authConfig.RefreshToken}
	data, err := sub2APIRequest[sub2APILoginData](ctx, a, source, http.MethodPost, "/auth/refresh", nil, payload, "")
	if err != nil {
		return authConfig, err
	}
	if data.AccessToken == "" {
		return authConfig, errors.New("sub2api refresh response missing access token")
	}
	authConfig.AccessToken = data.AccessToken
	if data.RefreshToken != "" {
		authConfig.RefreshToken = data.RefreshToken
	}
	authConfig.ExpiresAt = sub2APIResolveExpiresAt(data.AccessToken, data.ExpiresAt, data.ExpiresIn)
	authConfig.SessionSource = "refresh"
	return authConfig, nil
}

func sub2APIRequest[T any](ctx context.Context, adapter *Sub2APIAdapter, source *model.UpstreamSource, method string, endpoint string, query url.Values, payload any, token string) (T, error) {
	var zero T

	requestCtx := ctx
	if _, ok := requestCtx.Deadline(); !ok {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(requestCtx, defaultSub2APIRequestTimeout)
		defer cancel()
	}

	requestURL, err := buildSub2APIURL(source, endpoint, query)
	if err != nil {
		return zero, err
	}
	if err := validateUpstreamSourceURL(source, requestURL); err != nil {
		return zero, err
	}

	var body io.Reader
	if payload != nil {
		data, err := common.Marshal(payload)
		if err != nil {
			return zero, err
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(requestCtx, method, requestURL, body)
	if err != nil {
		return zero, err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := sub2APIHTTPClient(adapter, source).Do(req)
	if err != nil {
		return zero, fmt.Errorf("%s %s failed: %s", method, requestURL, SanitizeUpstreamSourceError(err))
	}
	defer resp.Body.Close()

	var envelope sub2APIEnvelope[T]
	respBody, err := decodeSub2APIResponseBody(resp.Body, &envelope)
	if err != nil {
		if isUpstreamSourceCloudflareChallengeBody(resp.StatusCode, respBody) {
			return zero, ErrUpstreamSourceTurnstileRequired
		}
		// An empty/truncated/non-JSON body on a non-2xx status is almost always a
		// status-only upstream error (typically an expired session returning an
		// empty 401), so report the HTTP status + a body snippet instead of an
		// opaque "unexpected end of JSON input".
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return zero, fmt.Errorf("upstream request failed with status %d: %s", resp.StatusCode, upstreamSourceResponseSnippet(respBody))
		}
		return zero, fmt.Errorf("decode upstream response failed (HTTP %d): %s: %w", resp.StatusCode, upstreamSourceResponseSnippet(respBody), err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if strings.TrimSpace(envelope.Message) == "" {
			return zero, fmt.Errorf("upstream request failed with status %d", resp.StatusCode)
		}
		return zero, fmt.Errorf("upstream request failed with status %d: %s", resp.StatusCode, SanitizeUpstreamSourceError(errors.New(envelope.Message)))
	}
	if sub2APICodeIndicatesError(envelope.Code) {
		return zero, fmt.Errorf("upstream error %s: %s", sub2APICodeString(envelope.Code), SanitizeUpstreamSourceError(errors.New(envelope.Message)))
	}
	return envelope.Data, nil
}

// decodeSub2APIResponseBody decodes the response body into v and also
// returns the raw bytes read, so a decode failure can be inspected by the
// caller for a Cloudflare edge managed-challenge (HTML interstitial) instead
// of surfacing an opaque "decode failed" error.
func decodeSub2APIResponseBody(reader io.Reader, v any) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, sub2APIResponseBodyLimitBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > sub2APIResponseBodyLimitBytes {
		return body, fmt.Errorf("response body too large: limit %d bytes", sub2APIResponseBodyLimitBytes)
	}
	return body, common.Unmarshal(body, v)
}

func buildSub2APIURL(source *model.UpstreamSource, endpoint string, query url.Values) (string, error) {
	if source == nil {
		return "", errors.New("upstream source is required")
	}
	baseURL := strings.TrimSpace(source.BaseURL)
	if baseURL == "" {
		return "", errors.New("upstream source base URL is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	apiBasePath := strings.TrimSpace(source.AdminAPIBasePath)
	if apiBasePath == "" {
		apiBasePath = "/api/v1"
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + strings.Trim(strings.TrimRight(apiBasePath, "/")+"/"+strings.TrimLeft(endpoint, "/"), "/")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func validateUpstreamSourceURL(source *model.UpstreamSource, requestURL string) error {
	fetchSetting := system_setting.GetFetchSetting()
	allowPrivateIP := fetchSetting.AllowPrivateIp
	if source != nil {
		if config, err := parseUpstreamSourceSyncConfig(source.SyncConfig); err == nil && config.AllowPrivateIP {
			allowPrivateIP = true
		}
	}
	if err := common.ValidateURLWithFetchSetting(requestURL, fetchSetting.EnableSSRFProtection, allowPrivateIP, fetchSetting.DomainFilterMode, fetchSetting.IpFilterMode, fetchSetting.DomainList, fetchSetting.IpList, fetchSetting.AllowedPorts, fetchSetting.ApplyIPFilterForDomain); err != nil {
		return fmt.Errorf("request reject: %v", err)
	}
	return nil
}

func sub2APIHTTPClient(adapter *Sub2APIAdapter, source *model.UpstreamSource) *http.Client {
	var client *http.Client
	delegateExistingRedirect := false
	if adapter != nil && adapter.Client != nil {
		client = adapter.Client
		delegateExistingRedirect = true
	} else if defaultClient := GetHttpClient(); defaultClient != nil {
		client = defaultClient
	} else {
		client = http.DefaultClient
	}
	return sub2APIClientWithRedirectValidation(client, source, delegateExistingRedirect)
}

func sub2APIClientWithRedirectValidation(client *http.Client, source *model.UpstreamSource, delegateExistingRedirect bool) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	wrapped := *client
	existingCheckRedirect := client.CheckRedirect
	wrapped.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		urlStr := req.URL.String()
		if err := validateUpstreamSourceURL(source, urlStr); err != nil {
			return fmt.Errorf("redirect to %s blocked: %v", urlStr, err)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if delegateExistingRedirect && existingCheckRedirect != nil {
			return existingCheckRedirect(req, via)
		}
		return nil
	}
	return &wrapped
}

func normalizeSub2APIKey(key sub2APIKey) UpstreamKey {
	return UpstreamKey{
		ID:      formatNullableIntID(key.ID),
		Key:     key.Key,
		Name:    key.Name,
		GroupID: formatNullableIntID(key.GroupID),
	}
}

func formatNullableIntID(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}

func parseSub2APIGroupIDPayload(groupID string) (*int64, error) {
	trimmed := strings.TrimSpace(groupID)
	if trimmed == "" {
		return nil, errors.New("sub2api group ID is required")
	}
	parsed, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid sub2api group ID %q: %w", groupID, err)
	}
	return &parsed, nil
}

var upstreamSourceSensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)("(?:(?:access|refresh)_token|api[_-]?key|password|token)"\s*:\s*")[^"]*(")`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9][A-Za-z0-9_-]{16,}\b`),
	regexp.MustCompile(`(?i)(authorization\s*[:=]\s*)bearer\s+[^,\s;&]+`),
	regexp.MustCompile(`(?i)bearer\s+[^,\s;&]+`),
	regexp.MustCompile(`(?i)(cookie\s*[:=]\s*)[^,\s;&]+`),
	regexp.MustCompile(`(?i)(x-api-key\s*[:=]\s*)[^,\s;&]+`),
	regexp.MustCompile(`(?i)((?:access_token|refresh_token|api[_-]?key|password|token)\s*[=:]\s*)[^,\s;&]+`),
}

func SanitizeUpstreamSourceError(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	for _, pattern := range upstreamSourceSensitivePatterns {
		text = pattern.ReplaceAllString(text, "${1}[redacted]${2}")
	}
	if len(text) > 1024 {
		text = text[:1024]
	}
	return text
}
