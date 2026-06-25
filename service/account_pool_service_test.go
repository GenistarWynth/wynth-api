package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAccountPoolServiceStoresAccountSecretsEncrypted(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "primary-key",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-secret",
		},
	})
	require.NoError(t, err)

	var stored model.AccountPoolAccount
	require.NoError(t, model.DB.First(&stored, account.Id).Error)
	assert.NotContains(t, stored.CredentialConfig, "sk-secret")
	assert.True(t, account.HasCredential)
	assert.False(t, account.HasToken)
}

func TestAccountPoolServiceUpdateAccountPreservesSecretsAndSoftDeletes(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID:            pool.Id,
		Name:              "primary",
		AccountIdentifier: "account-a",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-original",
		},
		TokenState: AccountPoolTokenState{
			AccessToken: "access-original",
			Version:     1,
		},
		Priority:        10,
		Weight:          20,
		MaxConcurrency:  3,
		SupportedModels: []string{"gpt-5"},
	})
	require.NoError(t, err)
	var storedBefore model.AccountPoolAccount
	require.NoError(t, model.DB.First(&storedBefore, account.Id).Error)

	updated, err := service.UpdateAccount(pool.Id, account.Id, AccountPoolAccountCreateParams{
		Name:              "primary-updated",
		AccountIdentifier: "account-b",
		Credential: AccountPoolCredentialConfig{
			Type: AccountPoolCredentialTypeAPIKey,
		},
		Status:          model.AccountPoolAccountStatusDisabled,
		Priority:        30,
		Weight:          40,
		MaxConcurrency:  5,
		SupportedModels: []string{"gpt-5", "gpt-5-mini"},
		ModelMapping:    map[string]string{"gpt-5": "upstream-gpt-5"},
	})

	require.NoError(t, err)
	assert.Equal(t, "primary-updated", updated.Name)
	assert.Equal(t, "account-b", updated.AccountIdentifier)
	assert.Equal(t, model.AccountPoolAccountStatusDisabled, updated.Status)
	assert.Equal(t, int64(30), updated.Priority)
	assert.Equal(t, uint(40), updated.Weight)
	assert.Equal(t, 5, updated.MaxConcurrency)
	assert.Equal(t, []string{"gpt-5", "gpt-5-mini"}, updated.SupportedModels)
	assert.Equal(t, map[string]string{"gpt-5": "upstream-gpt-5"}, updated.ModelMapping)

	var storedAfter model.AccountPoolAccount
	require.NoError(t, model.DB.First(&storedAfter, account.Id).Error)
	assert.Equal(t, storedBefore.CredentialConfig, storedAfter.CredentialConfig)
	assert.Equal(t, storedBefore.TokenState, storedAfter.TokenState)

	updated, err = service.UpdateAccount(pool.Id, account.Id, AccountPoolAccountCreateParams{
		Name:              "primary-unlimited",
		Status:            model.AccountPoolAccountStatusEnabled,
		MaxConcurrency:    0,
		MaxConcurrencySet: true,
	})
	require.NoError(t, err)
	assert.Zero(t, updated.MaxConcurrency)

	unlimited, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID:            pool.Id,
		Name:              "created-unlimited",
		MaxConcurrency:    0,
		MaxConcurrencySet: true,
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-created-unlimited",
		},
	})
	require.NoError(t, err)
	assert.Zero(t, unlimited.MaxConcurrency)

	accounts, err := service.ListAccounts(pool.Id)
	require.NoError(t, err)
	require.Len(t, accounts, 2)
	assert.Equal(t, account.Id, accounts[0].Id)

	require.NoError(t, service.DeleteAccount(pool.Id, account.Id))
	accounts, err = service.ListAccounts(pool.Id)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	assert.Equal(t, unlimited.Id, accounts[0].Id)
	require.NoError(t, service.DeleteAccount(pool.Id, unlimited.Id))
	accounts, err = service.ListAccounts(pool.Id)
	require.NoError(t, err)
	assert.Empty(t, accounts)
	require.NoError(t, model.DB.First(&storedAfter, account.Id).Error)
	assert.Equal(t, model.AccountPoolAccountStatusDeleted, storedAfter.Status)
}

func TestAccountPoolServiceRejectsEnabledChannelBindingInPhaseOne(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusEnabled)

	_, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.ErrorContains(t, err, "account pool binding requires a disabled channel in phase 1")
}

func TestAccountPoolServiceCreatesDraftBindingForDisabledChannel(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)

	binding, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)

	assert.Equal(t, model.AccountPoolBindingStatusDraft, binding.Status)
	assert.Equal(t, channel.Id, binding.ChannelID)
	assert.Equal(t, channel.Name, binding.ChannelName)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, binding.ChannelStatus)

	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, reloaded.Status)
}

