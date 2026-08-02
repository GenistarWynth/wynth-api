package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaydto "github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSafeUpstreamSourceBillingProbeHeaderRejectsClientIdentityAndLiteralControls(t *testing.T) {
	for _, name := range []string{
		"User-Agent",
		"user-agent",
		"user_agent",
		"Forwarded",
		"X-Forwarded-For",
		"x_forwarded_for",
		"X-Real-IP",
		"x_real_ip",
		"True-Client-IP",
		"true_client_ip",
	} {
		t.Run(name, func(t *testing.T) {
			assert.False(t, safeUpstreamSourceBillingProbeHeader(name, "operator-value"))
		})
	}

	for _, value := range []string{
		"literal\x00control",
		"literal\tcontrol",
		"literal\rcontrol",
		"literal\ncontrol",
		"literal\x7fcontrol",
		"literal\u0085control",
	} {
		t.Run(fmt.Sprintf("control-%x", []byte(value)), func(t *testing.T) {
			assert.False(t, safeUpstreamSourceBillingProbeHeader("X-Operator", value))
		})
	}
}

func TestSafeUpstreamSourceBillingProbeHeaderRejectsHopByHopSeparatorVariants(t *testing.T) {
	for _, name := range []string{
		"Proxy_Connection",
		"Proxy_Authorization",
		"Content_Length",
		"Transfer_Encoding",
		"Accept_Encoding",
		"Keep_Alive",
	} {
		t.Run(name, func(t *testing.T) {
			assert.False(t, safeUpstreamSourceBillingProbeHeader(name, "operator-value"))
		})
	}
}

func TestApplyUpstreamSourceBillingProbeHeaderOverridesRejectsClientHeaderSeparatorVariants(t *testing.T) {
	for _, value := range []string{
		"{client-header:X-Trace}",
		"{clientheader:X-Trace}",
		"{client:header:X-Trace}",
		"{CLIENTHEADER:X-Trace}",
		"{client::header:X-Trace}",
		"{client.header:X-Trace}",
		"{client header:X-Trace}",
		"{CLIENT-HEADER:X-Trace}",
		"prefix {ClIeNt._ HeAdEr:X-Trace} suffix",
		"prefix {client:._ header:X-Trace} suffix",
		"{clientxheader:X-Ignore} then {clientheader:X-Trace}",
		"{client--header:X-Trace}",
	} {
		t.Run(value, func(t *testing.T) {
			header := http.Header{}
			raw, err := common.Marshal(map[string]string{"X-Operator": value})
			require.NoError(t, err)

			err = applyUpstreamSourceBillingProbeHeaderOverrides(header, string(raw), "sk-test-key")
			require.Error(t, err)
			assert.Empty(t, header)
		})
	}
}

func TestApplyUpstreamSourceBillingProbeHeaderOverridesAllowsNonControlOperatorHeaders(t *testing.T) {
	raw, err := common.Marshal(map[string]string{
		"X.Operator":       "prefix client-header:X-Trace suffix",
		"X-Static-Probe":   "present-{api_key}",
		"X-Braced-Text":    "prefix {client-header} suffix",
		"X-Closed-Text":    "{client}header:X-Trace",
		"X-No-Braces":      "clientheader:X-Trace",
		"X-No-Colon":       "{clientheader}",
		"X-Different-Word": "{clientxheader:X-Trace}",
		"X-Longer-Word":    "{clientheaderless:X-Trace}",
		"X-Nested-Open":    "{client{header:X-Trace}",
	})
	require.NoError(t, err)
	header := http.Header{}

	require.NoError(t, applyUpstreamSourceBillingProbeHeaderOverrides(header, string(raw), "sk-test-key"))
	assert.Equal(t, "prefix client-header:X-Trace suffix", header.Get("X.Operator"))
	assert.Equal(t, "present-sk-test-key", header.Get("X-Static-Probe"))
	assert.Equal(t, "prefix {client-header} suffix", header.Get("X-Braced-Text"))
	assert.Equal(t, "{client}header:X-Trace", header.Get("X-Closed-Text"))
	assert.Equal(t, "clientheader:X-Trace", header.Get("X-No-Braces"))
	assert.Equal(t, "{clientheader}", header.Get("X-No-Colon"))
	assert.Equal(t, "{clientxheader:X-Trace}", header.Get("X-Different-Word"))
	assert.Equal(t, "{clientheaderless:X-Trace}", header.Get("X-Longer-Word"))
	assert.Equal(t, "{client{header:X-Trace}", header.Get("X-Nested-Open"))
}

