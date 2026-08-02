package service

import (
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	UpstreamSourceBillingProbeMaxBodyBytes = 64 * 1024

	UpstreamSourceBillingProbeErrorIneligible         = "ineligible"
	UpstreamSourceBillingProbeErrorInvalidBaseURL     = "invalid_base_url"
	UpstreamSourceBillingProbeErrorInvalidProxy       = "invalid_proxy"
	UpstreamSourceBillingProbeErrorNetworkNotAllowed  = "network_not_allowed"
	UpstreamSourceBillingProbeErrorRequestBuild       = "request_build_failed"
	UpstreamSourceBillingProbeErrorRequestFailed      = "request_failed"
	UpstreamSourceBillingProbeErrorRequestTimeout     = "request_timeout"
	UpstreamSourceBillingProbeErrorRedirectNotAllowed = "redirect_not_allowed"
	UpstreamSourceBillingProbeErrorResponseRead       = "response_read_failed"
	UpstreamSourceBillingProbeErrorResponseTooLarge   = "response_too_large"
	UpstreamSourceBillingProbeErrorUnauthorized       = "unauthorized"
	UpstreamSourceBillingProbeErrorForbidden          = "forbidden"
	UpstreamSourceBillingProbeErrorUnsupported        = "unsupported"
	UpstreamSourceBillingProbeErrorRateLimited        = "rate_limited"
	UpstreamSourceBillingProbeErrorHTTP               = "http_error"
	UpstreamSourceBillingProbeErrorInvalidResponse    = "invalid_response"
)

const (
	upstreamSourceBillingProbeObservationMaxAge    = 24 * time.Hour
	upstreamSourceBillingProbeObservationMaxFuture = 5 * time.Minute
)

type upstreamSourceBillingProbeRawResponse struct {
	Object                  *string  `json:"object"`
	SchemaVersion           *int     `json:"schema_version"`
	BillingScope            *string  `json:"billing_scope"`
	GroupRateMultiplier     *float64 `json:"group_rate_multiplier"`
	UserRateMultiplier      *float64 `json:"user_rate_multiplier"`
	ResolvedRateMultiplier  *float64 `json:"resolved_rate_multiplier"`
	PeakRateEnabled         *bool    `json:"peak_rate_enabled"`
	PeakStart               *string  `json:"peak_start"`
	PeakEnd                 *string  `json:"peak_end"`
	PeakRateMultiplier      *float64 `json:"peak_rate_multiplier"`
	AppliedPeakMultiplier   *float64 `json:"applied_peak_multiplier"`
	EffectiveRateMultiplier *float64 `json:"effective_rate_multiplier"`
	Timezone                *string  `json:"timezone"`
	ObservedAt              *string  `json:"observed_at"`
}

type upstreamSourceBillingObservation struct {
	GroupRateMultiplier     float64   `json:"group_rate_multiplier"`
	UserRateMultiplier      *float64  `json:"user_rate_multiplier,omitempty"`
	ResolvedRateMultiplier  float64   `json:"resolved_rate_multiplier"`
	PeakRateEnabled         bool      `json:"peak_rate_enabled"`
	PeakStart               string    `json:"peak_start,omitempty"`
	PeakEnd                 string    `json:"peak_end,omitempty"`
	PeakRateMultiplier      *float64  `json:"peak_rate_multiplier,omitempty"`
	AppliedPeakMultiplier   *float64  `json:"applied_peak_multiplier,omitempty"`
	EffectiveRateMultiplier float64   `json:"effective_rate_multiplier"`
	Timezone                string    `json:"timezone,omitempty"`
	ObservedAt              time.Time `json:"observed_at"`
}

