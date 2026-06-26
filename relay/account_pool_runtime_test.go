package relay

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func TestAccountPoolRuntimeAttemptsRetryAnotherAccountWhenCredentialResolutionFails(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)
	first := model.AccountPoolAccount{
		PoolID:   pool.Id,
		Name:     "missing-credential",
		Status:   model.AccountPoolAccountStatusEnabled,
		Priority: 100,
	}
	require.NoError(t, model.DB.Create(&first).Error)
	second := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:     "fallback",
		Priority: 50,
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-fallback",
		},
	})
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	baseRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}
	attempts := 0

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		attempts++
		assert.Equal(t, second.Id, service.GetSelectedAccountPoolAccountID(ctx))
		assert.Equal(t, "sk-fallback", info.ApiKey)
		return nil
	})

	require.Nil(t, newAPIError)
	assert.Equal(t, 1, attempts)
	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, first.Id).Error)
	assert.Greater(t, reloaded.TempDisabledUntil, int64(0))
	assert.Contains(t, reloaded.LastError, "no runtime credential")
}

func TestAccountPoolRuntimeSnapshotRestorePreservesNilConversionChain(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:            "sk-original",
			UpstreamModelName: "gpt-5",
		},
		RequestConversionChain: nil,
	}
	snapshot := snapshotAccountPoolRuntimeRelay(info)
	info.RequestConversionChain = []types.RelayFormat{types.RelayFormatClaude}

	restoreAccountPoolRuntimeRelay(info, snapshot)

	assert.Nil(t, info.RequestConversionChain)
}

func TestAccountPoolRuntimeAttemptsRetryAnotherAccountBeforeResponse(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)
	first := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:     "first",
		Priority: 100,
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-first",
		},
	})
	second := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:     "second",
		Priority: 50,
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-second",
		},
	})
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	baseRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}
	selected := make([]int, 0, 2)

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		selected = append(selected, service.GetSelectedAccountPoolAccountID(ctx))
		if len(selected) == 1 {
			return types.NewErrorWithStatusCode(errors.New("first account failed"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
		}
		assert.Equal(t, "sk-second", info.ApiKey)
		assert.Equal(t, second.Id, service.GetSelectedAccountPoolAccountID(ctx))
		return nil
	})

	require.Nil(t, newAPIError)
	assert.Equal(t, []int{first.Id, second.Id}, selected)
}

func TestAccountPoolRuntimeAttemptsRecordFailureBeforeRetryingNextAccount(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)
	first := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{Name: "first", Priority: 100})
	second := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{Name: "second", Priority: 50})
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	baseRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}
	selected := make([]int, 0, 2)

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		selected = append(selected, service.GetSelectedAccountPoolAccountID(ctx))
		if len(selected) == 1 {
			return types.NewErrorWithStatusCode(errors.New("rate limited"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests)
		}
		return nil
	})

	require.Nil(t, newAPIError)
	assert.Equal(t, []int{first.Id, second.Id}, selected)
	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, first.Id).Error)
	assert.Greater(t, reloaded.RateLimitedUntil, int64(0))
	assert.Contains(t, reloaded.LastError, "rate limited")
}

func TestAccountPoolRuntimeAttemptsRecordSuccessForSelectedAccount(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)
	account := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:               "successful-account",
		LastUsedAt:         100,
		RateLimitedUntil:   1200,
		TempDisabledUntil:  1300,
		TempDisabledReason: "previous temporary failure",
		LastError:          "previous failure",
	})
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	baseRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		assert.Equal(t, account.Id, service.GetSelectedAccountPoolAccountID(ctx))
		return nil
	})

	require.Nil(t, newAPIError)
	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Greater(t, reloaded.LastUsedAt, int64(100))
	assert.Zero(t, reloaded.RateLimitedUntil)
	assert.Zero(t, reloaded.TempDisabledUntil)
	assert.Empty(t, reloaded.TempDisabledReason)
	assert.Empty(t, reloaded.LastError)
}

