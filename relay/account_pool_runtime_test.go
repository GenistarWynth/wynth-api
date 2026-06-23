package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAccountPoolRelayHookNoopsForUnboundChannel(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	channel := createAccountPoolRelayTestChannel(t)
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "upstream-gpt-5")
	request := &dto.GeneralOpenAIRequest{Model: "upstream-gpt-5"}

	newAPIError := applyAccountPoolRuntimeSelection(ctx, info, request)

	require.Nil(t, newAPIError)
	assert.Equal(t, "sk-channel", info.ApiKey)
	assert.Equal(t, "upstream-gpt-5", info.UpstreamModelName)
	assert.Equal(t, "upstream-gpt-5", request.Model)
}

func TestAccountPoolRelayHookAppliesSelectedAccount(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBinding(t, pool.Id, channel.Id)
	account := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:            "relay-account",
		SupportedModels: []string{"upstream-gpt-5"},
		ModelMapping: map[string]string{
			"upstream-gpt-5": "account-gpt-5",
		},
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-account",
		},
	})
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "upstream-gpt-5")
	request := &dto.GeneralOpenAIRequest{Model: "upstream-gpt-5"}

	newAPIError := applyAccountPoolRuntimeSelection(ctx, info, request)

	require.Nil(t, newAPIError)
	defer service.ReleaseAccountPoolRuntimeSelection(ctx)
	assert.Equal(t, "sk-account", info.ApiKey)
	assert.Equal(t, "account-gpt-5", info.UpstreamModelName)
	assert.Equal(t, "account-gpt-5", request.Model)
	assert.Equal(t, "client-gpt-5", info.OriginModelName)
	assert.Equal(t, account.Id, service.GetSelectedAccountPoolAccountID(ctx))
}

func TestAccountPoolRelayHookMapsNoSchedulableAccountToRetriable503(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBinding(t, pool.Id, channel.Id)
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "upstream-gpt-5")
	request := &dto.GeneralOpenAIRequest{Model: "upstream-gpt-5"}

	newAPIError := applyAccountPoolRuntimeSelection(ctx, info, request)

	require.NotNil(t, newAPIError)
	require.ErrorIs(t, newAPIError, service.ErrAccountPoolNoSchedulableAccount)
	assert.Equal(t, http.StatusServiceUnavailable, newAPIError.StatusCode)
	assert.Equal(t, types.ErrorCodeGetChannelFailed, newAPIError.GetErrorCode())
	assert.False(t, types.IsSkipRetryError(newAPIError))
}

func TestAccountPoolRelayHookMapsMissingCredentialToRetriable503(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBinding(t, pool.Id, channel.Id)
	account := model.AccountPoolAccount{
		PoolID: pool.Id,
		Name:   "missing-credential",
		Status: model.AccountPoolAccountStatusEnabled,
	}
	require.NoError(t, model.DB.Create(&account).Error)
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "upstream-gpt-5")
	request := &dto.GeneralOpenAIRequest{Model: "upstream-gpt-5"}

	newAPIError := applyAccountPoolRuntimeSelection(ctx, info, request)

	require.NotNil(t, newAPIError)
	require.ErrorContains(t, newAPIError, "account pool selected account has no runtime credential")
	assert.NotContains(t, newAPIError.Error(), "account_id=")
	assert.Equal(t, http.StatusServiceUnavailable, newAPIError.StatusCode)
	assert.Equal(t, types.ErrorCodeGetChannelFailed, newAPIError.GetErrorCode())
	assert.False(t, types.IsSkipRetryError(newAPIError))
}

func TestAccountPoolRelayTextHelperStopsBeforeUpstreamWhenPoolExhausted(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBinding(t, pool.Id, channel.Id)
	setAccountPoolRelayChannelContext(ctx, channel.Id)
	ctx.Set("model_mapping", `{"client-gpt-5":"upstream-gpt-5"}`)
	request := &dto.GeneralOpenAIRequest{Model: "client-gpt-5"}
	info := relaycommon.GenRelayInfoOpenAI(ctx, request)

	newAPIError := TextHelper(ctx, info)

	require.NotNil(t, newAPIError)
	require.ErrorIs(t, newAPIError, service.ErrAccountPoolNoSchedulableAccount)
	assert.Equal(t, http.StatusServiceUnavailable, newAPIError.StatusCode)
	assert.Equal(t, types.ErrorCodeGetChannelFailed, newAPIError.GetErrorCode())
	assert.False(t, types.IsSkipRetryError(newAPIError))
}

func TestAccountPoolRelayResponsesHelperStopsBeforeUpstreamWhenPoolExhausted(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/responses")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBinding(t, pool.Id, channel.Id)
	setAccountPoolRelayChannelContext(ctx, channel.Id)
	ctx.Set("model_mapping", `{"client-gpt-5":"upstream-gpt-5"}`)
	request := &dto.OpenAIResponsesRequest{
		Model: "client-gpt-5",
		Input: []byte(`"hello"`),
	}
	info := relaycommon.GenRelayInfoResponses(ctx, request)

	newAPIError := ResponsesHelper(ctx, info)

	require.NotNil(t, newAPIError)
	require.ErrorIs(t, newAPIError, service.ErrAccountPoolNoSchedulableAccount)
	assert.Equal(t, http.StatusServiceUnavailable, newAPIError.StatusCode)
	assert.Equal(t, types.ErrorCodeGetChannelFailed, newAPIError.GetErrorCode())
	assert.False(t, types.IsSkipRetryError(newAPIError))
}

func TestAccountPoolRelayUnsupportedGuardNoopsForUnboundChannel(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/images/generations")
	channel := createAccountPoolRelayTestChannel(t)
	setAccountPoolRelayChannelContext(ctx, channel.Id)
	info := newAccountPoolRelayTestInfo(channel.Id, "gpt-image-1", "gpt-image-1")

	newAPIError := rejectUnsupportedAccountPoolRuntime(ctx, info, "image")

	require.Nil(t, newAPIError)
}

