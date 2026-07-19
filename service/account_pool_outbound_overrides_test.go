package service

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeAccountPoolOutboundOverridesTrustedHostsByPlatform(t *testing.T) {
	tests := []struct {
		name       string
		platform   string
		credential AccountPoolCredentialConfig
		baseURL    string
	}{
		{name: "openai", platform: model.AccountPoolPlatformOpenAI, baseURL: "https://api.openai.com/v1/"},
		{name: "anthropic", platform: model.AccountPoolPlatformAnthropic, baseURL: "https://api.anthropic.com/"},
		{name: "gemini", platform: model.AccountPoolPlatformGemini, baseURL: "https://generativelanguage.googleapis.com/"},
		{
			name:     "vertex",
			platform: model.AccountPoolPlatformGemini,
			credential: AccountPoolCredentialConfig{
				Type:               AccountPoolCredentialTypeServiceAccount,
				ServiceAccountJSON: `{}`,
			},
			baseURL: "https://us-central1-aiplatform.googleapis.com/",
		},
		{name: "xai", platform: model.AccountPoolPlatformXAI, baseURL: "https://api.x.ai/v1/"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseURL := test.baseURL
			test.credential.BaseURL = &baseURL
			got, err := normalizeAccountPoolOutboundOverrides(
				context.Background(),
				test.platform,
				test.credential,
				func(context.Context, string) ([]net.IP, error) {
					return nil, errors.New("trusted hosts must not require DNS")
				},
			)
			require.NoError(t, err)
			require.NotNil(t, got.BaseURL)
			assert.Equal(t, test.baseURL[:len(test.baseURL)-1], *got.BaseURL)
		})
	}
}

func TestNormalizeAccountPoolOutboundOverridesRejectsUnsafeInputsForEveryPlatform(t *testing.T) {
	platforms := []string{
		model.AccountPoolPlatformOpenAI,
		model.AccountPoolPlatformAnthropic,
		model.AccountPoolPlatformGemini,
		model.AccountPoolPlatformXAI,
	}
	for _, platform := range platforms {
		t.Run(platform, func(t *testing.T) {
			privateURL := "https://127.0.0.1/v1"
			_, err := normalizeAccountPoolOutboundOverrides(context.Background(), platform, AccountPoolCredentialConfig{
				BaseURL: &privateURL,
			}, nil)
			require.Error(t, err)

			httpURL := "http://gateway.example.com/v1"
			_, err = normalizeAccountPoolOutboundOverrides(context.Background(), platform, AccountPoolCredentialConfig{
				BaseURL: &httpURL,
			}, nil)
			require.Error(t, err)

			_, err = normalizeAccountPoolOutboundOverrides(context.Background(), platform, AccountPoolCredentialConfig{
				HeaderOverrides: map[string]string{"Authorization": "secret"},
			}, nil)
			require.Error(t, err)
		})
	}

	baseURL := "https://grok.com"
	_, err := normalizeAccountPoolOutboundOverrides(context.Background(), model.AccountPoolPlatformGrokWeb, AccountPoolCredentialConfig{
		BaseURL: &baseURL,
	}, nil)
	require.ErrorContains(t, err, "not supported for grok_web")
}

func TestAccountPoolRuntimeOutboundOverridesAccountWinsForEverySupportedPlatform(t *testing.T) {
	tests := []struct {
		platform string
		baseURL  string
	}{
		{platform: model.AccountPoolPlatformOpenAI, baseURL: "https://api.openai.com/account"},
		{platform: model.AccountPoolPlatformAnthropic, baseURL: "https://api.anthropic.com/account"},
		{platform: model.AccountPoolPlatformGemini, baseURL: "https://generativelanguage.googleapis.com/account"},
		{platform: model.AccountPoolPlatformXAI, baseURL: "https://api.x.ai/account"},
	}

	for _, test := range tests {
		t.Run(test.platform, func(t *testing.T) {
			setupAccountPoolServiceTestDB(t)
			svc := AccountPoolService{}
			pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, test.platform)
			channel := createAccountPoolServiceTestChannel(t, 1)
			createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
			enabled := true
			createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
				Name: "overridden",
				Credential: AccountPoolCredentialConfig{
					Type:                  AccountPoolCredentialTypeAPIKey,
					APIKey:                "account-key",
					BaseURL:               &test.baseURL,
					HeaderOverrideEnabled: &enabled,
					HeaderOverrides: map[string]string{
						"X-Shared":  "account",
						"X-Account": "present",
					},
				},
			})

			ctx := newAccountPoolRuntimeTestContext()
			info := newAccountPoolRuntimeTestRelayInfo(channel.Id, "model", "model")
			info.ChannelMeta.HeadersOverride = map[string]interface{}{
				"X-Shared":  "channel",
				"X-Channel": "preserved",
			}
			require.NoError(t, ApplyAccountPoolRuntimeSelection(ctx, info, &dto.GeneralOpenAIRequest{Model: "model"}))
			defer ReleaseAccountPoolRuntimeSelection(ctx)

			assert.Equal(t, test.baseURL, info.RuntimeBaseURL)
			assert.Equal(t, "account", info.RuntimeHeadersOverride["x-shared"])
			assert.Equal(t, "present", info.RuntimeHeadersOverride["x-account"])
			assert.Equal(t, "preserved", info.RuntimeHeadersOverride["x-channel"])
		})
	}
}
