package channel

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFinalizeAnthropicOAuthAuthHeader_RemovesXApiKey verifies the defense-in-depth
// finalizer that prevents an OAuth Bearer token from leaking into x-api-key:
//
// After processHeaderOverride applies channel HeadersOverride templates (which can
// set x-api-key via {api_key} placeholder), if RuntimeAnthropicOAuth=true the
// finalizer must:
//   - delete x-api-key (and X-Api-Key)
//   - ensure Authorization: Bearer <token> is present
func TestFinalizeAnthropicOAuthAuthHeader_RemovesXApiKey(t *testing.T) {
	apiKey := "oauth-token-abc"

	// Simulate what DoApiRequest accumulates after SetupRequestHeader + HeadersOverride:
	// HeadersOverride set x-api-key (e.g. via {api_key} placeholder).
	header := http.Header{}
	header.Set("Authorization", "Bearer "+apiKey)
	header.Set("x-api-key", apiKey) // leaked by HeadersOverride

	finalizeAnthropicOAuthAuthHeader(header, apiKey)

	assert.Equal(t, "Bearer "+apiKey, header.Get("Authorization"),
		"Authorization: Bearer must be present after finalize")
	assert.Empty(t, header.Get("x-api-key"),
		"x-api-key must be removed by OAuth finalizer")
	assert.Empty(t, header.Get("X-Api-Key"),
		"X-Api-Key (canonical) must be removed by OAuth finalizer")
}

// TestFinalizeAnthropicOAuthAuthHeader_RestoresMissingAuthorization verifies that
// the finalizer sets Authorization: Bearer if it was missing (e.g. HeadersOverride
// deleted it or it was never set).
func TestFinalizeAnthropicOAuthAuthHeader_RestoresMissingAuthorization(t *testing.T) {
	apiKey := "oauth-token-xyz"

	header := http.Header{}
	// No Authorization header; only x-api-key
	header.Set("x-api-key", apiKey)

	finalizeAnthropicOAuthAuthHeader(header, apiKey)

	assert.Equal(t, "Bearer "+apiKey, header.Get("Authorization"),
		"finalize must restore Authorization: Bearer when missing")
	assert.Empty(t, header.Get("x-api-key"),
		"x-api-key must be removed")
}

// TestFinalizeAnthropicOAuthAuthHeader_NoOpWhenClean verifies that the finalizer is
// a no-op on a header map that is already correct (Bearer present, no x-api-key).
func TestFinalizeAnthropicOAuthAuthHeader_NoOpWhenClean(t *testing.T) {
	apiKey := "oauth-token-clean"

	header := http.Header{}
	header.Set("Authorization", "Bearer "+apiKey)
	header.Set("anthropic-beta", "oauth-2025-04-20")

	finalizeAnthropicOAuthAuthHeader(header, apiKey)

	require.Equal(t, "Bearer "+apiKey, header.Get("Authorization"))
	require.Empty(t, header.Get("x-api-key"))
	require.Equal(t, "oauth-2025-04-20", header.Get("anthropic-beta"),
		"other headers must be undisturbed")
}
