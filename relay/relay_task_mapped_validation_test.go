package relay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelayTaskSubmitValidatesMappedAliRequestBeforePricing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name          string
		body          string
		upstreamModel string
	}{
		{
			name:          "mapped Wan27 i2v requires media",
			body:          `{"model":"batch7a-ali-i2v-alias","prompt":"animate"}`,
			upstreamModel: "wan2.7-i2v",
		},
		{
			name:          "mapped Wan27 t2v validates size",
			body:          `{"model":"batch7a-ali-text-alias","prompt":"animate","size":"720p"}`,
			upstreamModel: "wan2.7-t2v",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var upstreamCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				upstreamCalls.Add(1)
			}))
			t.Cleanup(server.Close)

			request := httptest.NewRequest(http.MethodPost, "/v1/video/generations", strings.NewReader(tt.body))
			request.Header.Set("Content-Type", "application/json")
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = request
			context.Set(string(constant.ContextKeyChannelType), constant.ChannelTypeAli)
			context.Set(string(constant.ContextKeyChannelBaseUrl), server.URL)

			var parsedRequest struct {
				Model string `json:"model"`
			}
			require.NoError(t, common.Unmarshal([]byte(tt.body), &parsedRequest))
			context.Set(string(constant.ContextKeyOriginalModel), parsedRequest.Model)
			mapping, err := common.Marshal(map[string]string{parsedRequest.Model: tt.upstreamModel})
			require.NoError(t, err)
			context.Set(string(constant.ContextKeyChannelModelMapping), string(mapping))

			info := &relaycommon.RelayInfo{
				OriginModelName: parsedRequest.Model,
				UserGroup:       "default",
				UsingGroup:      "default",
				TaskRelayInfo:   &relaycommon.TaskRelayInfo{},
			}

			result, taskErr := RelayTaskSubmit(context, info)

			require.Nil(t, result)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
			assert.Equal(t, "invalid_request", taskErr.Code)
			assert.Nil(t, info.Billing)
			assert.False(t, info.ForcePreConsume)
			assert.Equal(t, int32(0), upstreamCalls.Load())
		})
	}
}
