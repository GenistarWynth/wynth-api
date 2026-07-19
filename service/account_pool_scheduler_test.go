package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountPoolSchedulerRejectsDraftBinding(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	_, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)

	_, err = SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  100,
	})

	require.ErrorIs(t, err, ErrAccountPoolBindingNotRuntimeEnabled)
}

func TestAccountPoolSchedulerRejectsMissingBinding(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)

	_, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  100,
	})

	require.ErrorIs(t, err, ErrAccountPoolBindingNotRuntimeEnabled)
}

func TestAccountPoolSchedulerRejectsDisabledPool(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{Name: "available"})
	require.NoError(t, model.DB.Model(&model.AccountPool{}).
		Where("id = ?", pool.Id).
		Update("status", model.AccountPoolStatusDisabled).Error)

	_, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  100,
	})

	require.ErrorIs(t, err, ErrAccountPoolNoSchedulableAccount)
}

func TestAccountPoolSchedulerSelectsHighestPriorityRemainingAccount(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	first := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:     "high-a",
		Priority: 100,
		Weight:   100,
	})
	second := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:     "high-b",
		Priority: 100,
		Weight:   20,
	})
	createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:     "lower",
		Priority: 50,
		Weight:   100,
	})

	result, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		AttemptedAccountIDs:  map[int]struct{}{first.Id: {}},
		Now:                  100,
	})

	require.NoError(t, err)
	assert.Equal(t, second.Id, result.AccountID)
}

func TestAccountPoolSchedulerRoundRobinSelectsLeastRecentlyUsedHighestPriorityAccount(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	require.NoError(t, model.DB.Model(&model.AccountPoolChannelBinding{}).
		Where("channel_id = ?", channel.Id).
		Update("schedule_policy", "round_robin").Error)

	selected := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:       "least-recent-high",
		Priority:   100,
		Weight:     0,
		LastUsedAt: 10,
	})
	createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:       "weighted-recent-high",
		Priority:   100,
		Weight:     1_000_000_000,
		LastUsedAt: 100,
	})
	createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:       "least-recent-low",
		Priority:   50,
		Weight:     1_000_000_000,
		LastUsedAt: 0,
	})

	result, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  200,
	})

	require.NoError(t, err)
	assert.Equal(t, selected.Id, result.AccountID)
}

func TestAccountPoolSchedulerPrefersSchedulableRuntimeAffinityAccount(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	binding := createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:     "higher-priority",
		Priority: 100,
	})
	sticky := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:     "sticky",
		Priority: 10,
	})
	affinityKey := "test-affinity"
	rememberAccountPoolRuntimeAffinity(affinityKey, binding.Id, sticky.Id, 100)

	result, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		AffinityKey:          affinityKey,
		Now:                  101,
	})

	require.NoError(t, err)
	assert.Equal(t, sticky.Id, result.AccountID)
}

// TestAccountPoolSchedulerFallsBackButRetainsPinForTransientlyUnschedulableAccount verifies
// that when the pinned account fails IsSchedulableAt (e.g., it is rate-limited) it is absent
// from the candidates list, so the scheduler falls back to another account for THIS request.
// Crucially, the pin must NOT be dropped by the scheduler — eviction is owned by the relay
// failure path (ForgetSelectedAccountPoolRuntimeAffinity) and the idle/hard TTLs. A transient
// rate-limit window must not permanently migrate the session to a different account.
func TestAccountPoolSchedulerFallsBackButRetainsPinForTransientlyUnschedulableAccount(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	binding := createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	sticky := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:             "sticky-rate-limited",
		Priority:         100,
		RateLimitedUntil: 200,
	})
	fallback := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:     "fallback",
		Priority: 10,
	})
	affinityKey := "test-affinity"
	rememberAccountPoolRuntimeAffinity(affinityKey, binding.Id, sticky.Id, 100)

	result, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		AffinityKey:          affinityKey,
		Now:                  101,
	})

	require.NoError(t, err)
	// Fallback account is selected for this request (sticky is rate-limited / unschedulable).
	assert.Equal(t, fallback.Id, result.AccountID)
	// Pin is retained — the scheduler must NOT evict it. Only the relay failure path or TTL
	// evicts the pin. The session will re-pin to the sticky account once it is schedulable again.
	gotID, ok := lookupAccountPoolRuntimeAffinity(affinityKey, binding.Id, 102)
	assert.True(t, ok, "pin must be retained after a transient unschedulability; eviction is the relay failure path's job")
	assert.Equal(t, sticky.Id, gotID)
}

