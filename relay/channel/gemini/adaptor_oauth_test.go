package gemini

import (
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newGeminiAdaptorOAuthTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return ctx
}

func newGeminiAdaptorOAuthTestInfo(apiKey string, oauthEnabled bool) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RuntimeGeminiOAuth: oauthEnabled,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey: apiKey,
		},
	}
}

// TestGeminiSetupRequestHeaderOAuthBearerAuth verifies that when RuntimeGeminiOAuth=true:
// - Authorization: Bearer <key> is set
// - x-goog-api-key is NOT set
// - User-Agent contains GeminiCLI
func TestGeminiSetupRequestHeaderOAuthBearerAuth(t *testing.T) {
	adaptor := &Adaptor{}
	c := newGeminiAdaptorOAuthTestContext()
	info := newGeminiAdaptorOAuthTestInfo("ya29.my-oauth-token", true)
	header := make(http.Header)

	err := adaptor.SetupRequestHeader(c, &header, info)

	require.NoError(t, err)
	assert.Equal(t, "Bearer ya29.my-oauth-token", header.Get("Authorization"),
		"OAuth path must set Authorization: Bearer <key>")
	assert.Empty(t, header.Get("x-goog-api-key"),
		"OAuth path must NOT set x-goog-api-key")
	assert.Contains(t, header.Get("User-Agent"), "GeminiCLI",
		"OAuth path must set GeminiCLI User-Agent")
}

// TestGeminiSetupRequestHeaderAPIKeyPath verifies that when RuntimeGeminiOAuth=false:
// - x-goog-api-key is set
// - Authorization header is NOT set
func TestGeminiSetupRequestHeaderAPIKeyPath(t *testing.T) {
	adaptor := &Adaptor{}
	c := newGeminiAdaptorOAuthTestContext()
	info := newGeminiAdaptorOAuthTestInfo("AIzaSy-api-key", false)
	header := make(http.Header)

	err := adaptor.SetupRequestHeader(c, &header, info)

	require.NoError(t, err)
	assert.Equal(t, "AIzaSy-api-key", header.Get("x-goog-api-key"),
		"API-key path must set x-goog-api-key")
	assert.Empty(t, header.Get("Authorization"),
		"API-key path must NOT set Authorization header")
}
