package claude

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAdaptorOAuthTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	return ctx
}

func newAdaptorOAuthTestInfo(apiKey string, oauthEnabled bool) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RuntimeAnthropicOAuth: oauthEnabled,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey: apiKey,
		},
	}
}

// TestSetupRequestHeaderOAuthBearerAuth verifies that when RuntimeAnthropicOAuth=true:
// - Authorization: Bearer <key> is set
// - x-api-key is NOT set
// - anthropic-beta contains oauth-2025-04-20
// - Anthropic-Dangerous-Direct-Browser-Access: true is set
// - User-Agent contains claude-cli
func TestSetupRequestHeaderOAuthBearerAuth(t *testing.T) {
	adaptor := &Adaptor{}
	c := newAdaptorOAuthTestContext()
	info := newAdaptorOAuthTestInfo("sk-oauth-token", true)
	header := make(http.Header)

	err := adaptor.SetupRequestHeader(c, &header, info)

	require.NoError(t, err)
	assert.Equal(t, "Bearer sk-oauth-token", header.Get("Authorization"), "OAuth path must set Authorization: Bearer <key>")
	assert.Empty(t, header.Get("x-api-key"), "OAuth path must NOT set x-api-key")
	assert.Contains(t, header.Get("anthropic-beta"), "oauth-2025-04-20", "OAuth path must include oauth-2025-04-20 in anthropic-beta")
	assert.Equal(t, "true", header.Get("Anthropic-Dangerous-Direct-Browser-Access"), "OAuth path must set Anthropic-Dangerous-Direct-Browser-Access: true")
	assert.Contains(t, header.Get("User-Agent"), "claude-cli", "OAuth path must set claude-cli User-Agent")
	assert.Equal(t, "2023-06-01", header.Get("anthropic-version"), "OAuth path must set anthropic-version")
}

// TestSetupRequestHeaderAPIKeyPath verifies that when RuntimeAnthropicOAuth=false:
// - x-api-key is set
// - Authorization header is NOT set
func TestSetupRequestHeaderAPIKeyPath(t *testing.T) {
	adaptor := &Adaptor{}
	c := newAdaptorOAuthTestContext()
	info := newAdaptorOAuthTestInfo("sk-regular-key", false)
	header := make(http.Header)

	err := adaptor.SetupRequestHeader(c, &header, info)

	require.NoError(t, err)
	assert.Equal(t, "sk-regular-key", header.Get("x-api-key"), "API-key path must set x-api-key")
	assert.Empty(t, header.Get("Authorization"), "API-key path must NOT set Authorization header")
}

// TestSetupRequestHeaderOAuthBetaContainsAllRequiredFlags verifies the complete beta flag list.
func TestSetupRequestHeaderOAuthBetaContainsAllRequiredFlags(t *testing.T) {
	adaptor := &Adaptor{}
	c := newAdaptorOAuthTestContext()
	info := newAdaptorOAuthTestInfo("sk-oauth-token", true)
	header := make(http.Header)

	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))

	beta := header.Get("anthropic-beta")
	for _, flag := range strings.Split(AnthropicOAuthBetaFeatures, ",") {
		assert.Contains(t, beta, flag, "OAuth beta flags must include %q", flag)
	}
}

// newAdaptorOAuthTestContextWithBeta returns a test context whose incoming request
// carries the given anthropic-beta header value (simulating a real client sending it).
func newAdaptorOAuthTestContextWithBeta(beta string) *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	if beta != "" {
		req.Header.Set("anthropic-beta", beta)
	}
	ctx.Request = req
	return ctx
}

// TestSetupRequestHeaderOAuthMergesClientBeta verifies FIX 3:
// When the client sends its own anthropic-beta value, the OAuth SetupRequestHeader
// must MERGE (union, deduplicate) the required OAuth bundle flags with the client-supplied
// flags rather than overwriting them. Required OAuth flags must always be present.
func TestSetupRequestHeaderOAuthMergesClientBeta(t *testing.T) {
	adaptor := &Adaptor{}
	info := newAdaptorOAuthTestInfo("sk-oauth-token", true)

	t.Run("client flag is preserved alongside OAuth bundle", func(t *testing.T) {
		c := newAdaptorOAuthTestContextWithBeta("custom-flag-2025")
		header := make(http.Header)

		require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))

		beta := header.Get("anthropic-beta")
		assert.Contains(t, beta, "oauth-2025-04-20",
			"required OAuth flag must be present after merge")
		assert.Contains(t, beta, "custom-flag-2025",
			"client-supplied beta flag must be preserved after merge")
	})

	t.Run("no client beta uses bundle as-is", func(t *testing.T) {
		c := newAdaptorOAuthTestContextWithBeta("")
		header := make(http.Header)

		require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))

		beta := header.Get("anthropic-beta")
		for _, flag := range strings.Split(AnthropicOAuthBetaFeatures, ",") {
			assert.Contains(t, beta, flag,
				"all required OAuth flags must be present when no client beta sent")
		}
	})

	t.Run("duplicate flags are not included twice", func(t *testing.T) {
		// Client sends a flag that's already in the OAuth bundle.
		c := newAdaptorOAuthTestContextWithBeta("oauth-2025-04-20")
		header := make(http.Header)

		require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))

		beta := header.Get("anthropic-beta")
		// Count occurrences of "oauth-2025-04-20" in the comma-separated list.
		count := 0
		for _, f := range strings.Split(beta, ",") {
			if strings.TrimSpace(f) == "oauth-2025-04-20" {
				count++
			}
		}
		assert.Equal(t, 1, count, "duplicate flags must not appear more than once in anthropic-beta")
	})
}