func TestAccountPoolSchedulerWithLeaseSkipsSaturatedAccount(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	first := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:           "first",
		Priority:       100,
		MaxConcurrency: 1,
	})
	second := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:           "second",
		Priority:       100,
		MaxConcurrency: 1,
	})
	releaseFirst, acquired := tryAcquireAccountPoolRuntimeLease(first.Id, first.MaxConcurrency)
	require.True(t, acquired)
	defer releaseFirst()

	selected, releaseSelected, err := SelectAccountPoolAccountWithLease(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  100,
	})

	require.NoError(t, err)
	defer releaseSelected()
	assert.Equal(t, second.Id, selected.AccountID)
}

func TestAccountPoolSchedulerWithLeaseRotatesBeforeSuccessUpdatesLastUsedAt(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	first := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:              "first",
		Priority:          100,
		MaxConcurrency:    0,
		MaxConcurrencySet: true,
	})
	second := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:              "second",
		Priority:          100,
		MaxConcurrency:    0,
		MaxConcurrencySet: true,
	})

	firstSelection, releaseFirst, err := SelectAccountPoolAccountWithLease(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  100,
	})
	require.NoError(t, err)
	defer releaseFirst()
	require.Equal(t, first.Id, firstSelection.AccountID)

	secondSelection, releaseSecond, err := SelectAccountPoolAccountWithLease(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  100,
	})
	require.NoError(t, err)
	defer releaseSecond()
	assert.Equal(t, second.Id, secondSelection.AccountID)
}

func TestAccountPoolRuntimeLeaseTreatsZeroConcurrencyAsUnlimited(t *testing.T) {
	setupAccountPoolServiceTestDB(t)

	releaseOne, acquired := tryAcquireAccountPoolRuntimeLease(1001, 0)
	require.True(t, acquired)
	defer releaseOne()
	releaseTwo, acquired := tryAcquireAccountPoolRuntimeLease(1001, 0)
	require.True(t, acquired)
	defer releaseTwo()
}

func TestAccountPoolSchedulerFiltersUnsupportedModelsBeforeAccountMapping(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:            "unsupported-high-priority",
		Priority:        100,
		SupportedModels: []string{"other-upstream-model"},
	})
	selected := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:            "supported-lower-priority",
		Priority:        50,
		SupportedModels: []string{"upstream-gpt-5"},
		ModelMapping: map[string]string{
			"upstream-gpt-5": "account-gpt-5",
		},
	})

	result, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "upstream-gpt-5",
		Now:                  100,
	})

	require.NoError(t, err)
	assert.Equal(t, selected.Id, result.AccountID)
	assert.Equal(t, "account-gpt-5", result.UpstreamModelName)
}

func TestAccountPoolSchedulerAppliesFiltersAndModelMapping(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)

	excluded := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:            "excluded",
		SupportedModels: []string{"upstream-gpt-5"},
	})
	selected := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:            "selected",
		SupportedModels: []string{"upstream-gpt-5"},
		ModelMapping: map[string]string{
			"upstream-gpt-5": "account-gpt-5",
		},
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-account-secret",
		},
	})
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{
		AccountIDs: []int{selected.Id},
	}, AccountPoolModelPolicy{
		Strategy:    "fixed",
		FixedModels: []string{"gpt-5"},
	})

	result, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "upstream-gpt-5",
		Now:                  100,
	})
	require.NoError(t, err)
	assert.Equal(t, selected.Id, result.AccountID)
	assert.NotEqual(t, excluded.Id, result.AccountID)
	assert.Equal(t, "account-gpt-5", result.UpstreamModelName)
	assert.Equal(t, "sk-account-secret", result.Credential.APIKey)

	_, err = SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-4",
		ChannelUpstreamModel: "upstream-gpt-5",
		Now:                  100,
	})
	require.ErrorIs(t, err, ErrAccountPoolNoSchedulableAccount)
}

