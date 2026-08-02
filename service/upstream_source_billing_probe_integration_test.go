package service

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaydto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type upstreamSourceBillingProbeFixture struct {
	source  model.UpstreamSource
	mapping model.UpstreamSourceChannelMapping
	channel model.Channel
	probe   model.UpstreamSourceBillingProbe
}

func setupUpstreamSourceBillingProbeServiceTest(t *testing.T) {
	t.Helper()
	oldDB := model.DB
	oldDatabaseType := common.MainDatabaseType()
	dsn := "file:billing-probe-" + common.GetUUID() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	fetchSetting := system_setting.GetFetchSetting()
	oldFetchSetting := *fetchSetting
	fetchSetting.EnableSSRFProtection = true
	fetchSetting.AllowPrivateIp = false
	fetchSetting.DomainFilterMode = false
	fetchSetting.IpFilterMode = false
	fetchSetting.DomainList = nil
	fetchSetting.IpList = nil
	fetchSetting.AllowedPorts = nil
	fetchSetting.ApplyIPFilterForDomain = true
	t.Cleanup(func() {
		model.DB = oldDB
		common.SetMainDatabaseType(oldDatabaseType)
		*fetchSetting = oldFetchSetting
		_ = sqlDB.Close()
	})
	require.NoError(t, db.AutoMigrate(
		&model.UpstreamSource{},
		&model.UpstreamSourceChannelMapping{},
		&model.UpstreamSourceBillingProbe{},
		&model.Channel{},
	))
}

func createUpstreamSourceBillingProbeFixture(t *testing.T, baseURL string, allowPrivate bool) upstreamSourceBillingProbeFixture {
	t.Helper()
	syncConfig, err := common.Marshal(map[string]any{"allow_private_ip": allowPrivate})
	require.NoError(t, err)
	source := model.UpstreamSource{
		Name:         "probe-source",
		Type:         model.UpstreamSourceTypeSub2API,
		Status:       model.UpstreamSourceStatusEnabled,
		BaseURL:      "https://management.invalid",
		RelayBaseURL: baseURL,
		SyncConfig:   string(syncConfig),
		AuthConfig:   `{"access_token":"management-secret"}`,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	mapping := model.UpstreamSourceChannelMapping{
		SourceID:         source.Id,
		SyncEnabled:      true,
		UpstreamGroupID:  "group-1",
		DiscoveryStatus:  model.UpstreamMappingDiscoveryStatusActive,
		UpstreamKeyID:    "upstream-key-id-not-a-secret",
		UpstreamPlatform: "openai",
	}
	require.NoError(t, model.DB.Create(&mapping).Error)
	channel := model.Channel{
		Type:        1,
		Key:         "sk-mapping-bearer-secret",
		Status:      common.ChannelStatusEnabled,
		Name:        "generated-probe-channel",
		BaseURL:     common.GetPointer(baseURL),
		Models:      "gpt-4o-mini",
		Group:       "default",
		CreatedTime: time.Now().Unix(),
	}
	channel.SetSetting(relaydto.ChannelSettings{})
	channel.SetOtherSettings(relaydto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:  source.Id,
		GeneratedByUpstreamMappingID: mapping.Id,
	})
	require.NoError(t, model.DB.Create(&channel).Error)
	require.NoError(t, model.DB.Model(&mapping).Updates(map[string]any{
		"local_channel_id": channel.Id,
	}).Error)
	mapping.LocalChannelID = channel.Id
	probe := model.UpstreamSourceBillingProbe{
		SourceID:        source.Id,
		MappingID:       mapping.Id,
		Enabled:         true,
		IntervalMinutes: 5,
		NextProbeAt:     0,
	}
	require.NoError(t, model.DB.Create(&probe).Error)
	return upstreamSourceBillingProbeFixture{source: source, mapping: mapping, channel: channel, probe: probe}
}

func newUpstreamSourceBillingProbeTestService(now time.Time) *UpstreamSourceBillingProbeService {
	svc := NewUpstreamSourceBillingProbeService()
	svc.now = func() time.Time { return now }
	svc.requestTimeout = time.Second
	svc.jitter = func(interval time.Duration) time.Duration { return interval }
	svc.batchSize = 20
	svc.concurrency = 4
	return svc
}

func upstreamSourceBillingProbeSuccessBody(t *testing.T, observedAt time.Time) []byte {
	t.Helper()
	return billingProbeProtocolBody(t, validBillingProbeProtocolFields(observedAt))
}

func TestUpstreamSourceBillingProbeUsesGeneratedChannelBearerURLAndForcedHeaders(t *testing.T) {
	setupUpstreamSourceBillingProbeServiceTest(t)
	now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	body := upstreamSourceBillingProbeSuccessBody(t, now.Add(-time.Minute))
	var requestURL string
	var authorization string
	var accept string
	var staticHeader string
	var cookie string
	var host string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURL = r.URL.String()
		authorization = r.Header.Get("Authorization")
		accept = r.Header.Get("Accept")
		staticHeader = r.Header.Get("X-Static-Probe")
		cookie = r.Header.Get("Cookie")
		host = r.Host
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	baseURL := server.URL + "/openai/v1?tenant=a#must-drop"
	fixture := createUpstreamSourceBillingProbeFixture(t, baseURL, true)
	headerOverride, err := common.Marshal(map[string]any{
		"X-Static-Probe": "present-{api_key}",
	})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).
		Update("header_override", string(headerOverride)).Error)

	response, err := newUpstreamSourceBillingProbeTestService(now).ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, model.UpstreamSourceBillingProbeStatusOK, response.Status)
	assert.Equal(t, "/openai/v1/sub2api/billing?tenant=a", requestURL)
	assert.Equal(t, "Bearer sk-mapping-bearer-secret", authorization)
	assert.Equal(t, "application/json", accept)
	assert.Equal(t, "present-sk-mapping-bearer-secret", staticHeader)
	assert.Empty(t, cookie)
	assert.NotEmpty(t, host)

	var stored model.UpstreamSourceBillingProbe
	require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&stored).Error)
	assert.NotEmpty(t, stored.LastGoodIdentityFingerprint)
	assert.Empty(t, stored.ClaimIdentityFingerprint)
	assert.NotContains(t, stored.LastGoodIdentityFingerprint, fixture.channel.Key)
	assert.Equal(t, now.Unix(), stored.ReceivedAt)
	assert.Equal(t, now.Add(10*time.Minute).Unix(), stored.FreshUntil)
	assert.Equal(t, 0.8, *stored.EffectiveRateMultiplier)
	encoded, err := common.Marshal(stored)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), fixture.channel.Key)
	assert.NotContains(t, string(encoded), "management-secret")
}

