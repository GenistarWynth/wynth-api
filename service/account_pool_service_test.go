package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
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
	assert.NotContains(t, account.CredentialPreview, "sk-secret")
	assert.Equal(t, MaskAccountPoolSecretValue("sk-secret"), account.CredentialPreview)
	assert.False(t, account.HasToken)
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
	assert.NotContains(t, accounts[0].CredentialPreview, "sk-list-secret")
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

func TestAccountPoolServicePoolCRUDSoftDeletesAndUpdatesZeroValues(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}

	pool, err := service.CreatePool(AccountPoolCreateParams{
		Name:                  "  shared pool  ",
		DefaultProxyID:        123,
		DefaultMonitorEnabled: true,
		Remark:                "created",
	})
	require.NoError(t, err)
	assert.Equal(t, "shared pool", pool.Name)
	assert.Equal(t, model.AccountPoolPlatformOpenAI, pool.Platform)

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

	require.NoError(t, model.DB.AutoMigrate(
		&model.Channel{},
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

	channel := model.Channel{
		Type:   1,
		Key:    "sk-channel",
		Name:   "channel-a",
		Status: status,
	}
	require.NoError(t, model.DB.Create(&channel).Error)
	return channel
}
