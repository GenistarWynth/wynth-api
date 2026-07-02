package service

import (
	"errors"
	"strings"
)

// ErrUpstreamSourceTurnstileRequired signals that an upstream gateway login was
// blocked by a Cloudflare Turnstile / managed challenge and cannot be completed
// with stored email+password alone. The admin must import a session.
var ErrUpstreamSourceTurnstileRequired = errors.New("upstream source login blocked by Cloudflare Turnstile; import a session")

// upstreamSourceCloudflareMarkers mirror relay/channel/grokweb cloudflareMarkers.
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