func TestUpstreamSourceBillingProbeDoesNotTransmitClientIdentityDotVariant(t *testing.T) {
	setupUpstreamSourceBillingProbeServiceTest(t)
	now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write(upstreamSourceBillingProbeSuccessBody(t, now))
	}))
	defer server.Close()
	fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)
	overrides, err := common.Marshal(map[string]any{"X.Forwarded.For": "spoofed-client-identity"})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).
		Update("header_override", string(overrides)).Error)

	response, err := newUpstreamSourceBillingProbeTestService(now).
		ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, UpstreamSourceBillingProbeErrorIneligible, response.ErrorCode)
	assert.Zero(t, calls.Load())
}

func TestUpstreamSourceBillingProbeStoresObservationWithoutMutatingAdvertisedPricing(t *testing.T) {
	setupUpstreamSourceBillingProbeServiceTest(t)
	now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(upstreamSourceBillingProbeSuccessBody(t, now.Add(-time.Minute)))
	}))
	defer server.Close()
	fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)

	upstreamRate := 0.5
	advertisedRate := 0.375
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", fixture.mapping.Id).Updates(map[string]any{
		"upstream_rate_multiplier":  &upstreamRate,
		"effective_rate_multiplier": &advertisedRate,
	}).Error)
	settings := fixture.channel.GetOtherSettings()
	settings.ChannelAutoPriorityRateMultiplier = 0.625
	fixture.channel.SetOtherSettings(settings)
	remark := "operator-managed advertised rate"
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).Updates(map[string]any{
		"name":     "generated-advertised-label",
		"remark":   &remark,
		"settings": fixture.channel.OtherSettings,
	}).Error)

	response, err := newUpstreamSourceBillingProbeTestService(now).ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
	require.NoError(t, err)
	require.NotNil(t, response)
	require.NotNil(t, response.EffectiveRateMultiplier)
	assert.Equal(t, 0.8, *response.EffectiveRateMultiplier)

	var mapping model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.First(&mapping, fixture.mapping.Id).Error)
	require.NotNil(t, mapping.UpstreamRateMultiplier)
	require.NotNil(t, mapping.EffectiveRateMultiplier)
	assert.Equal(t, upstreamRate, *mapping.UpstreamRateMultiplier)
	assert.Equal(t, advertisedRate, *mapping.EffectiveRateMultiplier)

	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, fixture.channel.Id).Error)
	assert.Equal(t, "generated-advertised-label", channel.Name)
	require.NotNil(t, channel.Remark)
	assert.Equal(t, remark, *channel.Remark)
	assert.Equal(t, 0.625, channel.GetOtherSettings().ChannelAutoPriorityRateMultiplier)
}

func TestUpstreamSourceBillingProbeRejectsUnsafeStaticHeaderControlsBeforeRequest(t *testing.T) {
	tests := []struct {
		name       string
		overrides  any
		bearerKey  string
		invalidRaw string
	}{
		{name: "wildcard passthrough", overrides: map[string]any{"*": ""}},
		{name: "short regex passthrough", overrides: map[string]any{"re:^X-Client-": ""}},
		{name: "regex passthrough", overrides: map[string]any{"regex:^X-Client-": ""}},
		{name: "client header placeholder", overrides: map[string]any{"X-Trace": "{client_header:X-Trace}"}},
		{name: "authorization override", overrides: map[string]any{"Authorization": "Bearer operator-value"}},
		{name: "accept override", overrides: map[string]any{"Accept": "text/plain"}},
		{name: "hop by hop override", overrides: map[string]any{"Connection": "close"}},
		{name: "proxy authentication override", overrides: map[string]any{"Proxy-Authenticate": "Basic realm=probe"}},
		{name: "host override", overrides: map[string]any{"Host": "attacker.invalid"}},
		{name: "cookie override", overrides: map[string]any{"Cookie": "session=secret"}},
		{name: "non string value", overrides: map[string]any{"X-Trace": 7}},
		{name: "header name CRLF", overrides: map[string]any{"X-Trace\r\nInjected": "value"}},
		{name: "header value CRLF", overrides: map[string]any{"X-Trace": "value\r\nInjected: secret"}},
		{name: "bearer key CRLF", overrides: map[string]any{"X-Upstream-Key": "{api_key}"}, bearerKey: "sk-safe\r\nInjected: secret"},
		{name: "bearer key leading CRLF", overrides: map[string]any{"X-Upstream-Key": "{api_key}"}, bearerKey: "\r\nsk-leading-secret"},
		{name: "bearer key trailing CRLF", overrides: map[string]any{"X-Upstream-Key": "{api_key}"}, bearerKey: "sk-trailing-secret\r\n"},
		{name: "malformed JSON", invalidRaw: `{"X-Trace":`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupUpstreamSourceBillingProbeServiceTest(t)
			now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				_, _ = w.Write(upstreamSourceBillingProbeSuccessBody(t, now))
			}))
			defer server.Close()
			fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)
			raw := tt.invalidRaw
			if raw == "" {
				encoded, err := common.Marshal(tt.overrides)
				require.NoError(t, err)
				raw = string(encoded)
			}
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).
				Update("header_override", raw).Error)
			if tt.bearerKey != "" {
				require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).
					Update("key", tt.bearerKey).Error)
			}

			response, err := newUpstreamSourceBillingProbeTestService(now).
				ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
			if tt.bearerKey != "" {
				require.ErrorIs(t, err, ErrUpstreamSourceBillingProbeLeaseUnavailable)
				assert.Nil(t, response)
			} else {
				require.NoError(t, err)
				require.NotNil(t, response)
				assert.Equal(t, model.UpstreamSourceBillingProbeStatusFailed, response.Status)
				assert.Equal(t, UpstreamSourceBillingProbeErrorIneligible, response.ErrorCode)
			}
			assert.Zero(t, calls.Load())
			responseJSON, err := common.Marshal(response)
			require.NoError(t, err)
			assert.NotContains(t, string(responseJSON), fixture.channel.Key)
			if tt.bearerKey != "" {
				assert.NotContains(t, string(responseJSON), tt.bearerKey)
			}
		})
	}
}

