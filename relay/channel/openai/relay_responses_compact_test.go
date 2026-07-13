package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runCompactHandler(t *testing.T, contentType, body string) (*httptest.ResponseRecorder, error, int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body))}
	usage, apiErr := OaiResponsesCompactionHandler(c, resp)
	if apiErr != nil {
		return w, apiErr, 0
	}
	return w, nil, usage.TotalTokens
}

func TestCompactJSONCompatibility(t *testing.T) {
	body := `{"id":"r1","object":"response.compaction","output":[{"type":"compaction","encrypted_content":"x","future_field":7}],"usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15,"input_tokens_details":{"cached_tokens":4,"cached_creation_tokens":5,"cache_write_tokens":6}},"future_top":"kept"}`
	w, err, total := runCompactHandler(t, "application/json", body)
	require.NoError(t, err)
	assert.JSONEq(t, body, w.Body.String())
	assert.Equal(t, 15, total)
}

func TestCompactUsagePropagatesNativeAndLegacyCacheWriteTokens(t *testing.T) {
	body := `{"usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15,"input_tokens_details":{"cached_tokens":4,"cached_creation_tokens":5,"cache_write_tokens":6}}}`
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}

	usage, apiErr := OaiResponsesCompactionHandler(c, resp)
	require.Nil(t, apiErr)
	assert.Equal(t, 4, usage.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 5, usage.PromptTokensDetails.CachedCreationTokens)
	assert.Equal(t, 6, usage.PromptTokensDetails.CacheWriteTokens)
}

func TestCompactSSEProducesTerminalJSONPreservingUnknownFields(t *testing.T) {
	body := "data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"a\",\"type\":\"message\",\"future\":1}}\n\n" +
		"data: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"c\",\"type\":\"compaction\",\"encrypted_content\":\"x\",\"future\":2}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"object\":\"response.compaction\",\"output\":[{\"id\":\"a\",\"type\":\"message\",\"future\":1}],\"usage\":{\"input_tokens\":2,\"output_tokens\":3,\"total_tokens\":5},\"future_top\":true}}\n\n"
	w, err, total := runCompactHandler(t, "text/event-stream", body)
	require.NoError(t, err)
	assert.Equal(t, 5, total)
	assert.JSONEq(t, `{"id":"r1","object":"response.compaction","output":[{"id":"a","type":"message","future":1},{"id":"c","type":"compaction","encrypted_content":"x","future":2}],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5},"future_top":true}`, w.Body.String())
	assert.NotContains(t, w.Body.String(), "data:")
}

func TestCompactSSEDoneItemReplacesAddedSnapshot(t *testing.T) {
	body := "data: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"c\",\"type\":\"compaction\"}}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"c\",\"type\":\"compaction\",\"encrypted_content\":\"complete\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"output\":[],\"usage\":{\"total_tokens\":1}}}\n\n"
	w, err, _ := runCompactHandler(t, "text/event-stream", body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"r1","output":[{"id":"c","type":"compaction","encrypted_content":"complete"}],"usage":{"total_tokens":1}}`, w.Body.String())
}

func TestCompactSSERrejectsFailedAndTruncatedStreams(t *testing.T) {
	cases := []string{
		"data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"secret upstream detail\",\"type\":\"server_error\"}}}\n\n",
		"data: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"r1\",\"output\":[]}}\n\n",
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"a\"}}\n\n",
		"data: {not-json}\n\n",
	}
	for _, body := range cases {
		w, err, total := runCompactHandler(t, "text/event-stream", body)
		require.Error(t, err)
		assert.Zero(t, total)
		assert.Empty(t, w.Body.String())
		assert.NotContains(t, err.Error(), "secret upstream detail")
	}
}

func TestCompactResponseBodyIsBounded(t *testing.T) {
	body := strings.Repeat("x", compactResponseBodyLimit+1)
	w, err, total := runCompactHandler(t, "application/json", body)
	require.Error(t, err)
	assert.Zero(t, total)
	assert.Empty(t, w.Body.String())
	assert.Less(t, len(err.Error()), 1024)
}