func TestAccountPoolRelayUnsupportedGuardRejectsEnabledBinding(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/images/generations")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBinding(t, pool.Id, channel.Id)
	setAccountPoolRelayChannelContext(ctx, channel.Id)
	info := newAccountPoolRelayTestInfo(channel.Id, "gpt-image-1", "gpt-image-1")

	newAPIError := rejectUnsupportedAccountPoolRuntime(ctx, info, "image")

	require.NotNil(t, newAPIError)
	assert.Equal(t, http.StatusServiceUnavailable, newAPIError.StatusCode)
	assert.Equal(t, types.ErrorCodeGetChannelFailed, newAPIError.GetErrorCode())
	assert.False(t, types.IsSkipRetryError(newAPIError))
	assert.Contains(t, newAPIError.Error(), "image")
	assert.NotContains(t, newAPIError.Error(), "sk-")
}

func TestAccountPoolRelayUnsupportedGuardUsesContextChannelWhenMetaMissing(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/images/generations")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBinding(t, pool.Id, channel.Id)
	setAccountPoolRelayChannelContext(ctx, channel.Id)
	info := &relaycommon.RelayInfo{}

	newAPIError := rejectUnsupportedAccountPoolRuntime(ctx, info, "image")

	require.NotNil(t, newAPIError)
	assert.Equal(t, http.StatusServiceUnavailable, newAPIError.StatusCode)
	assert.Equal(t, types.ErrorCodeGetChannelFailed, newAPIError.GetErrorCode())
	assert.False(t, types.IsSkipRetryError(newAPIError))
}

func TestAccountPoolRelayImageHelperRejectsEnabledBindingBeforeUpstream(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/images/generations")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBinding(t, pool.Id, channel.Id)
	setAccountPoolRelayChannelContext(ctx, channel.Id)
	common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "gpt-image-1")
	request := &dto.ImageRequest{Model: "gpt-image-1", Prompt: "draw a cube"}
	info := relaycommon.GenRelayInfoImage(ctx, request)

	newAPIError := ImageHelper(ctx, info)

	require.NotNil(t, newAPIError)
	assert.Equal(t, http.StatusServiceUnavailable, newAPIError.StatusCode)
	assert.Equal(t, types.ErrorCodeGetChannelFailed, newAPIError.GetErrorCode())
	assert.False(t, types.IsSkipRetryError(newAPIError))
}

func setupAccountPoolRelayTestDB(t *testing.T) {
	t.Helper()

	oldDB := model.DB
	oldSecret := common.CryptoSecret
	oldStable := common.CryptoSecretStable

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB = db
	common.CryptoSecret = "account-pool-relay-test-secret"
	common.CryptoSecretStable = true

	require.NoError(t, model.DB.AutoMigrate(
		&model.Channel{},
		&model.Ability{},
		&model.AccountPool{},
		&model.AccountPoolAccount{},
		&model.AccountPoolProxy{},
		&model.AccountPoolChannelBinding{},
	))

	t.Cleanup(func() {
		model.DB = oldDB
		common.CryptoSecret = oldSecret
		common.CryptoSecretStable = oldStable
	})
}

func newAccountPoolRelayTestContext(path string) *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, path, nil)
	return ctx
}

func createAccountPoolRelayTestPool(t *testing.T) model.AccountPool {
	t.Helper()
	pool := model.AccountPool{
		Name:     "relay-pool",
		Platform: model.AccountPoolPlatformOpenAI,
		Status:   model.AccountPoolStatusEnabled,
	}
	require.NoError(t, model.DB.Create(&pool).Error)
	return pool
}

func createAccountPoolRelayTestChannel(t *testing.T) model.Channel {
	t.Helper()
	channel := model.Channel{
		Type:   constant.ChannelTypeOpenAI,
		Key:    "sk-channel",
		Name:   "relay-channel",
		Status: common.ChannelStatusManuallyDisabled,
	}
	require.NoError(t, model.DB.Create(&channel).Error)
	return channel
}

func createAccountPoolRelayTestEnabledBinding(t *testing.T, poolID int, channelID int) model.AccountPoolChannelBinding {
	t.Helper()

	bindingView, err := service.AccountPoolService{}.CreateBinding(service.AccountPoolBindingCreateParams{
		PoolID:    poolID,
		ChannelID: channelID,
	})
	require.NoError(t, err)
	_, err = service.AccountPoolService{}.ActivateBinding(poolID, bindingView.Id)
	require.NoError(t, err)
	var binding model.AccountPoolChannelBinding
	require.NoError(t, model.DB.First(&binding, bindingView.Id).Error)
	return binding
}

func createAccountPoolRelayTestAccount(
	t *testing.T,
	poolID int,
	params service.AccountPoolAccountCreateParams,
) service.AccountPoolAccountView {
	t.Helper()
	params.PoolID = poolID
	if params.Credential.Type == "" {
		params.Credential = service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-account",
		}
	}
	account, err := service.AccountPoolService{}.CreateAccount(params)
	require.NoError(t, err)
	return account
}

func newAccountPoolRelayTestInfo(channelID int, originModel string, upstreamModel string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: originModel,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         channelID,
			ApiKey:            "sk-channel",
			UpstreamModelName: upstreamModel,
		},
	}
}

func setAccountPoolRelayChannelContext(ctx *gin.Context, channelID int) {
	common.SetContextKey(ctx, constant.ContextKeyChannelId, channelID)
	common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(ctx, constant.ContextKeyChannelKey, "sk-channel")
	common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "client-gpt-5")
}
