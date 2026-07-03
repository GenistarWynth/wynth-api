package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquireNewAPISessionViaBrowser(t *testing.T) {
	// The cookie->token exchange hits an httptest server on a loopback
	// high-numbered port; relax the SSRF fetch setting the same way sibling
	// new-api tests in this package do (see withSub2APIFetchSetting).
	withSub2APIFetchSetting(t, true)

	// The acquire function gates on upstreamBrowserEnabled() (CDP URL configured)
	// before consulting the stubbed upstreamBrowserLogin below; set a dummy URL
	// so the orchestration path under test actually runs.
	originalCDPURL := common.UpstreamBrowserCDPURL
	common.UpstreamBrowserCDPURL = "http://127.0.0.1:9222"
	t.Cleanup(func() { common.UpstreamBrowserCDPURL = originalCDPURL })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/user/self"):
			w.Write([]byte(`{"success":true,"data":{"id":5}}`))
		case strings.HasSuffix(r.URL.Path, "/user/token"):
			w.Write([]byte(`{"success":true,"data":"browser-tok"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	orig := upstreamBrowserLogin
	upstreamBrowserLogin = func(ctx context.Context, source *model.UpstreamSource, email, password string) (upstreamBrowserSession, error) {
		return upstreamBrowserSession{Cookies: []*http.Cookie{{Name: "session", Value: "abc"}}}, nil
	}
	t.Cleanup(func() { upstreamBrowserLogin = orig })

	source := &model.UpstreamSource{Type: model.UpstreamSourceTypeNewAPI, BaseURL: server.URL, AdminAPIBasePath: "/api"}
	got, err := acquireNewAPISessionViaBrowser(context.Background(), source, newAPIAuthConfig{Email: "a@b.com", Password: "p"})
	require.NoError(t, err)
	assert.Equal(t, "browser-tok", got.AccessToken)
	assert.Equal(t, 5, got.UserID)
	assert.Equal(t, "browser", got.SessionSource)
}

// TestAcquireNewAPISessionViaBrowserRejectsAnonymousSession guards against a
// headless login that silently no-ops (e.g. the browser is left on an
// anonymous page): /user/self can still return HTTP 200 with id:0, and the
// acquirer must not persist that as a bogus "browser" session.
func TestAcquireNewAPISessionViaBrowserRejectsAnonymousSession(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	originalCDPURL := common.UpstreamBrowserCDPURL
	common.UpstreamBrowserCDPURL = "ws://stub"
	t.Cleanup(func() { common.UpstreamBrowserCDPURL = originalCDPURL })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/user/self"):
			w.Write([]byte(`{"success":true,"data":{"id":0}}`))
		case strings.HasSuffix(r.URL.Path, "/user/token"):
			w.Write([]byte(`{"success":true,"data":"should-not-be-used"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	orig := upstreamBrowserLogin
	upstreamBrowserLogin = func(ctx context.Context, source *model.UpstreamSource, email, password string) (upstreamBrowserSession, error) {
		return upstreamBrowserSession{Cookies: []*http.Cookie{{Name: "session", Value: "abc"}}}, nil
	}
	t.Cleanup(func() { upstreamBrowserLogin = orig })

	source := &model.UpstreamSource{Type: model.UpstreamSourceTypeNewAPI, BaseURL: server.URL, AdminAPIBasePath: "/api"}
	got, err := acquireNewAPISessionViaBrowser(context.Background(), source, newAPIAuthConfig{Email: "a@b.com", Password: "p"})
	require.Error(t, err)
	assert.Empty(t, got.AccessToken)
}

// TestAcquireSub2APISessionViaBrowser mirrors TestAcquireNewAPISessionViaBrowser
// for the sub2api acquirer: the token is read from browser localStorage rather
// than exchanged via cookie-based HTTP calls, and validated with a single
// authenticated probe (GET /groups/available) before being accepted.
func TestAcquireSub2APISessionViaBrowser(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/groups/available":
			assert.Equal(t, "Bearer jwt-xyz", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":0,"message":"","data":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	originalCDPURL := common.UpstreamBrowserCDPURL
	common.UpstreamBrowserCDPURL = "ws://stub"
	t.Cleanup(func() { common.UpstreamBrowserCDPURL = originalCDPURL })

	orig := upstreamBrowserLogin
	upstreamBrowserLogin = func(ctx context.Context, source *model.UpstreamSource, email, password string) (upstreamBrowserSession, error) {
		return upstreamBrowserSession{LocalStorage: map[string]string{"token": "jwt-xyz"}}, nil
	}
	t.Cleanup(func() { upstreamBrowserLogin = orig })

	source := &model.UpstreamSource{Type: model.UpstreamSourceTypeSub2API, BaseURL: server.URL, AdminAPIBasePath: "/api/v1"}
	got, err := acquireSub2APISessionViaBrowser(context.Background(), source, sub2APIAuthConfig{Email: "a@b.com", Password: "p"})
	require.NoError(t, err)
	assert.Equal(t, "jwt-xyz", got.AccessToken)
	assert.Equal(t, "browser", got.SessionSource)
	// "jwt-xyz" is not a well-formed JWT and no explicit expiry was supplied,
	// so expiry is unresolved (0 = never) rather than the old arbitrary
	// now+3600 fallback. See
	// TestAcquireSub2APISessionViaBrowserDerivesExpiryFromJWT for the case
	// where the browser-extracted token IS a JWT with an exp claim.
	assert.Equal(t, int64(0), got.ExpiresAt)
}

// TestAcquireSub2APISessionViaBrowserDerivesExpiryFromJWT verifies that a
// browser-extracted access token which happens to be a JWT has its real exp
// claim used to populate ExpiresAt, instead of the old arbitrary now+3600
// fallback that could expire a still-valid session early or misreport a
// longer-lived one (mirrors TestSub2APIImportDerivesExpiryFromJWT for the
// manual-import path).
func TestAcquireSub2APISessionViaBrowserDerivesExpiryFromJWT(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	futureExp := time.Now().Add(2 * time.Hour).Unix()
	jwt := buildTestJWT(t, map[string]any{"exp": futureExp})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/groups/available":
			assert.Equal(t, "Bearer "+jwt, r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":0,"message":"","data":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	originalCDPURL := common.UpstreamBrowserCDPURL
	common.UpstreamBrowserCDPURL = "ws://stub"
	t.Cleanup(func() { common.UpstreamBrowserCDPURL = originalCDPURL })

	orig := upstreamBrowserLogin
	upstreamBrowserLogin = func(ctx context.Context, source *model.UpstreamSource, email, password string) (upstreamBrowserSession, error) {
		return upstreamBrowserSession{LocalStorage: map[string]string{"token": jwt}}, nil
	}
	t.Cleanup(func() { upstreamBrowserLogin = orig })

	source := &model.UpstreamSource{Type: model.UpstreamSourceTypeSub2API, BaseURL: server.URL, AdminAPIBasePath: "/api/v1"}
	got, err := acquireSub2APISessionViaBrowser(context.Background(), source, sub2APIAuthConfig{Email: "a@b.com", Password: "p"})
	require.NoError(t, err)
	assert.Equal(t, jwt, got.AccessToken)
	assert.Equal(t, futureExp, got.ExpiresAt, "expires_at should be derived from the JWT exp claim, not now+3600")
}

// TestAcquireSub2APISessionViaBrowserRejectsTokenThatFailsProbe guards
// against accepting whatever happens to sit in localStorage: a headless
// session can leave a stale/expired/unrelated value there, and persisting it
// unvalidated for up to an hour would replay the same failure on every
// subsequent discover/sync. The acquirer must probe the token with an
// authenticated request before accepting it.
func TestAcquireSub2APISessionViaBrowserRejectsTokenThatFailsProbe(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/groups/available":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":401,"message":"invalid token"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	originalCDPURL := common.UpstreamBrowserCDPURL
	common.UpstreamBrowserCDPURL = "ws://stub"
	t.Cleanup(func() { common.UpstreamBrowserCDPURL = originalCDPURL })

	orig := upstreamBrowserLogin
	upstreamBrowserLogin = func(ctx context.Context, source *model.UpstreamSource, email, password string) (upstreamBrowserSession, error) {
		return upstreamBrowserSession{LocalStorage: map[string]string{"token": "stale-jwt"}}, nil
	}
	t.Cleanup(func() { upstreamBrowserLogin = orig })

	source := &model.UpstreamSource{Type: model.UpstreamSourceTypeSub2API, BaseURL: server.URL, AdminAPIBasePath: "/api/v1"}
	got, err := acquireSub2APISessionViaBrowser(context.Background(), source, sub2APIAuthConfig{Email: "a@b.com", Password: "p"})
	require.Error(t, err)
	assert.Empty(t, got.AccessToken, "an unvalidated token must not be accepted")
}
