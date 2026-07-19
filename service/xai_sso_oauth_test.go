package service

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type xaiSSOFakeClient struct {
	t             *testing.T
	tokenCalls    int
	cookieHeaders []string
}

func (c *xaiSSOFakeClient) Do(req *http.Request) (*http.Response, error) {
	c.cookieHeaders = append(c.cookieHeaders, req.Header.Get("Cookie"))
	switch req.URL.String() {
	case xaiSSOAccountsURL:
		return xaiSSOTestResponse(http.StatusOK, http.Header{"Set-Cookie": {"session=web-session; Path=/"}}, `{}`), nil
	case xaiSSODeviceCodeURL:
		values := readXAISSOForm(c.t, req)
		assert.Equal(c.t, xaiOAuthDefaultClientID, values.Get("client_id"))
		assert.Equal(c.t, xaiSSOBuildScope, values.Get("scope"))
		return xaiSSOTestResponse(http.StatusOK, http.Header{"Set-Cookie": {"csrf=csrf-token; Path=/"}}, `{"device_code":"device-1","user_code":"USER-1","verification_uri_complete":"https://auth.x.ai/oauth2/device/complete","interval":1,"expires_in":60}`), nil
	case "https://auth.x.ai/oauth2/device/complete":
		return xaiSSOTestResponse(http.StatusOK, nil, `<html>ok</html>`), nil
	case xaiSSOVerifyURL:
		values := readXAISSOForm(c.t, req)
		assert.Equal(c.t, "USER-1", values.Get("user_code"))
		return xaiSSOTestResponse(http.StatusFound, http.Header{"Location": {"/oauth2/device/consent"}}, ``), nil
	case "https://auth.x.ai/oauth2/device/consent":
		return xaiSSOTestResponse(http.StatusOK, nil, `<html>consent</html>`), nil
	case xaiSSOApproveURL:
		values := readXAISSOForm(c.t, req)
		assert.Equal(c.t, "USER-1", values.Get("user_code"))
		assert.Equal(c.t, "allow", values.Get("action"))
		return xaiSSOTestResponse(http.StatusSeeOther, http.Header{"Location": {"/oauth2/device/done"}}, ``), nil
	case "https://auth.x.ai/oauth2/device/done":
		return xaiSSOTestResponse(http.StatusOK, nil, `<html>done</html>`), nil
	case xaiSSOTokenURL:
		c.tokenCalls++
		values := readXAISSOForm(c.t, req)
		assert.Equal(c.t, xaiSSODeviceGrantType, values.Get("grant_type"))
		assert.Equal(c.t, "device-1", values.Get("device_code"))
		if c.tokenCalls == 1 {
			return xaiSSOTestResponse(http.StatusBadRequest, nil, `{"error":"authorization_pending"}`), nil
		}
		return xaiSSOTestResponse(http.StatusOK, nil, `{"access_token":"access-token","refresh_token":"refresh-token","id_token":"id-token","token_type":"Bearer","expires_in":3600,"scope":"`+xaiSSOBuildScope+`"}`), nil
	default:
		c.t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
		return nil, nil
	}
}

func TestConvertXAISSOToOAuthCompletesTrustedDeviceFlow(t *testing.T) {
	client := &xaiSSOFakeClient{t: t}
	info, err := convertXAISSOToOAuth(context.Background(), "Cookie: foo=bar; sso=sso-token", xaiSSODeviceOptions{
		HTTPClient: client,
		Sleep: func(context.Context, time.Duration) error {
			return nil
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "access-token", info.AccessToken)
	assert.Equal(t, "refresh-token", info.RefreshToken)
	assert.Equal(t, "id-token", info.IDToken)
	assert.Equal(t, xaiSSOBuildScope, info.Scope)
	assert.Equal(t, 2, client.tokenCalls)
	require.NotEmpty(t, client.cookieHeaders)
	assert.Contains(t, client.cookieHeaders[0], "sso=sso-token")
	assert.Contains(t, client.cookieHeaders[0], "sso-rw=sso-token")
	lastCookies := client.cookieHeaders[len(client.cookieHeaders)-1]
	assert.Contains(t, lastCookies, "session=web-session")
	assert.Contains(t, lastCookies, "csrf=csrf-token")
}

func TestNormalizeXAISSOToken(t *testing.T) {
	assert.Equal(t, "token-1", NormalizeXAISSOToken("Cookie: foo=bar; sso=token-1; sso-rw=token-2"))
	assert.Equal(t, "token-2", NormalizeXAISSOToken("sso-rw=token-2; foo=bar"))
	assert.Equal(t, "raw-token", NormalizeXAISSOToken(" raw-token ; ignored=1"))
	assert.Equal(t, "clean-token", NormalizeXAISSOToken("clean\r\n-token\x00"))
}

func TestConvertXAISSOToOAuthRejectsUntrustedRedirect(t *testing.T) {
	client := xaiSSOClientFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case xaiSSOAccountsURL:
			return xaiSSOTestResponse(http.StatusOK, nil, `{}`), nil
		case xaiSSODeviceCodeURL:
			return xaiSSOTestResponse(http.StatusOK, nil, `{"device_code":"device-1","user_code":"USER-1","verification_uri_complete":"https://evil.example/device","interval":1,"expires_in":60}`), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL.String())
			return nil, nil
		}
	})

	_, err := convertXAISSOToOAuth(context.Background(), "sso-token", xaiSSODeviceOptions{
		HTTPClient: client,
		Sleep:      func(context.Context, time.Duration) error { return nil },
	})
	require.ErrorContains(t, err, "trusted")
}

type xaiSSOClientFunc func(*http.Request) (*http.Response, error)

func (f xaiSSOClientFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func xaiSSOTestResponse(status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func readXAISSOForm(t *testing.T, req *http.Request) url.Values {
	t.Helper()
	data, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	values, err := url.ParseQuery(string(data))
	require.NoError(t, err)
	return values
}
