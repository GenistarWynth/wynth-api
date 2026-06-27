package relay

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Realtime/WebSocket relay (WssHelper) drives runAccountPoolRuntimeAttempts with a
// NIL request — there is no body to rebuild per attempt and no affinity signal to
// derive from one. This test protects that WS-specific shape end to end through the
// runtime loop:
//   - pooled selection injects each account's credential with a nil request (the
//     code path every other relay handler covers only with a non-nil request), and
//   - a handshake/dial failure (ErrorCodeDoRequestFailed, raised before any frame
//     flows) sidelines the account and retries the next one — exactly how WssHelper
//     reports an upstream dial failure.
func TestAccountPoolRuntimeRealtimeNilRequestRetriesDialFailure(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/realtime")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)

	first := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:     "rt-first",
		Priority: 100,
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-rt-first",
		},
	})
	second := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:     "rt-second",
		Priority: 50,
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-rt-second",
		},
	})
	info := newAccountPoolRelayTestInfo(channel.Id, "client-realtime", "gpt-4o-realtime")

	var injectedKeys []string
	sawNilRequest := true
	attempts := 0

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		return nil, nil // WS shape: no request body
	}, func(request dto.Request) *types.NewAPIError {
		attempts++
		if request != nil {
			sawNilRequest = false
		}
		injectedKeys = append(injectedKeys, info.ApiKey)
		// First account: simulate an upstream handshake/dial failure (retryable,
		// before any frame flows). Second account: simulate a successful session.
		if service.GetSelectedAccountPoolAccountID(ctx) == first.Id {
			return types.NewError(errors.New("dial failed to wss upstream: bad handshake"), types.ErrorCodeDoRequestFailed)
		}
		return nil
	})

	require.Nil(t, newAPIError)
	assert.True(t, sawNilRequest, "WS attempts must receive a nil request")
	assert.Equal(t, 2, attempts, "a dial failure on the first account must retry the second")
	require.Len(t, injectedKeys, 2)
	assert.Equal(t, "sk-rt-first", injectedKeys[0], "first attempt uses the first account's credential")
	assert.Equal(t, "sk-rt-second", injectedKeys[1], "retry injects the second account's credential")
	assert.Equal(t, second.Id, service.GetSelectedAccountPoolAccountID(ctx))

	// The dial-failed account was recorded/sidelined; the successful one was not.
	var reloadedFirst model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloadedFirst, first.Id).Error)
	assert.NotEmpty(t, reloadedFirst.LastError, "dial-failed account records the failure")
}

// For a channel with no account-pool binding, WssHelper's loop must be a fully
// transparent pass-through: exactly one attempt, the channel credential left
// untouched, and no pooled account selected or recorded.
func TestAccountPoolRuntimeRealtimeNonPooledChannelIsTransparentPassThrough(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/realtime")
	channel := createAccountPoolRelayTestChannel(t) // no pool binding
	info := newAccountPoolRelayTestInfo(channel.Id, "client-realtime", "gpt-4o-realtime")

	attempts := 0
	sawNilRequest := true
	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		return nil, nil
	}, func(request dto.Request) *types.NewAPIError {
		attempts++
		if request != nil {
			sawNilRequest = false
		}
		return nil
	})

	require.Nil(t, newAPIError)
	assert.Equal(t, 1, attempts, "non-pooled channel runs exactly one attempt")
	assert.True(t, sawNilRequest, "WS attempt receives a nil request")
	assert.Equal(t, 0, service.GetSelectedAccountPoolAccountID(ctx), "no pooled account selected for a non-pooled channel")
	assert.Equal(t, "sk-channel", info.ApiKey, "channel credential left untouched")
}
