package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountPoolRuntimeNoopsWithoutRuntimeBinding(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	ctx := newAccountPoolRuntimeTestContext()
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	info := newAccountPoolRuntimeTestRelayInfo(channel.Id, "client-gpt-5", "channel-gpt-5")
	request := &dto.GeneralOpenAIRequest{Model: "channel-gpt-5"}
	ctx.Set("use_channel", []string{"123"})

	err := ApplyAccountPoolRuntimeSelection(ctx, info, request)

	require.NoError(t, err)
	assert.Equal(t, "channel-key", info.ApiKey)
	assert.Equal(t, "channel-gpt-5", info.UpstreamModelName)
	assert.Equal(t, "channel-gpt-5", request.Model)
	assert.Zero(t, GetSelectedAccountPoolAccountID(ctx))
	assert.Equal(t, []string{"123"}, ctx.GetStringSlice("use_channel"))
}

func TestAccountPoolRuntimeNoopsWithoutChannelMeta(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	ctx := newAccountPoolRuntimeTestContext()
	info := &relaycommon.RelayInfo{OriginModelName: "client-gpt-5"}
	request := &dto.GeneralOpenAIRequest{Model: "client-gpt-5"}

	err := ApplyAccountPoolRuntimeSelection(ctx, info, request)

	require.NoError(t, err)
	assert.Nil(t, info.ChannelMeta)
	assert.Equal(t, "client-gpt-5", request.Model)
}

func TestAccountPoolRuntimeNoopsForDraftBinding(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	ctx := newAccountPoolRuntimeTestContext()
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	_, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)
	info := newAccountPoolRuntimeTestRelayInfo(channel.Id, "client-gpt-5", "channel-gpt-5")
	request := &dto.GeneralOpenAIRequest{Model: "channel-gpt-5"}

	err = ApplyAccountPoolRuntimeSelection(ctx, info, request)

	require.NoError(t, err)
	assert.Equal(t, "channel-key", info.ApiKey)
	assert.Equal(t, "channel-gpt-5", info.UpstreamModelName)
	assert.Equal(t, "channel-gpt-5", request.Model)
	assert.Zero(t, GetSelectedAccountPoolAccountID(ctx))
}

func TestAccountPoolRuntimeAppliesSelectedAccountCredentialAndModel(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	ctx := newAccountPoolRuntimeTestContext()
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:            "runtime-account",
		SupportedModels: []string{"channel-gpt-5"},
		ModelMapping: map[string]string{
			"channel-gpt-5": "account-gpt-5",
		},
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-runtime-account",
		},
	})
	info := newAccountPoolRuntimeTestRelayInfo(channel.Id, "client-gpt-5", "channel-gpt-5")
	request := &dto.GeneralOpenAIRequest{Model: "channel-gpt-5"}
	ctx.Set("use_channel", []string{"7"})

	err := ApplyAccountPoolRuntimeSelection(ctx, info, request)

	require.NoError(t, err)
	assert.Equal(t, "sk-runtime-account", info.ApiKey)
	assert.Equal(t, "account-gpt-5", info.UpstreamModelName)
	assert.Equal(t, "account-gpt-5", request.Model)
	assert.Equal(t, "client-gpt-5", info.OriginModelName)
	assert.Equal(t, account.Id, GetSelectedAccountPoolAccountID(ctx))
	assert.Contains(t, GetAccountPoolAttemptedAccountIDs(ctx), account.Id)
	assert.Equal(t, []string{"7"}, ctx.GetStringSlice("use_channel"))
}

func TestAccountPoolRuntimeLeaseExhaustsThenAllowsSelectionAfterRelease(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	ctx := newAccountPoolRuntimeTestContext()
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:           "single-slot",
		MaxConcurrency: 1,
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-single-slot",
		},
	})
	info := newAccountPoolRuntimeTestRelayInfo(channel.Id, "client-gpt-5", "gpt-5")
	request := &dto.GeneralOpenAIRequest{Model: "gpt-5"}

	err := ApplyAccountPoolRuntimeSelection(ctx, info, request)
	require.NoError(t, err)
	assert.Equal(t, account.Id, GetSelectedAccountPoolAccountID(ctx))

	_, _, err = SelectAccountPoolAccountWithLease(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "client-gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  100,
	})
	require.ErrorIs(t, err, ErrAccountPoolNoSchedulableAccount)

	ReleaseAccountPoolRuntimeSelection(ctx)
	selected, release, err := SelectAccountPoolAccountWithLease(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "client-gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  100,
	})
	require.NoError(t, err)
	defer release()
	assert.Equal(t, account.Id, selected.AccountID)
}

func TestAccountPoolRuntimeFallsBackToAccessToken(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	ctx := newAccountPoolRuntimeTestContext()
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name: "runtime-token-account",
		Credential: AccountPoolCredentialConfig{
			Type: AccountPoolCredentialTypeOAuth,
		},
		TokenState: AccountPoolTokenState{
			AccessToken: "access-runtime-token",
		},
	})
	info := newAccountPoolRuntimeTestRelayInfo(channel.Id, "client-gpt-5", "channel-gpt-5")
	request := &dto.GeneralOpenAIRequest{Model: "channel-gpt-5"}

	err := ApplyAccountPoolRuntimeSelection(ctx, info, request)

	require.NoError(t, err)
	assert.Equal(t, "access-runtime-token", info.ApiKey)
	assert.Equal(t, "channel-gpt-5", info.UpstreamModelName)
	assert.Equal(t, "channel-gpt-5", request.Model)
}

func TestAccountPoolRuntimeErrorsWhenEnabledAccountHasNoCredential(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	ctx := newAccountPoolRuntimeTestContext()
	pool := model.AccountPool{Name: "runtime-pool", Platform: model.AccountPoolPlatformOpenAI}
	require.NoError(t, model.DB.Create(&pool).Error)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	account := model.AccountPoolAccount{
		PoolID: pool.Id,
		Name:   "no-runtime-secret",
		Status: model.AccountPoolAccountStatusEnabled,
	}
	require.NoError(t, model.DB.Create(&account).Error)
	info := newAccountPoolRuntimeTestRelayInfo(channel.Id, "client-gpt-5", "channel-gpt-5")
	request := &dto.GeneralOpenAIRequest{Model: "channel-gpt-5"}

	err := ApplyAccountPoolRuntimeSelection(ctx, info, request)

	require.ErrorContains(t, err, "account pool selected account has no runtime credential")
	assert.NotContains(t, err.Error(), "account_id=")
}

func newAccountPoolRuntimeTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	return ctx
}

func newAccountPoolRuntimeTestRelayInfo(channelID int, originModel string, upstreamModel string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: originModel,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         channelID,
			ApiKey:            "channel-key",
			UpstreamModelName: upstreamModel,
		},
	}
}
