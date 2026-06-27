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
