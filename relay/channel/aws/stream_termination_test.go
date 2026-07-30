package aws

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFinalizeAWSStreamGatesClaudeTerminalFrames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	chunk := `{"type":"content_block_delta","delta":{"type":"text_delta","text":"partial"}}`

	tests := []struct {
		name         string
		termination  relaycommon.StreamTermination
		emitPartial  bool
		wantErr      bool
		wantTerminal bool
	}{
		{
			name:        "zero byte abnormal",
			termination: relaycommon.StreamTermination{Kind: relaycommon.StreamTerminationAbnormal, EndReason: relaycommon.StreamEndReasonScannerErr, EndError: io.ErrUnexpectedEOF},
			wantErr:     true,
		},
		{
			name:        "partial abnormal",
			termination: relaycommon.StreamTermination{Kind: relaycommon.StreamTerminationAbnormal, EndReason: relaycommon.StreamEndReasonScannerErr, EndError: io.ErrUnexpectedEOF},
			emitPartial: true,
			wantErr:     true,
		},
		{
			name:         "normal empty",
			termination:  relaycommon.StreamTermination{Kind: relaycommon.StreamTerminationNormal, EndReason: relaycommon.StreamEndReasonEOF},
			wantTerminal: true,
		},
		{
			name:         "normal success",
			termination:  relaycommon.StreamTermination{Kind: relaycommon.StreamTerminationNormal, EndReason: relaycommon.StreamEndReasonEOF},
			emitPartial:  true,
			wantTerminal: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			context.Set(common.RequestIdKey, "aws-stream-termination")
			info := &relaycommon.RelayInfo{
				IsStream:           true,
				RelayFormat:        types.RelayFormatOpenAI,
				ShouldIncludeUsage: true,
				ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "claude-aws-test"},
			}
			claudeInfo := &claude.ClaudeResponseInfo{
				ResponseId:   "chatcmpl-aws-test",
				Model:        "claude-aws-test",
				ResponseText: strings.Builder{},
				Usage:        &dto.Usage{},
			}
			if test.emitPartial {
				require.Nil(t, claude.HandleStreamResponseData(context, info, claudeInfo, chunk))
			}

			streamErr := finalizeAWSStream(context, info, claudeInfo, test.termination)

			if test.wantErr {
				require.NotNil(t, streamErr)
				assert.Equal(t, types.ErrorCodeReadResponseBodyFailed, streamErr.GetErrorCode())
			} else {
				assert.Nil(t, streamErr)
			}
			if test.emitPartial {
				assert.Contains(t, recorder.Body.String(), "partial")
			}
			if test.wantTerminal {
				assert.Contains(t, recorder.Body.String(), "[DONE]")
			} else {
				assert.NotContains(t, recorder.Body.String(), "[DONE]")
			}
			if test.name == "zero byte abnormal" {
				assert.Empty(t, recorder.Body.String())
				assert.False(t, info.HasSendResponse())
			}
		})
	}
}
