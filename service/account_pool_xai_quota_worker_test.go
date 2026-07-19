package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountPoolXAIQuotaCreateProbeIsBestEffortAndAsync(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, service, model.AccountPoolPlatformXAI)

	started := make(chan accountPoolXAIQuotaProbeCandidate, 1)
	release := make(chan struct{})
	finished := make(chan struct{})
	setAccountPoolXAIQuotaProbeRunnerForTest(t, func(_ context.Context, poolID int, accountID int) (AccountPoolXAIQuotaSnapshot, error) {
		started <- accountPoolXAIQuotaProbeCandidate{PoolID: poolID, AccountID: accountID}
		<-release
		close(finished)
		return AccountPoolXAIQuotaSnapshot{}, errors.New("probe unavailable")
	})
	accountPoolXAIQuotaCreateProbeEnabled.Store(true)
	t.Cleanup(func() { accountPoolXAIQuotaCreateProbeEnabled.Store(false) })

	type createResult struct {
		account AccountPoolAccountView
		err     error
	}
	resultChannel := make(chan createResult, 1)
	go func() {
		account, err := service.CreateAccount(AccountPoolAccountCreateParams{
			PoolID: pool.Id,
			Name:   "created-oauth",
			Credential: AccountPoolCredentialConfig{
				Type:         AccountPoolCredentialTypeOAuth,
				RefreshToken: "refresh-token",
			},
			TokenState: AccountPoolTokenState{AccessToken: "access-token", ExpiresAt: time.Now().Add(time.Hour).Unix()},
		})
		resultChannel <- createResult{account: account, err: err}
	}()

	var result createResult
	select {
	case result = <-resultChannel:
		require.NoError(t, result.err)
	case <-time.After(time.Second):
		t.Fatal("account creation waited for the best-effort quota probe")
	}

	select {
	case candidate := <-started:
		assert.Equal(t, pool.Id, candidate.PoolID)
		assert.Equal(t, result.account.Id, candidate.AccountID)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for create-time quota probe")
	}

	var stored model.AccountPoolAccount
	require.NoError(t, model.DB.First(&stored, result.account.Id).Error)
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fake quota probe completion")
	}
}

func TestAccountPoolXAIQuotaSkipCreateProbeForIneligibleAccount(t *testing.T) {
	tests := []struct {
		name       string
		platform   string
		credential AccountPoolCredentialConfig
		want       bool
	}{
		{name: "xai oauth", platform: model.AccountPoolPlatformXAI, credential: AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth}, want: true},
		{name: "xai api key", platform: model.AccountPoolPlatformXAI, credential: AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeAPIKey}, want: false},
		{name: "openai oauth", platform: model.AccountPoolPlatformOpenAI, credential: AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth}, want: false},
		{name: "grok web cookie", platform: model.AccountPoolPlatformGrokWeb, credential: AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeGrokWebCookie}, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, accountPoolXAIQuotaCreateProbeEligible(test.platform, test.credential))
		})
	}
}

func TestListDueAccountPoolXAIQuotaProbeCandidatesFiltersOrdersAndLimits(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	now := time.Unix(2_000_000_000, 0).UTC()
	xaiPool := createAccountPoolServiceTestPoolWithPlatform(t, service, model.AccountPoolPlatformXAI)
	disabledXAIPool := createAccountPoolServiceTestPoolWithPlatform(t, service, model.AccountPoolPlatformXAI)
	require.NoError(t, model.DB.Model(&model.AccountPool{}).Where("id = ?", disabledXAIPool.Id).Update("status", model.AccountPoolStatusDisabled).Error)
	openAIPool := createAccountPoolServiceTestPoolWithPlatform(t, service, model.AccountPoolPlatformOpenAI)

	missing := createAccountPoolXAIQuotaWorkerTestAccount(t, service, xaiPool.Id, "missing", model.AccountPoolAccountStatusEnabled, nil)
	oldest := createAccountPoolXAIQuotaWorkerTestAccount(t, service, xaiPool.Id, "oldest", model.AccountPoolAccountStatusEnabled, accountPoolXAIQuotaWorkerSnapshot(t, now.Add(-3*time.Hour).Unix()))
	_ = createAccountPoolXAIQuotaWorkerTestAccount(t, service, xaiPool.Id, "stale", model.AccountPoolAccountStatusEnabled, accountPoolXAIQuotaWorkerSnapshot(t, now.Add(-2*time.Hour).Unix()))
	_ = createAccountPoolXAIQuotaWorkerTestAccount(t, service, xaiPool.Id, "fresh", model.AccountPoolAccountStatusEnabled, accountPoolXAIQuotaWorkerSnapshot(t, now.Add(-10*time.Minute).Unix()))
	_ = createAccountPoolXAIQuotaWorkerTestAccount(t, service, xaiPool.Id, "disabled", model.AccountPoolAccountStatusDisabled, nil)
	_ = createAccountPoolXAIQuotaWorkerTestAccount(t, service, disabledXAIPool.Id, "disabled-pool", model.AccountPoolAccountStatusEnabled, nil)
	_ = createAccountPoolXAIQuotaWorkerTestAccount(t, service, openAIPool.Id, "openai", model.AccountPoolAccountStatusEnabled, nil)

	apiKey, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: xaiPool.Id,
		Name:   "api-key",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "xai-key",
		},
	})
	require.NoError(t, err)

	candidates, err := listDueAccountPoolXAIQuotaProbeCandidates(context.Background(), now, time.Hour, 2)
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	assert.Equal(t, []int{missing.Id, oldest.Id}, []int{candidates[0].AccountID, candidates[1].AccountID})
	assert.NotEqual(t, apiKey.Id, candidates[0].AccountID)
	assert.Equal(t, int64(0), candidates[0].FetchedAt)
	assert.Equal(t, now.Add(-3*time.Hour).Unix(), candidates[1].FetchedAt)
}

