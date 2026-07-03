package service

import (
	"errors"
	"net/http"
	"strings"
)

// ErrUpstreamSourceTurnstileRequired signals that an upstream gateway login was
// blocked by a Cloudflare Turnstile / managed challenge and cannot be completed
// with stored email+password alone. The admin must import a session.
var ErrUpstreamSourceTurnstileRequired = errors.New("upstream source login blocked by Cloudflare Turnstile; import a session")

// upstreamSourceCloudflareMarkers are CF challenge/block markers, adapted
// from relay/channel/grokweb's cloudflareMarkers for the upstream-login
// case: adds "turnstile" (new-api's own widget-rejection message) and drops
// the bare "cloudflare" marker (too broad here; cf-ray/challenge-platform/
// attention-required already catch edge challenges without it).
var upstreamSourceCloudflareMarkers = []string{
	"turnstile",
	"cf_clearance",
	"cf-ray",
	"just a moment",
	"challenge-platform",
	"attention required",
}

func isUpstreamSourceTurnstileError(err error) bool {
	if err == nil {
		return false
	}
	var reqErr newAPIRequestError
	if errors.As(err, &reqErr) {
		if upstreamSourceTextHasCloudflareMarker(reqErr.Message) {
			return true
		}
	}
	return upstreamSourceTextHasCloudflareMarker(err.Error())
}

func upstreamSourceTextHasCloudflareMarker(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range upstreamSourceCloudflareMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// isUpstreamSourceCloudflareChallengeBody reports whether an HTTP response body
// is a Cloudflare edge managed-challenge / block (HTML interstitial). Mirrors
// relay/channel/grokweb isCloudflareChallenge so a decode failure on such a
// body is surfaced as a turnstile block, not an opaque "decode failed".
func isUpstreamSourceCloudflareChallengeBody(statusCode int, body []byte) bool {
	if statusCode != http.StatusForbidden && statusCode != http.StatusServiceUnavailable {
		return false
	}
	return upstreamSourceTextHasCloudflareMarker(string(body))
}