func TestAccountPoolServiceCreateBoundChannelCreatesDisabledChannelAndDraftBinding(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool, err := service.CreatePool(AccountPoolCreateParams{
		Name:                  "pool-with-random-schedule",
		Platform:              model.AccountPoolPlatformOpenAI,
		DefaultSchedulePolicy: AccountPoolSchedulePolicyRandom,
	})
	require.NoError(t, err)

	binding, err := service.CreateBoundChannel(AccountPoolBoundChannelCreateParams{
		PoolID: pool.Id,
		Name:   "  Pool runtime channel  ",
		AccountFilterConfig: AccountPoolAccountFilterConfig{
			AccountIDs: []int{101, 202},
		},
		ModelPolicy: AccountPoolModelPolicy{
			Strategy:    "fixed",
			FixedModels: []string{"gpt-5", "gpt-5-mini"},
		},
		AccountRetryTimes: 2,
	})

	require.NoError(t, err)
	assert.Equal(t, model.AccountPoolBindingStatusDraft, binding.Status)
	assert.Equal(t, "Pool runtime channel", binding.ChannelName)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, binding.ChannelStatus)
	assert.Equal(t, AccountPoolSchedulePolicyRandom, binding.SchedulePolicy)
	assert.Equal(t, 2, binding.AccountRetryTimes)
	var filter AccountPoolAccountFilterConfig
	require.NoError(t, common.UnmarshalJsonStr(binding.AccountFilterConfig, &filter))
	assert.Equal(t, []int{101, 202}, filter.AccountIDs)
	var policy AccountPoolModelPolicy
	require.NoError(t, common.UnmarshalJsonStr(binding.ModelPolicy, &policy))
	assert.Equal(t, AccountPoolModelPolicy{
		Strategy:    "fixed",
		FixedModels: []string{"gpt-5", "gpt-5-mini"},
	}, policy)

	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, binding.ChannelID).Error)
	assert.Equal(t, "Pool runtime channel", channel.Name)
	assert.Equal(t, constant.ChannelTypeOpenAI, channel.Type)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, channel.Status)
	assert.NotEmpty(t, channel.Key)
	enabled, err := AccountPoolRuntimeEnabledForChannel(channel.Id)
	require.NoError(t, err)
	assert.False(t, enabled)

	secondBinding, err := service.CreateBoundChannel(AccountPoolBoundChannelCreateParams{
		PoolID: pool.Id,
		Name:   "Pool runtime channel 2",
	})
	require.NoError(t, err)
	var secondChannel model.Channel
	require.NoError(t, model.DB.First(&secondChannel, secondBinding.ChannelID).Error)
	assert.NotEqual(t, channel.Key, secondChannel.Key)
}

func TestAccountPoolServicePersistsCapabilityAutoDetectSettings(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}

	disabled, err := service.CreatePool(AccountPoolCreateParams{
		Name: "capability-disabled",
	})
	require.NoError(t, err)
	assert.False(t, disabled.CapabilityCheckEnabled)
	assert.Zero(t, disabled.CapabilityCheckIntervalMinutes)
	assert.Equal(t, AccountPoolCapabilityModeModelsEndpoint, disabled.CapabilityCheckMode)
	assert.Equal(t, DefaultAccountPoolCapabilityCheckTimeoutSeconds, disabled.CapabilityCheckTimeoutSeconds)

	pool, err := service.CreatePool(AccountPoolCreateParams{
		Name:                           "capability-auto",
		CapabilityCheckEnabled:         true,
		CapabilityCheckIntervalMinutes: 30,
		CapabilityCheckMode:            AccountPoolCapabilityModeProbeModels,
		CapabilityCheckChannelID:       12,
		CapabilityCheckModels:          []string{"gpt-5", "gpt-5-mini"},
		CapabilityCheckTimeoutSeconds:  45,
		CapabilityCheckMerge:           true,
	})
	require.NoError(t, err)

	assert.True(t, pool.CapabilityCheckEnabled)
	assert.Equal(t, 30, pool.CapabilityCheckIntervalMinutes)
	assert.Equal(t, AccountPoolCapabilityModeProbeModels, pool.CapabilityCheckMode)
	assert.Equal(t, 12, pool.CapabilityCheckChannelID)
	assert.Equal(t, `["gpt-5","gpt-5-mini"]`, pool.CapabilityCheckModels)
	assert.Equal(t, 45, pool.CapabilityCheckTimeoutSeconds)
	assert.True(t, pool.CapabilityCheckMerge)

	updated, err := service.UpdatePool(pool.Id, AccountPoolCreateParams{
		Name:                           "capability-auto-updated",
		CapabilityCheckEnabled:         true,
		CapabilityCheckIntervalMinutes: 0,
		CapabilityCheckMode:            AccountPoolCapabilityModeModelsEndpoint,
		CapabilityCheckModels:          []string{"gpt-5.1"},
		CapabilityCheckTimeoutSeconds:  0,
	})
	require.NoError(t, err)

	assert.True(t, updated.CapabilityCheckEnabled)
	assert.Equal(t, DefaultAccountPoolCapabilityCheckIntervalMinutes, updated.CapabilityCheckIntervalMinutes)
	assert.Equal(t, AccountPoolCapabilityModeModelsEndpoint, updated.CapabilityCheckMode)
	assert.Zero(t, updated.CapabilityCheckChannelID)
	assert.Equal(t, `["gpt-5.1"]`, updated.CapabilityCheckModels)
	assert.Equal(t, DefaultAccountPoolCapabilityCheckTimeoutSeconds, updated.CapabilityCheckTimeoutSeconds)
	assert.False(t, updated.CapabilityCheckMerge)
}

func TestAccountPoolServiceCreateBoundChannelUsesFixedModelsWithoutExplicitStrategy(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	binding, err := service.CreateBoundChannel(AccountPoolBoundChannelCreateParams{
		PoolID: pool.Id,
		Name:   "pool-runtime-channel",
		ModelPolicy: AccountPoolModelPolicy{
			FixedModels: []string{" gpt-5 ", "gpt-5", "gpt-5-mini"},
		},
	})

	require.NoError(t, err)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, binding.ChannelID).Error)
	assert.Equal(t, "gpt-5,gpt-5-mini", channel.Models)
	var count int64
	require.NoError(t, model.DB.Model(&model.Ability{}).Where("channel_id = ?", binding.ChannelID).Count(&count).Error)
	assert.Equal(t, int64(2), count)
}

func TestAccountPoolServiceCreateBoundChannelRejectsBlankName(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	_, err := service.CreateBoundChannel(AccountPoolBoundChannelCreateParams{
		PoolID: pool.Id,
		Name:   "   ",
	})

	require.ErrorContains(t, err, "account pool channel name is required")
}

