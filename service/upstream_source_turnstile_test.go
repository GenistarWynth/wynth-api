package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsUpstreamSourceTurnstileError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"newapi own turnstile 200", newAPIRequestError{StatusCode: http.StatusOK, Message: "Turnstile token 为空"}, true},
		{"newapi turnstile verify failed", newAPIRequestError{StatusCode: http.StatusOK, Message: "Turnstile 校验失败，请刷新重试！"}, true},
		{"cf challenge platform", errors.New("challenge-platform blocked"), true},
		{"ordinary auth error", newAPIRequestError{StatusCode: http.StatusUnauthorized, Message: "invalid access token"}, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isUpstreamSourceTurnstileError(tc.err))
		})
	}
}

// TestIsUpstreamSourceCloudflareChallengeBody covers the case
// isUpstreamSourceTurnstileError structurally cannot: a Cloudflare EDGE
// managed-challenge (HTML interstitial) that never reaches
// isUpstreamSourceTurnstileError as an error string, because
// common.Unmarshal (stdlib json) errors never contain body text and the
// body is discarded before markers can be seen. Callers must inspect the
// raw response body/status directly (see decodeNewAPIResponseBody /
// decodeSub2APIResponseBody callers) using this helper instead.
func TestIsUpstreamSourceCloudflareChallengeBody(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		body       string
		want       bool
	}{
		{"edge challenge html 403", http.StatusForbidden, "<html>Just a moment... cf-ray: abc</html>", true},
		{"edge challenge html 503", http.StatusServiceUnavailable, "<html>please wait... challenge-platform loading</html>", true},
		{"wrong status code", http.StatusOK, "<html>cloudflare</html>", false},
		{"403 without marker", http.StatusForbidden, `{"success":false}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isUpstreamSourceCloudflareChallengeBody(tc.statusCode, []byte(tc.body)))
		})
	}
}