func TestAccountPoolRuntimeAttemptsDoNotRecordChannelTestSuccessOrAffinity(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)
	first := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:       "channel-test-first",
		Priority:   10,
		LastUsedAt: 100,
	})
	sessionID := t.Name() + ":session"
	baseRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}

	channelTestCtx := newAccountPoolRelayTestContext("/v1/chat/completions")
	channelTestCtx.Request.Header.Set("Session_id", sessionID)
	channelTestInfo := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	channelTestInfo.IsChannelTest = true

	newAPIError := runAccountPoolRuntimeAttempts(channelTestCtx, channelTestInfo, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		assert.Equal(t, first.Id, service.GetSelectedAccountPoolAccountID(channelTestCtx))
		return nil
	})
	require.Nil(t, newAPIError)

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, first.Id).Error)
	assert.Equal(t, int64(100), reloaded.LastUsedAt)
	assert.Zero(t, reloaded.SuccessCount)
	assert.Zero(t, reloaded.LastSuccessAt)

	second := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:     "normal-high-priority",
		Priority: 100,
	})
	normalCtx := newAccountPoolRelayTestContext("/v1/chat/completions")
	normalCtx.Request.Header.Set("Session_id", sessionID)
	normalInfo := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	selected := make([]int, 0, 1)

	newAPIError = runAccountPoolRuntimeAttempts(normalCtx, normalInfo, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		selected = append(selected, service.GetSelectedAccountPoolAccountID(normalCtx))
		return nil
	})
	require.Nil(t, newAPIError)
	assert.Equal(t, []int{second.Id}, selected)
}

func TestAccountPoolRuntimeAttemptsDoNotRecordChannelTestFailure(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 0)
	account := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name: "channel-test-failure",
	})
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	info.IsChannelTest = true
	baseRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		assert.Equal(t, account.Id, service.GetSelectedAccountPoolAccountID(ctx))
		return types.NewErrorWithStatusCode(errors.New("channel test upstream failed"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
	})
	require.NotNil(t, newAPIError)

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Empty(t, reloaded.LastError)
	assert.Zero(t, reloaded.LastFailureAt)
	assert.Zero(t, reloaded.FailureCount)
	assert.Zero(t, reloaded.TempDisabledUntil)
	assert.Empty(t, reloaded.TempDisabledReason)
	assert.Zero(t, reloaded.RateLimitedUntil)
}

func TestAccountPoolRuntimeAffinityIsSoftAndBreaksOnFailure(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)
	first := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:     "first",
		Priority: 10,
	})
	sessionID := t.Name() + ":codex-session-1"

	firstCtx := newAccountPoolRelayTestContext("/v1/chat/completions")
	firstCtx.Request.Header.Set("Session_id", sessionID)
	firstInfo := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	baseRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}

	newAPIError := runAccountPoolRuntimeAttempts(firstCtx, firstInfo, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		assert.Equal(t, first.Id, service.GetSelectedAccountPoolAccountID(firstCtx))
		return nil
	})
	require.Nil(t, newAPIError)

	second := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:     "second",
		Priority: 100,
	})

	stickyCtx := newAccountPoolRelayTestContext("/v1/chat/completions")
	stickyCtx.Request.Header.Set("Session_id", sessionID)
	stickyInfo := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	stickySelected := make([]int, 0, 1)

	newAPIError = runAccountPoolRuntimeAttempts(stickyCtx, stickyInfo, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		stickySelected = append(stickySelected, service.GetSelectedAccountPoolAccountID(stickyCtx))
		return nil
	})
	require.Nil(t, newAPIError)
	assert.Equal(t, []int{first.Id}, stickySelected)

	breakCtx := newAccountPoolRelayTestContext("/v1/chat/completions")
	breakCtx.Request.Header.Set("Session_id", sessionID)
	breakInfo := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	breakSelected := make([]int, 0, 2)

	newAPIError = runAccountPoolRuntimeAttempts(breakCtx, breakInfo, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		breakSelected = append(breakSelected, service.GetSelectedAccountPoolAccountID(breakCtx))
		if len(breakSelected) == 1 {
			return types.NewErrorWithStatusCode(errors.New("sticky account failed"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
		}
		return nil
	})
	require.Nil(t, newAPIError)
	assert.Equal(t, []int{first.Id, second.Id}, breakSelected)

	nextCtx := newAccountPoolRelayTestContext("/v1/chat/completions")
	nextCtx.Request.Header.Set("Session_id", sessionID)
	nextInfo := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	nextSelected := make([]int, 0, 1)

	newAPIError = runAccountPoolRuntimeAttempts(nextCtx, nextInfo, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		nextSelected = append(nextSelected, service.GetSelectedAccountPoolAccountID(nextCtx))
		return nil
	})
	require.Nil(t, newAPIError)
	assert.Equal(t, []int{second.Id}, nextSelected)
}

