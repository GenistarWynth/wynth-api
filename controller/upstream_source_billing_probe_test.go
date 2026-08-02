package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaydto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createUpstreamSourceBillingProbeAPIFixture(t *testing.T, baseURL string) (model.UpstreamSource, model.UpstreamSourceChannelMapping, model.Channel) {
	t.Helper()
	source := createUpstreamSourceAPITestSource(t, `{"access_token":"management-session-secret","password":"admin-password"}`)
	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Updates(map[string]any{
		"relay_base_url": baseURL,
		"sync_config":    `{"allow_private_ip":true}`,
	}).Error)
	source.RelayBaseURL = baseURL
	source.SyncConfig = `{"allow_private_ip":true}`
	advertisedRate := 0.42
	mapping := model.UpstreamSourceChannelMapping{
		SourceID:                source.Id,
		SyncEnabled:             true,
		UpstreamGroupID:         "billing-api-group",
		DiscoveryStatus:         model.UpstreamMappingDiscoveryStatusActive,
		UpstreamKeyID:           "upstream-key-id",
		EffectiveRateMultiplier: &advertisedRate,
	}
	require.NoError(t, model.DB.Create(&mapping).Error)
	channel := model.Channel{
		Type:    constant.ChannelTypeOpenAI,
		Key:     "sk-api-bearer-secret",
		Status:  common.ChannelStatusEnabled,
		Name:    "billing-api-channel",
		BaseURL: common.GetPointer(baseURL),
		Models:  "gpt-4o-mini",
		Group:   "default",
	}
	channel.SetOtherSettings(relaydto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:  source.Id,
		GeneratedByUpstreamMappingID: mapping.Id,
	})
	require.NoError(t, model.DB.Create(&channel).Error)
	require.NoError(t, model.DB.Model(&mapping).Update("local_channel_id", channel.Id).Error)
	mapping.LocalChannelID = channel.Id
	return source, mapping, channel
}