func TestUpstreamSourceBillingProbePersistsPinnedOneDigitPeakClock(t *testing.T) {
	setupUpstreamSourceBillingProbeServiceTest(t)
	now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	fields := validBillingProbeProtocolFields(now)
	fields["peak_rate_enabled"] = true
	fields["peak_start"] = "1:30"
	fields["peak_end"] = "3:00"
	fields["peak_rate_multiplier"] = 2.0
	fields["applied_peak_multiplier"] = 2.0
	fields["effective_rate_multiplier"] = 1.6
	fields["timezone"] = "UTC"
	body := billingProbeProtocolBody(t, fields)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()
	fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)

	response, err := newUpstreamSourceBillingProbeTestService(now).
		ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, model.UpstreamSourceBillingProbeStatusOK, response.Status)
	assert.Equal(t, "1:30", response.PeakStart)
	assert.Equal(t, "3:00", response.PeakEnd)
	var stored model.UpstreamSourceBillingProbe
	require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&stored).Error)
	assert.Equal(t, "1:30", stored.PeakStart)
	assert.Equal(t, "3:00", stored.PeakEnd)
	startMinute, startOK := parseUpstreamSourceBillingClock(stored.PeakStart)
	endMinute, endOK := parseUpstreamSourceBillingClock(stored.PeakEnd)
	assert.True(t, startOK)
	assert.True(t, endOK)
	assert.Equal(t, 90, startMinute)
	assert.Equal(t, 180, endMinute)
}

func TestUpstreamSourceBillingProbeSSRFPrivateIPRequiresExplicitSourceOptIn(t *testing.T) {
	for _, allowPrivate := range []bool{false, true} {
		t.Run(strconv.FormatBool(allowPrivate), func(t *testing.T) {
			setupUpstreamSourceBillingProbeServiceTest(t)
			now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
			body := upstreamSourceBillingProbeSuccessBody(t, now)
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				_, _ = w.Write(body)
			}))
			defer server.Close()
			fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, allowPrivate)

			response, err := newUpstreamSourceBillingProbeTestService(now).ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
			require.NoError(t, err)
			require.NotNil(t, response)
			if allowPrivate {
				assert.Equal(t, model.UpstreamSourceBillingProbeStatusOK, response.Status)
				assert.Equal(t, int64(1), calls.Load())
			} else {
				assert.Equal(t, model.UpstreamSourceBillingProbeStatusFailed, response.Status)
				assert.Equal(t, UpstreamSourceBillingProbeErrorNetworkNotAllowed, response.ErrorCode)
				assert.Zero(t, calls.Load())
			}
		})
	}
}

func TestUpstreamSourceBillingProbeRejectsRedirects(t *testing.T) {
	setupUpstreamSourceBillingProbeServiceTest(t)
	now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	var redirectedCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			redirectedCalls.Add(1)
			_, _ = w.Write(upstreamSourceBillingProbeSuccessBody(t, now))
			return
		}
		http.Redirect(w, r, "/redirected", http.StatusFound)
	}))
	defer server.Close()
	fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)

	response, err := newUpstreamSourceBillingProbeTestService(now).ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, model.UpstreamSourceBillingProbeStatusFailed, response.Status)
	assert.Equal(t, UpstreamSourceBillingProbeErrorRedirectNotAllowed, response.ErrorCode)
	assert.Zero(t, redirectedCalls.Load())
}

func TestUpstreamSourceBillingProbeProxyErrorsNeverFallBackDirect(t *testing.T) {
	for _, tc := range []struct {
		name      string
		proxy     func(t *testing.T) string
		errorCode string
	}{
		{name: "invalid", proxy: func(*testing.T) string { return "://invalid-proxy" }, errorCode: UpstreamSourceBillingProbeErrorInvalidProxy},
		{name: "unreachable", proxy: func(t *testing.T) string {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			address := listener.Addr().String()
			require.NoError(t, listener.Close())
			return "http://" + address
		}, errorCode: UpstreamSourceBillingProbeErrorRequestFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupUpstreamSourceBillingProbeServiceTest(t)
			now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
			var targetCalls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				targetCalls.Add(1)
				_, _ = w.Write(upstreamSourceBillingProbeSuccessBody(t, now))
			}))
			defer server.Close()
			fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)
			settings := relaydto.ChannelSettings{Proxy: tc.proxy(t)}
			fixture.channel.SetSetting(settings)
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).
				Update("setting", fixture.channel.Setting).Error)

			response, err := newUpstreamSourceBillingProbeTestService(now).ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
			require.NoError(t, err)
			require.NotNil(t, response)
			assert.Equal(t, model.UpstreamSourceBillingProbeStatusFailed, response.Status)
			assert.Equal(t, tc.errorCode, response.ErrorCode)
			assert.Zero(t, targetCalls.Load())
		})
	}
}

func TestUpstreamSourceBillingProbeBodyLimitAndTimeout(t *testing.T) {
	t.Run("body limit", func(t *testing.T) {
		setupUpstreamSourceBillingProbeServiceTest(t)
		now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
		const rawBody = "oversized-non-rate-limit-body-secret"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat(rawBody, UpstreamSourceBillingProbeMaxBodyBytes/len(rawBody)+1))
		}))
		defer server.Close()
		fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)

		response, err := newUpstreamSourceBillingProbeTestService(now).ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
		require.NoError(t, err)
		require.NotNil(t, response)
		assert.Equal(t, model.UpstreamSourceBillingProbeStatusFailed, response.Status)
		assert.Equal(t, UpstreamSourceBillingProbeErrorResponseTooLarge, response.ErrorCode)
		var stored model.UpstreamSourceBillingProbe
		require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&stored).Error)
		assert.Equal(t, http.StatusOK, stored.HTTPStatus)
		assert.Equal(t, now.Add(5*time.Minute).Unix(), stored.NextProbeAt)
		responseJSON, err := common.Marshal(response)
		require.NoError(t, err)
		storedJSON, err := common.Marshal(stored)
		require.NoError(t, err)
		assert.NotContains(t, string(responseJSON), rawBody)
		assert.NotContains(t, string(storedJSON), rawBody)
	})

	t.Run("timeout", func(t *testing.T) {
		setupUpstreamSourceBillingProbeServiceTest(t)
		now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer server.Close()
		fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)
		svc := newUpstreamSourceBillingProbeTestService(now)
		svc.requestTimeout = 25 * time.Millisecond

		started := time.Now()
		response, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
		require.NoError(t, err)
		assert.Equal(t, UpstreamSourceBillingProbeErrorRequestTimeout, response.ErrorCode)
		assert.Less(t, time.Since(started), time.Second)
	})
}

