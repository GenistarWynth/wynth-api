// Best-effort reverse-engineered grok.com web request headers.
// See package doc in constants.go for the fragility warning.
package grokweb

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// buildSSOCookie builds the Cookie header value for an SSO-authenticated grok.com
// request. Mirror of grok2api headers.py build_sso_cookie: the same token is
// sent as both `sso` and `sso-rw`. A leading "sso=" prefix on the supplied
// token is stripped. If cfClearance is non-empty it is appended.
func buildSSOCookie(ssoToken, cfClearance string) string {
	tok := strings.TrimSpace(ssoToken)
	tok = strings.TrimPrefix(tok, "sso=")
	cookie := "sso=" + tok + "; sso-rw=" + tok
	if cf := strings.TrimSpace(cfClearance); cf != "" {
		cookie += "; cf_clearance=" + cf
	}
	return cookie
}

// applyGrokHeaders sets all headers required for a grok.com web chat request.
// Mirror of grok2api headers.py build_http_headers (JSON content-type branch).
func applyGrokHeaders(h *http.Header, ssoToken, cfClearance string) {
	h.Set("Accept", "*/*")
	h.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	h.Set("Baggage", baggageHeader)
	h.Set("Content-Type", "application/json")
	h.Set("Origin", "https://grok.com")
	h.Set("Priority", "u=1, i")
	h.Set("Referer", "https://grok.com/")
	h.Set("Sec-Fetch-Dest", "empty")
	h.Set("Sec-Fetch-Mode", "cors")
	h.Set("Sec-Fetch-Site", "same-origin")
	h.Set("Sec-Ch-Ua", `"Google Chrome";v="136", "Chromium";v="136", "Not(A:Brand";v="24"`)
	h.Set("Sec-Ch-Ua-Mobile", "?0")
	h.Set("Sec-Ch-Ua-Platform", `"macOS"`)
	h.Set("User-Agent", defaultUserAgent)
	h.Set("x-statsig-id", staticStatsigID)
	h.Set("x-xai-request-id", common.GetUUID())
	h.Set("Cookie", buildSSOCookie(ssoToken, cfClearance))
}