func TestAccountPoolServiceActivateBindingEnablesAbilitiesButNotChannel(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	binding, err := service.CreateBoundChannel(AccountPoolBoundChannelCreateParams{
		PoolID: pool.Id,
		Name:   "pool-runtime-channel",
		ModelPolicy: AccountPoolModelPolicy{
			Strategy:    "fixed",
			FixedModels: []string{"gpt-5"},
		},
	})
	require.NoError(t, err)

	var ability model.Ability
	require.NoError(t, model.DB.Where("channel_id = ? AND model = ?", binding.ChannelID, "gpt-5").First(&ability).Error)
	assert.False(t, ability.Enabled)

	activated, err := service.ActivateBinding(pool.Id, binding.Id)
	require.NoError(t, err)
	assert.Equal(t, model.AccountPoolBindingStatusEnabled, activated.Status)

	require.NoError(t, model.DB.Where("channel_id = ? AND model = ?", binding.ChannelID, "gpt-5").First(&ability).Error)
	assert.True(t, ability.Enabled)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, binding.ChannelID).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, channel.Status)

	disabled, err := service.DisableBinding(pool.Id, binding.Id)
	require.NoError(t, err)
	assert.Equal(t, model.AccountPoolBindingStatusDisabled, disabled.Status)
	require.NoError(t, model.DB.Where("channel_id = ? AND model = ?", binding.ChannelID, "gpt-5").First(&ability).Error)
	assert.False(t, ability.Enabled)

	_, err = service.ActivateBinding(pool.Id, binding.Id)
	require.NoError(t, err)
	require.NoError(t, service.DeleteBinding(pool.Id, binding.Id))
	require.NoError(t, model.DB.Where("channel_id = ? AND model = ?", binding.ChannelID, "gpt-5").First(&ability).Error)
	assert.False(t, ability.Enabled)
}

func TestAccountPoolServiceActivateBindingEnablesMemoryCacheRoutingButNotChannel(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		model.InitChannelCache()
	})
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	binding, err := service.CreateBoundChannel(AccountPoolBoundChannelCreateParams{
		PoolID: pool.Id,
		Name:   "pool-cache-runtime-channel",
		ModelPolicy: AccountPoolModelPolicy{
			Strategy:    "fixed",
			FixedModels: []string{"gpt-5"},
		},
	})
	require.NoError(t, err)

	_, err = service.ActivateBinding(pool.Id, binding.Id)
	require.NoError(t, err)
	model.InitChannelCache()
	channel, err := model.GetRandomSatisfiedChannel("default", "gpt-5", 0, "/v1/chat/completions")
	require.NoError(t, err)
	require.NotNil(t, channel)
	assert.Equal(t, binding.ChannelID, channel.Id)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, channel.Status)

	_, err = service.DisableBinding(pool.Id, binding.Id)
	require.NoError(t, err)
	model.InitChannelCache()
	channel, err = model.GetRandomSatisfiedChannel("default", "gpt-5", 0, "/v1/chat/completions")
	require.NoError(t, err)
	assert.Nil(t, channel)
}

func TestAccountPoolServiceCreateBindingUsesPoolDefaultSchedulePolicy(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool, err := service.CreatePool(AccountPoolCreateParams{
		Name:                  "pool-with-default-schedule",
		Platform:              model.AccountPoolPlatformOpenAI,
		DefaultSchedulePolicy: "random",
	})
	require.NoError(t, err)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)

	binding, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})

	require.NoError(t, err)
	assert.Equal(t, "random", binding.SchedulePolicy)
}

func TestAccountPoolServiceRejectsUnsupportedSchedulePolicy(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}

	_, err := service.CreatePool(AccountPoolCreateParams{
		Name:                  "invalid-default-policy",
		Platform:              model.AccountPoolPlatformOpenAI,
		DefaultSchedulePolicy: "priority",
	})
	require.ErrorContains(t, err, "account pool schedule policy must be round_robin or random")

	pool := createAccountPoolServiceTestPool(t, service)
	_, err = service.UpdatePool(pool.Id, AccountPoolCreateParams{
		Name:                  pool.Name,
		Platform:              model.AccountPoolPlatformOpenAI,
		DefaultSchedulePolicy: "priority",
	})
	require.ErrorContains(t, err, "account pool schedule policy must be round_robin or random")

	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	_, err = service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:         pool.Id,
		ChannelID:      channel.Id,
		SchedulePolicy: "priority",
	})
	require.ErrorContains(t, err, "account pool schedule policy must be round_robin or random")

	binding, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)
	_, err = service.UpdateBinding(pool.Id, binding.Id, AccountPoolBindingCreateParams{
		ChannelID:      channel.Id,
		SchedulePolicy: "priority",
	})
	require.ErrorContains(t, err, "account pool schedule policy must be round_robin or random")
}

func TestAccountPoolServiceIgnoresLegacyInvalidPoolDefaultSchedulePolicy(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	require.NoError(t, model.DB.Model(&model.AccountPool{}).
		Where("id = ?", pool.Id).
		Update("default_schedule_policy", "priority").Error)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)

	binding, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})

	require.NoError(t, err)
	assert.Equal(t, AccountPoolSchedulePolicyRoundRobin, binding.SchedulePolicy)
}

func TestAccountPoolServiceCreateBindingRejectsNonPhaseOneStatus(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)

	_, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
		Status:    "enabled",
	})

	require.ErrorContains(t, err, "account pool binding status must be draft or disabled in phase 1")
}

func TestAccountPoolServiceRejectsDuplicateBindingChannel(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	otherPool, err := service.CreatePool(AccountPoolCreateParams{
		Name:     "pool-b",
		Platform: model.AccountPoolPlatformOpenAI,
	})
	require.NoError(t, err)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	otherChannel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	binding, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)

	_, err = service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    otherPool.Id,
		ChannelID: channel.Id,
	})
	require.ErrorContains(t, err, "account pool channel is already bound")

	otherBinding, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    otherPool.Id,
		ChannelID: otherChannel.Id,
	})
	require.NoError(t, err)
	_, err = service.UpdateBinding(otherPool.Id, otherBinding.Id, AccountPoolBindingCreateParams{
		ChannelID: channel.Id,
	})
	require.ErrorContains(t, err, "account pool channel is already bound")

	updated, err := service.UpdateBinding(pool.Id, binding.Id, AccountPoolBindingCreateParams{
		ChannelID: channel.Id,
	})
	require.NoError(t, err)
	assert.Equal(t, channel.Id, updated.ChannelID)
}