func TestUpstreamSourceAPIBillingProbeAuthorizationValidationManualRunAndRedaction(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	fetchSetting := system_setting.GetFetchSetting()
	previousFetchSetting := *fetchSetting
	fetchSetting.EnableSSRFProtection = true
	fetchSetting.AllowPrivateIp = false
	fetchSetting.DomainFilterMode = false
	fetchSetting.IpFilterMode = false
	fetchSetting.DomainList = nil
	fetchSetting.IpList = nil
	fetchSetting.AllowedPorts = nil
	fetchSetting.ApplyIPFilterForDomain = true
	t.Cleanup(func() { *fetchSetting = previousFetchSetting })
	router := upstreamSourceAPIRouter(true)
	var calls atomic.Int64
	var failResponse atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "Bearer sk-api-bearer-secret", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Accept"))
		if failResponse.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"upstream transport detail"}`))
			return
		}
		body, err := common.Marshal(map[string]any{
			"object":                    "sub2api.key_billing",
			"schema_version":            1,
			"billing_scope":             "token",
			"group_rate_multiplier":     0.8,
			"resolved_rate_multiplier":  0.8,
			"peak_rate_enabled":         false,
			"effective_rate_multiplier": 0.8,
			"observed_at":               time.Now().UTC().Format(time.RFC3339Nano),
		})
		require.NoError(t, err)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	querySecret := "tenant-secret"
	baseURL := server.URL + "/v1?tenant=" + querySecret + "#fragment-secret"
	source, mapping, channel := createUpstreamSourceBillingProbeAPIFixture(t, baseURL)
	endpoint := "/api/upstream_sources/" + strconv.Itoa(source.Id) + "/mappings/" + strconv.Itoa(mapping.Id) + "/billing_probe"

	unauthorizedRecorder := httptest.NewRecorder()
	unauthorizedRequest := httptest.NewRequest(http.MethodPut, endpoint, bytes.NewBufferString(`{"enabled":true,"interval_minutes":5}`))
	unauthorizedRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(unauthorizedRecorder, unauthorizedRequest)
	assert.Equal(t, http.StatusUnauthorized, unauthorizedRecorder.Code)
	assert.Zero(t, calls.Load())

	defaults := upstreamSourceAPIRequest[dto.UpstreamSourceBillingProbeResponse](t, router, http.MethodGet, endpoint, nil, true)
	require.True(t, defaults.Success, defaults.Message)
	assert.False(t, defaults.Data.Enabled)
	assert.Equal(t, model.DefaultUpstreamSourceBillingProbeIntervalMinutes, defaults.Data.IntervalMinutes)
	assert.Equal(t, model.UpstreamSourceBillingProbeStatusIdle, defaults.Data.Status)
	assert.Equal(t, "missing", defaults.Data.EmpiricalStatus)
	assert.Equal(t, 0.42, *defaults.Data.AdvertisedEffectiveRateMultiplier)

	invalid := upstreamSourceAPIRequest[dto.UpstreamSourceBillingProbeResponse](t, router, http.MethodPut, endpoint, map[string]any{
		"enabled":          true,
		"interval_minutes": model.MinUpstreamSourceBillingProbeIntervalMinutes - 1,
	}, true)
	assert.False(t, invalid.Success)
	var count int64
	require.NoError(t, model.DB.Model(&model.UpstreamSourceBillingProbe{}).Count(&count).Error)
	assert.Zero(t, count)

	configured := upstreamSourceAPIRequest[dto.UpstreamSourceBillingProbeResponse](t, router, http.MethodPut, endpoint, map[string]any{
		"enabled":          true,
		"interval_minutes": 5,
	}, true)
	require.True(t, configured.Success, configured.Message)
	assert.True(t, configured.Data.Enabled)
	assert.Equal(t, 5, configured.Data.IntervalMinutes)
	assert.Equal(t, model.UpstreamSourceBillingProbeStatusIdle, configured.Data.Status)
	assert.Equal(t, "missing", configured.Data.EmpiricalStatus)
	assert.Equal(t, 0.42, *configured.Data.AdvertisedEffectiveRateMultiplier)
	assert.Zero(t, calls.Load(), "configuration must not silently run a probe")

	run := upstreamSourceAPIRequest[dto.UpstreamSourceBillingProbeResponse](t, router, http.MethodPost, endpoint+"/run", nil, true)
	require.True(t, run.Success, run.Message)
	assert.Equal(t, model.UpstreamSourceBillingProbeStatusOK, run.Data.Status, "probe error code: %s", run.Data.ErrorCode)
	assert.True(t, run.Data.Fresh)
	assert.True(t, run.Data.HasLastGood)
	assert.Equal(t, "ready", run.Data.EmpiricalStatus)
	assert.Greater(t, run.Data.LastAttemptAt, int64(0))
	require.NotNil(t, run.Data.EmpiricalNominalRateMultiplier)
	assert.Equal(t, 0.8, *run.Data.EmpiricalNominalRateMultiplier)
	require.NotNil(t, run.Data.EffectiveRateMultiplier)
	assert.Equal(t, 0.8, *run.Data.EffectiveRateMultiplier)
	assert.Equal(t, 0.42, *run.Data.AdvertisedEffectiveRateMultiplier)
	assert.Equal(t, int64(1), calls.Load())

	read := upstreamSourceAPIRequest[dto.UpstreamSourceBillingProbeResponse](t, router, http.MethodGet, endpoint, nil, true)
	require.True(t, read.Success, read.Message)
	assert.Equal(t, run.Data.Status, read.Data.Status)
	assert.Equal(t, run.Data.ReceivedAt, read.Data.ReceivedAt)
	assert.Equal(t, run.Data.FreshUntil, read.Data.FreshUntil)

	failResponse.Store(true)
	failed := upstreamSourceAPIRequest[dto.UpstreamSourceBillingProbeResponse](t, router, http.MethodPost, endpoint+"/run", nil, true)
	require.True(t, failed.Success, failed.Message)
	assert.Equal(t, model.UpstreamSourceBillingProbeStatusFailed, failed.Data.Status)
	assert.Equal(t, "temporary_failed", failed.Data.EmpiricalStatus)
	failedRaw, err := common.Marshal(failed)
	require.NoError(t, err)
	assert.NotContains(t, string(failedRaw), "error_code", "transport failure detail must stay server-side")

	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", channel.Id).Update("key", "sk-rotated-api-bearer").Error)
	mismatched := upstreamSourceAPIRequest[dto.UpstreamSourceBillingProbeResponse](t, router, http.MethodGet, endpoint, nil, true)
	require.True(t, mismatched.Success, mismatched.Message)
	assert.Equal(t, "identity_mismatch", mismatched.Data.EmpiricalStatus)
	assert.False(t, mismatched.Data.Fresh)
	assert.Nil(t, mismatched.Data.EmpiricalNominalRateMultiplier)

	raw, err := common.Marshal(mismatched)
	require.NoError(t, err)
	for _, secret := range []string{
		channel.Key,
		"sk-rotated-api-bearer",
		querySecret,
		"fragment-secret",
		"management-session-secret",
		"admin-password",
		"identity_fingerprint",
		"auth_revision",
		"authorization",
	} {
		assert.NotContains(t, string(raw), secret)
	}

	var stored model.UpstreamSourceBillingProbe
	require.NoError(t, model.DB.Where("mapping_id = ?", mapping.Id).First(&stored).Error)
	assert.NotEmpty(t, stored.LastGoodIdentityFingerprint)
	assert.Empty(t, stored.ClaimIdentityFingerprint)
}

func TestUpstreamSourceAPIBillingProbeManualRunDoesNotBypassOwnershipEligibility(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	source, mapping, channel := createUpstreamSourceBillingProbeAPIFixture(t, server.URL)
	endpoint := "/api/upstream_sources/" + strconv.Itoa(source.Id) + "/mappings/" + strconv.Itoa(mapping.Id) + "/billing_probe"
	configured := upstreamSourceAPIRequest[dto.UpstreamSourceBillingProbeResponse](t, router, http.MethodPut, endpoint, map[string]any{
		"enabled":          true,
		"interval_minutes": 5,
	}, true)
	require.True(t, configured.Success, configured.Message)
	settings := channel.GetOtherSettings()
	settings.GeneratedByUpstreamMappingID++
	channel.SetOtherSettings(settings)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("settings", channel.OtherSettings).Error)

	run := upstreamSourceAPIRequest[dto.UpstreamSourceBillingProbeResponse](t, router, http.MethodPost, endpoint+"/run", nil, true)
	assert.False(t, run.Success)
	assert.Contains(t, run.Message, "lease is unavailable")
	assert.Zero(t, calls.Load())
}

func TestUpstreamSourceAPIBillingProbeCostSourceDefaultsAdvertisedAndRoundTripsExplicitEmpirical(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	source, mapping, _ := createUpstreamSourceBillingProbeAPIFixture(t, "https://relay.example")
	endpoint := "/api/upstream_sources/" + strconv.Itoa(source.Id) + "/mappings/" + strconv.Itoa(mapping.Id) + "/billing_probe"

	defaults := upstreamSourceAPIRequest[dto.UpstreamSourceBillingProbeResponse](t, router, http.MethodGet, endpoint, nil, true)
	require.True(t, defaults.Success, defaults.Message)
	assert.Equal(t, model.UpstreamSourceAutoPriorityCostSourceAdvertised, defaults.Data.AutoPriorityCostSource)
	assert.Equal(t, "missing", defaults.Data.EmpiricalStatus)

	mappings := upstreamSourceAPIRequest[[]dto.UpstreamSourceMappingResponse](
		t,
		router,
		http.MethodGet,
		"/api/upstream_sources/"+strconv.Itoa(source.Id)+"/mappings",
		nil,
		true,
	)
	require.True(t, mappings.Success, mappings.Message)
	require.Len(t, mappings.Data, 1)
	assert.Equal(t, model.UpstreamSourceAutoPriorityCostSourceAdvertised, mappings.Data[0].AutoPriorityCostSource)

	configured := upstreamSourceAPIRequest[dto.UpstreamSourceBillingProbeResponse](t, router, http.MethodPut, endpoint, map[string]any{
		"enabled":                   true,
		"interval_minutes":          5,
		"auto_priority_cost_source": model.UpstreamSourceAutoPriorityCostSourceEmpiricalProbe,
	}, true)
	require.True(t, configured.Success, configured.Message)
	assert.Equal(t, model.UpstreamSourceAutoPriorityCostSourceEmpiricalProbe, configured.Data.AutoPriorityCostSource)
	assert.Equal(t, "missing", configured.Data.EmpiricalStatus)

	var reloaded model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.First(&reloaded, mapping.Id).Error)
	assert.Equal(t, model.UpstreamSourceAutoPriorityCostSourceEmpiricalProbe, reloaded.AutoPriorityCostSource)

	invalid := upstreamSourceAPIRequest[dto.UpstreamSourceBillingProbeResponse](t, router, http.MethodPut, endpoint, map[string]any{
		"enabled":                   true,
		"interval_minutes":          5,
		"auto_priority_cost_source": "passive_monitoring",
	}, true)
	assert.False(t, invalid.Success)
	require.NoError(t, model.DB.First(&reloaded, mapping.Id).Error)
	assert.Equal(t, model.UpstreamSourceAutoPriorityCostSourceEmpiricalProbe, reloaded.AutoPriorityCostSource)
}
