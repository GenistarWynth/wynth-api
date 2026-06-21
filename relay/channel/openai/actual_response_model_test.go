package openai

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
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
