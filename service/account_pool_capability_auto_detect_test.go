package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListDueAccountPoolCapabilityAutoDetectJobs(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	now := int64(10_000)

	duePool, err := service.CreatePool(AccountPoolCreateParams{
		Name:                           "due",
		CapabilityCheckEnabled:         true,
		CapabilityCheckIntervalMinutes: 30,
		CapabilityCheckMode:            AccountPoolCapabilityModeProbeModels,
		CapabilityCheckChannelID:       42,
		CapabilityCheckModels:          []string{"gpt-5", "gpt-5-mini"},
		CapabilityCheckTimeoutSeconds:  45,
		CapabilityCheckMerge:           true,
	})
	require.NoError(t, err)
	dueAccount := createAccountPoolCapabilityAutoDetectAccount(t, service, duePool.Id, "due", model.AccountPoolAccountStatusEnabled, now-31*60)
	recentAccount := createAccountPoolCapabilityAutoDetectAccount(t, service, duePool.Id, "recent", model.AccountPoolAccountStatusEnabled, now-10*60)
	disabledAccount := createAccountPoolCapabilityAutoDetectAccount(t, service, duePool.Id, "disabled", model.AccountPoolAccountStatusDisabled, 0)
	deletedAccount := createAccountPoolCapabilityAutoDetectAccount(t, service, duePool.Id, "deleted", model.AccountPoolAccountStatusDeleted, 0)

	disabledPool, err := service.CreatePool(AccountPoolCreateParams{
		Name:                           "disabled-schedule",
		CapabilityCheckEnabled:         false,
		CapabilityCheckIntervalMinutes: 5,
	})
	require.NoError(t, err)
	createAccountPoolCapabilityAutoDetectAccount(t, service, disabledPool.Id, "disabled-pool-account", model.AccountPoolAccountStatusEnabled, 0)

	jobs, err := listDueAccountPoolCapabilityAutoDetectJobs(context.Background(), now)
	require.NoError(t, err)
	require.Len(t, jobs, 1)

	assert.Equal(t, duePool.Id, jobs[0].pool.Id)
	assert.Equal(t, []int{dueAccount.Id}, jobs[0].accountIDs)
	assert.Equal(t, []int{dueAccount.Id}, jobs[0].request.AccountIDs)
	assert.Equal(t, duePool.Id, jobs[0].request.PoolID)
	assert.Equal(t, AccountPoolCapabilityModeProbeModels, jobs[0].request.Mode)
	assert.Equal(t, 42, jobs[0].request.ChannelID)
	assert.Equal(t, []string{"gpt-5", "gpt-5-mini"}, jobs[0].request.CandidateModels)
	assert.True(t, jobs[0].request.Apply)
	assert.True(t, jobs[0].request.Merge)
	assert.Equal(t, 45, jobs[0].request.TimeoutSeconds)

	assert.NotContains(t, jobs[0].accountIDs, recentAccount.Id)
	assert.NotContains(t, jobs[0].accountIDs, disabledAccount.Id)
	assert.NotContains(t, jobs[0].accountIDs, deletedAccount.Id)
}

func TestRunDueAccountPoolCapabilityAutoDetectUsesPoolSettings(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	now := int64(20_000)

	pool, err := service.CreatePool(AccountPoolCreateParams{
		Name:                           "runner",
		CapabilityCheckEnabled:         true,
		CapabilityCheckIntervalMinutes: 15,
		CapabilityCheckMode:            AccountPoolCapabilityModeModelsEndpoint,
		CapabilityCheckChannelID:       77,
		CapabilityCheckModels:          []string{"gpt-5.1"},
		CapabilityCheckTimeoutSeconds:  20,
	})
	require.NoError(t, err)
	account := createAccountPoolCapabilityAutoDetectAccount(t, service, pool.Id, "runner-account", model.AccountPoolAccountStatusEnabled, 0)

	var captured []AccountPoolCapabilityDetectRequest
	results := runDueAccountPoolCapabilityAutoDetectWithDetector(context.Background(), now, func(ctx context.Context, req AccountPoolCapabilityDetectRequest) (AccountPoolCapabilityPoolResult, error) {
		captured = append(captured, req)
		return AccountPoolCapabilityPoolResult{
			Total:     len(req.AccountIDs),
			Succeeded: len(req.AccountIDs),
		}, nil
	})

	require.Len(t, results, 1)
	assert.Equal(t, 1, results[0].Total)
	assert.Equal(t, 1, results[0].Succeeded)
	require.Len(t, captured, 1)
	assert.Equal(t, pool.Id, captured[0].PoolID)
	assert.Equal(t, []int{account.Id}, captured[0].AccountIDs)
	assert.Equal(t, 77, captured[0].ChannelID)
	assert.Equal(t, AccountPoolCapabilityModeModelsEndpoint, captured[0].Mode)
	assert.Equal(t, []string{"gpt-5.1"}, captured[0].CandidateModels)
	assert.True(t, captured[0].Apply)
	assert.False(t, captured[0].Merge)
	assert.Equal(t, 20, captured[0].TimeoutSeconds)
}

func TestRunDueAccountPoolCapabilityAutoDetectRecordsDetectorErrors(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	now := int64(30_000)

	pool, err := service.CreatePool(AccountPoolCreateParams{
		Name:                           "detector-error",
		CapabilityCheckEnabled:         true,
		CapabilityCheckIntervalMinutes: 15,
	})
	require.NoError(t, err)
	createAccountPoolCapabilityAutoDetectAccount(t, service, pool.Id, "failing-account", model.AccountPoolAccountStatusEnabled, 0)

	results := runDueAccountPoolCapabilityAutoDetectWithDetector(context.Background(), now, func(ctx context.Context, req AccountPoolCapabilityDetectRequest) (AccountPoolCapabilityPoolResult, error) {
		return AccountPoolCapabilityPoolResult{}, errors.New("detector unavailable")
	})

	require.Len(t, results, 1)
	assert.Equal(t, 1, results[0].Total)
	assert.Equal(t, 1, results[0].Failed)
	assert.Contains(t, results[0].Results[0].Errors, "detector unavailable")
}