func TestUpstreamSourceBillingProbeRateLimitPrecedesBodyHandling(t *testing.T) {
	t.Run("oversized streaming body", func(t *testing.T) {
		setupUpstreamSourceBillingProbeServiceTest(t)
		now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
		const (
			rawBodySecret   = "rate-limit-body-secret"
			rawHeaderSecret = "rate-limit-header-secret"
			rawQuerySecret  = "rate-limit-query-secret"
		)
		bodyClosed := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "3600")
			w.Header().Set("X-Upstream-Debug", rawHeaderSecret)
			w.WriteHeader(http.StatusTooManyRequests)
			flusher := w.(http.Flusher)
			flusher.Flush()
			chunk := []byte(strings.Repeat(rawBodySecret, UpstreamSourceBillingProbeMaxBodyBytes/len(rawBodySecret)+1))
			for {
				if _, err := w.Write(chunk); err != nil {
					close(bodyClosed)
					return
				}
				flusher.Flush()
			}
		}))
		defer server.Close()
		fixture := createUpstreamSourceBillingProbeFixture(t, server.URL+"?tenant="+rawQuerySecret, true)

		response, err := newUpstreamSourceBillingProbeTestService(now).
			ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
		require.NoError(t, err)
		require.NotNil(t, response)
		assert.Equal(t, model.UpstreamSourceBillingProbeStatusFailed, response.Status)
		assert.Equal(t, UpstreamSourceBillingProbeErrorRateLimited, response.ErrorCode)

		var stored model.UpstreamSourceBillingProbe
		require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&stored).Error)
		assert.Equal(t, http.StatusTooManyRequests, stored.HTTPStatus)
		assert.GreaterOrEqual(t, stored.NextProbeAt-now.Unix(), int64(3600))

		responseJSON, err := common.Marshal(response)
		require.NoError(t, err)
		storedJSON, err := common.Marshal(stored)
		require.NoError(t, err)
		for _, secret := range []string{
			rawBodySecret,
			rawHeaderSecret,
			rawQuerySecret,
			fixture.channel.Key,
			server.URL,
		} {
			assert.NotContains(t, string(responseJSON), secret)
			assert.NotContains(t, string(storedJSON), secret)
		}

		select {
		case <-bodyClosed:
		case <-time.After(time.Second):
			t.Fatal("probe did not close the rate-limited response body")
		}
	})

	t.Run("empty body", func(t *testing.T) {
		setupUpstreamSourceBillingProbeServiceTest(t)
		now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer server.Close()
		fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)

		response, err := newUpstreamSourceBillingProbeTestService(now).
			ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
		require.NoError(t, err)
		require.NotNil(t, response)
		assert.Equal(t, UpstreamSourceBillingProbeErrorRateLimited, response.ErrorCode)
		var stored model.UpstreamSourceBillingProbe
		require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&stored).Error)
		assert.Equal(t, http.StatusTooManyRequests, stored.HTTPStatus)
		assert.Equal(t, now.Add(time.Hour).Unix(), stored.NextProbeAt)
	})

	t.Run("truncated body", func(t *testing.T) {
		setupUpstreamSourceBillingProbeServiceTest(t)
		now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "3600")
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, "truncated-rate-limit-body-secret")
		}))
		defer server.Close()
		fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)

		response, err := newUpstreamSourceBillingProbeTestService(now).
			ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
		require.NoError(t, err)
		require.NotNil(t, response)
		assert.Equal(t, UpstreamSourceBillingProbeErrorRateLimited, response.ErrorCode)
		var stored model.UpstreamSourceBillingProbe
		require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&stored).Error)
		assert.Equal(t, http.StatusTooManyRequests, stored.HTTPStatus)
		assert.Equal(t, now.Add(time.Hour).Unix(), stored.NextProbeAt)
	})

	for _, retryAfter := range []string{
		"malformed-rate-limit-header-secret",
		"-1",
		"18446747674",
	} {
		t.Run("invalid retry after "+retryAfter, func(t *testing.T) {
			setupUpstreamSourceBillingProbeServiceTest(t)
			now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
			const rawBodySecret = "invalid-retry-after-body-secret"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", retryAfter)
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, rawBodySecret)
			}))
			defer server.Close()
			fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)

			response, err := newUpstreamSourceBillingProbeTestService(now).
				ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
			require.NoError(t, err)
			require.NotNil(t, response)
			assert.Equal(t, UpstreamSourceBillingProbeErrorRateLimited, response.ErrorCode)
			var stored model.UpstreamSourceBillingProbe
			require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&stored).Error)
			assert.Equal(t, http.StatusTooManyRequests, stored.HTTPStatus)
			assert.Equal(t, now.Add(5*time.Minute).Unix(), stored.NextProbeAt)

			responseJSON, err := common.Marshal(response)
			require.NoError(t, err)
			storedJSON, err := common.Marshal(stored)
			require.NoError(t, err)
			for _, secret := range []string{rawBodySecret, retryAfter, fixture.channel.Key, server.URL} {
				assert.NotContains(t, string(responseJSON), secret)
				assert.NotContains(t, string(storedJSON), secret)
			}
		})
	}
}

func TestUpstreamSourceBillingProbeRetryAfterNeverShortensRequestedDelay(t *testing.T) {
	setupUpstreamSourceBillingProbeServiceTest(t)
	now := time.Date(2026, time.July, 13, 2, 0, 0, 900_000_000, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)

	response, err := newUpstreamSourceBillingProbeTestService(now).
		ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
	require.NoError(t, err)
	require.Equal(t, UpstreamSourceBillingProbeErrorRateLimited, response.ErrorCode)
	var stored model.UpstreamSourceBillingProbe
	require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&stored).Error)
	assert.GreaterOrEqual(t, time.Unix(stored.NextProbeAt, 0).Sub(now), 600*time.Second)
}

