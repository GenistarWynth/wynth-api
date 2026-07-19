package service

import (
	"context"
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
)

const (
	xaiSSOBuildScope       = "openid profile email offline_access grok-cli:access api:access conversations:read conversations:write"
	xaiSSOAccountsURL      = "https://accounts.x.ai/"
	xaiSSODeviceCodeURL    = "https://auth.x.ai/oauth2/device/code"
	xaiSSOVerifyURL        = "https://auth.x.ai/oauth2/device/verify"
	xaiSSOApproveURL       = "https://auth.x.ai/oauth2/device/approve"
	xaiSSOTokenURL         = "https://auth.x.ai/oauth2/token"
	xaiSSODeviceGrantType  = "urn:ietf:params:oauth:grant-type:device_code"
	xaiSSOTimeout          = 90 * time.Second
	xaiSSOMaxBodyBytes     = 2 << 20
	xaiSSODefaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

type xaiSSOHTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type xaiSSODeviceOptions struct {
	HTTPClient xaiSSOHTTPClient
	UserAgent  string
	Sleep      func(context.Context, time.Duration) error
}

type xaiSSODeviceFlow struct {
	client    xaiSSOHTTPClient
	userAgent string
	cookies   map[string]string
	sleep     func(context.Context, time.Duration) error
}

func ConvertXAISSOToOAuth(ctx context.Context, ssoToken string, proxyURL string) (*XAIOAuthTokenInfo, error) {
	baseClient, err := getXAIOAuthHTTPClient(proxyURL)
	if err != nil {
		return nil, err
	}
	client := *baseClient
	client.Timeout = xaiSSOTimeout
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	requestContext, cancel := context.WithTimeout(ctx, xaiSSOTimeout)
	defer cancel()
	return convertXAISSOToOAuth(requestContext, ssoToken, xaiSSODeviceOptions{HTTPClient: &client})
}

func convertXAISSOToOAuth(ctx context.Context, ssoToken string, options xaiSSODeviceOptions) (*XAIOAuthTokenInfo, error) {
	ssoToken = NormalizeXAISSOToken(ssoToken)
	if ssoToken == "" {
		return nil, errors.New("xai sso token is required")
	}
	if options.HTTPClient == nil {
		return nil, errors.New("xai sso http client is required")
	}
	userAgent := strings.TrimSpace(options.UserAgent)
	if userAgent == "" {
		userAgent = xaiSSODefaultUserAgent
	}
	sleep := options.Sleep
	if sleep == nil {
		sleep = sleepXAISSOContext
	}
	flow := xaiSSODeviceFlow{
		client:    options.HTTPClient,
		userAgent: userAgent,
		cookies: map[string]string{
			"sso":    ssoToken,
			"sso-rw": ssoToken,
		},
		sleep: sleep,
	}
	return flow.convert(ctx)
}

func (f *xaiSSODeviceFlow) convert(ctx context.Context) (*XAIOAuthTokenInfo, error) {
	status, finalURL, _, err := f.do(ctx, http.MethodGet, xaiSSOAccountsURL, nil)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized || strings.Contains(finalURL, "sign-in") || strings.Contains(finalURL, "sign-up") {
		return nil, errors.New("xai sso token is invalid or expired")
	}
	if status < http.StatusOK || status >= http.StatusBadRequest {
		return nil, fmt.Errorf("validate xai sso: status=%d", status)
	}

	status, _, body, err := f.do(ctx, http.MethodPost, xaiSSODeviceCodeURL, url.Values{
		"client_id": {xaiOAuthDefaultClientID},
		"scope":     {xaiSSOBuildScope},
	})
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("start xai device flow: status=%d", status)
	}
	var device struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		Interval                int    `json:"interval"`
		ExpiresIn               int    `json:"expires_in"`
	}
	if err := common.Unmarshal(body, &device); err != nil {
		return nil, fmt.Errorf("parse xai device response: %w", err)
	}
	if device.DeviceCode == "" || device.UserCode == "" {
		return nil, errors.New("xai device response is incomplete")
	}
	if !isTrustedXAIOAuthURL(device.VerificationURIComplete) {
		return nil, errors.New("xai device verification URL is not trusted")
	}
	if device.Interval <= 0 {
		device.Interval = 5
	}
	if device.ExpiresIn <= 0 {
		device.ExpiresIn = 1800
	}

	status, _, _, err = f.do(ctx, http.MethodGet, device.VerificationURIComplete, nil)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusBadRequest {
		return nil, fmt.Errorf("open xai device verification: status=%d", status)
	}
	status, finalURL, _, err = f.do(ctx, http.MethodPost, xaiSSOVerifyURL, url.Values{"user_code": {device.UserCode}})
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusBadRequest || !strings.Contains(finalURL, "consent") {
		return nil, errors.New("xai device verification did not reach consent")
	}
	status, finalURL, _, err = f.do(ctx, http.MethodPost, xaiSSOApproveURL, url.Values{
		"user_code":      {device.UserCode},
		"action":         {"allow"},
		"principal_type": {"User"},
		"principal_id":   {""},
	})
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusBadRequest || !strings.Contains(finalURL, "done") {
		return nil, errors.New("xai device approval did not complete")
	}
	return f.pollToken(ctx, device.DeviceCode, time.Duration(device.Interval)*time.Second, time.Duration(device.ExpiresIn)*time.Second)
}

