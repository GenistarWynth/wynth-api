package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

type upstreamBrowserSession struct {
	Cookies      []*http.Cookie
	LocalStorage map[string]string
}

type upstreamBrowserLoginFunc func(ctx context.Context, source *model.UpstreamSource, email, password string) (upstreamBrowserSession, error)

// upstreamBrowserLogin is swappable so tests can stub the browser session
// acquisition without driving a real Chrome instance.
var upstreamBrowserLogin upstreamBrowserLoginFunc = chromedpUpstreamBrowserLogin

// upstreamBrowserSemaphore bounds concurrent browser sessions (they are heavy).
var upstreamBrowserSemaphore = make(chan struct{}, 2)

const upstreamBrowserTimeout = 60 * time.Second

func upstreamBrowserEnabled() bool {
	return strings.TrimSpace(common.UpstreamBrowserCDPURL) != ""
}

func acquireNewAPISessionViaBrowser(ctx context.Context, source *model.UpstreamSource, cfg newAPIAuthConfig) (newAPIAuthConfig, error) {
	if !upstreamBrowserEnabled() {
		return cfg, errors.New("headless browser not configured")
	}
	if strings.TrimSpace(cfg.Email) == "" || cfg.Password == "" {
		return cfg, errors.New("email and password are required for headless login")
	}
	session, err := upstreamBrowserLogin(ctx, source, cfg.Email, cfg.Password)
	if err != nil {
		return cfg, err
	}
	adapter := &NewAPIAdapter{}
	self, err := newAPIRequest[newAPILoginData](ctx, adapter, source, http.MethodGet, "/user/self", nil, nil, newAPIAuthConfig{}, session.Cookies)
	if err != nil {
		return cfg, err
	}
	token, err := newAPIRequest[string](ctx, adapter, source, http.MethodGet, "/user/token", nil, nil, newAPIAuthConfig{UserID: self.ID}, session.Cookies)
	if err != nil {
		return cfg, err
	}
	cfg.AccessToken = strings.TrimSpace(token)
	cfg.UserID = self.ID
	cfg.SessionSource = "browser"
	return cfg, nil
}

func acquireSub2APISessionViaBrowser(ctx context.Context, source *model.UpstreamSource, cfg sub2APIAuthConfig) (sub2APIAuthConfig, error) {
	if !upstreamBrowserEnabled() {
		return cfg, errors.New("headless browser not configured")
	}
	session, err := upstreamBrowserLogin(ctx, source, cfg.Email, cfg.Password)
	if err != nil {
		return cfg, err
	}
	token := firstNonEmpty(session.LocalStorage, "token", "access_token", "auth_token", "authToken")
	if token == "" {
		return cfg, errors.New("could not locate sub2api token in browser session; import manually")
	}
	cfg.AccessToken = token
	if cfg.ExpiresAt == 0 {
		cfg.ExpiresAt = common.GetTimestamp() + 3600
	}
	cfg.SessionSource = "browser"
	return cfg, nil
}

func firstNonEmpty(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(m[k]); v != "" {
			return v
		}
	}
	return ""
}

// chromedpUpstreamBrowserLogin drives a real browser (via the CDP sidecar) to
// pass Turnstile and capture the post-login session. Best-effort: Cloudflare
// frequently blocks headless traffic, in which case the caller falls back to
// manual session import.
func chromedpUpstreamBrowserLogin(ctx context.Context, source *model.UpstreamSource, email, password string) (upstreamBrowserSession, error) {
	loginURL := strings.TrimRight(source.BaseURL, "/") + "/login"
	if err := validateUpstreamSourceURL(source, loginURL); err != nil {
		return upstreamBrowserSession{}, err
	}

	upstreamBrowserSemaphore <- struct{}{}
	defer func() { <-upstreamBrowserSemaphore }()

	allocCtx, cancelAlloc := chromedp.NewRemoteAllocator(ctx, common.UpstreamBrowserCDPURL)
	defer cancelAlloc()
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()
	timeoutCtx, cancelTimeout := context.WithTimeout(browserCtx, upstreamBrowserTimeout)
	defer cancelTimeout()

	var localStorageJSON string
	err := chromedp.Run(timeoutCtx,
		chromedp.Navigate(loginURL),
		chromedp.WaitVisible(`input[type="password"]`, chromedp.ByQuery),
		chromedp.SendKeys(`input[type="email"], input[name="email"], input[name="username"]`, email, chromedp.ByQuery),
		chromedp.SendKeys(`input[type="password"]`, password, chromedp.ByQuery),
		// Give the Turnstile widget time to auto-solve before submitting.
		chromedp.Sleep(4*time.Second),
		// NOTE: `:has-text(...)` is Playwright syntax and is not a valid CSS
		// selector for chromedp's ByQuery (which resolves via the DOM's
		// native querySelector). new-api and sub2api-derived login forms use
		// a standard `<button type="submit">`, so we target that instead.
		// This is the single most upstream-specific step in this flow: if a
		// given deployment's login form deviates from that markup, the click
		// is best-effort and the admin falls back to manual session import.
		chromedp.Click(`button[type="submit"]`, chromedp.ByQuery),
		chromedp.Sleep(4*time.Second),
		chromedp.Evaluate(`JSON.stringify(Object.assign({}, window.localStorage))`, &localStorageJSON),
	)
	if err != nil {
		return upstreamBrowserSession{}, err
	}

	cookies, err := chromedpCaptureCookies(timeoutCtx)
	if err != nil {
		return upstreamBrowserSession{}, err
	}
	localStorage := map[string]string{}
	_ = common.UnmarshalJsonStr(localStorageJSON, &localStorage)
	return upstreamBrowserSession{Cookies: cookies, LocalStorage: localStorage}, nil
}

// chromedpCaptureCookies reads all cookies visible to the current browser tab
// via the CDP Network domain and converts them into standard net/http
// cookies so they can be replayed against the upstream's HTTP API.
func chromedpCaptureCookies(ctx context.Context) ([]*http.Cookie, error) {
	var cdpCookies []*network.Cookie
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var actionErr error
		cdpCookies, actionErr = network.GetCookies().Do(ctx)
		return actionErr
	}))
	if err != nil {
		return nil, err
	}

	cookies := make([]*http.Cookie, 0, len(cdpCookies))
	for _, cdpCookie := range cdpCookies {
		if cdpCookie == nil {
			continue
		}
		cookies = append(cookies, &http.Cookie{
			Name:   cdpCookie.Name,
			Value:  cdpCookie.Value,
			Domain: cdpCookie.Domain,
			Path:   cdpCookie.Path,
		})
	}
	return cookies, nil
}