func buildUpstreamSourceBillingURL(baseURL string) (string, error) {
	trimmed := strings.TrimSpace(baseURL)
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed == nil || !parsed.IsAbs() || parsed.Opaque != "" || parsed.Host == "" || parsed.Hostname() == "" {
		return "", errUpstreamSourceBillingProbeInvalidURL
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errUpstreamSourceBillingProbeInvalidURL
	}
	if parsed.User != nil {
		return "", errUpstreamSourceBillingProbeInvalidURL
	}

	const endpoint = "/v1/sub2api/billing"
	const relative = "/sub2api/billing"
	escapedPath := strings.TrimRight(parsed.EscapedPath(), "/")
	escapedSegments := strings.Split(escapedPath, "/")
	decodedSegments := make([]string, len(escapedSegments))
	for i, segment := range escapedSegments {
		decodedSegments[i], err = url.PathUnescape(segment)
		if err != nil {
			return "", errUpstreamSourceBillingProbeInvalidURL
		}
	}
	hasEndpoint := len(decodedSegments) >= 2 &&
		decodedSegments[len(decodedSegments)-2] == "sub2api" &&
		decodedSegments[len(decodedSegments)-1] == "billing"
	if !hasEndpoint {
		if upstreamSourceBillingSegmentHasVersionSuffix(decodedSegments[len(decodedSegments)-1]) {
			escapedPath += relative
		} else {
			escapedPath += endpoint
		}
	}
	path, err := url.PathUnescape(escapedPath)
	if err != nil {
		return "", errUpstreamSourceBillingProbeInvalidURL
	}
	parsed.Path = path
	parsed.RawPath = escapedPath
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String(), nil
}

func upstreamSourceBillingSegmentHasVersionSuffix(segment string) bool {
	segment = strings.ToLower(strings.TrimSpace(segment))
	if segment == "" {
		return false
	}
	if len(segment) < 2 || segment[0] != 'v' || segment[1] < '0' || segment[1] > '9' {
		return false
	}
	i := 1
	for i < len(segment) && segment[i] >= '0' && segment[i] <= '9' {
		i++
	}
	if i == len(segment) {
		return true
	}
	if segment[i] == '.' {
		i++
		if i == len(segment) || segment[i] < '0' || segment[i] > '9' {
			return false
		}
		for i < len(segment) && segment[i] >= '0' && segment[i] <= '9' {
			i++
		}
		return i == len(segment)
	}
	suffix := segment[i:]
	return strings.HasPrefix(suffix, "alpha") || strings.HasPrefix(suffix, "beta") || strings.HasPrefix(suffix, "preview")
}

