package service

import (
	"bytes"
	"context"
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

type Sub2APIAdapter struct {
	Client *http.Client
}

type sub2APIEnvelope[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type sub2APIAuthConfig struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
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
	Platform       string   `json:"platform"`
	Status         string   `json:"status"`
	RateMultiplier *float64 `json:"rate_multiplier"`
}

type sub2APIKey struct {
	ID      *int64 `json:"id"`
	Key     string `json:"key"`
	Name    string `json:"name"`
	GroupID *int64 `json:"group_id"`
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
		return UpstreamKey{}, err
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
	if authConfig.AccessToken != "" && authConfig.ExpiresAt > time.Now().Unix() {
		return authConfig.AccessToken, nil
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
	authConfig.ExpiresAt = data.ExpiresAt
	if authConfig.ExpiresAt == 0 && data.ExpiresIn > 0 {
		authConfig.ExpiresAt = time.Now().Unix() + data.ExpiresIn
	}
	if authConfig.ExpiresAt == 0 {
		authConfig.ExpiresAt = time.Now().Add(time.Hour).Unix()
	}
	updated, err := common.Marshal(authConfig)
	if err != nil {
		return "", err
	}
	source.AuthConfig = string(updated)
	return authConfig.AccessToken, nil
}

func parseSub2APIAuthConfig(source *model.UpstreamSource) (sub2APIAuthConfig, error) {
	if source == nil {
		return sub2APIAuthConfig{}, errors.New("upstream source is required")
	}
	if strings.TrimSpace(source.AuthConfig) == "" {
		return sub2APIAuthConfig{}, nil
	}
	var authConfig sub2APIAuthConfig
	if err := common.UnmarshalJsonStr(source.AuthConfig, &authConfig); err != nil {
		return sub2APIAuthConfig{}, err
	}
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
	if err := decodeSub2APIResponseBody(resp.Body, &envelope); err != nil {
		return zero, fmt.Errorf("decode upstream response failed: %w", err)
	}
	if envelope.Code != 0 {
		return zero, fmt.Errorf("upstream error %d: %s", envelope.Code, SanitizeUpstreamSourceError(errors.New(envelope.Message)))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zero, fmt.Errorf("upstream request failed with status %d", resp.StatusCode)
	}
	return envelope.Data, nil
}

func decodeSub2APIResponseBody(reader io.Reader, v any) error {
	body, err := io.ReadAll(io.LimitReader(reader, sub2APIResponseBodyLimitBytes+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > sub2APIResponseBodyLimitBytes {
		return fmt.Errorf("response body too large: limit %d bytes", sub2APIResponseBodyLimitBytes)
	}
	return common.Unmarshal(body, v)
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
