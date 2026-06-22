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
	require.NoError(t, DB.AutoMigrate(&AccountPool{}))

	pool := AccountPool{
		Name:     "self-hosted",
		Platform: AccountPoolPlatformOpenAI,
	}
	require.NoError(t, DB.Create(&pool).Error)

	assert.Equal(t, AccountPoolStatusEnabled, pool.Status)
	assert.NotZero(t, pool.CreatedTime)
	assert.NotZero(t, pool.UpdatedTime)
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
