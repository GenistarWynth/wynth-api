package operation_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexAffinityTemplatePassesCurrentHeadersAndKeepsOrigin(t *testing.T) {
	template := channelAffinitySetting.Rules[0].ParamOverrideTemplate
	operations, ok := template["operations"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, operations, 1)
	assert.Equal(t, true, operations[0]["keep_origin"])
	headers, ok := operations[0]["value"].([]string)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{
		"Originator", "Session_id", "Thread_id", "Session-Id", "Thread-Id", "X-Client-Request-Id",
		"User-Agent", "X-Codex-Beta-Features", "X-Codex-Turn-State", "X-Codex-Turn-Metadata",
		"X-Codex-Window-Id", "X-Codex-Parent-Thread-Id", "X-OpenAI-Subagent",
		"X-OpenAI-Memgen-Request", "X-ResponsesAPI-Include-Timing-Metrics",
		"X-OpenAI-Internal-Codex-Responses-Lite",
	}, headers)
}