func TestAccountPoolServiceActivateBindingEnablesRuntimeButNotChannel(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	binding, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)

	activated, err := service.ActivateBinding(pool.Id, binding.Id)

	require.NoError(t, err)
	assert.Equal(t, model.AccountPoolBindingStatusEnabled, activated.Status)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, activated.ChannelStatus)
	assert.True(t, activated.RuntimeEnabled)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, reloaded.Status)
	assert.False(t, model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusEnabled, "manual enable after account pool activation"))
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, reloaded.Status)

	again, err := service.ActivateBinding(pool.Id, binding.Id)
	require.NoError(t, err)
	assert.Equal(t, model.AccountPoolBindingStatusEnabled, again.Status)
	assert.True(t, again.RuntimeEnabled)
}

func TestAccountPoolServiceDisableBindingDisablesRuntime(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	binding, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)
	_, err = service.ActivateBinding(pool.Id, binding.Id)
	require.NoError(t, err)
	enabled, err := AccountPoolRuntimeEnabledForChannel(channel.Id)
	require.NoError(t, err)
	require.True(t, enabled)

	disabled, err := service.DisableBinding(pool.Id, binding.Id)

	require.NoError(t, err)
	assert.Equal(t, model.AccountPoolBindingStatusDisabled, disabled.Status)
	assert.False(t, disabled.RuntimeEnabled)
	enabled, err = AccountPoolRuntimeEnabledForChannel(channel.Id)
	require.NoError(t, err)
	assert.False(t, enabled)
}

func TestAccountPoolServiceDeleteBindingReleasesChannelAndInvalidatesRuntime(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	binding, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)
	_, err = service.ActivateBinding(pool.Id, binding.Id)
	require.NoError(t, err)
	enabled, err := AccountPoolRuntimeEnabledForChannel(channel.Id)
	require.NoError(t, err)
	require.True(t, enabled)

	require.NoError(t, service.DeleteBinding(pool.Id, binding.Id))

	enabled, err = AccountPoolRuntimeEnabledForChannel(channel.Id)
	require.NoError(t, err)
	assert.False(t, enabled)
	var reloadedBinding model.AccountPoolChannelBinding
	require.Error(t, model.DB.First(&reloadedBinding, binding.Id).Error)
	var reloadedChannel model.Channel
	require.NoError(t, model.DB.First(&reloadedChannel, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, reloadedChannel.Status)
	rebound, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)
	assert.Equal(t, channel.Id, rebound.ChannelID)
}

func TestAccountPoolServiceUpdateBindingConfigPreservesRuntimeStatus(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	oldChannel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	newChannel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	binding, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: oldChannel.Id,
	})
	require.NoError(t, err)
	_, err = service.ActivateBinding(pool.Id, binding.Id)
	require.NoError(t, err)
	enabled, err := AccountPoolRuntimeEnabledForChannel(oldChannel.Id)
	require.NoError(t, err)
	require.True(t, enabled)

	updated, err := service.UpdateBinding(pool.Id, binding.Id, AccountPoolBindingCreateParams{
		ChannelID: newChannel.Id,
		AccountFilterConfig: AccountPoolAccountFilterConfig{
			AccountIDs: []int{101, 202},
		},
		ModelPolicy: AccountPoolModelPolicy{
			Strategy:    "fixed",
			FixedModels: []string{"gpt-5", "gpt-5-mini"},
		},
		SchedulePolicy:    "random",
		AccountRetryTimes: 3,
	})

	require.NoError(t, err)
	assert.Equal(t, model.AccountPoolBindingStatusEnabled, updated.Status)
	assert.Equal(t, newChannel.Id, updated.ChannelID)
	assert.Equal(t, 3, updated.AccountRetryTimes)
	assert.Equal(t, "random", updated.SchedulePolicy)
	var filter AccountPoolAccountFilterConfig
	require.NoError(t, common.UnmarshalJsonStr(updated.AccountFilterConfig, &filter))
	assert.Equal(t, []int{101, 202}, filter.AccountIDs)
	var policy AccountPoolModelPolicy
	require.NoError(t, common.UnmarshalJsonStr(updated.ModelPolicy, &policy))
	assert.Equal(t, AccountPoolModelPolicy{
		Strategy:    "fixed",
		FixedModels: []string{"gpt-5", "gpt-5-mini"},
	}, policy)
	enabled, err = AccountPoolRuntimeEnabledForChannel(oldChannel.Id)
	require.NoError(t, err)
	assert.False(t, enabled)
	enabled, err = AccountPoolRuntimeEnabledForChannel(newChannel.Id)
	require.NoError(t, err)
	assert.True(t, enabled)
}

func TestAccountPoolServiceUpdateBindingFixedModelsCreatesRoutingAbilities(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	binding, err := service.CreateBoundChannel(AccountPoolBoundChannelCreateParams{
		PoolID: pool.Id,
		Name:   "pool-runtime-channel",
	})
	require.NoError(t, err)

	var count int64
	require.NoError(t, model.DB.Model(&model.Ability{}).Where("channel_id = ?", binding.ChannelID).Count(&count).Error)
	require.Zero(t, count)

	_, err = service.UpdateBinding(pool.Id, binding.Id, AccountPoolBindingCreateParams{
		ChannelID: binding.ChannelID,
		ModelPolicy: AccountPoolModelPolicy{
			Strategy:    "fixed",
			FixedModels: []string{"gpt-5", "gpt-5-mini"},
		},
	})
	require.NoError(t, err)
	_, err = service.ActivateBinding(pool.Id, binding.Id)
	require.NoError(t, err)

	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, binding.ChannelID).Error)
	assert.Equal(t, "gpt-5,gpt-5-mini", channel.Models)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, channel.Status)
	var gpt5Ability model.Ability
	require.NoError(t, model.DB.Where("channel_id = ? AND model = ?", binding.ChannelID, "gpt-5").First(&gpt5Ability).Error)
	assert.True(t, gpt5Ability.Enabled)
	var miniAbility model.Ability
	require.NoError(t, model.DB.Where("channel_id = ? AND model = ?", binding.ChannelID, "gpt-5-mini").First(&miniAbility).Error)
	assert.True(t, miniAbility.Enabled)
}