func (f *xaiSSODeviceFlow) pollToken(ctx context.Context, deviceCode string, interval time.Duration, expiresIn time.Duration) (*XAIOAuthTokenInfo, error) {
	if interval < time.Second {
		interval = time.Second
	}
	pollWindow := expiresIn
	if pollWindow <= 0 || pollWindow > 75*time.Second {
		pollWindow = 75 * time.Second
	}
	deadline := time.Now().Add(pollWindow)
	for time.Now().Before(deadline) {
		if err := f.sleep(ctx, interval); err != nil {
			return nil, err
		}
		status, _, body, err := f.do(ctx, http.MethodPost, xaiSSOTokenURL, url.Values{
			"grant_type":  {xaiSSODeviceGrantType},
			"client_id":   {xaiOAuthDefaultClientID},
			"device_code": {deviceCode},
		})
		if err != nil {
			return nil, err
		}
		var payload struct {
			AccessToken      string `json:"access_token"`
			RefreshToken     string `json:"refresh_token"`
			IDToken          string `json:"id_token"`
			TokenType        string `json:"token_type"`
			ExpiresIn        int64  `json:"expires_in"`
			Scope            string `json:"scope"`
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		if err := common.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("parse xai device token response: %w", err)
		}
		if status >= http.StatusOK && status < http.StatusMultipleChoices && strings.TrimSpace(payload.AccessToken) != "" {
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
				ClientID:     xaiOAuthDefaultClientID,
				Scope:        strings.TrimSpace(payload.Scope),
			}
			applyXAIOAuthClaims(info, info.IDToken)
			applyXAIOAuthClaims(info, info.AccessToken)
			return info, nil
		}
		switch strings.TrimSpace(payload.Error) {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "access_denied", "expired_token":
			return nil, errors.New("xai device authorization was denied or expired")
		default:
			message := strings.TrimSpace(payload.ErrorDescription)
			if message == "" {
				message = strings.TrimSpace(payload.Error)
			}
			if message == "" {
				message = strconv.Itoa(status)
			}
			return nil, fmt.Errorf("xai device token polling failed: %s", message)
		}
	}
	return nil, errors.New("xai device token polling timed out")
}