func TestUpstreamSourceBillingProbeHTTPStatusesRetryAfterAndNoBackoff(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		retryAfter string
		wantStatus string
		wantError  string
		wantDelay  time.Duration
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized, wantStatus: model.UpstreamSourceBillingProbeStatusFailed, wantError: UpstreamSourceBillingProbeErrorUnauthorized, wantDelay: 5 * time.Minute},
		{name: "forbidden", statusCode: http.StatusForbidden, wantStatus: model.UpstreamSourceBillingProbeStatusFailed, wantError: UpstreamSourceBillingProbeErrorForbidden, wantDelay: 5 * time.Minute},
		{name: "not found", statusCode: http.StatusNotFound, wantStatus: model.UpstreamSourceBillingProbeStatusUnsupported, wantError: UpstreamSourceBillingProbeErrorUnsupported, wantDelay: 5 * time.Minute},
		{name: "method not allowed", statusCode: http.StatusMethodNotAllowed, wantStatus: model.UpstreamSourceBillingProbeStatusUnsupported, wantError: UpstreamSourceBillingProbeErrorUnsupported, wantDelay: 5 * time.Minute},
		{name: "rate limited", statusCode: http.StatusTooManyRequests, retryAfter: "600", wantStatus: model.UpstreamSourceBillingProbeStatusFailed, wantError: UpstreamSourceBillingProbeErrorRateLimited, wantDelay: 10 * time.Minute},
		{name: "ordinary failure ignores retry after", statusCode: http.StatusInternalServerError, retryAfter: "600", wantStatus: model.UpstreamSourceBillingProbeStatusFailed, wantError: UpstreamSourceBillingProbeErrorHTTP, wantDelay: 5 * time.Minute},
		{name: "malformed success", statusCode: http.StatusOK, wantStatus: model.UpstreamSourceBillingProbeStatusFailed, wantError: UpstreamSourceBillingProbeErrorInvalidResponse, wantDelay: 5 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupUpstreamSourceBillingProbeServiceTest(t)
			now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.retryAfter != "" {
					w.Header().Set("Retry-After", tt.retryAfter)
				}
				w.WriteHeader(tt.statusCode)
				_, _ = io.WriteString(w, `{"not":"the pinned schema"}`)
			}))
			defer server.Close()
			fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)
			svc := newUpstreamSourceBillingProbeTestService(now)

			response, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, response.Status)
			assert.Equal(t, tt.wantError, response.ErrorCode)
			var stored model.UpstreamSourceBillingProbe
			require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&stored).Error)
			assert.Equal(t, now.Add(tt.wantDelay).Unix(), stored.NextProbeAt)
			assert.Equal(t, 1, stored.FailureCount)
			assert.Equal(t, tt.statusCode, stored.HTTPStatus)
			var source model.UpstreamSource
			require.NoError(t, model.DB.First(&source, fixture.source.Id).Error)
			assert.Empty(t, source.LastDiscoveryError)
			assert.Empty(t, source.LastSyncError)

			nextNow := now.Add(time.Minute)
			svc.now = func() time.Time { return nextNow }
			_, err = svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
			require.NoError(t, err)
			require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&stored).Error)
			assert.Equal(t, 2, stored.FailureCount)
			assert.Equal(t, nextNow.Add(tt.wantDelay).Unix(), stored.NextProbeAt, "failure count must not introduce exponential backoff")
		})
	}
}

func TestUpstreamSourceBillingProbeRetainsLastGoodAndNeverProjectsUnsupportedOrStaleAsFresh(t *testing.T) {
	setupUpstreamSourceBillingProbeServiceTest(t)
	now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	status := atomic.Int64{}
	status.Store(http.StatusOK)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		code := int(status.Load())
		w.WriteHeader(code)
		if code == http.StatusOK {
			_, _ = w.Write(upstreamSourceBillingProbeSuccessBody(t, now))
		}
	}))
	defer server.Close()
	fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)
	svc := newUpstreamSourceBillingProbeTestService(now)

	first, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
	require.NoError(t, err)
	require.Equal(t, model.UpstreamSourceBillingProbeStatusOK, first.Status)
	require.True(t, first.Fresh)
	var stored model.UpstreamSourceBillingProbe
	require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&stored).Error)
	assert.Equal(t, int64(1), stored.LastGoodGeneration)
	receivedAt := first.ReceivedAt
	freshUntil := first.FreshUntil
	effective := *first.EffectiveRateMultiplier

	status.Store(http.StatusInternalServerError)
	svc.now = func() time.Time { return now.Add(time.Minute) }
	failed, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
	require.NoError(t, err)
	assert.Equal(t, model.UpstreamSourceBillingProbeStatusFailed, failed.Status)
	assert.True(t, failed.HasLastGood)
	assert.True(t, failed.Fresh)
	assert.Equal(t, receivedAt, failed.ReceivedAt)
	assert.Equal(t, freshUntil, failed.FreshUntil)
	assert.Equal(t, effective, *failed.EffectiveRateMultiplier)
	require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&stored).Error)
	assert.Equal(t, int64(1), stored.LastGoodGeneration, "a retained failed attempt must not authorize a new last-good generation")

	stale, err := svc.GetMapping(context.Background(), fixture.source.Id, fixture.mapping.Id, now.Add(11*time.Minute))
	require.NoError(t, err)
	assert.True(t, stale.HasLastGood)
	assert.False(t, stale.Fresh)

	status.Store(http.StatusNotFound)
	svc.now = func() time.Time { return now.Add(2 * time.Minute) }
	unsupported, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
	require.NoError(t, err)
	assert.Equal(t, model.UpstreamSourceBillingProbeStatusUnsupported, unsupported.Status)
	assert.True(t, unsupported.HasLastGood)
	assert.False(t, unsupported.Fresh)
	assert.Equal(t, effective, *unsupported.EffectiveRateMultiplier)
	require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&stored).Error)
	assert.Equal(t, int64(1), stored.LastGoodGeneration, "unsupported attempts must not advance retained data")

	status.Store(http.StatusInternalServerError)
	svc.now = func() time.Time { return now.Add(3 * time.Minute) }
	stillUnsupported, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
	require.NoError(t, err)
	assert.Equal(t, model.UpstreamSourceBillingProbeStatusUnsupported, stillUnsupported.Status)
	assert.True(t, stillUnsupported.Unsupported)
	assert.True(t, stillUnsupported.HasLastGood)
	assert.False(t, stillUnsupported.Fresh, "an ordinary failure must not undo unsupported invalidation")

	status.Store(http.StatusOK)
	svc.now = func() time.Time { return now.Add(4 * time.Minute) }
	revalidated, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
	require.NoError(t, err)
	assert.Equal(t, model.UpstreamSourceBillingProbeStatusOK, revalidated.Status)
	assert.False(t, revalidated.Unsupported)
	assert.True(t, revalidated.Fresh)
	require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&stored).Error)
	assert.Equal(t, int64(2), stored.LastGoodGeneration)
}