func TestAccountPoolRuntimeAttemptsDoNotRetrySkipRetryError(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)
	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{Name: "only"})
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	baseRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}
	attempts := 0

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		attempts++
		return types.NewErrorWithStatusCode(errors.New("bad request"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	})

	require.NotNil(t, newAPIError)
	assert.Equal(t, 1, attempts)
	assert.True(t, types.IsSkipRetryError(newAPIError))
}

func TestAccountPoolRuntimeAttemptsDoNotRecordSkipRetryErrorAsAccountFailure(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)
	account := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{Name: "skip-retry"})
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	baseRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		return types.NewErrorWithStatusCode(errors.New("client request is invalid"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	})

	require.NotNil(t, newAPIError)
	assert.True(t, types.IsSkipRetryError(newAPIError))
	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Empty(t, reloaded.LastError)
	assert.Zero(t, reloaded.RateLimitedUntil)
	assert.Zero(t, reloaded.TempDisabledUntil)
}

func TestAccountPoolRuntimeAttemptsDoNotRetryAfterResponseStarted(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)
	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{Name: "first", Priority: 100})
	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{Name: "second", Priority: 50})
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	baseRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}
	attempts := 0

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		attempts++
		info.StartTime = time.Now().Add(-time.Second)
		info.FirstResponseTime = time.Now()
		return types.NewErrorWithStatusCode(errors.New("stream already started"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
	})

	require.NotNil(t, newAPIError)
	assert.Equal(t, 1, attempts)
	assert.Equal(t, http.StatusInternalServerError, newAPIError.StatusCode)
}

func TestAccountPoolRuntimeAttemptsReturnPoolExhaustionWhenRetryBudgetHasNoCandidate(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)
	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{Name: "only"})
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	baseRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}
	attempts := 0

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		attempts++
		return types.NewErrorWithStatusCode(errors.New("single account failed"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
	})

	require.NotNil(t, newAPIError)
	require.ErrorIs(t, newAPIError, service.ErrAccountPoolNoSchedulableAccount)
	assert.Equal(t, 1, attempts)
	assert.Equal(t, http.StatusServiceUnavailable, newAPIError.StatusCode)
	assert.Equal(t, types.ErrorCodeGetChannelFailed, newAPIError.GetErrorCode())
}

func TestAccountPoolRuntimeAttemptsResetMappedModelForEachRetry(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)
	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:            "first",
		Priority:        100,
		SupportedModels: []string{"channel-gpt-5"},
		ModelMapping: map[string]string{
			"channel-gpt-5": "account-one-model",
		},
	})
	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:            "second",
		Priority:        50,
		SupportedModels: []string{"channel-gpt-5"},
		ModelMapping: map[string]string{
			"channel-gpt-5": "account-two-model",
		},
	})
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "channel-gpt-5")
	baseRequest := &dto.GeneralOpenAIRequest{Model: "channel-gpt-5"}
	models := make([]string, 0, 2)

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		textRequest, ok := request.(*dto.GeneralOpenAIRequest)
		require.True(t, ok)
		models = append(models, textRequest.Model)
		if len(models) == 1 {
			return types.NewErrorWithStatusCode(errors.New("mapped model account failed"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
		}
		return nil
	})

	require.Nil(t, newAPIError)
	assert.Equal(t, []string{"account-one-model", "account-two-model"}, models)
}

