package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAccountPoolTestDB(t *testing.T) {
	t.Helper()

	oldDB := DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	DB = db
	t.Cleanup(func() {
		DB = oldDB
	})
}

func TestAccountPoolAutoMigrateSQLite(t *testing.T) {
	setupAccountPoolTestDB(t)

	require.NoError(t, DB.AutoMigrate(
		&Channel{},
		&AccountPool{},
		&AccountPoolAccount{},
		&AccountPoolProxy{},
		&AccountPoolChannelBinding{},
	))
}

func TestAccountPoolModelDefaults(t *testing.T) {
	setupAccountPoolTestDB(t)
	require.NoError(t, DB.AutoMigrate(&AccountPool{}, &AccountPoolAccount{}, &AccountPoolProxy{}, &AccountPoolChannelBinding{}))

	pool := AccountPool{
		Name:     "self-hosted",
		Platform: AccountPoolPlatformOpenAI,
	}
	require.NoError(t, DB.Create(&pool).Error)

	assert.Equal(t, AccountPoolStatusEnabled, pool.Status)
	assert.NotZero(t, pool.CreatedTime)
	assert.NotZero(t, pool.UpdatedTime)

	account := AccountPoolAccount{
		PoolID: pool.Id,
		Name:   "account-a",
	}
	require.NoError(t, DB.Create(&account).Error)
	assert.Equal(t, AccountPoolAccountStatusEnabled, account.Status)
	assert.Equal(t, 1, account.MaxConcurrency)
	assert.NotZero(t, account.CreatedTime)
	assert.NotZero(t, account.UpdatedTime)

	proxy := AccountPoolProxy{
		Name:     "proxy-a",
		Protocol: "http",
		Host:     "127.0.0.1",
		Port:     8080,
	}
	require.NoError(t, DB.Create(&proxy).Error)
	assert.Equal(t, AccountPoolProxyStatusEnabled, proxy.Status)
	assert.NotZero(t, proxy.CreatedTime)
	assert.NotZero(t, proxy.UpdatedTime)

	binding := AccountPoolChannelBinding{
		PoolID:    pool.Id,
		ChannelID: 1,
	}
	require.NoError(t, DB.Create(&binding).Error)
	assert.Equal(t, AccountPoolBindingStatusDraft, binding.Status)
	assert.NotZero(t, binding.CreatedTime)
	assert.NotZero(t, binding.UpdatedTime)
}