func TestAccountPoolServiceUpdateEnabledBindingMovesAbilityToNewChannel(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	binding, err := service.CreateBoundChannel(AccountPoolBoundChannelCreateParams{
		PoolID: pool.Id,
		Name:   "old-runtime-channel",
		ModelPolicy: AccountPoolModelPolicy{
			Strategy:    "fixed",
			FixedModels: []string{"gpt-5"},
		},
	})
	require.NoError(t, err)
	_, err = service.ActivateBinding(pool.Id, binding.Id)
	require.NoError(t, err)
	newChannel := model.Channel{
		Type:   constant.ChannelTypeOpenAI,
		Key:    "new-runtime-key",
		Name:   "new-runtime-channel",
		Status: common.ChannelStatusManuallyDisabled,
		Group:  "default",
		Models: "gpt-5",
	}
	require.NoError(t, model.DB.Create(&newChannel).Error)
	require.NoError(t, newChannel.AddAbilities(nil))

	updated, err := service.UpdateBinding(pool.Id, binding.Id, AccountPoolBindingCreateParams{
		ChannelID: newChannel.Id,
		ModelPolicy: AccountPoolModelPolicy{
			Strategy:    "fixed",
			FixedModels: []string{"gpt-5"},
		},
	})

	require.NoError(t, err)
	assert.Equal(t, model.AccountPoolBindingStatusEnabled, updated.Status)
	assert.Equal(t, newChannel.Id, updated.ChannelID)
	var oldAbility model.Ability
	require.NoError(t, model.DB.Where("channel_id = ? AND model = ?", binding.ChannelID, "gpt-5").First(&oldAbility).Error)
	assert.False(t, oldAbility.Enabled)
	var newAbility model.Ability
	require.NoError(t, model.DB.Where("channel_id = ? AND model = ?", newChannel.Id, "gpt-5").First(&newAbility).Error)
	assert.True(t, newAbility.Enabled)
}

func TestAccountPoolServiceBindingActivationRejectsWrongPoolAndUnsupportedChannel(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	otherPool := model.AccountPool{Name: "other-pool", Platform: model.AccountPoolPlatformOpenAI}
	require.NoError(t, model.DB.Create(&otherPool).Error)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	binding, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)

	_, err = service.ActivateBinding(otherPool.Id, binding.Id)
	require.Error(t, err)

	unsupported := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeMidjourney, common.ChannelStatusManuallyDisabled)
	_, err = service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: unsupported.Id,
	})
	require.ErrorContains(t, err, "OpenAI-compatible")

	legacyBinding := model.AccountPoolChannelBinding{
		PoolID:    pool.Id,
		ChannelID: unsupported.Id,
		Status:    model.AccountPoolBindingStatusDraft,
	}
	require.NoError(t, model.DB.Create(&legacyBinding).Error)
	_, err = service.ActivateBinding(pool.Id, legacyBinding.Id)
	require.ErrorContains(t, err, "OpenAI-compatible")

	anotherUnsupported := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeMidjourney, common.ChannelStatusManuallyDisabled)
	legacyEnabledBinding := model.AccountPoolChannelBinding{
		PoolID:    pool.Id,
		ChannelID: anotherUnsupported.Id,
		Status:    model.AccountPoolBindingStatusEnabled,
	}
	require.NoError(t, model.DB.Create(&legacyEnabledBinding).Error)
	disabled, err := service.DisableBinding(pool.Id, legacyEnabledBinding.Id)
	require.NoError(t, err)
	assert.Equal(t, model.AccountPoolBindingStatusDisabled, disabled.Status)
}

func TestAccountPoolServiceDeletePoolDeletesBindingsAndPreservesChannelStatus(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	binding, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)

	require.NoError(t, service.DeletePool(pool.Id))

	var reloadedChannel model.Channel
	require.NoError(t, model.DB.First(&reloadedChannel, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, reloadedChannel.Status)

	var reloadedBinding model.AccountPoolChannelBinding
	require.Error(t, model.DB.First(&reloadedBinding, binding.Id).Error)

	newPool := createAccountPoolServiceTestPool(t, service)
	rebound, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    newPool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)
	assert.Equal(t, channel.Id, rebound.ChannelID)
}

func TestAccountPoolServiceDeletePoolInvalidatesRuntimeEnabledCache(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	binding, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)
	_, err = service.ActivateBinding(pool.Id, binding.Id)
	require.NoError(t, err)
	enabled, err := AccountPoolRuntimeEnabledForChannel(channel.Id)
	require.NoError(t, err)
	require.True(t, enabled)

	require.NoError(t, service.DeletePool(pool.Id))

	enabled, err = AccountPoolRuntimeEnabledForChannel(channel.Id)
	require.NoError(t, err)
	assert.False(t, enabled)
}