func TestUpstreamSourceBillingProbeUnsupportedInvalidationSurvivesUnidentifiedFailureAndIdentityRevert(t *testing.T) {
	setupUpstreamSourceBillingProbeServiceTest(t)
	now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	var status atomic.Int64
	status.Store(http.StatusOK)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		statusCode := int(status.Load())
		w.WriteHeader(statusCode)
		if statusCode == http.StatusOK {
			_, _ = w.Write(upstreamSourceBillingProbeSuccessBody(t, now))
		}
	}))
	defer server.Close()
	fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)
	svc := newUpstreamSourceBillingProbeTestService(now)

	lastGood, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
	require.NoError(t, err)
	require.True(t, lastGood.Fresh)

	status.Store(http.StatusNotFound)
	svc.now = func() time.Time { return now.Add(time.Minute) }
	unsupported, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
	require.NoError(t, err)
	require.True(t, unsupported.Unsupported)
	require.False(t, unsupported.Fresh)

	unsafeHeaders, err := common.Marshal(map[string]any{"Authorization": "operator-value"})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", fixture.channel.Id).
		Update("header_override", string(unsafeHeaders)).Error)
	svc.now = func() time.Time { return now.Add(2 * time.Minute) }
	unidentified, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
	require.NoError(t, err)
	require.NotNil(t, unidentified)
	require.False(t, unidentified.Fresh)

	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", fixture.channel.Id).
		Update("header_override", nil).Error)
	reverted, err := svc.GetMapping(context.Background(), fixture.source.Id, fixture.mapping.Id, now.Add(3*time.Minute))
	require.NoError(t, err)
	require.NotNil(t, reverted)
	assert.True(t, reverted.Unsupported, "404 invalidation must remain monotonic until a later success")
	assert.False(t, reverted.Fresh, "identity reversion without a successful probe must not relabel the old observation fresh")
}

func TestUpstreamSourceBillingProbeOversizedNotFoundInvalidatesLastGood(t *testing.T) {
	setupUpstreamSourceBillingProbeServiceTest(t)
	now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	var status atomic.Int64
	status.Store(http.StatusOK)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		code := int(status.Load())
		w.WriteHeader(code)
		if code == http.StatusOK {
			_, _ = w.Write(upstreamSourceBillingProbeSuccessBody(t, now))
			return
		}
		_, _ = io.WriteString(w, strings.Repeat("x", UpstreamSourceBillingProbeMaxBodyBytes+1))
	}))
	defer server.Close()
	fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)
	svc := newUpstreamSourceBillingProbeTestService(now)

	first, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
	require.NoError(t, err)
	require.True(t, first.Fresh)

	status.Store(http.StatusNotFound)
	svc.now = func() time.Time { return now.Add(time.Minute) }
	response, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
	require.NoError(t, err)
	assert.Equal(t, model.UpstreamSourceBillingProbeStatusUnsupported, response.Status)
	assert.Equal(t, UpstreamSourceBillingProbeErrorUnsupported, response.ErrorCode)
	assert.False(t, response.Fresh)
}

func TestUpstreamSourceBillingProbeLastGoodIdentityTransitions(t *testing.T) {
	for _, tt := range []struct {
		name string
		run  func(t *testing.T, fixture upstreamSourceBillingProbeFixture, svc *UpstreamSourceBillingProbeService, status *atomic.Int64, rate *atomic.Int64, now time.Time)
	}{
		{
			name: "rotated identity failure retains stale last good",
			run: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, svc *UpstreamSourceBillingProbeService, status *atomic.Int64, _ *atomic.Int64, now time.Time) {
				var before model.UpstreamSourceBillingProbe
				require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&before).Error)
				require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).
					Update("key", "sk-rotated-bearer-secret").Error)
				status.Store(http.StatusInternalServerError)
				svc.now = func() time.Time { return now.Add(time.Minute) }

				failed, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
				require.NoError(t, err)
				require.NotNil(t, failed)
				assert.True(t, failed.HasLastGood)
				assert.False(t, failed.Fresh)
				require.NotNil(t, failed.EffectiveRateMultiplier)
				assert.Equal(t, 0.8, *failed.EffectiveRateMultiplier)
				var after model.UpstreamSourceBillingProbe
				require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&after).Error)
				assert.Equal(t, before.LastGoodIdentityFingerprint, after.LastGoodIdentityFingerprint)
				assert.Empty(t, after.ClaimIdentityFingerprint)
			},
		},
		{
			name: "same identity failure retains fresh last good",
			run: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, svc *UpstreamSourceBillingProbeService, status *atomic.Int64, _ *atomic.Int64, now time.Time) {
				status.Store(http.StatusInternalServerError)
				svc.now = func() time.Time { return now.Add(time.Minute) }

				failed, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
				require.NoError(t, err)
				require.NotNil(t, failed)
				assert.True(t, failed.HasLastGood)
				assert.True(t, failed.Fresh)
			},
		},
		{
			name: "unsupported invalidation is monotonic across later failure",
			run: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, svc *UpstreamSourceBillingProbeService, status *atomic.Int64, _ *atomic.Int64, now time.Time) {
				status.Store(http.StatusNotFound)
				svc.now = func() time.Time { return now.Add(time.Minute) }
				unsupported, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
				require.NoError(t, err)
				require.NotNil(t, unsupported)
				require.False(t, unsupported.Fresh)

				status.Store(http.StatusInternalServerError)
				svc.now = func() time.Time { return now.Add(2 * time.Minute) }
				failed, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
				require.NoError(t, err)
				require.NotNil(t, failed)
				assert.True(t, failed.Unsupported)
				assert.False(t, failed.Fresh)
			},
		},
		{
			name: "success under current identity replaces last good",
			run: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, svc *UpstreamSourceBillingProbeService, status *atomic.Int64, rate *atomic.Int64, now time.Time) {
				var before model.UpstreamSourceBillingProbe
				require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&before).Error)
				require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).
					Update("key", "sk-rotated-bearer-secret").Error)
				status.Store(http.StatusInternalServerError)
				svc.now = func() time.Time { return now.Add(time.Minute) }
				failed, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
				require.NoError(t, err)
				require.False(t, failed.Fresh)

				rate.Store(6)
				status.Store(http.StatusOK)
				svc.now = func() time.Time { return now.Add(2 * time.Minute) }
				revalidated, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
				require.NoError(t, err)
				require.NotNil(t, revalidated)
				assert.True(t, revalidated.Fresh)
				require.NotNil(t, revalidated.EffectiveRateMultiplier)
				assert.Equal(t, 0.6, *revalidated.EffectiveRateMultiplier)
				var after model.UpstreamSourceBillingProbe
				require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&after).Error)
				assert.NotEqual(t, before.LastGoodIdentityFingerprint, after.LastGoodIdentityFingerprint)
				assert.Empty(t, after.ClaimIdentityFingerprint)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setupUpstreamSourceBillingProbeServiceTest(t)
			now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
			var status atomic.Int64
			status.Store(http.StatusOK)
			var rate atomic.Int64
			rate.Store(8)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				code := int(status.Load())
				w.WriteHeader(code)
				if code != http.StatusOK {
					return
				}
				fields := validBillingProbeProtocolFields(now)
				multiplier := float64(rate.Load()) / 10
				fields["group_rate_multiplier"] = multiplier
				fields["resolved_rate_multiplier"] = multiplier
				fields["effective_rate_multiplier"] = multiplier
				_, _ = w.Write(billingProbeProtocolBody(t, fields))
			}))
			defer server.Close()
			fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)
			svc := newUpstreamSourceBillingProbeTestService(now)

			first, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
			require.NoError(t, err)
			require.NotNil(t, first)
			require.True(t, first.Fresh)
			tt.run(t, fixture, svc, &status, &rate, now)
		})
	}
}

