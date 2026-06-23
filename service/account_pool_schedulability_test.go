package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountPoolChannelSchedulabilityReportsUnboundChannelAsDisabledRuntime(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)

	result, err := CheckAccountPoolChannelSchedulability(AccountPoolSchedulabilityRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  100,
	})

	require.NoError(t, err)
	assert.False(t, result.RuntimeEnabled)
	assert.False(t, result.Schedulable)
	assert.Equal(t, AccountPoolSchedulabilityReasonNotBound, result.Reason)
}

func TestAccountPoolChannelSchedulabilityReportsEnabledRuntimeWithUsableAccount(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	binding := createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:            "usable",
		SupportedModels: []string{"gpt-5"},
	})

	result, err := CheckAccountPoolChannelSchedulability(AccountPoolSchedulabilityRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  100,
	})

	require.NoError(t, err)
	assert.True(t, result.RuntimeEnabled)
	assert.True(t, result.Schedulable)
	assert.Equal(t, AccountPoolSchedulabilityReasonReady, result.Reason)
	assert.Equal(t, pool.Id, result.PoolID)
	assert.Equal(t, binding.Id, result.BindingID)
}

func TestAccountPoolChannelSchedulabilityReportsNoSchedulableAccount(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	binding := createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:             "rate-limited",
		SupportedModels:  []string{"gpt-5"},
		RateLimitedUntil: 200,
	})

	result, err := CheckAccountPoolChannelSchedulability(AccountPoolSchedulabilityRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  100,
	})

	require.NoError(t, err)
	assert.True(t, result.RuntimeEnabled)
	assert.False(t, result.Schedulable)
	assert.Equal(t, AccountPoolSchedulabilityReasonNoSchedulableAccount, result.Reason)
	assert.Equal(t, pool.Id, result.PoolID)
	assert.Equal(t, binding.Id, result.BindingID)
}