func TestAccountPoolRuntimeAttemptsResetProxyForEachRetry(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	proxy := createAccountPoolRelayTestProxy(t, service.AccountPoolProxyCreateParams{
		Name:     "first-proxy",
		Protocol: "http",
		Host:     "first-proxy.local",
		Port:     8080,
	})
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)
	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:     "first",
		Priority: 100,
		ProxyID:  proxy.Id,
	})
	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:     "second",
		Priority: 50,
	})
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	baseRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}
	proxies := make([]string, 0, 2)

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		proxies = append(proxies, info.RuntimeProxy)
		if len(proxies) == 1 {
			return types.NewErrorWithStatusCode(errors.New("proxied account failed"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
		}
		return nil
	})

	require.Nil(t, newAPIError)
	assert.Equal(t, []string{"http://first-proxy.local:8080", ""}, proxies)
}

func TestAccountPoolRuntimeAttemptsPreserveChannelProxyWhenAccountProxyEmpty(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	proxy := createAccountPoolRelayTestProxy(t, service.AccountPoolProxyCreateParams{
		Name:     "first-proxy",
		Protocol: "http",
		Host:     "first-proxy.local",
		Port:     8080,
	})
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)
	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:     "first",
		Priority: 100,
		ProxyID:  proxy.Id,
	})
	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:     "second",
		Priority: 50,
	})
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	info.ChannelSetting.Proxy = "http://channel-proxy.local:8081"
	baseRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}
	proxies := make([]string, 0, 2)

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		proxies = append(proxies, accountPoolRelayTestEffectiveProxy(info))
		if len(proxies) == 1 {
			return types.NewErrorWithStatusCode(errors.New("proxied account failed"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
		}
		return nil
	})

	require.Nil(t, newAPIError)
	assert.Equal(t, []string{"http://first-proxy.local:8080", "http://channel-proxy.local:8081"}, proxies)
}

func TestAccountPoolRuntimeAttemptsUsePoolDefaultProxyForEachRetry(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	proxy := createAccountPoolRelayTestProxy(t, service.AccountPoolProxyCreateParams{
		Name:     "pool-proxy",
		Protocol: "socks5",
		Host:     "pool-proxy.local",
		Port:     1080,
	})
	require.NoError(t, model.DB.Model(&model.AccountPool{}).
		Where("id = ?", pool.Id).
		Update("default_proxy_id", proxy.Id).Error)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)
	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:     "first",
		Priority: 100,
	})
	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:     "second",
		Priority: 50,
	})
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	baseRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}
	proxies := make([]string, 0, 2)

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		proxies = append(proxies, accountPoolRelayTestEffectiveProxy(info))
		if len(proxies) == 1 {
			return types.NewErrorWithStatusCode(errors.New("pool-proxied account failed"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
		}
		return nil
	})

	require.Nil(t, newAPIError)
	assert.Equal(t, []string{"socks5://pool-proxy.local:1080", "socks5://pool-proxy.local:1080"}, proxies)
}

func TestAccountPoolRuntimeAttemptsResetRuntimeHeaderOverrideForEachRetry(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)
	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:     "first",
		Priority: 100,
	})
	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:     "second",
		Priority: 50,
	})
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	info.UseRuntimeHeadersOverride = true
	info.RuntimeHeadersOverride = map[string]any{
		"x-static": "channel-value",
	}
	baseRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}
	overrides := make([]map[string]any, 0, 2)

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		overrides = append(overrides, cloneAccountPoolRelayTestRuntimeHeaders(info.RuntimeHeadersOverride))
		info.RuntimeHeadersOverride["x-account-attempt"] = "first-account"
		if len(overrides) == 1 {
			return types.NewErrorWithStatusCode(errors.New("first account failed"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
		}
		return nil
	})

	require.Nil(t, newAPIError)
	require.Len(t, overrides, 2)
	assert.Equal(t, map[string]any{"x-static": "channel-value"}, overrides[0])
	assert.Equal(t, map[string]any{"x-static": "channel-value"}, overrides[1])
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