func TestAccountPoolServiceProxyCreateListRedactsPassword(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}

	proxy, err := service.CreateProxy(AccountPoolProxyCreateParams{
		Name:     "proxy-a",
		Protocol: "http",
		Host:     "127.0.0.1",
		Port:     8080,
		Username: "proxy-user",
		Password: "proxy-password-secret",
	})
	require.NoError(t, err)
	assert.True(t, proxy.HasPassword)

	var stored model.AccountPoolProxy
	require.NoError(t, model.DB.First(&stored, proxy.Id).Error)
	assert.NotContains(t, stored.Password, "proxy-password-secret")
	assert.NotContains(t, stored.Password, "proxy-user")

	proxies, err := service.ListProxies()
	require.NoError(t, err)
	require.Len(t, proxies, 1)
	assert.True(t, proxies[0].HasPassword)
	assert.Equal(t, "proxy-user", proxies[0].Username)
}

func TestAccountPoolServiceUpdateProxyPreservesPasswordAndSoftDeletesReferences(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	proxy, err := service.CreateProxy(AccountPoolProxyCreateParams{
		Name:     "proxy-a",
		Protocol: "http",
		Host:     "127.0.0.1",
		Port:     8080,
		Username: "proxy-user",
		Password: "proxy-password-secret",
	})
	require.NoError(t, err)
	fallback, err := service.CreateProxy(AccountPoolProxyCreateParams{
		Name:            "proxy-b",
		Protocol:        "http",
		Host:            "127.0.0.2",
		Port:            8081,
		FallbackProxyID: proxy.Id,
	})
	require.NoError(t, err)
	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "proxied-account",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-proxied",
		},
		ProxyID: proxy.Id,
	})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.AccountPool{}).
		Where("id = ?", pool.Id).
		Update("default_proxy_id", proxy.Id).Error)
	var storedBefore model.AccountPoolProxy
	require.NoError(t, model.DB.First(&storedBefore, proxy.Id).Error)

	updated, err := service.UpdateProxy(proxy.Id, AccountPoolProxyCreateParams{
		Name:            "proxy-updated",
		Protocol:        "socks5",
		Host:            "10.0.0.1",
		Port:            1080,
		Username:        "proxy-user-updated",
		Status:          model.AccountPoolProxyStatusDisabled,
		FallbackProxyID: 0,
	})

	require.NoError(t, err)
	assert.Equal(t, "proxy-updated", updated.Name)
	assert.Equal(t, "socks5", updated.Protocol)
	assert.Equal(t, "10.0.0.1", updated.Host)
	assert.Equal(t, 1080, updated.Port)
	assert.Equal(t, "proxy-user-updated", updated.Username)
	assert.Equal(t, model.AccountPoolProxyStatusDisabled, updated.Status)
	assert.True(t, updated.HasPassword)
	var storedAfter model.AccountPoolProxy
	require.NoError(t, model.DB.First(&storedAfter, proxy.Id).Error)
	assert.Equal(t, storedBefore.Password, storedAfter.Password)
	proxyAuth, err := DecryptAccountPoolProxyAuthConfig(storedAfter.Password)
	require.NoError(t, err)
	assert.Equal(t, "proxy-password-secret", proxyAuth.Password)

	updated, err = service.UpdateProxy(proxy.Id, AccountPoolProxyCreateParams{
		Name:            "proxy-rekeyed",
		Protocol:        "socks5",
		Host:            "10.0.0.1",
		Port:            1080,
		Username:        "proxy-user-updated",
		Password:        "proxy-password-new",
		Status:          model.AccountPoolProxyStatusEnabled,
		FallbackProxyID: 0,
	})
	require.NoError(t, err)
	assert.True(t, updated.HasPassword)
	var storedRekeyed model.AccountPoolProxy
	require.NoError(t, model.DB.First(&storedRekeyed, proxy.Id).Error)
	assert.NotEqual(t, storedAfter.Password, storedRekeyed.Password)
	proxyAuth, err = DecryptAccountPoolProxyAuthConfig(storedRekeyed.Password)
	require.NoError(t, err)
	assert.Equal(t, "proxy-password-new", proxyAuth.Password)

	require.NoError(t, service.DeleteProxy(proxy.Id))
	proxies, err := service.ListProxies()
	require.NoError(t, err)
	require.Len(t, proxies, 1)
	assert.Equal(t, fallback.Id, proxies[0].Id)
	var deletedProxy model.AccountPoolProxy
	require.NoError(t, model.DB.First(&deletedProxy, proxy.Id).Error)
	assert.Equal(t, model.AccountPoolProxyStatusDeleted, deletedProxy.Status)
	var reloadedPool model.AccountPool
	require.NoError(t, model.DB.First(&reloadedPool, pool.Id).Error)
	assert.Zero(t, reloadedPool.DefaultProxyID)
	var reloadedAccount model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloadedAccount, account.Id).Error)
	assert.Zero(t, reloadedAccount.ProxyID)
	var reloadedFallback model.AccountPoolProxy
	require.NoError(t, model.DB.First(&reloadedFallback, fallback.Id).Error)
	assert.Zero(t, reloadedFallback.FallbackProxyID)
}

func TestAccountPoolServiceUpdateProxyRejectsFallbackCycle(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	proxyA, err := service.CreateProxy(AccountPoolProxyCreateParams{
		Name:     "proxy-a",
		Protocol: "http",
		Host:     "127.0.0.1",
		Port:     8080,
	})
	require.NoError(t, err)
	proxyB, err := service.CreateProxy(AccountPoolProxyCreateParams{
		Name:            "proxy-b",
		Protocol:        "http",
		Host:            "127.0.0.2",
		Port:            8081,
		FallbackProxyID: proxyA.Id,
	})
	require.NoError(t, err)

	_, err = service.UpdateProxy(proxyA.Id, AccountPoolProxyCreateParams{
		Name:            proxyA.Name,
		Protocol:        proxyA.Protocol,
		Host:            proxyA.Host,
		Port:            proxyA.Port,
		Status:          model.AccountPoolProxyStatusEnabled,
		FallbackProxyID: proxyB.Id,
	})

	require.ErrorContains(t, err, "cycle")
}