func parseUpstreamSourceBillingResponse(body []byte, receivedAt time.Time) (upstreamSourceBillingObservation, error) {
	var response upstreamSourceBillingProbeRawResponse
	if err := common.Unmarshal(body, &response); err != nil {
		return upstreamSourceBillingObservation{}, errUpstreamSourceBillingProbeInvalidResponse
	}
	if response.Object == nil || *response.Object != "sub2api.key_billing" ||
		response.SchemaVersion == nil || *response.SchemaVersion != 1 ||
		response.BillingScope == nil || *response.BillingScope != "token" {
		return upstreamSourceBillingObservation{}, errUpstreamSourceBillingProbeInvalidResponse
	}
	if response.GroupRateMultiplier == nil || response.ResolvedRateMultiplier == nil ||
		response.PeakRateEnabled == nil || response.EffectiveRateMultiplier == nil || response.ObservedAt == nil {
		return upstreamSourceBillingObservation{}, errUpstreamSourceBillingProbeInvalidResponse
	}
	for _, value := range []float64{
		*response.GroupRateMultiplier,
		*response.ResolvedRateMultiplier,
		*response.EffectiveRateMultiplier,
	} {
		if !validUpstreamSourceBillingMultiplier(value) {
			return upstreamSourceBillingObservation{}, errUpstreamSourceBillingProbeInvalidResponse
		}
	}
	if response.UserRateMultiplier != nil && !validUpstreamSourceBillingMultiplier(*response.UserRateMultiplier) {
		return upstreamSourceBillingObservation{}, errUpstreamSourceBillingProbeInvalidResponse
	}
	expectedResolved := *response.GroupRateMultiplier
	if response.UserRateMultiplier != nil {
		expectedResolved = *response.UserRateMultiplier
	}
	if !billingMultipliersEqual(*response.ResolvedRateMultiplier, expectedResolved) {
		return upstreamSourceBillingObservation{}, errUpstreamSourceBillingProbeInvalidResponse
	}

	observedAt, err := time.Parse(time.RFC3339Nano, *response.ObservedAt)
	if err != nil || observedAt.IsZero() {
		return upstreamSourceBillingObservation{}, errUpstreamSourceBillingProbeInvalidResponse
	}
	observedAt = observedAt.UTC()
	receivedAt = receivedAt.UTC()
	if observedAt.Before(receivedAt.Add(-upstreamSourceBillingProbeObservationMaxAge)) ||
		observedAt.After(receivedAt.Add(upstreamSourceBillingProbeObservationMaxFuture)) {
		return upstreamSourceBillingObservation{}, errUpstreamSourceBillingProbeInvalidResponse
	}

	observation := upstreamSourceBillingObservation{
		GroupRateMultiplier:     *response.GroupRateMultiplier,
		UserRateMultiplier:      response.UserRateMultiplier,
		ResolvedRateMultiplier:  *response.ResolvedRateMultiplier,
		PeakRateEnabled:         *response.PeakRateEnabled,
		EffectiveRateMultiplier: *response.EffectiveRateMultiplier,
		ObservedAt:              observedAt,
	}
	appliedPeak := 1.0
	if observation.PeakRateEnabled {
		if response.PeakStart == nil || response.PeakEnd == nil || response.PeakRateMultiplier == nil ||
			response.AppliedPeakMultiplier == nil || response.Timezone == nil ||
			!validUpstreamSourceBillingMultiplier(*response.PeakRateMultiplier) ||
			!validUpstreamSourceBillingMultiplier(*response.AppliedPeakMultiplier) {
			return upstreamSourceBillingObservation{}, errUpstreamSourceBillingProbeInvalidResponse
		}
		startMinute, startOK := parseUpstreamSourceBillingClock(*response.PeakStart)
		endMinute, endOK := parseUpstreamSourceBillingClock(*response.PeakEnd)
		timezoneName := *response.Timezone
		if !startOK || !endOK || startMinute >= endMinute || timezoneName == "" || timezoneName == "Local" {
			return upstreamSourceBillingObservation{}, errUpstreamSourceBillingProbeInvalidResponse
		}
		location, err := time.LoadLocation(timezoneName)
		if err != nil {
			return upstreamSourceBillingObservation{}, errUpstreamSourceBillingProbeInvalidResponse
		}
		local := observedAt.In(location)
		minute := local.Hour()*60 + local.Minute()
		if minute >= startMinute && minute < endMinute {
			appliedPeak = *response.PeakRateMultiplier
		}
		if !billingMultipliersEqual(*response.AppliedPeakMultiplier, appliedPeak) {
			return upstreamSourceBillingObservation{}, errUpstreamSourceBillingProbeInvalidResponse
		}
		observation.PeakStart = *response.PeakStart
		observation.PeakEnd = *response.PeakEnd
		observation.PeakRateMultiplier = response.PeakRateMultiplier
		observation.AppliedPeakMultiplier = response.AppliedPeakMultiplier
		observation.Timezone = timezoneName
	} else if response.AppliedPeakMultiplier != nil && !billingMultipliersEqual(*response.AppliedPeakMultiplier, 1) {
		return upstreamSourceBillingObservation{}, errUpstreamSourceBillingProbeInvalidResponse
	}

	expectedEffective := observation.ResolvedRateMultiplier * appliedPeak
	if !validUpstreamSourceBillingMultiplier(expectedEffective) ||
		!billingMultipliersEqual(observation.EffectiveRateMultiplier, expectedEffective) {
		return upstreamSourceBillingObservation{}, errUpstreamSourceBillingProbeInvalidResponse
	}
	return observation, nil
}

func validUpstreamSourceBillingMultiplier(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func billingMultipliersEqual(left float64, right float64) bool {
	if math.IsNaN(left) || math.IsNaN(right) || math.IsInf(left, 0) || math.IsInf(right, 0) {
		return false
	}
	scale := math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
	return math.Abs(left-right) <= 1e-9*scale
}

func parseUpstreamSourceBillingClock(value string) (int, bool) {
	colon := strings.IndexByte(value, ':')
	if (colon != 1 && colon != 2) || len(value)-colon-1 != 2 {
		return 0, false
	}
	hour := 0
	for i := 0; i < colon; i++ {
		digit := value[i] - '0'
		if digit > 9 {
			return 0, false
		}
		hour = hour*10 + int(digit)
	}
	minuteTens := value[colon+1] - '0'
	minuteOnes := value[colon+2] - '0'
	if minuteTens > 9 || minuteOnes > 9 {
		return 0, false
	}
	minute := int(minuteTens)*10 + int(minuteOnes)
	if hour > 23 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

func upstreamSourceBillingProbeRetryAfter(header http.Header, now time.Time) time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil &&
		seconds > 0 && seconds <= int64(math.MaxInt64)/int64(time.Second) {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if delay := at.Sub(now); delay > 0 {
			return delay
		}
	}
	return 0
}
