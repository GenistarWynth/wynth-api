package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleLastResponseRecordsOpenAIModelBeforeDownstreamConversion(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-5.5",
		},
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{},
	}

	lastStreamData := `{"id":"resp_1","created":123,"model":"gpt-5.4","choices":[{"delta":{"content":"hi"},"index":0}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`

	responseID := ""
	createdAt := int64(0)
	systemFingerprint := ""
	model := info.UpstreamModelName
	var usage *dto.Usage
	containStreamUsage := false
	shouldSendLastResp := false

	err := handleLastResponse(
		lastStreamData,
		&responseID,
		&createdAt,
		&systemFingerprint,
		&model,
		&usage,
		&containStreamUsage,
		info,
		&shouldSendLastResp,
	)
	require.NoError(t, err)
	require.NotNil(t, usage)

	assert.Equal(t, "gpt-5.4", info.ActualResponseModel)
	assert.Equal(t, relaycommon.ActualResponseModelSourceOpenAIChat, info.ActualResponseModelSource)
	assert.Equal(t, "gpt-5.4", model)
	assert.True(t, containStreamUsage)
}

func TestOaiResponsesHandlerActualResponseModelBestEffortWithNilInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest(http.MethodPost, "/v1/responses", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(`{
			"id":"resp_1",
			"object":"response",
			"model":"gpt-5.4",
			"output":[],
			"tools":[],
			"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}
		}`)),
	}

	usage, err := OaiResponsesHandler(c, nil, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	assert.Equal(t, 2, usage.PromptTokens)
	assert.Equal(t, 3, usage.CompletionTokens)
	assert.Equal(t, 5, usage.TotalTokens)
}
