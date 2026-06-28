// Best-effort reverse-engineered grok.com web failure classification.
// See package doc in constants.go for the fragility warning.
//
// The goal of this file is to turn grok.com upstream failures into typed
// *types.NewAPIError values whose StatusCode / UpstreamStatusCode let a LATER
// account-pool slice classify them (expire credential / cool down / retry)
// without re-reading the consumed body.
package grokweb

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/types"
)

// invalidCredentialMarkers are substrings that indicate the SSO token / account
// is invalid or blocked. Mirror of grok2api xai_usage.is_invalid_credentials_body.
var invalidCredentialMarkers = []string{
	"invalid-credentials",
	"bad-credentials",
	"failed to look up session id",
	"blocked-user",
	"email-domain-rejected",
	"session not found",
	"account suspended",
	"token revoked",
	"token expired",
}

// cloudflareMarkers indicate a Cloudflare challenge / block (HTML or JSON).
var cloudflareMarkers = []string{
	"cf_clearance",
	"cf-ray",
	"just a moment",
	"cloudflare",
	"attention required",
	"challenge-platform",
}

// isInvalidCredentialBody reports whether body contains an invalid/blocked
// account marker (case-insensitive).
func isInvalidCredentialBody(body string) bool {
	text := strings.ToLower(body)
	for _, m := range invalidCredentialMarkers {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

// isCloudflareChallenge reports whether the status + body look like a Cloudflare
// challenge or block.
func isCloudflareChallenge(statusCode int, body string) bool {
	if statusCode != http.StatusForbidden && statusCode != http.StatusServiceUnavailable {
		return false
	}
	text := strings.ToLower(body)
	for _, m := range cloudflareMarkers {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

// classifyHTTPError maps a non-2xx grok.com HTTP response to a typed error.
// The returned error always records the upstream status + body so the pool
// slice can inspect it later.
//
// Priority (most specific first):
//   - invalid/blocked credentials (400/401/403 + marker) -> channel:invalid_key,
//     status forced to 401 so a later classifier can EXPIRE the account.
//   - 429 -> rate-limited (bad_response_status_code, status 429).
//   - Cloudflare challenge (403/503 + cf markers) -> transport-ish do_request_failed
//     so it is treated as infrastructure, not an account problem.
//   - everything else -> bad_response_status_code with the original status.
func classifyHTTPError(statusCode int, header http.Header, body []byte) *types.NewAPIError {
	bodyStr := string(body)

	var apiErr *types.NewAPIError
	switch {
	case (statusCode == http.StatusBadRequest ||
		statusCode == http.StatusUnauthorized ||
		statusCode == http.StatusForbidden) && isInvalidCredentialBody(bodyStr):
		apiErr = types.NewError(
			fmt.Errorf("grok-web: invalid or blocked credentials: %s", truncate(bodyStr, 200)),
			types.ErrorCodeChannelInvalidKey,
			types.ErrOptionWithStatusCode(http.StatusUnauthorized),
		)
	case statusCode == http.StatusTooManyRequests:
		apiErr = types.NewError(
			fmt.Errorf("grok-web: rate limited: %s", truncate(bodyStr, 200)),
			types.ErrorCodeBadResponseStatusCode,
			types.ErrOptionWithStatusCode(http.StatusTooManyRequests),
		)
	case isCloudflareChallenge(statusCode, bodyStr):
		apiErr = types.NewError(
			fmt.Errorf("grok-web: cloudflare challenge (status %d)", statusCode),
			types.ErrorCodeDoRequestFailed,
			types.ErrOptionWithStatusCode(statusCode),
		)
	default:
		apiErr = types.NewError(
			fmt.Errorf("grok-web: upstream status %d: %s", statusCode, truncate(bodyStr, 200)),
			types.ErrorCodeBadResponseStatusCode,
			types.ErrOptionWithStatusCode(statusCode),
		)
	}

	apiErr.SetUpstreamResponse(header, body, statusCode)
	return apiErr
}

// inBandErrorToAPIError maps a grok in-band SSE error frame to a typed error.
// code == 8 or rate-limit text => 429; invalid-credential text => 401; else 502.
func inBandErrorToAPIError(e *grokInBandError) *types.NewAPIError {
	if e == nil {
		return types.NewError(errors.New("grok-web: unknown stream error"), types.ErrorCodeBadResponse)
	}
	msg := strings.TrimSpace(e.Message)
	lower := strings.ToLower(msg)

	if isInvalidCredentialBody(lower) {
		return types.NewError(
			fmt.Errorf("grok-web: invalid or blocked credentials: %s", truncate(msg, 200)),
			types.ErrorCodeChannelInvalidKey,
			types.ErrOptionWithStatusCode(http.StatusUnauthorized),
		)
	}

	rateLimited := strings.Contains(lower, "too many requests") || strings.Contains(lower, "rate limit")
	if code, ok := e.Code.(float64); ok && code == 8 {
		rateLimited = true
	}
	if rateLimited {
		return types.NewError(
			fmt.Errorf("grok-web: rate limited: %s", truncate(msg, 200)),
			types.ErrorCodeBadResponseStatusCode,
			types.ErrOptionWithStatusCode(http.StatusTooManyRequests),
		)
	}

	return types.NewError(
		fmt.Errorf("grok-web: upstream stream error: %s", truncate(msg, 200)),
		types.ErrorCodeBadResponse,
		types.ErrOptionWithStatusCode(http.StatusBadGateway),
	)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