func (f *xaiSSODeviceFlow) do(ctx context.Context, method string, endpoint string, form url.Values) (int, string, []byte, error) {
	if !isTrustedXAIOAuthURL(endpoint) {
		return 0, "", nil, errors.New("xai oauth URL is not trusted")
	}
	currentURL := endpoint
	currentMethod := method
	currentForm := form
	for redirects := 0; redirects <= 8; redirects++ {
		var body io.Reader
		if currentForm != nil {
			body = strings.NewReader(currentForm.Encode())
		}
		request, err := http.NewRequestWithContext(ctx, currentMethod, currentURL, body)
		if err != nil {
			return 0, currentURL, nil, err
		}
		request.Header.Set("Accept", "application/json, text/html;q=0.9, */*;q=0.8")
		request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
		request.Header.Set("User-Agent", f.userAgent)
		if cookie := f.cookieHeader(); cookie != "" {
			request.Header.Set("Cookie", cookie)
		}
		if currentForm != nil {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		response, err := f.client.Do(request)
		if err != nil {
			return 0, currentURL, nil, err
		}
		f.captureCookies(response)
		data, readErr := io.ReadAll(io.LimitReader(response.Body, xaiSSOMaxBodyBytes+1))
		_ = response.Body.Close()
		if readErr != nil {
			return response.StatusCode, currentURL, nil, readErr
		}
		if len(data) > xaiSSOMaxBodyBytes {
			return response.StatusCode, currentURL, nil, errors.New("xai oauth response exceeds 2 MiB")
		}
		if response.StatusCode < http.StatusMultipleChoices || response.StatusCode >= http.StatusBadRequest {
			return response.StatusCode, currentURL, data, nil
		}
		location := strings.TrimSpace(response.Header.Get("Location"))
		if location == "" {
			return response.StatusCode, currentURL, data, errors.New("xai oauth redirect missing Location")
		}
		base, _ := url.Parse(currentURL)
		next, err := url.Parse(location)
		if err != nil {
			return response.StatusCode, currentURL, data, err
		}
		currentURL = base.ResolveReference(next).String()
		if !isTrustedXAIOAuthURL(currentURL) {
			return response.StatusCode, currentURL, data, errors.New("xai oauth redirected to untrusted host")
		}
		if response.StatusCode == http.StatusSeeOther || ((response.StatusCode == http.StatusMovedPermanently || response.StatusCode == http.StatusFound) && currentMethod != http.MethodGet && currentMethod != http.MethodHead) {
			currentMethod = http.MethodGet
			currentForm = nil
		}
	}
	return 0, currentURL, nil, errors.New("xai oauth redirected too many times")
}

func (f *xaiSSODeviceFlow) captureCookies(response *http.Response) {
	for _, cookie := range response.Cookies() {
		name := strings.TrimSpace(cookie.Name)
		value := strings.TrimSpace(cookie.Value)
		if name == "" || len(name) > 128 || len(value) > 16384 || strings.ContainsAny(name+value, "\r\n\x00") {
			continue
		}
		if cookie.MaxAge < 0 {
			delete(f.cookies, name)
			continue
		}
		f.cookies[name] = value
	}
}

func (f *xaiSSODeviceFlow) cookieHeader() string {
	keys := make([]string, 0, len(f.cookies))
	for key := range f.cookies {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+f.cookies[key])
	}
	return strings.Join(parts, "; ")
}

func NormalizeXAISSOToken(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "cookie:") {
		value = strings.TrimSpace(value[len("cookie:"):])
	}
	for _, part := range strings.Split(value, ";") {
		name, token, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "sso", "sso-rw":
			return sanitizeXAISSOToken(token)
		}
	}
	if token, _, found := strings.Cut(value, ";"); found {
		value = strings.TrimSpace(token)
	}
	return sanitizeXAISSOToken(value)
}

func sanitizeXAISSOToken(value string) string {
	return strings.NewReplacer("\r", "", "\n", "", "\x00", "").Replace(strings.TrimSpace(value))
}

func isTrustedXAIOAuthURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "x.ai" || strings.HasSuffix(host, ".x.ai")
}

func sleepXAISSOContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
