package service

import (
	"bytes"
	"context"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const defaultNewAPIRequestTimeout = 30 * time.Second

type NewAPIAdapter struct {
	Client *http.Client
}

type newAPIEnvelope[T any] struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type newAPIAuthConfig struct {
	Email         string `json:"email"`
	Password      string `json:"password"`
	AccessToken   string `json:"access_token"`
	UserID        int    `json:"user_id"`
	SessionSource string `json:"session_source,omitempty"`
}

type newAPILoginData struct {
	ID int `json:"id"`
}

type newAPIUserGroup struct {
	Ratio any    `json:"ratio"`
	Desc  string `json:"desc"`
}

type newAPIToken struct {
	ID                 int     `json:"id"`
	Key                string  `json:"key"`
	Status             int     `json:"status"`
	Name               string  `json:"name"`
	ExpiredTime        int64   `json:"expired_time"`
	RemainQuota        int     `json:"remain_quota"`
	UnlimitedQuota     bool    `json:"unlimited_quota"`
	ModelLimitsEnabled bool    `json:"model_limits_enabled"`
	ModelLimits        string  `json:"model_limits"`
	AllowIps           *string `json:"allow_ips"`
	Group              string  `json:"group"`
	CrossGroupRetry    bool    `json:"cross_group_retry"`
}

type newAPITokenPage struct {
	Items    []newAPIToken `json:"items"`
	Total    int           `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
}

type newAPITokenKeyData struct {
	Key string `json:"key"`
}

func (a NewAPIAdapter) DiscoverGroups(ctx context.Context, source *model.UpstreamSource) ([]UpstreamGroup, error) {
	groupMap, err := newAPIManagementRequest[map[string]newAPIUserGroup](ctx, &a, source, http.MethodGet, "/user/self/groups", nil, nil)
	if err != nil {
		return nil, err
	}

	groupIDs := make([]string, 0, len(groupMap))
	for groupID := range groupMap {
		groupIDs = append(groupIDs, groupID)
	}
	sort.Strings(groupIDs)

	groups := make([]UpstreamGroup, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		groupID = strings.TrimSpace(groupID)
		if groupID == "" || groupID == "auto" {
			continue
		}
		item := groupMap[groupID]
		rate, ok := parseNewAPIGroupRatio(item.Ratio)
		if !ok {
			continue
		}
		rateCopy := rate
		groups = append(groups, UpstreamGroup{
			ID:                      groupID,
			Name:                    groupID,
			Description:             strings.TrimSpace(item.Desc),
			Platform:                inferNewAPIGroupPlatform(groupID, item.Desc),
			Status:                  "enabled",
			RateMultiplier:          &rateCopy,
			EffectiveRateMultiplier: &rateCopy,
		})
	}
	return groups, nil
}

func (a NewAPIAdapter) CreateKey(ctx context.Context, source *model.UpstreamSource, groupID string, name string) (UpstreamKey, error) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return UpstreamKey{}, errors.New("new-api group ID is required")
	}
	name = strings.TrimSpace(name)
	payload := map[string]any{
		"name":                 name,
		"expired_time":         -1,
		"remain_quota":         0,
		"unlimited_quota":      true,
		"model_limits_enabled": false,
		"model_limits":         "",
		"group":                groupID,
		"cross_group_retry":    false,
	}
	if _, err := newAPIManagementRequest[struct{}](ctx, &a, source, http.MethodPost, "/token/", nil, payload); err != nil {
		return UpstreamKey{}, err
	}

	token, err := a.findTokenByNameAndGroup(ctx, source, name, groupID)
	if err != nil {
		return UpstreamKey{}, err
	}
	return a.tokenToUpstreamKey(ctx, source, token)
}

func (a NewAPIAdapter) UpdateKey(ctx context.Context, source *model.UpstreamSource, keyID string, groupID string, name string) (UpstreamKey, error) {
	tokenID, err := strconv.Atoi(strings.TrimSpace(keyID))
	if err != nil || tokenID <= 0 {
		return UpstreamKey{}, fmt.Errorf("invalid new-api token ID %q", keyID)
	}
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return UpstreamKey{}, errors.New("new-api group ID is required")
	}

	token, err := newAPIManagementRequest[newAPIToken](ctx, &a, source, http.MethodGet, "/token/"+strconv.Itoa(tokenID), nil, nil)
	if err != nil {
		return UpstreamKey{}, err
	}
	if token.ID == 0 {
		token.ID = tokenID
	}
	token.Name = strings.TrimSpace(name)
	token.Group = groupID
	if token.Status == 0 {
		token.Status = common.TokenStatusEnabled
	}
	if token.ExpiredTime == 0 {
		token.ExpiredTime = -1
	}
	payload := map[string]any{
		"id":                   token.ID,
		"name":                 token.Name,
		"status":               token.Status,
		"expired_time":         token.ExpiredTime,
		"remain_quota":         token.RemainQuota,
		"unlimited_quota":      token.UnlimitedQuota,
		"model_limits_enabled": token.ModelLimitsEnabled,
		"model_limits":         token.ModelLimits,
		"allow_ips":            token.AllowIps,
		"group":                token.Group,
		"cross_group_retry":    token.CrossGroupRetry,
	}
	if _, err := newAPIManagementRequest[newAPIToken](ctx, &a, source, http.MethodPut, "/token/", nil, payload); err != nil {
		return UpstreamKey{}, err
	}
	return a.tokenToUpstreamKey(ctx, source, token)
}

func (a NewAPIAdapter) ListKeys(ctx context.Context, source *model.UpstreamSource, groupID string) ([]UpstreamKey, error) {
	tokens, err := a.searchTokens(ctx, source, "")
	if err != nil {
		return nil, err
	}
	groupID = strings.TrimSpace(groupID)
	keys := make([]UpstreamKey, 0, len(tokens))
	for _, token := range tokens {
		if strings.TrimSpace(token.Group) != groupID {
			continue
		}
		key, err := a.tokenToUpstreamKey(ctx, source, token)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func (a NewAPIAdapter) ensureManagementAuth(ctx context.Context, source *model.UpstreamSource) (newAPIAuthConfig, error) {
	authConfig, err := parseNewAPIAuthConfig(source)
	if err != nil {
		return newAPIAuthConfig{}, err
	}
	if authConfig.AccessToken != "" && authConfig.UserID > 0 {
		return authConfig, nil
	}
	// Headless browser first (per chosen strategy) when configured.
	if upstreamBrowserEnabled() {
		if acquired, bErr := acquireNewAPISessionViaBrowser(ctx, source, authConfig); bErr == nil && acquired.AccessToken != "" {
			if marshaled := mustMarshalNewAPIAuthConfig(acquired); marshaled != "" {
				source.AuthConfig = marshaled
			}
			return acquired, nil
		}
	}
	// Fall back to password login (works only without Turnstile).
	return a.loginManagementAuth(ctx, source, authConfig)
}

func mustMarshalNewAPIAuthConfig(cfg newAPIAuthConfig) string {
	data, err := common.Marshal(cfg)
	if err != nil {
		return ""
	}
	return string(data)
}

func (a NewAPIAdapter) refreshManagementAuth(ctx context.Context, source *model.UpstreamSource, authConfig newAPIAuthConfig) (newAPIAuthConfig, error) {
	authConfig.AccessToken = ""
	authConfig.UserID = 0
	return a.loginManagementAuth(ctx, source, authConfig)
}

func (a NewAPIAdapter) loginManagementAuth(ctx context.Context, source *model.UpstreamSource, authConfig newAPIAuthConfig) (newAPIAuthConfig, error) {
	if strings.TrimSpace(authConfig.Email) == "" || authConfig.Password == "" {
		return newAPIAuthConfig{}, errors.New("new-api username/email and password are required")
	}

	loginPayload := map[string]string{
		"username": strings.TrimSpace(authConfig.Email),
		"password": authConfig.Password,
	}
	loginData, cookies, err := newAPIRequestWithCookies[newAPILoginData](ctx, &a, source, http.MethodPost, "/user/login", nil, loginPayload, newAPIAuthConfig{}, nil)
	if err != nil {
		if isUpstreamSourceTurnstileError(err) {
			return newAPIAuthConfig{}, ErrUpstreamSourceTurnstileRequired
		}
		return newAPIAuthConfig{}, err
	}
	if loginData.ID <= 0 {
		return newAPIAuthConfig{}, errors.New("new-api login response missing user id")
	}
	authConfig.UserID = loginData.ID
	accessToken, err := newAPIRequest[string](ctx, &a, source, http.MethodGet, "/user/token", nil, nil, authConfig, cookies)
	if err != nil {
		return newAPIAuthConfig{}, err
	}
	if strings.TrimSpace(accessToken) == "" {
		return newAPIAuthConfig{}, errors.New("new-api access token response is empty")
	}
	authConfig.AccessToken = strings.TrimSpace(accessToken)

	updated, err := common.Marshal(authConfig)
	if err != nil {
		return newAPIAuthConfig{}, err
	}
	source.AuthConfig = string(updated)
	return authConfig, nil
}

// newAPIExchangeCookieForToken replays an admin-pasted new-api session cookie
// against /user/self (to resolve the user id) and /user/token (to mint an
// access token) so a manually imported cookie can be normalized into the
// same access_token + user_id pair used by the login flow.
func newAPIExchangeCookieForToken(source *model.UpstreamSource, cookieHeader string) (string, int, error) {
	return newAPIExchangeCookiesForToken(context.Background(), source, parseCookieHeader(cookieHeader))
}

// newAPIExchangeCookiesForToken resolves a new-api admin access token + user id
// from browser session cookies: GET /user/self for the id, GET /user/token for
// the token. Shared by manual cookie import and headless-browser acquisition.
func newAPIExchangeCookiesForToken(ctx context.Context, source *model.UpstreamSource, cookies []*http.Cookie) (string, int, error) {
	adapter := &NewAPIAdapter{}
	self, err := newAPIRequest[newAPILoginData](ctx, adapter, source, http.MethodGet, "/user/self", nil, nil, newAPIAuthConfig{}, cookies)
	if err != nil {
		return "", 0, err
	}
	if self.ID <= 0 {
		return "", 0, errors.New("session did not resolve a user id")
	}
	token, err := newAPIRequest[string](ctx, adapter, source, http.MethodGet, "/user/token", nil, nil, newAPIAuthConfig{UserID: self.ID}, cookies)
	if err != nil {
		return "", 0, err
	}
	return strings.TrimSpace(token), self.ID, nil
}

func parseCookieHeader(header string) []*http.Cookie {
	req := http.Request{Header: http.Header{"Cookie": []string{strings.TrimSpace(header)}}}
	return req.Cookies()
}

func parseNewAPIAuthConfig(source *model.UpstreamSource) (newAPIAuthConfig, error) {
	if source == nil {
		return newAPIAuthConfig{}, errors.New("upstream source is required")
	}
	raw, err := ReadUpstreamSourceAuthConfig(source.AuthConfig)
	if err != nil {
		return newAPIAuthConfig{}, err
	}
	if strings.TrimSpace(raw) == "" {
		return newAPIAuthConfig{}, nil
	}
	var authConfig newAPIAuthConfig
	if err := common.UnmarshalJsonStr(raw, &authConfig); err != nil {
		return newAPIAuthConfig{}, err
	}
	return authConfig, nil
}

func (a NewAPIAdapter) findTokenByNameAndGroup(ctx context.Context, source *model.UpstreamSource, name string, groupID string) (newAPIToken, error) {
	tokens, err := a.searchTokens(ctx, source, name)
	if err != nil {
		return newAPIToken{}, err
	}
	var matched newAPIToken
	for _, token := range tokens {
		if strings.TrimSpace(token.Name) == name && strings.TrimSpace(token.Group) == groupID {
			if matched.ID == 0 || token.ID > matched.ID {
				matched = token
			}
		}
	}
	if matched.ID > 0 {
		return matched, nil
	}
	return newAPIToken{}, fmt.Errorf("new-api token %q in group %q not found after create", name, groupID)
}

func (a NewAPIAdapter) searchTokens(ctx context.Context, source *model.UpstreamSource, keyword string) ([]newAPIToken, error) {
	tokens := make([]newAPIToken, 0)
	page := 1
	for {
		query := url.Values{}
		query.Set("keyword", keyword)
		query.Set("p", strconv.Itoa(page))
		query.Set("size", "100")
		data, err := newAPIManagementRequest[newAPITokenPage](ctx, &a, source, http.MethodGet, "/token/search", query, nil)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, data.Items...)
		if len(data.Items) == 0 {
			break
		}
		if data.Total > 0 && len(tokens) >= data.Total {
			break
		}
		if data.PageSize > 0 && len(data.Items) < data.PageSize {
			break
		}
		page++
	}
	return tokens, nil
}

func (a NewAPIAdapter) tokenToUpstreamKey(ctx context.Context, source *model.UpstreamSource, token newAPIToken) (UpstreamKey, error) {
	if token.ID <= 0 {
		return UpstreamKey{}, errors.New("new-api token ID is missing")
	}
	fullKey, err := newAPIManagementRequest[newAPITokenKeyData](ctx, &a, source, http.MethodPost, "/token/"+strconv.Itoa(token.ID)+"/key", nil, nil)
	if err != nil {
		return UpstreamKey{}, err
	}
	return UpstreamKey{
		ID:      strconv.Itoa(token.ID),
		Key:     strings.TrimSpace(fullKey.Key),
		Name:    strings.TrimSpace(token.Name),
		GroupID: strings.TrimSpace(token.Group),
	}, nil
}

func newAPIRequest[T any](ctx context.Context, adapter *NewAPIAdapter, source *model.UpstreamSource, method string, endpoint string, query url.Values, payload any, authConfig newAPIAuthConfig, cookies []*http.Cookie) (T, error) {
	data, _, err := newAPIRequestWithCookies[T](ctx, adapter, source, method, endpoint, query, payload, authConfig, cookies)
	return data, err
}

func newAPIManagementRequest[T any](ctx context.Context, adapter *NewAPIAdapter, source *model.UpstreamSource, method string, endpoint string, query url.Values, payload any) (T, error) {
	var zero T
	if adapter == nil {
		adapter = &NewAPIAdapter{}
	}
	authConfig, err := adapter.ensureManagementAuth(ctx, source)
	if err != nil {
		return zero, err
	}
	data, err := newAPIRequest[T](ctx, adapter, source, method, endpoint, query, payload, authConfig, nil)
	if err == nil {
		return data, nil
	}
	if !isNewAPIAuthError(err) || !newAPIAuthConfigHasCredentials(authConfig) {
		return zero, err
	}
	refreshedAuth, refreshErr := adapter.refreshManagementAuth(ctx, source, authConfig)
	if refreshErr != nil {
		if errors.Is(refreshErr, ErrUpstreamSourceTurnstileRequired) {
			return zero, refreshErr
		}
		return zero, fmt.Errorf("%w; refresh auth failed: %s", err, SanitizeUpstreamSourceError(refreshErr))
	}
	return newAPIRequest[T](ctx, adapter, source, method, endpoint, query, payload, refreshedAuth, nil)
}

func newAPIRequestWithCookies[T any](ctx context.Context, adapter *NewAPIAdapter, source *model.UpstreamSource, method string, endpoint string, query url.Values, payload any, authConfig newAPIAuthConfig, cookies []*http.Cookie) (T, []*http.Cookie, error) {
	var zero T

	requestCtx := ctx
	if _, ok := requestCtx.Deadline(); !ok {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(requestCtx, defaultNewAPIRequestTimeout)
		defer cancel()
	}

	requestURL, err := buildNewAPIURL(source, endpoint, query)
	if err != nil {
		return zero, nil, err
	}
	if err := validateUpstreamSourceURL(source, requestURL); err != nil {
		return zero, nil, err
	}

	var body io.Reader
	if payload != nil {
		data, err := common.Marshal(payload)
		if err != nil {
			return zero, nil, err
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(requestCtx, method, requestURL, body)
	if err != nil {
		return zero, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(authConfig.AccessToken) != "" {
		req.Header.Set("Authorization", strings.TrimSpace(authConfig.AccessToken))
	}
	if authConfig.UserID > 0 {
		req.Header.Set("New-Api-User", strconv.Itoa(authConfig.UserID))
	}
	for _, cookie := range cookies {
		if cookie != nil {
			req.AddCookie(cookie)
		}
	}

	resp, err := newAPIHTTPClient(adapter, source).Do(req)
	if err != nil {
		return zero, nil, fmt.Errorf("%s %s failed: %s", method, requestURL, SanitizeUpstreamSourceError(err))
	}
	defer resp.Body.Close()

	var envelope newAPIEnvelope[T]
	if err := decodeNewAPIResponseBody(resp.Body, &envelope); err != nil {
		return zero, nil, fmt.Errorf("decode upstream response failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zero, nil, newAPIRequestError{StatusCode: resp.StatusCode, Message: envelope.Message}
	}
	if !envelope.Success {
		if strings.TrimSpace(envelope.Message) == "" {
			return zero, nil, newAPIRequestError{StatusCode: resp.StatusCode, Message: "upstream request failed"}
		}
		return zero, nil, newAPIRequestError{StatusCode: resp.StatusCode, Message: envelope.Message}
	}
	return envelope.Data, resp.Cookies(), nil
}

type newAPIRequestError struct {
	StatusCode int
	Message    string
}

func (e newAPIRequestError) Error() string {
	message := strings.TrimSpace(SanitizeUpstreamSourceError(errors.New(e.Message)))
	if message == "" {
		message = "upstream request failed"
	}
	if e.StatusCode < 200 || e.StatusCode >= 300 {
		return fmt.Sprintf("upstream request failed with status %d: %s", e.StatusCode, message)
	}
	return "upstream request failed: " + message
}

func isNewAPIAuthError(err error) bool {
	if err == nil {
		return false
	}
	var requestErr newAPIRequestError
	if errors.As(err, &requestErr) {
		if requestErr.StatusCode == http.StatusUnauthorized || requestErr.StatusCode == http.StatusForbidden {
			return true
		}
		text := strings.ToLower(requestErr.Message)
		return strings.Contains(text, "access token") ||
			strings.Contains(text, "not logged in") ||
			strings.Contains(text, "new-api-user") ||
			strings.Contains(text, "unauthorized") ||
			strings.Contains(text, "未登录") ||
			strings.Contains(text, "访问令牌")
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "invalid access token") || strings.Contains(text, "not logged in")
}

func newAPIAuthConfigHasCredentials(authConfig newAPIAuthConfig) bool {
	return strings.TrimSpace(authConfig.Email) != "" && authConfig.Password != ""
}

func decodeNewAPIResponseBody(reader io.Reader, v any) error {
	body, err := io.ReadAll(io.LimitReader(reader, sub2APIResponseBodyLimitBytes+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > sub2APIResponseBodyLimitBytes {
		return fmt.Errorf("response body too large: limit %d bytes", sub2APIResponseBodyLimitBytes)
	}
	return common.Unmarshal(body, v)
}

func buildNewAPIURL(source *model.UpstreamSource, endpoint string, query url.Values) (string, error) {
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
		apiBasePath = "/api"
	}
	joinedPath := strings.Trim(strings.TrimRight(apiBasePath, "/")+"/"+strings.TrimLeft(endpoint, "/"), "/")
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + joinedPath
	if strings.HasSuffix(endpoint, "/") && !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func newAPIHTTPClient(adapter *NewAPIAdapter, source *model.UpstreamSource) *http.Client {
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

func parseNewAPIGroupRatio(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case stdjson.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func inferNewAPIGroupPlatform(name string, description string) string {
	text := strings.ToLower(name + " " + description)
	switch {
	case strings.Contains(text, "anthropic"), strings.Contains(text, "claude"):
		return "anthropic"
	case strings.Contains(text, "openai"), strings.Contains(text, "chatgpt"), strings.Contains(text, "gpt"):
		return "openai"
	default:
		return ""
	}
}