func TestAccountPoolServiceRejectsMissingProxyReferences(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	_, err := service.CreatePool(AccountPoolCreateParams{
		Name:           "missing-default-proxy",
		DefaultProxyID: 999,
	})
	require.ErrorContains(t, err, "account pool proxy not found")

	_, err = service.UpdatePool(pool.Id, AccountPoolCreateParams{
		Name:           pool.Name,
		Platform:       model.AccountPoolPlatformOpenAI,
		DefaultProxyID: 999,
	})
	require.ErrorContains(t, err, "account pool proxy not found")

	_, err = service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID:  pool.Id,
		Name:    "missing-account-proxy",
		ProxyID: 999,
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-missing-proxy",
		},
	})
	require.ErrorContains(t, err, "account pool proxy not found")

	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "valid-account",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-valid",
		},
	})
	require.NoError(t, err)
	_, err = service.UpdateAccount(pool.Id, account.Id, AccountPoolAccountCreateParams{
		Name:    account.Name,
		ProxyID: 999,
	})
	require.ErrorContains(t, err, "account pool proxy not found")

	_, err = service.CreateProxy(AccountPoolProxyCreateParams{
		Name:            "missing-fallback-proxy",
		Protocol:        "http",
		Host:            "127.0.0.1",
		Port:            8080,
		FallbackProxyID: 999,
	})
	require.ErrorContains(t, err, "account pool proxy not found")

	proxy, err := service.CreateProxy(AccountPoolProxyCreateParams{
		Name:     "valid-proxy",
		Protocol: "http",
		Host:     "127.0.0.2",
		Port:     8081,
	})
	require.NoError(t, err)
	_, err = service.UpdateProxy(proxy.Id, AccountPoolProxyCreateParams{
		Name:            proxy.Name,
		Protocol:        proxy.Protocol,
		Host:            proxy.Host,
		Port:            proxy.Port,
		Status:          model.AccountPoolProxyStatusEnabled,
		FallbackProxyID: 999,
	})
	require.ErrorContains(t, err, "account pool proxy not found")
}

func TestAccountPoolServiceRejectsDeletedProxyReferences(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	proxy, err := service.CreateProxy(AccountPoolProxyCreateParams{
		Name:     "proxy-a",
		Protocol: "http",
		Host:     "127.0.0.1",
		Port:     8080,
	})
	require.NoError(t, err)
	require.NoError(t, service.DeleteProxy(proxy.Id))

	_, err = service.UpdatePool(pool.Id, AccountPoolCreateParams{
		Name:           pool.Name,
		Platform:       model.AccountPoolPlatformOpenAI,
		DefaultProxyID: proxy.Id,
	})
	require.ErrorContains(t, err, "account pool proxy not found")

	_, err = service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID:  pool.Id,
		Name:    "deleted-account-proxy",
		ProxyID: proxy.Id,
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-deleted-proxy",
		},
	})
	require.ErrorContains(t, err, "account pool proxy not found")

	_, err = service.CreateProxy(AccountPoolProxyCreateParams{
		Name:            "deleted-fallback-proxy",
		Protocol:        "http",
		Host:            "127.0.0.2",
		Port:            8081,
		FallbackProxyID: proxy.Id,
	})
	require.ErrorContains(t, err, "account pool proxy not found")

	target, err := service.CreateProxy(AccountPoolProxyCreateParams{
		Name:     "valid-target-proxy",
		Protocol: "http",
		Host:     "127.0.0.3",
		Port:     8082,
	})
	require.NoError(t, err)
	_, err = service.UpdateProxy(target.Id, AccountPoolProxyCreateParams{
		Name:            target.Name,
		Protocol:        target.Protocol,
		Host:            target.Host,
		Port:            target.Port,
		Status:          model.AccountPoolProxyStatusEnabled,
		FallbackProxyID: proxy.Id,
	})
	require.ErrorContains(t, err, "account pool proxy not found")
}

func TestAccountPoolServiceListPresenceFlagsDoNotDecryptSecrets(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	_, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "credentialed-account",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-list-presence-secret",
		},
		TokenState: AccountPoolTokenState{
			AccessToken: "access-token-secret",
		},
	})
	require.NoError(t, err)
	_, err = service.CreateProxy(AccountPoolProxyCreateParams{
		Name:     "credentialed-proxy",
		Protocol: "http",
		Host:     "127.0.0.1",
		Port:     8080,
		Password: "proxy-password-secret",
	})
	require.NoError(t, err)

	common.CryptoSecret = "rotated-or-missing-secret"
	common.CryptoSecretStable = false

	accounts, err := service.ListAccounts(pool.Id)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	assert.True(t, accounts[0].HasCredential)
	assert.True(t, accounts[0].HasToken)

	proxies, err := service.ListProxies()
	require.NoError(t, err)
	require.Len(t, proxies, 1)
	assert.True(t, proxies[0].HasPassword)
}

func TestAccountPoolServiceListMethodsReturnBehaviorViews(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)

	createdAccount, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "model-scoped-key",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-list-secret",
		},
		TokenState: AccountPoolTokenState{
			RefreshToken: "refresh-list-secret",
			Version:      1,
		},
		SupportedModels: []string{"gpt-4o", "gpt-4o-mini"},
		ModelMapping: map[string]string{
			"gpt-4o": "upstream-gpt-4o",
		},
		Priority:       10,
		Weight:         20,
		MaxConcurrency: 3,
	})
	require.NoError(t, err)
	createdBinding, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)

	accounts, err := service.ListAccounts(pool.Id)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	assert.Equal(t, createdAccount.Id, accounts[0].Id)
	assert.Equal(t, []string{"gpt-4o", "gpt-4o-mini"}, accounts[0].SupportedModels)
	assert.Equal(t, map[string]string{"gpt-4o": "upstream-gpt-4o"}, accounts[0].ModelMapping)
	assert.True(t, accounts[0].HasCredential)
	assert.True(t, accounts[0].HasToken)
	assert.Equal(t, int64(10), accounts[0].Priority)
	assert.Equal(t, uint(20), accounts[0].Weight)

	bindings, err := service.ListBindings(pool.Id)
	require.NoError(t, err)
	require.Len(t, bindings, 1)
	assert.Equal(t, createdBinding.Id, bindings[0].Id)
	assert.Equal(t, channel.Id, bindings[0].ChannelID)
	assert.Equal(t, channel.Name, bindings[0].ChannelName)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, bindings[0].ChannelStatus)
}