func TestStoreClaimIdentityAcceptsMatchingLeaseWhenDriverReportsNoChangedRows(t *testing.T) {
	setupUpstreamSourceBillingProbeServiceTest(t)
	now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	fixture := createUpstreamSourceBillingProbeFixture(t, "https://relay.example", true)
	claimed, err := model.ClaimUpstreamSourceBillingProbe(fixture.mapping.Id, "matching-lease", now.Unix(), 60, false)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	identity, identityErr := loadUpstreamSourceBillingProbeIdentity(context.Background(), model.DB, fixture.source.Id, fixture.mapping.Id)
	require.Nil(t, identityErr)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceBillingProbe{}).
		Where("mapping_id = ?", fixture.mapping.Id).
		Updates(map[string]any{"claim_identity_fingerprint": identity.Fingerprint, "updated_time": now.Unix()}).Error)

	const callbackName = "test:billing_probe_zero_changed_rows"
	require.NoError(t, model.DB.Callback().Update().After("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "upstream_source_billing_probes" {
			tx.RowsAffected = 0
		}
	}))
	t.Cleanup(func() { _ = model.DB.Callback().Update().Remove(callbackName) })

	stored, err := newUpstreamSourceBillingProbeTestService(now).
		storeClaimIdentity(context.Background(), claimed, "matching-lease", identity.Fingerprint, now.Unix())
	require.NoError(t, err)
	assert.True(t, stored)
}

func TestUpstreamSourceBillingProbeGetMappingRejectsReboundMappingOwnership(t *testing.T) {
	setupUpstreamSourceBillingProbeServiceTest(t)
	now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	fixture := createUpstreamSourceBillingProbeFixture(t, "https://upstream.example", false)
	other := model.UpstreamSource{
		Name:         "other-owner",
		Type:         model.UpstreamSourceTypeSub2API,
		Status:       model.UpstreamSourceStatusEnabled,
		BaseURL:      "https://other.example",
		RelayBaseURL: "https://other.example",
	}
	require.NoError(t, model.DB.Create(&other).Error)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).
		Where("id = ?", fixture.mapping.Id).
		Update("source_id", other.Id).Error)

	response, err := NewUpstreamSourceBillingProbeService().GetMapping(
		context.Background(),
		fixture.source.Id,
		fixture.mapping.Id,
		now,
	)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.Nil(t, response)
}

func TestUpstreamSourceBillingProbeFreshRevalidatesCurrentIdentityAndEligibility(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, fixture upstreamSourceBillingProbeFixture, serverURL string)
	}{
		{name: "bearer key rotation", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).
				Update("key", "sk-rotated-secret").Error)
		}},
		{name: "source disabled", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", fixture.source.Id).
				Update("status", model.UpstreamSourceStatusDisabled).Error)
		}},
		{name: "source deleted", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", fixture.source.Id).
				Update("status", model.UpstreamSourceStatusDeleted).Error)
		}},
		{name: "channel disabled", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).
				Update("status", common.ChannelStatusManuallyDisabled).Error)
		}},
		{name: "mapping rebound", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", fixture.mapping.Id).
				Update("local_channel_id", 0).Error)
		}},
		{name: "generated ownership changed", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			settings := fixture.channel.GetOtherSettings()
			settings.GeneratedByUpstreamMappingID++
			fixture.channel.SetOtherSettings(settings)
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).
				Update("settings", fixture.channel.OtherSettings).Error)
		}},
		{name: "static headers changed", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			raw, err := common.Marshal(map[string]any{"X-Probe-Identity": "changed"})
			require.NoError(t, err)
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).
				Update("header_override", string(raw)).Error)
		}},
		{name: "effective base changed", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, serverURL string) {
			changed := serverURL + "/changed"
			require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", fixture.source.Id).
				Update("relay_base_url", changed).Error)
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).
				Update("base_url", changed).Error)
		}},
		{name: "proxy changed", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			fixture.channel.SetSetting(relaydto.ChannelSettings{Proxy: "http://proxy.example:8080"})
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).
				Update("setting", fixture.channel.Setting).Error)
		}},
		{name: "source network config changed", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", fixture.source.Id).
				Update("sync_config", `{"allow_private_ip":false}`).Error)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupUpstreamSourceBillingProbeServiceTest(t)
			now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(upstreamSourceBillingProbeSuccessBody(t, now))
			}))
			defer server.Close()
			fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)
			svc := newUpstreamSourceBillingProbeTestService(now)

			fresh, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
			require.NoError(t, err)
			require.True(t, fresh.Fresh)
			tt.mutate(t, fixture, server.URL)

			current, err := svc.GetMapping(context.Background(), fixture.source.Id, fixture.mapping.Id, now.Add(time.Minute))
			require.NoError(t, err)
			require.NotNil(t, current)
			assert.True(t, current.HasLastGood)
			assert.False(t, current.Fresh)
			assert.True(t, current.Stale)
			encoded, err := common.Marshal(current)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), fixture.channel.Key)
			assert.NotContains(t, string(encoded), "fingerprint")
		})
	}
}

