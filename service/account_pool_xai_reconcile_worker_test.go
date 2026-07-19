package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListDueAccountPoolXAIOAuthReconcilePoolIDsSkipsHealthyDisabledAndOtherPlatforms(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	now := int64(10_000)
	duePool := createAccountPoolXAIReconcileWorkerPool(t, service, "due", model.AccountPoolPlatformXAI)
	healthyPool := createAccountPoolXAIReconcileWorkerPool(t, service, "healthy", model.AccountPoolPlatformXAI)
	validAccessOnlyPool := createAccountPoolXAIReconcileWorkerPool(t, service, "access-only", model.AccountPoolPlatformXAI)
	disabledPool := createAccountPoolXAIReconcileWorkerPool(t, service, "disabled", model.AccountPoolPlatformXAI)
	otherPool := createAccountPoolXAIReconcileWorkerPool(t, service, "other", model.AccountPoolPlatformOpenAI)
	require.NoError(t, model.DB.Model(&model.AccountPool{}).Where("id = ?", disabledPool.Id).Update("status", model.AccountPoolStatusDisabled).Error)

	createAccountPoolXAIReconcileWorkerAccount(t, service, duePool.Id, "due", model.AccountPoolAccountStatusEnabled,
		AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth, RefreshToken: "refresh-due"},
		AccountPoolTokenState{AccessToken: "expired", ExpiresAt: now - 1})
	createAccountPoolXAIReconcileWorkerAccount(t, service, healthyPool.Id, "healthy", model.AccountPoolAccountStatusEnabled,
		AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth, RefreshToken: "refresh-healthy"},
		AccountPoolTokenState{AccessToken: "healthy", ExpiresAt: now + 3600})
	createAccountPoolXAIReconcileWorkerAccount(t, service, validAccessOnlyPool.Id, "access-only", model.AccountPoolAccountStatusEnabled,
		AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth},
		AccountPoolTokenState{AccessToken: "still-valid", ExpiresAt: now + 3600})
	createAccountPoolXAIReconcileWorkerAccount(t, service, disabledPool.Id, "disabled-pool", model.AccountPoolAccountStatusEnabled,
		AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth, RefreshToken: "refresh-disabled"},
		AccountPoolTokenState{AccessToken: "expired", ExpiresAt: now - 1})
	createAccountPoolXAIReconcileWorkerAccount(t, service, otherPool.Id, "other-platform", model.AccountPoolAccountStatusEnabled,
		AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth, RefreshToken: "refresh-other"},
		AccountPoolTokenState{AccessToken: "expired", ExpiresAt: now - 1})
	createAccountPoolXAIReconcileWorkerAccount(t, service, duePool.Id, "already-expired", model.AccountPoolAccountStatusExpired,
		AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth}, AccountPoolTokenState{})

	poolIDs, err := listDueAccountPoolXAIOAuthReconcilePoolIDs(context.Background(), now, accountPoolXAIOAuthDefaultNearExpiryWindowSeconds)
	require.NoError(t, err)
	assert.Equal(t, []int{duePool.Id}, poolIDs)
}

func TestRunDueAccountPoolXAIOAuthReconcileAppliesAndSkipsOverlappingTick(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	now := int64(20_000)
	pool := createAccountPoolXAIReconcileWorkerPool(t, service, "runner", model.AccountPoolPlatformXAI)
	createAccountPoolXAIReconcileWorkerAccount(t, service, pool.Id, "due", model.AccountPoolAccountStatusEnabled,
		AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth, RefreshToken: "refresh"},
		AccountPoolTokenState{AccessToken: "expired", ExpiresAt: now - 1})

	started := make(chan AccountPoolXAIOAuthReconcileParams, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	setAccountPoolXAIOAuthReconcileRunnerForTest(t, func(ctx context.Context, params AccountPoolXAIOAuthReconcileParams) (AccountPoolXAIOAuthReconcileResult, error) {
		calls.Add(1)
		started <- params
		select {
		case <-ctx.Done():
			return AccountPoolXAIOAuthReconcileResult{}, ctx.Err()
		case <-release:
			return AccountPoolXAIOAuthReconcileResult{PoolID: params.PoolID}, nil
		}
	})
	accountPoolXAIOAuthReconcileWorkerRunning.Store(false)
	t.Cleanup(func() { accountPoolXAIOAuthReconcileWorkerRunning.Store(false) })

	firstDone := make(chan struct{})
	go func() {
		runDueAccountPoolXAIOAuthReconcileOnce(context.Background(), now)
		close(firstDone)
	}()

	var params AccountPoolXAIOAuthReconcileParams
	select {
	case params = <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first reconcile worker tick")
	}
	assert.Equal(t, pool.Id, params.PoolID)
	assert.False(t, params.DryRun)
	assert.Equal(t, accountPoolXAIOAuthDefaultNearExpiryWindowSeconds, params.NearExpiryWindowSeconds)
	assert.Equal(t, now, params.Now)

	runDueAccountPoolXAIOAuthReconcileOnce(context.Background(), now)
	assert.Equal(t, int32(1), calls.Load())
	close(release)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first reconcile worker tick to finish")
	}
}

