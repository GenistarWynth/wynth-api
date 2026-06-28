package grokweb

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyHTTPErrorInvalidCredentialsExpires(t *testing.T) {
	err := classifyHTTPError(http.StatusUnauthorized, http.Header{}, []byte(`{"code":"invalid-credentials"}`))
	require.NotNil(t, err)
	// forced to 401 so a later classifier can EXPIRE the account
	assert.Equal(t, http.StatusUnauthorized, err.StatusCode)
	assert.Equal(t, types.ErrorCodeChannelInvalidKey, err.GetErrorCode())
	// upstream context preserved for the pool slice
	assert.Equal(t, http.StatusUnauthorized, err.GetUpstreamStatusCode())
}

func TestClassifyHTTPErrorRateLimited(t *testing.T) {
	err := classifyHTTPError(http.StatusTooManyRequests, http.Header{}, []byte("slow down"))
	require.NotNil(t, err)
	assert.Equal(t, http.StatusTooManyRequests, err.StatusCode)
	assert.Equal(t, types.ErrorCodeBadResponseStatusCode, err.GetErrorCode())
	assert.Equal(t, http.StatusTooManyRequests, err.GetUpstreamStatusCode())
}

func TestClassifyHTTPErrorCloudflareChallengeIsTransport(t *testing.T) {
	body := []byte(`<html><head><title>Just a moment...</title></head><body>cloudflare challenge-platform</body></html>`)
	err := classifyHTTPError(http.StatusForbidden, http.Header{}, body)
	require.NotNil(t, err)
	// cloudflare is infra, not an account problem
	assert.Equal(t, types.ErrorCodeDoRequestFailed, err.GetErrorCode())
	assert.Equal(t, http.StatusForbidden, err.GetUpstreamStatusCode())
}

// The three failure modes must be distinguishable by error code so the later
// account-pool classifier can react differently.
func TestFailureModesAreDistinguishable(t *testing.T) {
	invalid := classifyHTTPError(http.StatusForbidden, http.Header{}, []byte("blocked-user"))
	rate := classifyHTTPError(http.StatusTooManyRequests, http.Header{}, []byte("rate limit"))
	cf := classifyHTTPError(http.StatusServiceUnavailable, http.Header{}, []byte("cloudflare"))

	codes := map[types.ErrorCode]bool{
		invalid.GetErrorCode(): true,
		rate.GetErrorCode():    true,
		cf.GetErrorCode():      true,
	}
	assert.Len(t, codes, 3, "each failure mode must map to a distinct error code")
}

func TestInBandErrorRateLimitCode8(t *testing.T) {
	err := inBandErrorToAPIError(&grokInBandError{Message: "busy", Code: float64(8)})
	require.NotNil(t, err)
	assert.Equal(t, http.StatusTooManyRequests, err.StatusCode)
}

func TestInBandErrorGenericIsBadGateway(t *testing.T) {
	err := inBandErrorToAPIError(&grokInBandError{Message: "internal boom", Code: float64(2)})
	require.NotNil(t, err)
	assert.Equal(t, http.StatusBadGateway, err.StatusCode)
}