func TestUpstreamSourceBillingProbeDefaultOptOutAndOwnershipMismatchMakeNoRequest(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, fixture upstreamSourceBillingProbeFixture)
	}{
		{name: "default opt out", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture) {
			require.NoError(t, model.DB.Delete(&model.UpstreamSourceBillingProbe{}, "mapping_id = ?", fixture.mapping.Id).Error)
		}},
		{name: "owner source mismatch", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture) {
			settings := fixture.channel.GetOtherSettings()
			settings.GeneratedByUpstreamSourceID++
			fixture.channel.SetOtherSettings(settings)
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).Update("settings", fixture.channel.OtherSettings).Error)
		}},
		{name: "owner mapping mismatch", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture) {
			settings := fixture.channel.GetOtherSettings()
			settings.GeneratedByUpstreamMappingID++
			fixture.channel.SetOtherSettings(settings)
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).Update("settings", fixture.channel.OtherSettings).Error)
		}},
		{name: "multiple bearer keys", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture) {
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).Update("key", "key-a\nkey-b").Error)
		}},
		{name: "unsupported channel type", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture) {
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).
				Update("type", constant.ChannelTypeUnknown).Error)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupUpstreamSourceBillingProbeServiceTest(t)
			now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				_, _ = w.Write(upstreamSourceBillingProbeSuccessBody(t, now))
			}))
			defer server.Close()
			fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)
			tc.mutate(t, fixture)

			response, err := newUpstreamSourceBillingProbeTestService(now).ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
			if tc.name == "default opt out" {
				require.ErrorIs(t, err, ErrUpstreamSourceBillingProbeNotEnabled)
			} else {
				require.ErrorIs(t, err, ErrUpstreamSourceBillingProbeLeaseUnavailable)
			}
			assert.Nil(t, response)
			assert.Zero(t, calls.Load())
		})
	}
}

func TestUpdateUpstreamSourceBillingProbeRejectsMismatchedProbeOwner(t *testing.T) {
	setupUpstreamSourceBillingProbeServiceTest(t)
	now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	fixture := createUpstreamSourceBillingProbeFixture(t, "https://relay.example", false)
	otherSource := model.UpstreamSource{
		Name:   "other-probe-source",
		Type:   model.UpstreamSourceTypeSub2API,
		Status: model.UpstreamSourceStatusEnabled,
	}
	require.NoError(t, model.DB.Create(&otherSource).Error)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceBillingProbe{}).
		Where("mapping_id = ?", fixture.mapping.Id).
		Update("source_id", otherSource.Id).Error)

	response, err := UpdateUpstreamSourceBillingProbe(
		context.Background(),
		fixture.source.Id,
		fixture.mapping.Id,
		false,
		10,
		model.UpstreamSourceAutoPriorityCostSourceAdvertised,
		now,
	)
	require.Error(t, err)
	assert.Nil(t, response)

	var stored model.UpstreamSourceBillingProbe
	require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&stored).Error)
	assert.Equal(t, otherSource.Id, stored.SourceID)
	assert.True(t, stored.Enabled)
	assert.Equal(t, 5, stored.IntervalMinutes)
}

func TestUpstreamSourceBillingProbeRejectsClaimAfterSourceOwnershipRebind(t *testing.T) {
	setupUpstreamSourceBillingProbeServiceTest(t)
	now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write(upstreamSourceBillingProbeSuccessBody(t, now))
	}))
	defer server.Close()
	fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)
	staleCandidate := fixture.probe
	otherSource := model.UpstreamSource{
		Name:         "rebound-probe-source",
		Type:         model.UpstreamSourceTypeSub2API,
		Status:       model.UpstreamSourceStatusEnabled,
		BaseURL:      "https://management.invalid",
		RelayBaseURL: server.URL,
		SyncConfig:   fixture.source.SyncConfig,
	}
	require.NoError(t, model.DB.Create(&otherSource).Error)
	settings := fixture.channel.GetOtherSettings()
	settings.GeneratedByUpstreamSourceID = otherSource.Id
	fixture.channel.SetOtherSettings(settings)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).
		Update("settings", fixture.channel.OtherSettings).Error)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", fixture.mapping.Id).
		Update("source_id", otherSource.Id).Error)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceBillingProbe{}).Where("mapping_id = ?", fixture.mapping.Id).
		Update("source_id", otherSource.Id).Error)

	response, err := newUpstreamSourceBillingProbeTestService(now).
		probeMapping(context.Background(), staleCandidate, false)
	require.ErrorIs(t, err, ErrUpstreamSourceBillingProbeIdentityChanged)
	assert.Nil(t, response)
	assert.Zero(t, calls.Load())
	var stored model.UpstreamSourceBillingProbe
	require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&stored).Error)
	assert.Equal(t, otherSource.Id, stored.SourceID)
	assert.Empty(t, stored.LeaseToken)
	assert.Zero(t, stored.LeaseStartedAt)
}

func TestUpstreamSourceBillingProbeErrorsAreSecretRedacted(t *testing.T) {
	setupUpstreamSourceBillingProbeServiceTest(t)
	now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	secretQuery := "query-secret-value"
	fixture := createUpstreamSourceBillingProbeFixture(t, "http://127.0.0.1:1/v1?token="+secretQuery, true)
	svc := newUpstreamSourceBillingProbeTestService(now)

	response, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
	require.NoError(t, err)
	require.NotNil(t, response)
	encoded, err := common.Marshal(response)
	require.NoError(t, err)
	for _, secret := range []string{fixture.channel.Key, secretQuery, "management-secret", "Authorization", fixture.source.AuthConfig} {
		assert.NotContains(t, string(encoded), secret)
		assert.NotContains(t, response.ErrorCode, secret)
	}
	assert.True(t, errors.Is(ErrUpstreamSourceBillingProbeIdentityChanged, ErrUpstreamSourceBillingProbeIdentityChanged))
}

func TestUpstreamSourceBillingProbeConcurrentManualCallsShareDatabaseLease(t *testing.T) {
	setupUpstreamSourceBillingProbeServiceTest(t)
	now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	entered := make(chan struct{})
	unblock := make(chan struct{})
	var once sync.Once
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		once.Do(func() { close(entered) })
		<-unblock
		_, _ = w.Write(upstreamSourceBillingProbeSuccessBody(t, now))
	}))
	defer server.Close()
	fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)
	svc := newUpstreamSourceBillingProbeTestService(now)

	results := make(chan error, 2)
	go func() {
		_, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
		results <- err
	}()
	<-entered
	go func() {
		_, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
		results <- err
	}()

	secondErr := <-results
	require.ErrorIs(t, secondErr, ErrUpstreamSourceBillingProbeLeaseUnavailable)
	close(unblock)
	require.NoError(t, <-results)
	assert.Equal(t, int64(1), calls.Load())
}
