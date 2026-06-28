package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── platform normalize / validate ─────────────────────────────────────────────

func TestNormalizeAccountPoolPlatformGrokWeb(t *testing.T) {
	got, err := normalizeAccountPoolPlatform("grok_web")
	require.NoError(t, err)
	assert.Equal(t, model.AccountPoolPlatformGrokWeb, got)
}

func TestValidateAccountPoolRuntimeChannelAllowsGrokWeb(t *testing.T) {
	ch := model.Channel{Type: constant.ChannelTypeGrokWeb}
	require.NoError(t, validateAccountPoolRuntimeChannel(ch))
}

func TestValidateAccountPoolRuntimeChannelForPoolGrokWebMatrix(t *testing.T) {
	grokPool := model.AccountPool{Platform: model.AccountPoolPlatformGrokWeb}
	openaiPool := model.AccountPool{Platform: model.AccountPoolPlatformOpenAI}

	// grok_web pool ↔ GrokWeb channel: accepted.
	require.NoError(t, validateAccountPoolRuntimeChannelForPool(
		grokPool, model.Channel{Type: constant.ChannelTypeGrokWeb}))

	// grok_web pool with a non-grok channel: rejected.
	err := validateAccountPoolRuntimeChannelForPool(grokPool, model.Channel{Type: constant.ChannelTypeXai})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not compatible")

	// non-grok pool with a GrokWeb channel: rejected.
	err = validateAccountPoolRuntimeChannelForPool(openaiPool, model.Channel{Type: constant.ChannelTypeGrokWeb})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not compatible")
}

func TestCreateBoundChannelGrokWebDefaultsToChannelType59(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformGrokWeb)

	binding, err := svc.CreateBoundChannel(AccountPoolBoundChannelCreateParams{
		PoolID: pool.Id,
		Name:   "grok-web-channel",
	})
	require.NoError(t, err)

	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, binding.ChannelID).Error)
	assert.Equal(t, constant.ChannelTypeGrokWeb, channel.Type)
}

// ── credential resolution: no OAuth refresh for a cookie ──────────────────────

func TestResolveGrokWebCookieReturnsBareSSOWithoutOAuthRefresh(t *testing.T) {
	// A grok_web_cookie credential resolves directly via the APIKey short-circuit
	// (the sso token), with no OAuth refresh seam invoked and no "not supported" error.
	called := false
	setAccountPoolOAuthRefreshForTest(t, func(_ context.Context, _ string, _ string) (*CodexOAuthTokenResult, error) {
		called = true
		return nil, errors.New("oauth refresh must not be called for a grok_web cookie")
	})

	token, err := ResolveAccountPoolRuntimeCredential(context.Background(), AccountPoolRuntimeCredentialRequest{
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeGrokWebCookie,
			APIKey: "sso-token-abc",
		},
		Platform: model.AccountPoolPlatformGrokWeb,
		Now:      1000,
	})
	require.NoError(t, err)
	assert.Equal(t, "sso-token-abc", token)
	assert.False(t, called, "no OAuth refresh path must be taken for a cookie credential")
}

// ── end-to-end runtime selection → info.ApiKey shape ──────────────────────────

// TestGrokWebRuntimeSelectionBareSSO drives the real selection path and asserts that an
// account with only an sso token yields info.ApiKey == the bare sso token.
func TestGrokWebRuntimeSelectionBareSSO(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	ctx := newAccountPoolRuntimeTestContext()
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformGrokWeb)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeGrokWeb, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "grok-bare-sso",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeGrokWebCookie,
			APIKey: "sso-bare",
		},
	})
	info := newAccountPoolRuntimeTestRelayInfo(channel.Id, "client-grok", "channel-grok")
	request := &dto.GeneralOpenAIRequest{Model: "channel-grok"}

	require.NoError(t, ApplyAccountPoolRuntimeSelection(ctx, info, request))
	defer ReleaseAccountPoolRuntimeSelection(ctx)

	assert.Equal(t, "sso-bare", info.ApiKey)
	assert.False(t, info.RuntimeAnthropicOAuth)
	assert.False(t, info.RuntimeGeminiOAuth)
}

// TestGrokWebRuntimeSelectionWithCFClearance drives the real selection path and asserts
// that an account with sso + cf_clearance yields info.ApiKey as the JSON cookie form the
// grokweb adaptor parses.
func TestGrokWebRuntimeSelectionWithCFClearance(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	ctx := newAccountPoolRuntimeTestContext()
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformGrokWeb)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeGrokWeb, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "grok-with-cf",
		Credential: AccountPoolCredentialConfig{
			Type:        AccountPoolCredentialTypeGrokWebCookie,
			APIKey:      "sso-xyz",
			CFClearance: "cf-value-123",
		},
	})
	info := newAccountPoolRuntimeTestRelayInfo(channel.Id, "client-grok", "channel-grok")
	request := &dto.GeneralOpenAIRequest{Model: "channel-grok"}

	require.NoError(t, ApplyAccountPoolRuntimeSelection(ctx, info, request))
	defer ReleaseAccountPoolRuntimeSelection(ctx)

	var parsed map[string]string
	require.NoError(t, common.UnmarshalJsonStr(info.ApiKey, &parsed))
	assert.Equal(t, "sso-xyz", parsed["sso"])
	assert.Equal(t, "cf-value-123", parsed["cf_clearance"])
}

// ── failure: dead cookie (401) expires immediately ────────────────────────────

func TestRecordGrokWebCookie401ExpiresAccountImmediately(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformGrokWeb)
	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "dead-cookie",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeGrokWebCookie,
			APIKey: "sso-dead",
		},
	})

	err := types.NewErrorWithStatusCode(errors.New("invalid credential"), types.ErrorCodeChannelInvalidKey, http.StatusUnauthorized)
	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(account.Id, err, 1000, model.AccountPoolPlatformGrokWeb, ""))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	// A dead cookie is permanent (non-OAuth credential): a 401 expires the account on
	// the first strike rather than entering the OAuth two-strike cooldown.
	assert.Equal(t, model.AccountPoolAccountStatusExpired, reloaded.Status)
	assert.Zero(t, reloaded.TempDisabledUntil)
	assert.Zero(t, reloaded.RateLimitedUntil)
}

// ── import: sub2api grok_web cookie ───────────────────────────────────────────

func TestImportSub2APIGrokWebCookieCreatesCookieCredential(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformGrokWeb)

	result, err := svc.ImportAccounts(AccountPoolAccountImportParams{
		PoolID: pool.Id,
		Format: "sub2api",
		Content: `{
			"type": "sub2api-data",
			"version": 1,
			"exported_at": "2026-06-28T00:00:00Z",
			"accounts": [
				{
					"name": "grok-cookie",
					"platform": "grok_web",
					"type": "grok_web_cookie",
					"credentials": {
						"sso": "sso-import-token",
						"cf_clearance": "cf-import-value"
					},
					"priority": 5
				}
			]
		}`,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Imported)
	assert.Equal(t, 0, result.Failed)

	account := requireAccountPoolAccountByName(t, "grok-cookie")
	credential, err := DecryptAccountPoolCredentialConfig(account.CredentialConfig)
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCredentialTypeGrokWebCookie, credential.Type)
	assert.Equal(t, "sso-import-token", credential.APIKey)
	assert.Equal(t, "cf-import-value", credential.CFClearance)
}