func TestAccountPoolXAIQuotaWorkerConfigUsesPositiveOverridesAndDefaults(t *testing.T) {
	t.Run("positive overrides", func(t *testing.T) {
		t.Setenv(accountPoolXAIQuotaProbeIntervalEnv, "7")
		t.Setenv(accountPoolXAIQuotaProbeStaleEnv, "23")
		t.Setenv(accountPoolXAIQuotaProbeMaxPerTickEnv, "4")

		config := loadAccountPoolXAIQuotaProbeWorkerConfig()
		assert.Equal(t, 7*time.Minute, config.Interval)
		assert.Equal(t, 23*time.Minute, config.StaleAge)
		assert.Equal(t, 4, config.MaxPerTick)
	})

	t.Run("invalid values use defaults", func(t *testing.T) {
		t.Setenv(accountPoolXAIQuotaProbeIntervalEnv, "0")
		t.Setenv(accountPoolXAIQuotaProbeStaleEnv, "-1")
		t.Setenv(accountPoolXAIQuotaProbeMaxPerTickEnv, "invalid")

		config := loadAccountPoolXAIQuotaProbeWorkerConfig()
		assert.Equal(t, accountPoolXAIQuotaProbeDefaultInterval, config.Interval)
		assert.Equal(t, accountPoolXAIQuotaProbeDefaultStaleAge, config.StaleAge)
		assert.Equal(t, accountPoolXAIQuotaProbeDefaultMaxPerTick, config.MaxPerTick)
	})

	t.Run("overflowing durations use defaults", func(t *testing.T) {
		t.Setenv(accountPoolXAIQuotaProbeIntervalEnv, "9223372036854775807")
		t.Setenv(accountPoolXAIQuotaProbeStaleEnv, "9223372036854775807")

		config := loadAccountPoolXAIQuotaProbeWorkerConfig()
		assert.Equal(t, accountPoolXAIQuotaProbeDefaultInterval, config.Interval)
		assert.Equal(t, accountPoolXAIQuotaProbeDefaultStaleAge, config.StaleAge)
	})
}

func TestRunDueAccountPoolXAIQuotaProbeSkipsOverlappingTick(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, service, model.AccountPoolPlatformXAI)
	_ = createAccountPoolXAIQuotaWorkerTestAccount(t, service, pool.Id, "due", model.AccountPoolAccountStatusEnabled, nil)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	setAccountPoolXAIQuotaProbeRunnerForTest(t, func(ctx context.Context, _, _ int) (AccountPoolXAIQuotaSnapshot, error) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-ctx.Done():
			return AccountPoolXAIQuotaSnapshot{}, ctx.Err()
		case <-release:
			return AccountPoolXAIQuotaSnapshot{}, nil
		}
	})
	accountPoolXAIQuotaProbeWorkerRunning.Store(false)
	t.Cleanup(func() { accountPoolXAIQuotaProbeWorkerRunning.Store(false) })
	config := accountPoolXAIQuotaProbeWorkerConfig{StaleAge: time.Hour, MaxPerTick: 10}
	firstDone := make(chan struct{})
	go func() {
		runDueAccountPoolXAIQuotaProbeOnce(context.Background(), config)
		close(firstDone)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first quota worker tick")
	}
	runDueAccountPoolXAIQuotaProbeOnce(context.Background(), config)
	assert.Equal(t, int32(1), calls.Load())
	close(release)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first quota worker tick to finish")
	}
}

func createAccountPoolXAIQuotaWorkerTestAccount(
	t *testing.T,
	service AccountPoolService,
	poolID int,
	name string,
	status string,
	runtimeOptions *AccountPoolRuntimeOptions,
) model.AccountPoolAccount {
	t.Helper()
	view, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: poolID,
		Name:   name,
		Status: status,
		Credential: AccountPoolCredentialConfig{
			Type:         AccountPoolCredentialTypeOAuth,
			RefreshToken: "refresh-" + name,
		},
		TokenState: AccountPoolTokenState{AccessToken: "access-" + name, ExpiresAt: time.Now().Add(time.Hour).Unix()},
	})
	require.NoError(t, err)
	updates := map[string]any{"status": status}
	if runtimeOptions != nil {
		encoded, err := common.Marshal(runtimeOptions)
		require.NoError(t, err)
		updates["runtime_options"] = string(encoded)
	}
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).Where("id = ?", view.Id).Updates(updates).Error)
	var account model.AccountPoolAccount
	require.NoError(t, model.DB.First(&account, view.Id).Error)
	return account
}

func accountPoolXAIQuotaWorkerSnapshot(t *testing.T, fetchedAt int64) *AccountPoolRuntimeOptions {
	t.Helper()
	return &AccountPoolRuntimeOptions{XAIQuota: &AccountPoolXAIQuotaSnapshot{Source: "test", FetchedAt: fetchedAt}}
}

func setAccountPoolXAIQuotaProbeRunnerForTest(t *testing.T, runner accountPoolXAIQuotaProbeRunnerFunc) {
	t.Helper()
	previous := accountPoolXAIQuotaProbeRunner
	accountPoolXAIQuotaProbeRunner = runner
	t.Cleanup(func() { accountPoolXAIQuotaProbeRunner = previous })
}