func TestAccountPoolUpdatePreservesExistingStatus(t *testing.T) {
	setupAccountPoolTestDB(t)
	require.NoError(t, DB.AutoMigrate(&AccountPool{}, &AccountPoolAccount{}, &AccountPoolProxy{}, &AccountPoolChannelBinding{}))

	pool := AccountPool{
		Name:     "pool-a",
		Platform: AccountPoolPlatformOpenAI,
		Status:   AccountPoolStatusDisabled,
	}
	require.NoError(t, DB.Create(&pool).Error)
	require.NoError(t, DB.Model(&AccountPool{Id: pool.Id}).Updates(AccountPool{
		Name:   "pool-renamed",
		Remark: "updated",
	}).Error)
	var reloadedPool AccountPool
	require.NoError(t, DB.First(&reloadedPool, pool.Id).Error)
	assert.Equal(t, AccountPoolStatusDisabled, reloadedPool.Status)

	account := AccountPoolAccount{
		PoolID: pool.Id,
		Name:   "account-a",
		Status: AccountPoolAccountStatusDisabled,
	}
	require.NoError(t, DB.Create(&account).Error)
	require.NoError(t, DB.Model(&AccountPoolAccount{Id: account.Id}).Updates(AccountPoolAccount{
		Name:      "account-renamed",
		LastError: "updated",
	}).Error)
	var reloadedAccount AccountPoolAccount
	require.NoError(t, DB.First(&reloadedAccount, account.Id).Error)
	assert.Equal(t, AccountPoolAccountStatusDisabled, reloadedAccount.Status)

	proxy := AccountPoolProxy{
		Name:     "proxy-a",
		Protocol: "http",
		Host:     "127.0.0.1",
		Port:     8080,
		Status:   AccountPoolProxyStatusDisabled,
	}
	require.NoError(t, DB.Create(&proxy).Error)
	require.NoError(t, DB.Model(&AccountPoolProxy{Id: proxy.Id}).Updates(AccountPoolProxy{
		Name: "proxy-renamed",
		Host: "127.0.0.2",
	}).Error)
	var reloadedProxy AccountPoolProxy
	require.NoError(t, DB.First(&reloadedProxy, proxy.Id).Error)
	assert.Equal(t, AccountPoolProxyStatusDisabled, reloadedProxy.Status)

	binding := AccountPoolChannelBinding{
		PoolID:    pool.Id,
		ChannelID: 1,
		Status:    AccountPoolBindingStatusDisabled,
	}
	require.NoError(t, DB.Create(&binding).Error)
	require.NoError(t, DB.Model(&AccountPoolChannelBinding{Id: binding.Id}).Updates(AccountPoolChannelBinding{
		AccountRetryTimes: 2,
		SchedulePolicy:    `{"policy":"updated"}`,
	}).Error)
	var reloadedBinding AccountPoolChannelBinding
	require.NoError(t, DB.First(&reloadedBinding, binding.Id).Error)
	assert.Equal(t, AccountPoolBindingStatusDisabled, reloadedBinding.Status)
}

func TestAccountPoolBindingChannelIsUnique(t *testing.T) {
	setupAccountPoolTestDB(t)
	require.NoError(t, DB.AutoMigrate(&Channel{}, &AccountPool{}, &AccountPoolChannelBinding{}))

	channel := Channel{
		Type: 1,
		Key:  "test-key",
		Name: "channel-a",
	}
	require.NoError(t, DB.Create(&channel).Error)

	poolA := AccountPool{Name: "pool-a", Platform: AccountPoolPlatformOpenAI}
	require.NoError(t, DB.Create(&poolA).Error)
	poolB := AccountPool{Name: "pool-b", Platform: AccountPoolPlatformOpenAI}
	require.NoError(t, DB.Create(&poolB).Error)

	require.NoError(t, DB.Create(&AccountPoolChannelBinding{
		PoolID:    poolA.Id,
		ChannelID: channel.Id,
	}).Error)
	assert.Error(t, DB.Create(&AccountPoolChannelBinding{
		PoolID:    poolB.Id,
		ChannelID: channel.Id,
	}).Error)
}

func TestAccountPoolAccountSchedulabilityDerivesTransientState(t *testing.T) {
	now := common.GetTimestamp()
	account := AccountPoolAccount{
		Status:           AccountPoolAccountStatusEnabled,
		RateLimitedUntil: now + 60,
	}

	assert.False(t, account.IsSchedulableAt(now))
	assert.True(t, account.IsSchedulableAt(now+61))

	account.TempDisabledUntil = now + 120
	assert.False(t, account.IsSchedulableAt(now+61))

	account.TempDisabledUntil = 0
	account.Status = AccountPoolAccountStatusDisabled
	assert.False(t, account.IsSchedulableAt(now+61))
}

func TestAccountPoolProxyRejectsSelfFallback(t *testing.T) {
	setupAccountPoolTestDB(t)
	require.NoError(t, DB.AutoMigrate(&AccountPoolProxy{}))

	proxy := AccountPoolProxy{
		Name:     "proxy-a",
		Protocol: "http",
		Host:     "127.0.0.1",
		Port:     8080,
	}
	require.NoError(t, DB.Create(&proxy).Error)

	proxy.FallbackProxyID = proxy.Id
	assert.ErrorContains(t, DB.Save(&proxy).Error, "fallback proxy cannot reference itself")
}
