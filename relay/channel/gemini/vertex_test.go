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

func newVertexTestInfo(model string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RuntimeVertexServiceAccount: true,
		RuntimeVertexProjectID:      "proj-123",
		RuntimeVertexLocation:       "us-central1",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: model,
			ChannelBaseUrl:    "https://generativelanguage.googleapis.com",
		},
	}
}

func TestGetRequestURLVertexServiceAccount(t *testing.T) {
	a := &Adaptor{}

	t.Run("non-stream", func(t *testing.T) {
		info := newVertexTestInfo("gemini-2.5-pro")
		info.IsStream = false
		url, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t,
			"https://us-central1-aiplatform.googleapis.com/v1/projects/proj-123/locations/us-central1/publishers/google/models/gemini-2.5-pro:generateContent",
			url)
	})

	t.Run("stream", func(t *testing.T) {
		info := newVertexTestInfo("gemini-2.5-pro")
		info.IsStream = true
		url, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t,
			"https://us-central1-aiplatform.googleapis.com/v1/projects/proj-123/locations/us-central1/publishers/google/models/gemini-2.5-pro:streamGenerateContent?alt=sse",
			url)
	})
}

// TestGetRequestURLNonVertexUnchanged is the zero-regression assertion: with
// RuntimeVertexServiceAccount=false the URL must follow the standard models/ path
// and must NOT touch the aiplatform host.
func TestGetRequestURLNonVertexUnchanged(t *testing.T) {
	a := &Adaptor{}
	info := newVertexTestInfo("gemini-2.5-pro")
	info.RuntimeVertexServiceAccount = false
	info.IsStream = false

	url, err := a.GetRequestURL(info)
	require.NoError(t, err)
	assert.NotContains(t, url, "aiplatform.googleapis.com")
	assert.Contains(t, url, "/models/gemini-2.5-pro:generateContent")
}

func TestSetupRequestHeaderVertexServiceAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	a := &Adaptor{}
	info := newVertexTestInfo("gemini-2.5-pro")
	info.ApiKey = "ya29.minted-vertex-token"
	header := make(http.Header)

	require.NoError(t, a.SetupRequestHeader(c, &header, info))
	assert.Equal(t, "Bearer ya29.minted-vertex-token", header.Get("Authorization"))
	assert.Empty(t, header.Get("x-goog-api-key"))
}