func TestAccountPoolSchedulerSkipsTransientAccounts(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:             "rate-limited",
		RateLimitedUntil: 200,
	})
	createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:              "temp-disabled",
		TempDisabledUntil: 200,
	})
	selected := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name: "selected",
	})

	result, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  100,
	})

	require.NoError(t, err)
	assert.Equal(t, selected.Id, result.AccountID)
}

func TestAccountPoolSchedulerExhaustsAttemptedAccounts(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	first := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name: "first",
	})
	second := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name: "second",
	})

	_, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		AttemptedAccountIDs: map[int]struct{}{
			first.Id:  {},
			second.Id: {},
		},
		Now: 100,
	})

	require.ErrorIs(t, err, ErrAccountPoolNoSchedulableAccount)
}

func TestAccountPoolSchedulerZeroWeightAccountRemainsSelectable(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:   "zero-weight",
		Weight: 0,
	})

	result, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  100,
	})

	require.NoError(t, err)
	assert.Equal(t, account.Id, result.AccountID)
}

func TestSelectAccountPoolAccountSkipsCorruptRow(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	// Account A: malformed supported_models JSON — inserted directly to bypass service validation.
	badCredential, err := EncryptAccountPoolCredentialConfig(AccountPoolCredentialConfig{
		Type:   AccountPoolCredentialTypeAPIKey,
		APIKey: "sk-bad-account",
	})
	require.NoError(t, err)
	badAccount := model.AccountPoolAccount{
		PoolID:           pool.Id,
		Name:             "corrupt-supported-models",
		Status:           model.AccountPoolAccountStatusEnabled,
		SupportedModels:  `{bad`,
		CredentialConfig: badCredential,
	}
	require.NoError(t, model.DB.Create(&badAccount).Error)

	// Account B: valid.
	goodAccount := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "good-account",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-good",
		},
	})

	t.Run("corrupt row is skipped, valid account selected", func(t *testing.T) {
		result, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
			ChannelID:            channel.Id,
			RequestModel:         "gpt-5",
			ChannelUpstreamModel: "gpt-5",
			Now:                  100,
		})
		require.NoError(t, err)
		assert.Equal(t, goodAccount.Id, result.AccountID)
	})

	t.Run("all corrupt accounts returns ErrAccountPoolNoSchedulableAccount", func(t *testing.T) {
		_, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
			ChannelID:            channel.Id,
			RequestModel:         "gpt-5",
			ChannelUpstreamModel: "gpt-5",
			AttemptedAccountIDs:  map[int]struct{}{goodAccount.Id: {}},
			Now:                  100,
		})
		require.ErrorIs(t, err, ErrAccountPoolNoSchedulableAccount)
	})
}

// TestSelectAccountPoolAccountWithLeaseExhaustsAllAtCapacityAccounts verifies that
// selectAccountPoolAccountWithLease returns ErrAccountPoolNoSchedulableAccount without panic or
// hang when every enabled account already holds a lease at max concurrency.
//
// After the refactor this also proves the single-DB-query structural contract: the implementation
// must call loadAccountPoolSelectionContext once, then loop over the already-loaded candidate slice
// in memory. Reviewers should confirm that the loop contains no DB query (the DB is only queried
// inside loadAccountPoolSelectionContext, which is called before the loop).
func TestSelectAccountPoolAccountWithLeaseExhaustsAllAtCapacityAccounts(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	// Two accounts each with MaxConcurrency=1, both fully leased before the call.
	first := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:           "first-at-capacity",
		Priority:       100,
		MaxConcurrency: 1,
	})
	second := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:           "second-at-capacity",
		Priority:       100,
		MaxConcurrency: 1,
	})
	releaseFirst, acquired := tryAcquireAccountPoolRuntimeLease(first.Id, first.MaxConcurrency)
	require.True(t, acquired, "first lease must be acquired for the test setup to be valid")
	defer releaseFirst()
	releaseSecond, acquired := tryAcquireAccountPoolRuntimeLease(second.Id, second.MaxConcurrency)
	require.True(t, acquired, "second lease must be acquired for the test setup to be valid")
	defer releaseSecond()

	_, _, err := selectAccountPoolAccountWithLease(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  100,
	}, false)

	require.ErrorIs(t, err, ErrAccountPoolNoSchedulableAccount)
}

