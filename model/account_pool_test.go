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
	assert.True(t, DB.Migrator().HasIndex(&AccountPoolAccount{}, "idx_account_pool_status"))
}

// TestAccountPoolAccountOAuthTypeColumnSQLite verifies the production SQLite migration
// contract for the plaintext oauth_type column: AutoMigrate followed by the
// ensure-columns helper must leave the column present and writable/readable, so the
// runtime and account view can route/display oauth_type without decrypting secrets.
func TestAccountPoolAccountOAuthTypeColumnSQLite(t *testing.T) {
	setupAccountPoolTestDB(t)

	oldMainDBType := common.MainDatabaseType()
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() { common.SetMainDatabaseType(oldMainDBType) })

	require.NoError(t, DB.AutoMigrate(&AccountPool{}, &AccountPoolAccount{}, &AccountPoolProxy{}, &AccountPoolChannelBinding{}))
	require.NoError(t, EnsureAccountPoolAccountColumnsSQLite())

	require.True(t, DB.Migrator().HasColumn(&AccountPoolAccount{}, "oauth_type"), "oauth_type column must exist after migration")

	pool := AccountPool{Name: "pool-a", Platform: AccountPoolPlatformGemini}
	require.NoError(t, DB.Create(&pool).Error)
	account := AccountPoolAccount{PoolID: pool.Id, Name: "ca", OAuthType: "code_assist"}
	require.NoError(t, DB.Create(&account).Error)

	var reloaded AccountPoolAccount
	require.NoError(t, DB.First(&reloaded, account.Id).Error)
	assert.Equal(t, "code_assist", reloaded.OAuthType)

	// oauth_type must be independently updatable (drives the no-secret admin edit path).
	require.NoError(t, DB.Model(&AccountPoolAccount{Id: account.Id}).Update("oauth_type", "ai_studio").Error)
	require.NoError(t, DB.First(&reloaded, account.Id).Error)
	assert.Equal(t, "ai_studio", reloaded.OAuthType)
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
	assert.Zero(t, account.MaxConcurrency)
	assert.Zero(t, account.SuccessCount)
	assert.Zero(t, account.FailureCount)
	assert.Zero(t, account.LastSuccessAt)
	assert.Zero(t, account.LastFailureAt)
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

func TestAccountPoolAccountIsSchedulableAtOverloadUntil(t *testing.T) {
	now := int64(1000000)

	// Enabled account with OverloadUntil in the future: not schedulable
	a := AccountPoolAccount{
		Status:        AccountPoolAccountStatusEnabled,
		OverloadUntil: now + 10,
	}
	assert.False(t, a.IsSchedulableAt(now))

	// OverloadUntil in the past: schedulable
	a.OverloadUntil = now - 1
	assert.True(t, a.IsSchedulableAt(now))
}

func TestAccountPoolAccountIsSchedulableAtExpiryAutoPause(t *testing.T) {
	now := int64(1000000)

	// AutoPauseOnExpired=true, ExpiresAt in the past: not schedulable (expired)
	a := AccountPoolAccount{
		Status:             AccountPoolAccountStatusEnabled,
		AutoPauseOnExpired: true,
		ExpiresAt:          now - 1,
	}
	assert.False(t, a.IsSchedulableAt(now), "expired account with auto-pause enabled must be unschedulable")

	// AutoPauseOnExpired=true, ExpiresAt in the future: schedulable
	a.ExpiresAt = now + 100
	assert.True(t, a.IsSchedulableAt(now), "non-expired account with auto-pause enabled must be schedulable")

	// AutoPauseOnExpired=false, ExpiresAt in the past: schedulable (auto-pause off means expiry ignored)
	a.AutoPauseOnExpired = false
	a.ExpiresAt = now - 1
	assert.True(t, a.IsSchedulableAt(now), "auto-pause disabled: expiry must be ignored")

	// AutoPauseOnExpired=true, ExpiresAt=0 (never expires): schedulable
	a.AutoPauseOnExpired = true
	a.ExpiresAt = 0
	assert.True(t, a.IsSchedulableAt(now), "ExpiresAt=0 means never expires and must be schedulable")
}

func TestAccountPoolAccountQuotaExceededAt(t *testing.T) {
	now := int64(1_000_000)

	cases := []struct {
		name     string
		account  AccountPoolAccount
		now      int64
		exceeded bool
	}{
		{
			name:     "quota 0 is unlimited, never exceeded",
			account:  AccountPoolAccount{RequestQuota: 0, RequestQuotaUsed: 999},
			now:      now,
			exceeded: false,
		},
		{
			name:     "used < quota, no window, not exceeded",
			account:  AccountPoolAccount{RequestQuota: 10, RequestQuotaUsed: 5},
			now:      now,
			exceeded: false,
		},
		{
			name:     "used == quota, no window, exceeded",
			account:  AccountPoolAccount{RequestQuota: 10, RequestQuotaUsed: 10},
			now:      now,
			exceeded: true,
		},
		{
			name:     "used > quota, no window, exceeded",
			account:  AccountPoolAccount{RequestQuota: 10, RequestQuotaUsed: 15},
			now:      now,
			exceeded: true,
		},
		{
			name: "window elapsed: counter resets logically to 0, not exceeded",
			account: AccountPoolAccount{
				RequestQuota:              5,
				RequestQuotaUsed:          5,
				RequestQuotaWindowStart:   now - 100,
				RequestQuotaWindowSeconds: 50, // window ended at now-50
			},
			now:      now,
			exceeded: false,
		},
		{
			name: "within window, used >= quota, exceeded",
			account: AccountPoolAccount{
				RequestQuota:              5,
				RequestQuotaUsed:          5,
				RequestQuotaWindowStart:   now - 10,
				RequestQuotaWindowSeconds: 100, // window ends at now+90
			},
			now:      now,
			exceeded: true,
		},
		{
			name: "window seconds > 0 but window start is 0, uses actual used count",
			account: AccountPoolAccount{
				RequestQuota:              5,
				RequestQuotaUsed:          5,
				RequestQuotaWindowStart:   0,
				RequestQuotaWindowSeconds: 100,
			},
			now:      now,
			exceeded: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.exceeded, tc.account.QuotaExceededAt(tc.now))
		})
	}
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

func TestAccountPoolProxyRejectsFallbackCycle(t *testing.T) {
	setupAccountPoolTestDB(t)
	require.NoError(t, DB.AutoMigrate(&AccountPoolProxy{}))

	proxyA := AccountPoolProxy{Name: "proxy-a", Protocol: "http", Host: "127.0.0.1", Port: 8080}
	proxyB := AccountPoolProxy{Name: "proxy-b", Protocol: "http", Host: "127.0.0.2", Port: 8081}
	require.NoError(t, DB.Create(&proxyA).Error)
	require.NoError(t, DB.Create(&proxyB).Error)

	proxyB.FallbackProxyID = proxyA.Id
	require.NoError(t, DB.Save(&proxyB).Error)

	proxyA.FallbackProxyID = proxyB.Id
	assert.ErrorContains(t, DB.Save(&proxyA).Error, "fallback proxy cannot form a cycle")
}
