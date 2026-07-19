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
	t.Cleanup(func() { DB = oldDB })

	acquired, err := AcquireAccountPoolWorkerLease(context.Background(), "xai-probe", "owner-a", 1_000, 60)
	require.NoError(t, err)
	assert.True(t, acquired)

	acquired, err = AcquireAccountPoolWorkerLease(context.Background(), "xai-probe", "owner-b", 1_000, 60)
	require.NoError(t, err)
	assert.False(t, acquired)

	acquired, err = AcquireAccountPoolWorkerLease(context.Background(), "xai-probe", "owner-b", 1_061, 60)
	require.NoError(t, err)
	assert.True(t, acquired)

	renewed, err := RenewAccountPoolWorkerLease(context.Background(), "xai-probe", "owner-a", 1_062, 60)
	require.NoError(t, err)
	assert.False(t, renewed)
	renewed, err = RenewAccountPoolWorkerLease(context.Background(), "xai-probe", "owner-b", 1_062, 60)
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
	t.Cleanup(func() { DB = oldDB })

	const contenders = 8
	results := make(chan bool, contenders)
	errors := make(chan error, contenders)
	var wait sync.WaitGroup
	for index := 0; index < contenders; index++ {
		wait.Add(1)
		go func(owner string) {
			defer wait.Done()
			acquired, err := AcquireAccountPoolWorkerLease(context.Background(), "oauth-reconcile", owner, 2_000, 60)
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
