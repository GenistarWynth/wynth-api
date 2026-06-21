package service

import (
	"net/http/httptest"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newLogInfoGenerateTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	return ctx
}

func TestGenerateTextOtherInfoIncludesModelAudit(t *testing.T) {
	ctx := newLogInfoGenerateTestContext()
	startTime := time.Unix(1700000000, 0)
	firstResponseTime := startTime.Add(250 * time.Millisecond)
	info := &relaycommon.RelayInfo{
		StartTime:                 startTime,
		FirstResponseTime:         firstResponseTime,
		OriginModelName:           "gpt-5.5",
		ActualResponseModel:       "gpt-5.4",
		ActualResponseModelSource: relaycommon.ActualResponseModelSourceOpenAIChat,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-5.5",
			IsModelMapped:     false,
		},
	}

	other := GenerateTextOtherInfo(ctx, info, 1, 1, 1, 0, 1, 0, 1)

	require.NotNil(t, other)
	assert.Equal(t, "gpt-5.5", other["upstream_model_name"])
	_, ok := other["is_model_mapped"]
	assert.False(t, ok)
	assert.Equal(t, "gpt-5.4", other["actual_response_model"])
	assert.Equal(t, "openai_chat", other["actual_response_model_source"])
}

func TestGenerateTextOtherInfoKeepsMappingFlagOnlyForMappedRequests(t *testing.T) {
	ctx := newLogInfoGenerateTestContext()
	startTime := time.Unix(1700000000, 0)
	firstResponseTime := startTime.Add(250 * time.Millisecond)
	info := &relaycommon.RelayInfo{
		StartTime:         startTime,
		FirstResponseTime: firstResponseTime,
		OriginModelName:   "gpt-5.5",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-5.4",
			IsModelMapped:     true,
		},
	}

	other := GenerateTextOtherInfo(ctx, info, 1, 1, 1, 0, 1, 0, 1)

	require.NotNil(t, other)
	assert.Equal(t, true, other["is_model_mapped"])
	assert.Equal(t, "gpt-5.4", other["upstream_model_name"])
	_, ok := other["actual_response_model"]
	assert.False(t, ok)
	_, ok = other["actual_response_model_source"]
	assert.False(t, ok)
}