func TestAccountPoolServiceAccountViewReturnsEmptyCapabilityModelsWithoutMetadata(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "no-capability-metadata",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-test",
		},
	})
	require.NoError(t, err)

	require.NotNil(t, account.LastCapabilityCheckModels)
	assert.Empty(t, account.LastCapabilityCheckModels)
}

func TestAccountPoolServiceAccountViewIncludesCapabilityMetadata(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "capability-metadata",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-test",
		},
		SupportedModels: []string{"gpt-5"},
	})
	require.NoError(t, err)

	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", account.Id).
		Updates(map[string]any{
			"last_capability_check_at":     int64(1234),
			"last_capability_check_status": "success",
			"last_capability_check_error":  "capability check failed",
			"last_capability_check_models": `["gpt-5","gpt-5-mini"]`,
		}).Error)

	accounts, err := service.ListAccounts(pool.Id)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	assert.Equal(t, int64(1234), accounts[0].LastCapabilityCheckAt)
	assert.Equal(t, "success", accounts[0].LastCapabilityCheckStatus)
	assert.Equal(t, "capability check failed", accounts[0].LastCapabilityCheckError)
	assert.Equal(t, []string{"gpt-5", "gpt-5-mini"}, accounts[0].LastCapabilityCheckModels)
}

func TestAccountPoolServicePoolCRUDSoftDeletesAndUpdatesZeroValues(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	proxy, err := service.CreateProxy(AccountPoolProxyCreateParams{
		Name:     "pool-default-proxy",
		Protocol: "http",
		Host:     "127.0.0.1",
		Port:     8080,
	})
	require.NoError(t, err)

	pool, err := service.CreatePool(AccountPoolCreateParams{
		Name:                  "  shared pool  ",
		DefaultProxyID:        proxy.Id,
		DefaultMonitorEnabled: true,
		Remark:                "created",
	})
	require.NoError(t, err)
	assert.Equal(t, "shared pool", pool.Name)
	assert.Equal(t, model.AccountPoolPlatformOpenAI, pool.Platform)
	require.NoError(t, model.DB.Model(&model.AccountPool{}).
		Where("id = ?", pool.Id).
		Update("updated_time", int64(100)).Error)

	updated, err := service.UpdatePool(pool.Id, AccountPoolCreateParams{
		Name:                  "shared pool updated",
		Platform:              model.AccountPoolPlatformOpenAI,
		DefaultProxyID:        0,
		DefaultMonitorEnabled: false,
		Remark:                "updated",
	})
	require.NoError(t, err)
	assert.Zero(t, updated.DefaultProxyID)
	assert.False(t, updated.DefaultMonitorEnabled)
	assert.Equal(t, "updated", updated.Remark)
	assert.Greater(t, updated.UpdatedTime, int64(100))

	found, err := service.GetPool(pool.Id)
	require.NoError(t, err)
	assert.Equal(t, updated.Id, found.Id)

	pools, err := service.ListPools()
	require.NoError(t, err)
	require.Len(t, pools, 1)

	require.NoError(t, service.DeletePool(pool.Id))
	pools, err = service.ListPools()
	require.NoError(t, err)
	assert.Empty(t, pools)

	_, err = service.GetPool(pool.Id)
	require.Error(t, err)

	_, err = service.UpdatePool(pool.Id, AccountPoolCreateParams{Name: "should not update"})
	require.Error(t, err)

	var deleted model.AccountPool
	require.NoError(t, model.DB.First(&deleted, pool.Id).Error)
	assert.Equal(t, model.AccountPoolStatusDeleted, deleted.Status)
}

func TestAccountPoolServiceDraftBindingBlocksChannelEnable(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	tag := "account-pool-bound"
	channel.Tag = &tag
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", channel.Id).
		Update("tag", tag).Error)
	_, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)

	channel.Status = common.ChannelStatusEnabled
	err = channel.Update()
	require.ErrorContains(t, err, "account pool bound channel cannot be enabled in phase 1")

	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, reloaded.Status)

	assert.False(t, model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusEnabled, "manual restore"))
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, reloaded.Status)

	require.NoError(t, model.EnableChannelByTag(tag))
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, reloaded.Status)
}

func setupAccountPoolServiceTestDB(t *testing.T) {
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
	common.CryptoSecret = "account-pool-service-test-secret"
	common.CryptoSecretStable = true
	resetAccountPoolRuntimeLeasesForTest()
	resetAccountPoolRuntimeSelectionRecencyForTest()
	resetAccountPoolRuntimeAffinitiesForTest()

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

func createAccountPoolServiceTestPool(t *testing.T, service AccountPoolService) model.AccountPool {
	t.Helper()

	pool, err := service.CreatePool(AccountPoolCreateParams{
		Name:     "pool-a",
		Platform: model.AccountPoolPlatformOpenAI,
	})
	require.NoError(t, err)
	return pool
}

func createAccountPoolServiceTestChannel(t *testing.T, status int) model.Channel {
	t.Helper()

	return createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeOpenAI, status)
}

func createAccountPoolServiceTestChannelWithType(t *testing.T, channelType int, status int) model.Channel {
	t.Helper()

	channel := model.Channel{
		Type:   channelType,
		Key:    "sk-channel",
		Name:   "channel-a",
		Status: status,
	}
	require.NoError(t, model.DB.Create(&channel).Error)
	return channel
}
