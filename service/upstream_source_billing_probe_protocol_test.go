package service

import (
	"math"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func billingProbeProtocolBody(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	body, err := common.Marshal(fields)
	require.NoError(t, err)
	return body
}

func validBillingProbeProtocolFields(observedAt time.Time) map[string]any {
	return map[string]any{
		"object":                    "sub2api.key_billing",
		"schema_version":            1,
		"billing_scope":             "token",
		"group_rate_multiplier":     0.8,
		"resolved_rate_multiplier":  0.8,
		"peak_rate_enabled":         false,
		"effective_rate_multiplier": 0.8,
		"observed_at":               observedAt.UTC().Format(time.RFC3339Nano),
	}
}

func TestBuildUpstreamSourceBillingURLPreservesCompatibleBaseComponents(t *testing.T) {
	tests := []struct {
		name string
		base string
		want string
	}{
		{name: "root", base: "https://upstream.example", want: "https://upstream.example/v1/sub2api/billing"},
		{name: "v1", base: "https://upstream.example/v1", want: "https://upstream.example/v1/sub2api/billing"},
		{name: "prefixed v1", base: "https://upstream.example/openai/v1", want: "https://upstream.example/openai/v1/sub2api/billing"},
		{name: "version path", base: "https://upstream.example/openai/v2", want: "https://upstream.example/openai/v2/sub2api/billing"},
		{name: "unversioned prefix", base: "https://upstream.example/openai", want: "https://upstream.example/openai/v1/sub2api/billing"},
		{name: "existing endpoint", base: "https://upstream.example/v1/sub2api/billing", want: "https://upstream.example/v1/sub2api/billing"},
		{name: "query", base: "https://upstream.example/v1?tenant=a&redirect=%2F", want: "https://upstream.example/v1/sub2api/billing?tenant=a&redirect=%2F"},
		{name: "escaped path prefix", base: "https://upstream.example/tenant%2Fsegment/v1?key=value#drop", want: "https://upstream.example/tenant%2Fsegment/v1/sub2api/billing?key=value"},
		{name: "fragment dropped", base: "https://upstream.example/v1#secret-fragment", want: "https://upstream.example/v1/sub2api/billing"},
		{name: "ipv6", base: "http://[2001:db8::1]:8080/v1?tenant=a#stale", want: "http://[2001:db8::1]:8080/v1/sub2api/billing?tenant=a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildUpstreamSourceBillingURL(tt.base)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildUpstreamSourceBillingURLPreservesEscapedSegmentSemantics(t *testing.T) {
	tests := []struct {
		name string
		base string
		want string
	}{
		{
			name: "trailing encoded slash remains data",
			base: "https://upstream.example/tenant%2F?tenant=a#drop",
			want: "https://upstream.example/tenant%2F/v1/sub2api/billing?tenant=a",
		},
		{
			name: "escaped unreserved version is not duplicated",
			base: "https://upstream.example/%76%31?tenant=a#drop",
			want: "https://upstream.example/%76%31/sub2api/billing?tenant=a",
		},
		{
			name: "escaped unreserved endpoint is not duplicated",
			base: "https://upstream.example/v1/sub2api/%62illing?tenant=a#drop",
			want: "https://upstream.example/v1/sub2api/%62illing?tenant=a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildUpstreamSourceBillingURL(tt.base)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.NotContains(t, got, "%25", "valid escapes must not be double escaped")
		})
	}
}

func TestBuildUpstreamSourceBillingURLRejectsUnsafeOrMalformedBases(t *testing.T) {
	for _, base := range []string{
		"",
		"upstream.example/v1",
		"ftp://upstream.example/v1",
		"https://user:password@upstream.example/v1",
		"https://upstream.example/%zz",
		"https:///missing-host",
		"https://[2001:db8::1",
	} {
		t.Run(base, func(t *testing.T) {
			got, err := buildUpstreamSourceBillingURL(base)
			require.Error(t, err)
			assert.Empty(t, got)
			assert.NotContains(t, err.Error(), "password")
		})
	}
}

func TestParseUpstreamSourceBillingResponseAcceptsPinnedSchemaAndSanitizesUnknownFields(t *testing.T) {
	receivedAt := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	fields := validBillingProbeProtocolFields(receivedAt.Add(-time.Minute))
	fields["user_rate_multiplier"] = 0.6
	fields["resolved_rate_multiplier"] = 0.6
	fields["peak_rate_enabled"] = true
	fields["peak_start"] = "09:00"
	fields["peak_end"] = "18:00"
	fields["peak_rate_multiplier"] = 1.5
	fields["applied_peak_multiplier"] = 1.5
	fields["effective_rate_multiplier"] = 0.9
	fields["timezone"] = "Asia/Shanghai"
	fields["unexpected_secret"] = "must-not-survive"

	observation, err := parseUpstreamSourceBillingResponse(billingProbeProtocolBody(t, fields), receivedAt)
	require.NoError(t, err)
	assert.Equal(t, 0.8, observation.GroupRateMultiplier)
	require.NotNil(t, observation.UserRateMultiplier)
	assert.Equal(t, 0.6, *observation.UserRateMultiplier)
	assert.Equal(t, 0.6, observation.ResolvedRateMultiplier)
	assert.True(t, observation.PeakRateEnabled)
	assert.Equal(t, "09:00", observation.PeakStart)
	assert.Equal(t, "18:00", observation.PeakEnd)
	require.NotNil(t, observation.PeakRateMultiplier)
	assert.Equal(t, 1.5, *observation.PeakRateMultiplier)
	require.NotNil(t, observation.AppliedPeakMultiplier)
	assert.Equal(t, 1.5, *observation.AppliedPeakMultiplier)
	assert.Equal(t, 0.9, observation.EffectiveRateMultiplier)
	assert.Equal(t, "Asia/Shanghai", observation.Timezone)
	assert.Equal(t, receivedAt.Add(-time.Minute), observation.ObservedAt)

	encoded, err := common.Marshal(observation)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "unexpected_secret")
	assert.NotContains(t, string(encoded), "must-not-survive")
}

func TestParseUpstreamSourceBillingResponseRejectsSchemaNumericAndArithmeticViolations(t *testing.T) {
	receivedAt := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(map[string]any)
		raw    string
	}{
		{name: "object", mutate: func(fields map[string]any) { fields["object"] = "list" }},
		{name: "schema version", mutate: func(fields map[string]any) { fields["schema_version"] = 2 }},
		{name: "scope", mutate: func(fields map[string]any) { fields["billing_scope"] = "request" }},
		{name: "missing group", mutate: func(fields map[string]any) { delete(fields, "group_rate_multiplier") }},
		{name: "missing resolved", mutate: func(fields map[string]any) { delete(fields, "resolved_rate_multiplier") }},
		{name: "missing peak flag", mutate: func(fields map[string]any) { delete(fields, "peak_rate_enabled") }},
		{name: "missing effective", mutate: func(fields map[string]any) { delete(fields, "effective_rate_multiplier") }},
		{name: "missing observed", mutate: func(fields map[string]any) { delete(fields, "observed_at") }},
		{name: "missing peak start", mutate: func(fields map[string]any) {
			fields["peak_rate_enabled"] = true
			fields["peak_end"] = "18:00"
			fields["peak_rate_multiplier"] = 1.5
			fields["applied_peak_multiplier"] = 1.0
			fields["timezone"] = "UTC"
		}},
		{name: "missing peak end", mutate: func(fields map[string]any) {
			fields["peak_rate_enabled"] = true
			fields["peak_start"] = "09:00"
			fields["peak_rate_multiplier"] = 1.5
			fields["applied_peak_multiplier"] = 1.0
			fields["timezone"] = "UTC"
		}},
		{name: "missing peak multiplier", mutate: func(fields map[string]any) {
			fields["peak_rate_enabled"] = true
			fields["peak_start"] = "09:00"
			fields["peak_end"] = "18:00"
			fields["applied_peak_multiplier"] = 1.0
			fields["timezone"] = "UTC"
		}},
		{name: "missing applied peak multiplier", mutate: func(fields map[string]any) {
			fields["peak_rate_enabled"] = true
			fields["peak_start"] = "09:00"
			fields["peak_end"] = "18:00"
			fields["peak_rate_multiplier"] = 1.5
			fields["timezone"] = "UTC"
		}},
		{name: "missing peak timezone", mutate: func(fields map[string]any) {
			fields["peak_rate_enabled"] = true
			fields["peak_start"] = "09:00"
			fields["peak_end"] = "18:00"
			fields["peak_rate_multiplier"] = 1.5
			fields["applied_peak_multiplier"] = 1.0
		}},
		{name: "negative group", mutate: func(fields map[string]any) { fields["group_rate_multiplier"] = -0.1 }},
		{name: "negative user", mutate: func(fields map[string]any) { fields["user_rate_multiplier"] = -0.1 }},
		{name: "negative resolved", mutate: func(fields map[string]any) { fields["resolved_rate_multiplier"] = -0.1 }},
		{name: "negative effective", mutate: func(fields map[string]any) { fields["effective_rate_multiplier"] = -0.1 }},
		{name: "negative peak multiplier", mutate: func(fields map[string]any) {
			fields["peak_rate_enabled"] = true
			fields["peak_start"] = "09:00"
			fields["peak_end"] = "18:00"
			fields["peak_rate_multiplier"] = -1.0
			fields["applied_peak_multiplier"] = 1.0
			fields["timezone"] = "UTC"
		}},
		{name: "negative applied peak multiplier", mutate: func(fields map[string]any) {
			fields["peak_rate_enabled"] = true
			fields["peak_start"] = "09:00"
			fields["peak_end"] = "18:00"
			fields["peak_rate_multiplier"] = 1.5
			fields["applied_peak_multiplier"] = -1.0
			fields["timezone"] = "UTC"
		}},
		{name: "resolved differs from group", mutate: func(fields map[string]any) {
			fields["resolved_rate_multiplier"] = 0.7
			fields["effective_rate_multiplier"] = 0.7
		}},
		{name: "resolved differs from user", mutate: func(fields map[string]any) {
			fields["user_rate_multiplier"] = 0.6
			fields["resolved_rate_multiplier"] = 0.8
		}},
		{name: "effective arithmetic", mutate: func(fields map[string]any) { fields["effective_rate_multiplier"] = 0.81 }},
		{name: "observed far future", mutate: func(fields map[string]any) {
			fields["observed_at"] = receivedAt.Add(25 * time.Hour).Format(time.RFC3339)
		}},
		{name: "observed far past", mutate: func(fields map[string]any) {
			fields["observed_at"] = receivedAt.Add(-25 * time.Hour).Format(time.RFC3339)
		}},
		{name: "observed padded", mutate: func(fields map[string]any) {
			fields["observed_at"] = " " + receivedAt.Format(time.RFC3339) + " "
		}},
		{name: "malformed", raw: `{"object":`},
		{name: "nan string", raw: `{"object":"sub2api.key_billing","schema_version":1,"billing_scope":"token","group_rate_multiplier":"NaN","resolved_rate_multiplier":0,"peak_rate_enabled":false,"effective_rate_multiplier":0,"observed_at":"2026-07-13T02:00:00Z"}`},
		{name: "positive infinity overflow", raw: `{"object":"sub2api.key_billing","schema_version":1,"billing_scope":"token","group_rate_multiplier":1e999,"resolved_rate_multiplier":0,"peak_rate_enabled":false,"effective_rate_multiplier":0,"observed_at":"2026-07-13T02:00:00Z"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(tt.raw)
			if tt.raw == "" {
				fields := validBillingProbeProtocolFields(receivedAt)
				tt.mutate(fields)
				body = billingProbeProtocolBody(t, fields)
			}
			_, err := parseUpstreamSourceBillingResponse(body, receivedAt)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), string(body))
		})
	}

	assert.False(t, billingMultipliersEqual(math.NaN(), 1))
	assert.False(t, billingMultipliersEqual(math.Inf(1), math.Inf(1)))
	assert.True(t, billingMultipliersEqual(1, 1+5e-10))
	assert.False(t, billingMultipliersEqual(1, 1+2e-9))
}

func TestParseUpstreamSourceBillingResponseValidatesPeakWindowTimezoneAndDST(t *testing.T) {
	tests := []struct {
		name       string
		observedAt time.Time
		start      string
		end        string
		timezone   string
		applied    float64
		wantError  bool
	}{
		{name: "spring before skipped hour", observedAt: time.Date(2026, 3, 8, 6, 30, 0, 0, time.UTC), start: "01:00", end: "03:00", timezone: "America/New_York", applied: 2},
		{name: "spring after skipped hour", observedAt: time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC), start: "01:00", end: "03:00", timezone: "America/New_York", applied: 1},
		{name: "fall first repeated hour", observedAt: time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC), start: "01:00", end: "02:00", timezone: "America/New_York", applied: 2},
		{name: "fall second repeated hour", observedAt: time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC), start: "01:00", end: "02:00", timezone: "America/New_York", applied: 2},
		{name: "equal window", observedAt: time.Date(2026, 7, 13, 2, 0, 0, 0, time.UTC), start: "09:00", end: "09:00", timezone: "UTC", applied: 1, wantError: true},
		{name: "overnight window", observedAt: time.Date(2026, 7, 13, 2, 0, 0, 0, time.UTC), start: "18:00", end: "09:00", timezone: "UTC", applied: 1, wantError: true},
		{name: "one digit hour", observedAt: time.Date(2026, 7, 13, 2, 0, 0, 0, time.UTC), start: "9:00", end: "18:00", timezone: "UTC", applied: 1},
		{name: "invalid timezone", observedAt: time.Date(2026, 7, 13, 2, 0, 0, 0, time.UTC), start: "09:00", end: "18:00", timezone: "Mars/Olympus", applied: 1, wantError: true},
		{name: "padded timezone", observedAt: time.Date(2026, 7, 13, 2, 0, 0, 0, time.UTC), start: "09:00", end: "18:00", timezone: " UTC ", applied: 1, wantError: true},
		{name: "local is not IANA", observedAt: time.Date(2026, 7, 13, 2, 0, 0, 0, time.UTC), start: "09:00", end: "18:00", timezone: "Local", applied: 1, wantError: true},
		{name: "wrong applied multiplier", observedAt: time.Date(2026, 3, 8, 6, 30, 0, 0, time.UTC), start: "01:00", end: "03:00", timezone: "America/New_York", applied: 1, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := validBillingProbeProtocolFields(tt.observedAt)
			fields["peak_rate_enabled"] = true
			fields["peak_start"] = tt.start
			fields["peak_end"] = tt.end
			fields["peak_rate_multiplier"] = 2.0
			fields["applied_peak_multiplier"] = tt.applied
			fields["effective_rate_multiplier"] = 0.8 * tt.applied
			fields["timezone"] = tt.timezone

			observation, err := parseUpstreamSourceBillingResponse(
				billingProbeProtocolBody(t, fields),
				tt.observedAt.Add(time.Minute),
			)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, observation.AppliedPeakMultiplier)
			assert.Equal(t, tt.applied, *observation.AppliedPeakMultiplier)
		})
	}
}

func TestParseUpstreamSourceBillingClockMatchesPinnedGrammar(t *testing.T) {
	accepted := map[string]int{
		"0:00":  0,
		"00:00": 0,
		"1:30":  90,
		"01:30": 90,
		"9:05":  545,
		"09:05": 545,
		"23:59": 1439,
	}
	for value, expected := range accepted {
		t.Run("accept "+value, func(t *testing.T) {
			minutes, ok := parseUpstreamSourceBillingClock(value)
			assert.True(t, ok)
			assert.Equal(t, expected, minutes)
		})
	}

	for _, value := range []string{
		"", ":00", "000:00", "1:0", "1:000", "24:00", "23:60", "a:00", "1:a0", " 1:30", "1:30 ", "+1:30",
	} {
		t.Run("reject "+value, func(t *testing.T) {
			_, ok := parseUpstreamSourceBillingClock(value)
			assert.False(t, ok)
		})
	}
}
