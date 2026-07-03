package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
