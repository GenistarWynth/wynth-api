package model

import (
	"context"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAccountPoolWorkerLeaseAcquireContentionAndExpiry(t *testing.T) {
	oldDB := DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	DB = db
	require.NoError(t, db.AutoMigrate(&AccountPoolWorkerLease{}))
	oldClock := accountPoolWorkerLeaseNow
	now := int64(1_000)
	accountPoolWorkerLeaseNow = func(context.Context) (int64, error) { return now, nil }
	t.Cleanup(func() {
		DB = oldDB
		accountPoolWorkerLeaseNow = oldClock
	})

	acquired, err := AcquireAccountPoolWorkerLease(context.Background(), "xai-probe", "owner-a", 60)
	require.NoError(t, err)
	assert.True(t, acquired)
	var stored AccountPoolWorkerLease
	require.NoError(t, DB.Where("lease_key = ?", "xai-probe").First(&stored).Error)
	assert.Equal(t, int64(1_060), stored.ExpiresAt, "lease expiry must derive from the shared database clock")

	acquired, err = AcquireAccountPoolWorkerLease(context.Background(), "xai-probe", "owner-b", 60)
	require.NoError(t, err)
	assert.False(t, acquired)

	now = 1_061
	acquired, err = AcquireAccountPoolWorkerLease(context.Background(), "xai-probe", "owner-b", 60)
	require.NoError(t, err)
	assert.True(t, acquired)

	now = 1_062
	renewed, err := RenewAccountPoolWorkerLease(context.Background(), "xai-probe", "owner-a", 60)
	require.NoError(t, err)
	assert.False(t, renewed)
	renewed, err = RenewAccountPoolWorkerLease(context.Background(), "xai-probe", "owner-b", 60)
	require.NoError(t, err)
	assert.True(t, renewed)

	released, err := ReleaseAccountPoolWorkerLease(context.Background(), "xai-probe", "owner-a")
	require.NoError(t, err)
	assert.False(t, released)
	released, err = ReleaseAccountPoolWorkerLease(context.Background(), "xai-probe", "owner-b")
	require.NoError(t, err)
	assert.True(t, released)
}

func TestAccountPoolWorkerLeaseAllowsOnlyOneConcurrentOwner(t *testing.T) {
	oldDB := DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	DB = db
	require.NoError(t, db.AutoMigrate(&AccountPoolWorkerLease{}))
	oldClock := accountPoolWorkerLeaseNow
	accountPoolWorkerLeaseNow = func(context.Context) (int64, error) { return 2_000, nil }
	t.Cleanup(func() {
		DB = oldDB
		accountPoolWorkerLeaseNow = oldClock
	})

	const contenders = 8
	results := make(chan bool, contenders)
	errors := make(chan error, contenders)
	var wait sync.WaitGroup
	for index := 0; index < contenders; index++ {
		wait.Add(1)
		go func(owner string) {
			defer wait.Done()
			acquired, err := AcquireAccountPoolWorkerLease(context.Background(), "oauth-reconcile", owner, 60)
			results <- acquired
			errors <- err
		}(string(rune('a' + index)))
	}
	wait.Wait()
	close(results)
	close(errors)

	acquiredCount := 0
	for acquired := range results {
		if acquired {
			acquiredCount++
		}
	}
	for err := range errors {
		require.NoError(t, err)
	}
	assert.Equal(t, 1, acquiredCount)
}