func createAccountPoolRelayTestEnabledBindingWithRetryTimes(t *testing.T, poolID int, channelID int, accountRetryTimes int) model.AccountPoolChannelBinding {
	t.Helper()

	bindingView, err := service.AccountPoolService{}.CreateBinding(service.AccountPoolBindingCreateParams{
		PoolID:            poolID,
		ChannelID:         channelID,
		AccountRetryTimes: accountRetryTimes,
	})
	require.NoError(t, err)
	_, err = service.AccountPoolService{}.ActivateBinding(poolID, bindingView.Id)
	require.NoError(t, err)
	var binding model.AccountPoolChannelBinding
	require.NoError(t, model.DB.First(&binding, bindingView.Id).Error)
	return binding
}

func createAccountPoolRelayTestProxy(t *testing.T, params service.AccountPoolProxyCreateParams) service.AccountPoolProxyView {
	t.Helper()
	proxy, err := service.AccountPoolService{}.CreateProxy(params)
	require.NoError(t, err)
	return proxy
}

func accountPoolRelayTestEffectiveProxy(info *relaycommon.RelayInfo) string {
	if info == nil {
		return ""
	}
	if info.RuntimeProxy != "" {
		return info.RuntimeProxy
	}
	return info.ChannelSetting.Proxy
}

func cloneAccountPoolRelayTestRuntimeHeaders(headers map[string]any) map[string]any {
	if headers == nil {
		return nil
	}
	cloned := make(map[string]any, len(headers))
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
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

// TestRunAccountPoolRuntimeAttemptsPoolMode verifies that when pool_mode is
// enabled and a 500 error matches pool_mode_retry_status_codes, the same
// account is retried up to pool_mode_retry_count times without recording a
// failure and without adding the account to the attempted set. After pool-mode
// retries succeed the call returns nil error.
func TestRunAccountPoolRuntimeAttemptsPoolMode(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	// AccountRetryTimes=1 allows normal inter-account retry after pool-mode exhaustion.
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)

	// Create the pool-mode account and then set RuntimeOptions directly on the DB row.
	account := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:     "pool-mode-account",
		Priority: 100,
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-pool-mode",
		},
	})
	runtimeOptionsJSON := `{"pool_mode":true,"pool_mode_retry_count":2,"pool_mode_retry_status_codes":[500]}`
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", account.Id).
		Update("runtime_options", runtimeOptionsJSON).Error)

	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	baseRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}
	selectedIDs := make([]int, 0, 3)
	callCount := 0

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		callCount++
		selectedIDs = append(selectedIDs, service.GetSelectedAccountPoolAccountID(ctx))
		// Return a 500 error twice, then succeed on the third call.
		if callCount < 3 {
			return types.NewErrorWithStatusCode(errors.New("upstream 500"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
		}
		return nil
	})

	require.Nil(t, newAPIError)
	// All three attempts must use the same account (pool-mode same-account retry).
	assert.Equal(t, 3, callCount)
	assert.Equal(t, []int{account.Id, account.Id, account.Id}, selectedIDs)

	// Pool-mode retries must NOT record a failure on the DB account row.
	// After the successful third attempt, success is recorded instead.
	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Empty(t, reloaded.LastError, "pool-mode retries must not record failure")
	assert.Zero(t, reloaded.TempDisabledUntil, "pool-mode retries must not temp-disable the account")
	assert.Zero(t, reloaded.RateLimitedUntil, "pool-mode retries must not rate-limit the account")
	// The account must NOT be in the attempted set between pool-mode retries.
	// After success it may be in the attempted set (normal bookkeeping).
	assert.Greater(t, reloaded.SuccessCount, int64(0), "final success must be recorded")
}