func TestApplyUpstreamSourceBillingProbeHeaderOverridesDoesNotTreatAPIKeyContentAsControl(t *testing.T) {
	raw, err := common.Marshal(map[string]string{"X-Upstream-Key": "prefix-{api_key}"})
	require.NoError(t, err)
	header := http.Header{}

	require.NoError(t, applyUpstreamSourceBillingProbeHeaderOverrides(
		header,
		string(raw),
		"key-{client-header:not-an-operator-template}",
	))
	assert.Equal(t, "prefix-key-{client-header:not-an-operator-template}", header.Get("X-Upstream-Key"))
}

func TestSafeUpstreamSourceBillingProbeHeaderRejectsDotAndMixedDenylistSeparators(t *testing.T) {
	for _, name := range []string{
		"Proxy.Connection",
		"Proxy.Authorization",
		"Content.Length",
		"Transfer.Encoding",
		"Accept.Encoding",
		"Keep.Alive",
		"User.Agent",
		"X.Forwarded.For",
		"X.Real.IP",
		"True.Client.IP",
		"pRoXy._-Connection",
		"X.-_Forwarded..For",
		"Proxy!#$%&'*+-.^_`|~Connection",
	} {
		t.Run(name, func(t *testing.T) {
			assert.False(t, safeUpstreamSourceBillingProbeHeader(name, "operator-value"))
		})
	}

	for _, name := range []string{
		"X.Operator",
		"X.Mixed_Operator-Header",
		"X!Operator.Trace",
	} {
		t.Run("allowed/"+name, func(t *testing.T) {
			assert.True(t, safeUpstreamSourceBillingProbeHeader(name, "operator-value"))
		})
	}
}

func TestSafeUpstreamSourceBillingProbeHeaderRejectsNonASCIINameBeforeCaseFolding(t *testing.T) {
	assert.False(t, safeUpstreamSourceBillingProbeHeader("X-\u212Aey", "operator-value"))
}

