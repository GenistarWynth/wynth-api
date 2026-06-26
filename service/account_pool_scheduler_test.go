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

func TestAccountPoolSchedulerDropsRuntimeAffinityForUnschedulableAccount(t *testing.T) {
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
	assert.Equal(t, fallback.Id, result.AccountID)
	_, ok := lookupAccountPoolRuntimeAffinity(affinityKey, binding.Id, 102)
	assert.False(t, ok)
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
		PoolID:          pool.Id,
		Name:            "corrupt-supported-models",
		Status:          model.AccountPoolAccountStatusEnabled,
		SupportedModels: `{bad`,
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
			ChannelID:   channel.Id,
			RequestModel: "gpt-5",
			ChannelUpstreamModel: "gpt-5",
			AttemptedAccountIDs: map[int]struct{}{goodAccount.Id: {}},
			Now:         100,
		})
		require.ErrorIs(t, err, ErrAccountPoolNoSchedulableAccount)
	})
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