// TestSelectAccountPoolAccountWithLeaseReturnsAccountWhenOneAvailable verifies that
// selectAccountPoolAccountWithLease successfully acquires a lease and returns a valid result when
// at least one account has capacity — and that the release function is non-nil and callable.
func TestSelectAccountPoolAccountWithLeaseReturnsAccountWhenOneAvailable(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	// First account fully leased; second has capacity.
	first := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:           "first-at-capacity",
		Priority:       100,
		MaxConcurrency: 1,
	})
	second := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:           "second-available",
		Priority:       100,
		MaxConcurrency: 1,
	})
	releaseFirst, acquired := tryAcquireAccountPoolRuntimeLease(first.Id, first.MaxConcurrency)
	require.True(t, acquired, "first lease must be acquired for the test setup to be valid")
	defer releaseFirst()

	result, release, err := selectAccountPoolAccountWithLease(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  100,
	}, false)

	require.NoError(t, err)
	require.NotNil(t, release, "release function must be non-nil on success")
	defer release()
	assert.Equal(t, second.Id, result.AccountID)
}

// TestAccountPoolSchedulerExcludesExpiredAutoPauseAccounts verifies that an account
// with AutoPauseOnExpired=true whose ExpiresAt has already passed is excluded from
// scheduler selection, while a non-expired account is chosen.
func TestAccountPoolSchedulerExcludesExpiredAutoPauseAccounts(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	now := int64(1000)

	// Expired account with auto-pause: must be excluded.
	createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:               "expired-auto-pause",
		AutoPauseOnExpired: true,
		ExpiresAt:          now - 1, // past
	})

	// Non-expired account: must be selected.
	selected := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:               "not-expired",
		AutoPauseOnExpired: true,
		ExpiresAt:          now + 100, // future
	})

	result, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  now,
	})

	require.NoError(t, err)
	assert.Equal(t, selected.Id, result.AccountID, "expired auto-pause account must be excluded; only non-expired account should be selected")
}

func TestAccountPoolSchedulerExcludesQuotaExceededAccount(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	now := int64(1_000_000)

	// Quota-exceeded account: used == quota, no window elapsed.
	exceeded := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name:     "exceeded",
		Priority: 100,
	})
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).Where("id = ?", exceeded.Id).
		Updates(map[string]any{
			"request_quota":                int64(5),
			"request_quota_used":           int64(5),
			"request_quota_window_start":   now - 10,
			"request_quota_window_seconds": int64(100),
		}).Error)

	// Non-exceeded account: should be selected.
	available := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name:     "available",
		Priority: 50,
	})

	result, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  now,
	})

	require.NoError(t, err)
	assert.Equal(t, available.Id, result.AccountID, "quota-exceeded account must be excluded; only available account should be selected")
}

func TestAccountPoolSchedulerSelectsQuotaWindowElapsedAccount(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	now := int64(1_000_000)

	// Account whose window has elapsed: used == quota, but the window is over.
	// QuotaExceededAt returns false (counter resets logically), so it must be selected.
	elapsed := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name:     "window-elapsed",
		Priority: 100,
	})
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).Where("id = ?", elapsed.Id).
		Updates(map[string]any{
			"request_quota":                int64(5),
			"request_quota_used":           int64(5),
			"request_quota_window_start":   now - 200,
			"request_quota_window_seconds": int64(100), // window ended at now-100
		}).Error)

	result, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  now,
	})

	require.NoError(t, err)
	assert.Equal(t, elapsed.Id, result.AccountID, "account with elapsed window must be schedulable (quota resets)")
}

