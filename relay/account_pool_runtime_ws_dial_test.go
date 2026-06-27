package relay

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wssDialError must attach a captured upstream handshake rejection to the error
// so downstream failure classification can see the real status/header/body. A
// pure transport failure (no captured response) must stay a plain error.
func TestWssDialErrorAttachesHandshakeUpstreamContext(t *testing.T) {
	info := &relaycommon.RelayInfo{
		WsHandshakeStatusCode: http.StatusTooManyRequests,
		WsHandshakeHeader:     http.Header{"Retry-After": []string{"42"}},
		WsHandshakeBody:       []byte(`{"error":"rate limited"}`),
	}
	apiErr := wssDialError(info, errors.New("dial failed: bad handshake"))
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusTooManyRequests, apiErr.GetUpstreamStatusCode())
	require.NotNil(t, apiErr.GetUpstreamHeader())
	assert.Equal(t, "42", apiErr.GetUpstreamHeader().Get("Retry-After"))
	assert.Equal(t, []byte(`{"error":"rate limited"}`), apiErr.GetUpstreamBody())

	transport := wssDialError(&relaycommon.RelayInfo{}, errors.New("dial failed: connection refused"))
	require.NotNil(t, transport)
	assert.Equal(t, 0, transport.GetUpstreamStatusCode(), "no handshake response => no upstream status")
}

// End-to-end of the goal: a WS handshake 429 must drive a rate-limit cooldown,
// while a pure transport dial failure (no upstream status) must drive a transport
// (temp-disabled) cooldown — NOT the other way around.
func TestWssHandshake429DrivesRateLimitWhileTransportDrivesTempDisable(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	pool := createAccountPoolRelayTestPool(t)
	now := common.GetTimestamp()

	rateLimited := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name: "ws-429",
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-429",
		},
	})
	transport := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name: "ws-transport",
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-transport",
		},
	})

	// 429 handshake rejection → rate-limit cooldown.
	handshake429 := wssDialError(&relaycommon.RelayInfo{
		WsHandshakeStatusCode: http.StatusTooManyRequests,
	}, errors.New("dial failed: bad handshake"))
	require.NoError(t, service.RecordAccountPoolRuntimeAttemptFailure(
		rateLimited.Id, handshake429, now, model.AccountPoolPlatformOpenAI, ""))

	// Pure transport dial failure (no handshake response) → transport cooldown.
	transportErr := wssDialError(&relaycommon.RelayInfo{}, errors.New("dial failed: connection refused"))
	require.NoError(t, service.RecordAccountPoolRuntimeAttemptFailure(
		transport.Id, transportErr, now, model.AccountPoolPlatformOpenAI, ""))

	var reloaded429 model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded429, rateLimited.Id).Error)
	assert.Greater(t, reloaded429.RateLimitedUntil, now, "429 handshake must set a rate-limit cooldown")

	var reloadedTransport model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloadedTransport, transport.Id).Error)
	assert.Greater(t, reloadedTransport.TempDisabledUntil, int64(0), "transport dial failure must set a transport cooldown")
	assert.Equal(t, int64(0), reloadedTransport.RateLimitedUntil, "transport failure must NOT be mis-classified as rate-limit")
}