func TestUpstreamSourceBillingProbeDiscardsEveryMaterialIdentityChangeInFlight(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, fixture upstreamSourceBillingProbeFixture, serverURL string)
	}{
		{name: "bearer key", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).Update("key", "sk-rotated-secret").Error)
		}},
		{name: "channel base URL", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, serverURL string) {
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).Update("base_url", serverURL+"/changed").Error)
		}},
		{name: "channel proxy", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			fixture.channel.SetSetting(relaydto.ChannelSettings{Proxy: "http://127.0.0.1:3128"})
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).Update("setting", fixture.channel.Setting).Error)
		}},
		{name: "channel header overrides", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			raw, err := common.Marshal(map[string]any{"X-Rotated": "yes"})
			require.NoError(t, err)
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).Update("header_override", string(raw)).Error)
		}},
		{name: "mapping upstream key identity", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", fixture.mapping.Id).Update("upstream_key_id", "rotated-upstream-key-id").Error)
		}},
		{name: "mapping channel rebinding", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", fixture.mapping.Id).Update("local_channel_id", 0).Error)
		}},
		{name: "mapping disabled", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", fixture.mapping.Id).Update("sync_enabled", false).Error)
		}},
		{name: "mapping deleted", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			require.NoError(t, model.DB.Delete(&model.UpstreamSourceChannelMapping{}, fixture.mapping.Id).Error)
		}},
		{name: "channel disabled", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).Update("status", common.ChannelStatusManuallyDisabled).Error)
		}},
		{name: "generated source ownership", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			settings := fixture.channel.GetOtherSettings()
			settings.GeneratedByUpstreamSourceID++
			fixture.channel.SetOtherSettings(settings)
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).Update("settings", fixture.channel.OtherSettings).Error)
		}},
		{name: "generated mapping ownership", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			settings := fixture.channel.GetOtherSettings()
			settings.GeneratedByUpstreamMappingID++
			fixture.channel.SetOtherSettings(settings)
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).Update("settings", fixture.channel.OtherSettings).Error)
		}},
		{name: "source disabled", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", fixture.source.Id).Update("status", model.UpstreamSourceStatusDisabled).Error)
		}},
		{name: "source relay URL", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, serverURL string) {
			require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", fixture.source.Id).Update("relay_base_url", serverURL+"/rotated").Error)
		}},
		{name: "source network policy", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", fixture.source.Id).Update("sync_config", `{"allow_private_ip":false}`).Error)
		}},
		{name: "probe interval", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			require.NoError(t, model.DB.Model(&model.UpstreamSourceBillingProbe{}).Where("mapping_id = ?", fixture.mapping.Id).Update("interval_minutes", 10).Error)
		}},
		{name: "probe opt out", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture, _ string) {
			require.NoError(t, model.DB.Model(&model.UpstreamSourceBillingProbe{}).Where("mapping_id = ?", fixture.mapping.Id).Update("enabled", false).Error)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupUpstreamSourceBillingProbeServiceTest(t)
			now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
			entered := make(chan struct{})
			unblock := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				close(entered)
				<-unblock
				_, _ = w.Write(upstreamSourceBillingProbeSuccessBody(t, now))
			}))
			defer server.Close()
			fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)
			svc := newUpstreamSourceBillingProbeTestService(now)
			type result struct {
				response *dto.UpstreamSourceBillingProbeResponse
				err      error
			}
			resultCh := make(chan result, 1)
			go func() {
				response, err := svc.ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
				resultCh <- result{response: response, err: err}
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("probe request did not reach local fixture")
			}

			tt.mutate(t, fixture, server.URL)
			close(unblock)
			got := <-resultCh
			require.ErrorIs(t, got.err, ErrUpstreamSourceBillingProbeIdentityChanged)
			assert.Nil(t, got.response)
			assert.NotContains(t, got.err.Error(), fixture.channel.Key)

			var stored model.UpstreamSourceBillingProbe
			require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&stored).Error)
			assert.Empty(t, stored.LeaseToken)
			assert.Zero(t, stored.LeaseStartedAt)
			assert.Equal(t, model.UpstreamSourceBillingProbeStatusIdle, stored.Status)
			assert.Zero(t, stored.FailureCount)
			assert.Nil(t, stored.EffectiveRateMultiplier)
			if stored.Enabled {
				assert.LessOrEqual(t, stored.NextProbeAt, now.Unix())
			} else {
				assert.Zero(t, stored.NextProbeAt)
			}
		})
	}
}

func TestUpstreamSourceBillingProbeAcceptsInFlightScorerBookkeepingChanges(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*relaydto.ChannelOtherSettings)
	}{
		{name: "last run", mutate: func(settings *relaydto.ChannelOtherSettings) {
			settings.ChannelAutoPriorityLastRunAt = 123456789
		}},
		{name: "last applied", mutate: func(settings *relaydto.ChannelOtherSettings) {
			settings.ChannelAutoPriorityLastAppliedAt = 123456789
		}},
		{name: "last score", mutate: func(settings *relaydto.ChannelOtherSettings) {
			settings.ChannelAutoPriorityLastScore = &relaydto.ChannelAutoPriorityScore{
				Version:    "v1",
				ComputedAt: 123456789,
				FinalScore: 0.75,
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupUpstreamSourceBillingProbeServiceTest(t)
			now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
			entered := make(chan struct{})
			unblock := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				close(entered)
				<-unblock
				_, _ = w.Write(upstreamSourceBillingProbeSuccessBody(t, now))
			}))
			defer server.Close()
			fixture := createUpstreamSourceBillingProbeFixture(t, server.URL, true)

			type result struct {
				response *dto.UpstreamSourceBillingProbeResponse
				err      error
			}
			resultCh := make(chan result, 1)
			go func() {
				response, err := newUpstreamSourceBillingProbeTestService(now).
					ProbeMapping(context.Background(), fixture.source.Id, fixture.mapping.Id)
				resultCh <- result{response: response, err: err}
			}()

			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("probe request did not reach local fixture")
			}

			settings := fixture.channel.GetOtherSettings()
			tt.mutate(&settings)
			fixture.channel.SetOtherSettings(settings)
			require.NoError(t, model.DB.Model(&model.Channel{}).
				Where("id = ?", fixture.channel.Id).
				Update("settings", fixture.channel.OtherSettings).Error)
			close(unblock)

			select {
			case got := <-resultCh:
				require.NoError(t, got.err)
				require.NotNil(t, got.response)
				assert.Equal(t, model.UpstreamSourceBillingProbeStatusOK, got.response.Status)
			case <-time.After(time.Second):
				t.Fatal("probe request did not finish")
			}
		})
	}
}