func TestRunDueAccountPoolXAIOAuthReconcileSkipsTickHeldByAnotherInstance(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	now := common.GetTimestamp()
	pool := createAccountPoolXAIReconcileWorkerPool(t, service, "runner-lease", model.AccountPoolPlatformXAI)
	createAccountPoolXAIReconcileWorkerAccount(t, service, pool.Id, "due", model.AccountPoolAccountStatusEnabled,
		AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth, RefreshToken: "refresh"},
		AccountPoolTokenState{AccessToken: "expired", ExpiresAt: now - 1})
	acquired, err := model.AcquireAccountPoolWorkerLease(context.Background(), accountPoolXAIOAuthReconcileLeaseKey, "other-instance", now, 60)
	require.NoError(t, err)
	require.True(t, acquired)

	var calls atomic.Int32
	setAccountPoolXAIOAuthReconcileRunnerForTest(t, func(context.Context, AccountPoolXAIOAuthReconcileParams) (AccountPoolXAIOAuthReconcileResult, error) {
		calls.Add(1)
		return AccountPoolXAIOAuthReconcileResult{}, nil
	})
	accountPoolXAIOAuthReconcileWorkerRunning.Store(false)
	runDueAccountPoolXAIOAuthReconcileOnce(context.Background(), now)
	assert.Zero(t, calls.Load())
}

func TestAccountPoolXAIOAuthReconcileWorkerIntervalUsesPositiveOverride(t *testing.T) {
	t.Setenv(accountPoolXAIOAuthReconcileIntervalEnv, "9")
	assert.Equal(t, 9*time.Minute, loadAccountPoolXAIOAuthReconcileWorkerInterval())

	t.Setenv(accountPoolXAIOAuthReconcileIntervalEnv, "0")
	assert.Equal(t, accountPoolXAIOAuthReconcileDefaultInterval, loadAccountPoolXAIOAuthReconcileWorkerInterval())

	t.Setenv(accountPoolXAIOAuthReconcileIntervalEnv, "9223372036854775807")
	assert.Equal(t, accountPoolXAIOAuthReconcileDefaultInterval, loadAccountPoolXAIOAuthReconcileWorkerInterval())
}

func createAccountPoolXAIReconcileWorkerPool(t *testing.T, service AccountPoolService, name string, platform string) model.AccountPool {
	t.Helper()
	pool, err := service.CreatePool(AccountPoolCreateParams{Name: name, Platform: platform})
	require.NoError(t, err)
	return pool
}

func createAccountPoolXAIReconcileWorkerAccount(
	t *testing.T,
	service AccountPoolService,
	poolID int,
	name string,
	status string,
	credential AccountPoolCredentialConfig,
	state AccountPoolTokenState,
) AccountPoolAccountView {
	t.Helper()
	view, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID:     poolID,
		Name:       name,
		Status:     status,
		Credential: credential,
		TokenState: state,
	})
	require.NoError(t, err)
	if status != "" {
		require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).Where("id = ?", view.Id).Update("status", status).Error)
	}
	return view
}

func setAccountPoolXAIOAuthReconcileRunnerForTest(t *testing.T, runner accountPoolXAIOAuthReconcileRunnerFunc) {
	t.Helper()
	previous := accountPoolXAIOAuthReconcileRunner
	accountPoolXAIOAuthReconcileRunner = runner
	t.Cleanup(func() { accountPoolXAIOAuthReconcileRunner = previous })
}