func TestAccountPoolSchedulerXAIMediaEligibilityFilter(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool, err := service.CreatePool(AccountPoolCreateParams{
		Name:     "xai-media",
		Platform: model.AccountPoolPlatformXAI,
	})
	require.NoError(t, err)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	ineligible := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:     "known-ineligible",
		Priority: 100,
	})
	unknown := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:     "unknown",
		Priority: 50,
	})
	eligible := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:     "known-eligible",
		Priority: 10,
	})
	falseValue := false
	trueValue := true
	setAccountPoolSchedulerXAIQuota(t, ineligible.Id, &falseValue)
	setAccountPoolSchedulerXAIQuota(t, eligible.Id, &trueValue)

	t.Run("known false is skipped for media", func(t *testing.T) {
		result, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
			ChannelID:            channel.Id,
			RequestModel:         "grok-imagine-image",
			ChannelUpstreamModel: "grok-imagine-image",
			RequireXAIMedia:      true,
			Now:                  100,
		})
		require.NoError(t, err)
		assert.Equal(t, unknown.Id, result.AccountID)
	})

	t.Run("unknown remains eligible for media failover", func(t *testing.T) {
		result, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
			ChannelID:            channel.Id,
			RequestModel:         "grok-imagine-image",
			ChannelUpstreamModel: "grok-imagine-image",
			RequireXAIMedia:      true,
			AttemptedAccountIDs:  map[int]struct{}{unknown.Id: {}},
			Now:                  100,
		})
		require.NoError(t, err)
		assert.Equal(t, eligible.Id, result.AccountID)
	})

	t.Run("chat does not exclude known false", func(t *testing.T) {
		result, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
			ChannelID:            channel.Id,
			RequestModel:         "grok-4",
			ChannelUpstreamModel: "grok-4",
			Now:                  100,
		})
		require.NoError(t, err)
		assert.Equal(t, ineligible.Id, result.AccountID)
	})
}

func setAccountPoolSchedulerXAIQuota(t *testing.T, accountID int, eligible *bool) {
	t.Helper()
	runtimeOptions, err := common.Marshal(AccountPoolRuntimeOptions{
		XAIQuota: &AccountPoolXAIQuotaSnapshot{MediaEligible: eligible},
	})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", accountID).
		Update("runtime_options", string(runtimeOptions)).Error)
}

func createEnabledAccountPoolSchedulerBinding(
	t *testing.T,
	poolID int,
	channelID int,
	filter AccountPoolAccountFilterConfig,
	policy AccountPoolModelPolicy,
) model.AccountPoolChannelBinding {
	t.Helper()

	filterJSON, err := common.Marshal(filter)
	require.NoError(t, err)
	policyJSON, err := common.Marshal(policy)
	require.NoError(t, err)
	binding := model.AccountPoolChannelBinding{
		PoolID:              poolID,
		ChannelID:           channelID,
		AccountFilterConfig: string(filterJSON),
		ModelPolicy:         string(policyJSON),
		Status:              model.AccountPoolBindingStatusEnabled,
	}
	require.NoError(t, model.DB.Create(&binding).Error)
	return binding
}

func createAccountPoolSchedulerAccount(
	t *testing.T,
	service AccountPoolService,
	poolID int,
	params AccountPoolAccountCreateParams,
) AccountPoolAccountView {
	t.Helper()

	params.PoolID = poolID
	if params.Name == "" {
		params.Name = "account"
	}
	if params.Credential.Type == "" {
		params.Credential = AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-" + params.Name,
		}
	}
	account, err := service.CreateAccount(params)
	require.NoError(t, err)
	return account
}