func TestUpstreamSourceBillingProbeUnidentifiedCompletionDiscardsLifecycleChanges(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, fixture upstreamSourceBillingProbeFixture)
	}{
		{name: "source disabled", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture) {
			require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", fixture.source.Id).
				Update("status", model.UpstreamSourceStatusDisabled).Error)
		}},
		{name: "source deleted", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture) {
			require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", fixture.source.Id).
				Update("status", model.UpstreamSourceStatusDeleted).Error)
		}},
		{name: "mapping deleted", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture) {
			require.NoError(t, model.DB.Delete(&model.UpstreamSourceChannelMapping{}, fixture.mapping.Id).Error)
		}},
		{name: "mapping source rebound", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture) {
			other := model.UpstreamSource{
				Name: "other-source", Type: model.UpstreamSourceTypeSub2API,
				Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://other.example",
			}
			require.NoError(t, model.DB.Create(&other).Error)
			require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", fixture.mapping.Id).
				Update("source_id", other.Id).Error)
		}},
		{name: "mapping channel rebound", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture) {
			require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", fixture.mapping.Id).
				Update("local_channel_id", 0).Error)
		}},
		{name: "channel deleted", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture) {
			require.NoError(t, model.DB.Delete(&model.Channel{}, fixture.channel.Id).Error)
		}},
		{name: "channel disabled", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture) {
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).
				Update("status", common.ChannelStatusManuallyDisabled).Error)
		}},
		{name: "channel ownership rebound", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture) {
			settings := fixture.channel.GetOtherSettings()
			settings.GeneratedByUpstreamMappingID++
			fixture.channel.SetOtherSettings(settings)
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).
				Update("settings", fixture.channel.OtherSettings).Error)
		}},
		{name: "channel became multi-key", mutate: func(t *testing.T, fixture upstreamSourceBillingProbeFixture) {
			fixture.channel.ChannelInfo.IsMultiKey = true
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", fixture.channel.Id).
				Update("channel_info", fixture.channel.ChannelInfo).Error)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupUpstreamSourceBillingProbeServiceTest(t)
			now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
			fixture := createUpstreamSourceBillingProbeFixture(t, "https://relay.example", true)
			const token = "unidentified-lifecycle-lease"
			claimed, err := model.ClaimUpstreamSourceBillingProbe(fixture.mapping.Id, token, now.Unix(), 60, false)
			require.NoError(t, err)
			require.NotNil(t, claimed)
			tt.mutate(t, fixture)

			response, err := newUpstreamSourceBillingProbeTestService(now).completeUnidentifiedFailure(
				claimed,
				token,
				UpstreamSourceBillingProbeErrorIneligible,
				0,
				now,
			)
			require.ErrorIs(t, err, ErrUpstreamSourceBillingProbeIdentityChanged)
			assert.Nil(t, response)

			var stored model.UpstreamSourceBillingProbe
			require.NoError(t, model.DB.Where("mapping_id = ?", fixture.mapping.Id).First(&stored).Error)
			assert.Equal(t, model.UpstreamSourceBillingProbeStatusIdle, stored.Status)
			assert.Zero(t, stored.FailureCount)
			assert.Zero(t, stored.LastAttemptAt)
			assert.Empty(t, stored.LeaseToken)
			assert.Zero(t, stored.LeaseStartedAt)
		})
	}
}
