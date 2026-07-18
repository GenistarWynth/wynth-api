package sora

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildRequestBodyPreservesJSONImageFieldsAfterValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalImages := []string{" first-frame ", "", " last-frame "}
	body := `{"model":"sora-alias","prompt":"animate","image":" direct-image ","images":[" first-frame ",""," last-frame "]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request
	info := &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: "sora-2-pro"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	adaptor := &TaskAdaptor{}

	require.Nil(t, adaptor.ValidateRequestAndSetAction(context, info))
	requestBody, err := adaptor.BuildRequestBody(context, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(requestBody)
	require.NoError(t, err)
	var forwarded struct {
		Model  string   `json:"model"`
		Image  string   `json:"image"`
		Images []string `json:"images"`
	}
	require.NoError(t, common.Unmarshal(encoded, &forwarded))

	assert.Equal(t, "sora-2-pro", forwarded.Model)
	assert.Equal(t, " direct-image ", forwarded.Image)
	assert.Equal(t, originalImages, forwarded.Images)
}