func TestAccountPoolCapabilityAutoDetectJobTimeoutScalesWithAccountCount(t *testing.T) {
	job := accountPoolCapabilityAutoDetectJob{
		accountIDs: []int{1, 2, 3},
		request: AccountPoolCapabilityDetectRequest{
			TimeoutSeconds: 20,
		},
	}

	assert.Equal(t, 70*time.Second, accountPoolCapabilityAutoDetectJobTimeout(job))

	job.accountIDs = make([]int, 100)
	assert.Equal(t, accountPoolCapabilityAutoDetectMaxJobTimeout, accountPoolCapabilityAutoDetectJobTimeout(job))
}

func TestStartAccountPoolCapabilityAutoDetectWorkerCancellationClosesStableDone(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	oldMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = oldMaster })
	resetAccountPoolCapabilityAutoDetectWorkerForTest(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := StartAccountPoolCapabilityAutoDetectWorker(ctx)
	assert.Equal(t, done, StartAccountPoolCapabilityAutoDetectWorker(context.Background()), "repeated starts must return the stable done channel")
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for capability auto-detect worker shutdown")
	}
}

func TestStartAccountPoolCapabilityAutoDetectWorkerAlreadyCanceledSkipsInitialTick(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	now := common.GetTimestamp()
	account := model.AccountPoolAccount{
		PoolID:             pool.Id,
		Name:               "canceled-initial-tick",
		Status:             model.AccountPoolAccountStatusEnabled,
		ExpiresAt:          now - 1,
		AutoPauseOnExpired: true,
	}
	require.NoError(t, model.DB.Create(&account).Error)

	oldMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = oldMaster })
	resetAccountPoolCapabilityAutoDetectWorkerForTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := StartAccountPoolCapabilityAutoDetectWorker(ctx)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for already-canceled capability worker")
	}

	var stored model.AccountPoolAccount
	require.NoError(t, model.DB.First(&stored, account.Id).Error)
	assert.Equal(t, model.AccountPoolAccountStatusEnabled, stored.Status, "an already-canceled worker must not run its initial expiry sweep")
}

func TestRunDueAccountPoolCapabilityAutoDetectCancellationStopsBeforeNextJob(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	now := int64(40_000)
	for _, name := range []string{"cancel-first", "cancel-second"} {
		pool, err := service.CreatePool(AccountPoolCreateParams{
			Name:                           name,
			CapabilityCheckEnabled:         true,
			CapabilityCheckIntervalMinutes: 15,
		})
		require.NoError(t, err)
		createAccountPoolCapabilityAutoDetectAccount(t, service, pool.Id, name+"-account", model.AccountPoolAccountStatusEnabled, 0)
	}
	jobs, err := listDueAccountPoolCapabilityAutoDetectJobs(context.Background(), now)
	require.NoError(t, err)
	require.Len(t, jobs, 2)

	ctx, cancel := context.WithCancel(context.Background())
	detectorStarted := make(chan struct{}, 1)
	detectorCalls := 0
	resultCh := make(chan []AccountPoolCapabilityPoolResult, 1)
	go func() {
		resultCh <- runDueAccountPoolCapabilityAutoDetectWithDetector(ctx, now, func(ctx context.Context, _ AccountPoolCapabilityDetectRequest) (AccountPoolCapabilityPoolResult, error) {
			detectorCalls++
			detectorStarted <- struct{}{}
			<-ctx.Done()
			return AccountPoolCapabilityPoolResult{}, ctx.Err()
		})
	}()

	select {
	case <-detectorStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the first capability detector")
	}
	cancel()

	var results []AccountPoolCapabilityPoolResult
	select {
	case results = <-resultCh:
	case <-time.After(time.Second):
		t.Fatal("capability runner did not return after cancellation")
	}
	assert.Equal(t, 1, detectorCalls, "shutdown cancellation must not start a later job")
	assert.Empty(t, results, "shutdown cancellation must not be recorded as a capability failure")
}

func resetAccountPoolCapabilityAutoDetectWorkerForTest(t *testing.T) {
	t.Helper()
	accountPoolCapabilityAutoDetectOnce = sync.Once{}
	accountPoolCapabilityAutoDetectDone = make(chan struct{})
	accountPoolCapabilityAutoDetectRunning.Store(false)
	t.Cleanup(func() {
		accountPoolCapabilityAutoDetectOnce = sync.Once{}
		accountPoolCapabilityAutoDetectDone = make(chan struct{})
		accountPoolCapabilityAutoDetectRunning.Store(false)
	})
}

func createAccountPoolCapabilityAutoDetectAccount(t *testing.T, service AccountPoolService, poolID int, name string, status string, lastCapabilityCheckAt int64) model.AccountPoolAccount {
	t.Helper()

	view, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: poolID,
		Name:   name,
		Status: status,
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-" + name,
		},
	})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", view.Id).
		Updates(map[string]any{
			"status":                   status,
			"last_capability_check_at": lastCapabilityCheckAt,
		}).Error)

	var account model.AccountPoolAccount
	require.NoError(t, model.DB.First(&account, view.Id).Error)
	return account
}
