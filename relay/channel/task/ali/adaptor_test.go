package ali

import (
	"fmt"
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

type changedAliDurationValidationError struct{}

func (changedAliDurationValidationError) Error() string                { return "provider wording changed" }
func (changedAliDurationValidationError) aliDurationOutOfRangeMarker() {}

func newAliTestRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
}

func marshalAliRequest(t *testing.T, request *AliVideoRequest) map[string]any {
	t.Helper()
	body, err := common.Marshal(request)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, common.Unmarshal(body, &decoded))
	return decoded
}

func requireWan27MediaOnly(t *testing.T, request *AliVideoRequest, want []any) {
	t.Helper()
	decoded := marshalAliRequest(t, request)
	input, ok := decoded["input"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, want, input["media"])
	assert.NotContains(t, input, "img_url")
	assert.NotContains(t, input, "first_frame_url")
	assert.NotContains(t, input, "last_frame_url")
	assert.NotContains(t, input, "audio_url")
}

func TestAliModelListIncludesWan27(t *testing.T) {
	assert.Contains(t, ModelList, "wan2.7-i2v")
	assert.Contains(t, ModelList, "wan2.7-t2v")
}

func TestConvertToAliRequestWan27Media(t *testing.T) {
	tests := []struct {
		name      string
		req       relaycommon.TaskSubmitReq
		configure func(*relaycommon.RelayInfo)
		wantModel string
		wantMedia []any
	}{
		{
			name: "single image becomes first frame",
			req: relaycommon.TaskSubmitReq{
				Model:  "wan2.7-i2v",
				Prompt: "animate",
				Image:  " https://example.com/direct.png ",
			},
			wantModel: "wan2.7-i2v",
			wantMedia: []any{map[string]any{
				"type": "first_frame",
				"url":  "https://example.com/direct.png",
			}},
		},
		{
			name: "blank images are skipped and second non-empty image is last frame",
			req: relaycommon.TaskSubmitReq{
				Model:  "wan2.7-i2v",
				Prompt: "interpolate",
				Images: []string{" ", " https://example.com/first.png ", "", " https://example.com/last.png "},
			},
			wantModel: "wan2.7-i2v",
			wantMedia: []any{
				map[string]any{"type": "first_frame", "url": "https://example.com/first.png"},
				map[string]any{"type": "last_frame", "url": "https://example.com/last.png"},
			},
		},
		{
			name: "metadata legacy fields take priority over request fields",
			req: relaycommon.TaskSubmitReq{
				Model:          "wan2.7-i2v",
				Prompt:         "animate",
				Image:          "https://example.com/direct.png",
				Images:         []string{"https://example.com/images-first.png", "https://example.com/images-last.png"},
				InputReference: "https://example.com/reference.png",
				Metadata: map[string]any{
					"input": map[string]any{
						"first_frame_url": " https://example.com/metadata-first.png ",
						"img_url":         "https://example.com/metadata-img.png",
						"last_frame_url":  " https://example.com/metadata-last.png ",
						"audio_url":       " https://example.com/audio.wav ",
					},
				},
			},
			wantModel: "wan2.7-i2v",
			wantMedia: []any{
				map[string]any{"type": "first_frame", "url": "https://example.com/metadata-first.png"},
				map[string]any{"type": "last_frame", "url": "https://example.com/metadata-last.png"},
				map[string]any{"type": "driving_audio", "url": "https://example.com/audio.wav"},
			},
		},
		{
			name: "request image wins first frame while second Images entry remains last frame",
			req: relaycommon.TaskSubmitReq{
				Model:          "wan2.7-i2v",
				Prompt:         "animate",
				Image:          " https://example.com/direct.png ",
				Images:         []string{"https://example.com/images-first.png", " ", " https://example.com/images-last.png "},
				InputReference: "https://example.com/reference.png",
			},
			wantModel: "wan2.7-i2v",
			wantMedia: []any{
				map[string]any{"type": "first_frame", "url": "https://example.com/direct.png"},
				map[string]any{"type": "last_frame", "url": "https://example.com/images-last.png"},
			},
		},
		{
			name: "explicit metadata media remains authoritative",
			req: relaycommon.TaskSubmitReq{
				Model:  "wan2.7-i2v",
				Prompt: "continue clip",
				Image:  "https://example.com/direct.png",
				Metadata: map[string]any{
					"input": map[string]any{
						"media": []any{map[string]any{
							"type": "first_clip",
							"url":  "https://example.com/input.mp4",
						}},
						"img_url": "https://example.com/legacy.png",
					},
				},
			},
			wantModel: "wan2.7-i2v",
			wantMedia: []any{map[string]any{
				"type": "first_clip",
				"url":  "https://example.com/input.mp4",
			}},
		},
		{
			name: "mapped upstream model selects Wan27 protocol",
			req: relaycommon.TaskSubmitReq{
				Model:  "customer-video-alias",
				Prompt: "animate",
				Image:  "https://example.com/direct.png",
			},
			configure: func(info *relaycommon.RelayInfo) {
				info.IsModelMapped = true
				info.UpstreamModelName = "wan2.7-i2v-turbo"
			},
			wantModel: "wan2.7-i2v-turbo",
			wantMedia: []any{map[string]any{
				"type": "first_frame",
				"url":  "https://example.com/direct.png",
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := newAliTestRelayInfo()
			if tt.configure != nil {
				tt.configure(info)
			}

			aliReq, err := (&TaskAdaptor{}).convertToAliRequest(info, tt.req)

			require.NoError(t, err)
			assert.Equal(t, tt.wantModel, aliReq.Model)
			requireWan27MediaOnly(t, aliReq, tt.wantMedia)
		})
	}
}

func TestConvertToAliRequestWan27RequiresImageOrMedia(t *testing.T) {
	_, err := (&TaskAdaptor{}).convertToAliRequest(newAliTestRelayInfo(), relaycommon.TaskSubmitReq{
		Model:  "wan2.7-i2v",
		Prompt: "animate",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires image")
}

func TestConvertToAliRequestWan25KeepsLegacyProtocol(t *testing.T) {
	aliReq, err := (&TaskAdaptor{}).convertToAliRequest(newAliTestRelayInfo(), relaycommon.TaskSubmitReq{
		Model:  "wan2.5-i2v-preview",
		Prompt: "animate",
		Image:  " https://example.com/first.png ",
	})

	require.NoError(t, err)
	decoded := marshalAliRequest(t, aliReq)
	input, ok := decoded["input"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "https://example.com/first.png", input["img_url"])
	assert.NotContains(t, input, "media")
}

func TestConvertToAliRequestNormalizesFinalMetadataDuration(t *testing.T) {
	tests := []struct {
		name         string
		duration     int
		wantDuration int
		wantErr      bool
	}{
		{name: "zero defaults to five", duration: 0, wantDuration: 5},
		{name: "negative defaults to five", duration: -1, wantDuration: 5},
		{name: "positive is preserved", duration: 9, wantDuration: 9},
		{name: "over maximum is rejected", duration: relaycommon.MaxTaskDurationSeconds + 1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			aliReq, err := (&TaskAdaptor{}).convertToAliRequest(newAliTestRelayInfo(), relaycommon.TaskSubmitReq{
				Model:  "wan2.5-i2v-preview",
				Prompt: "animate",
				Image:  "https://example.com/first.png",
				Metadata: map[string]any{
					"parameters": map[string]any{"duration": tt.duration},
				},
			})

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "seconds must be between 1 and")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantDuration, aliReq.Parameters.Duration)
		})
	}
}

func TestAliValidationErrorCodeUsesDurationErrorType(t *testing.T) {
	err := fmt.Errorf("outer validation context: %w", changedAliDurationValidationError{})

	assert.Equal(t, "invalid_seconds", aliValidationErrorCode(err))
}

func TestConvertToAliRequestRejectsNullMetadataParameters(t *testing.T) {
	_, err := (&TaskAdaptor{}).convertToAliRequest(newAliTestRelayInfo(), relaycommon.TaskSubmitReq{
		Model:  "wan2.5-i2v-preview",
		Prompt: "animate",
		Image:  "https://example.com/first.png",
		Metadata: map[string]any{
			"parameters": nil,
		},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "parameters")
}

func TestAliBuildRequestAndEstimateBillingUseSameFinalDuration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", nil)
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Model:  "wan2.5-i2v-preview",
		Prompt: "animate",
		Image:  "https://example.com/first.png",
		Metadata: map[string]any{
			"parameters": map[string]any{"duration": -1},
		},
	})
	info := newAliTestRelayInfo()
	adaptor := &TaskAdaptor{}

	bodyReader, err := adaptor.BuildRequestBody(context, info)
	require.NoError(t, err)
	body, err := io.ReadAll(bodyReader)
	require.NoError(t, err)
	var upstream map[string]any
	require.NoError(t, common.Unmarshal(body, &upstream))
	parameters, ok := upstream["parameters"].(map[string]any)
	require.True(t, ok)

	ratios := adaptor.EstimateBilling(context, info)
	require.NotNil(t, ratios)
	assert.Equal(t, float64(5), parameters["duration"])
	assert.Equal(t, float64(5), ratios["seconds"])
}

func TestAliValidationRejectsMetadataDurationOverMaximum(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := strings.NewReader(`{"model":"wan2.5-i2v-preview","prompt":"animate","image":"https://example.com/first.png","metadata":{"parameters":{"duration":3601}}}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/video/generations", body)
	request.Header.Set("Content-Type", "application/json")
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request

	adaptor := &TaskAdaptor{}
	info := newAliTestRelayInfo()
	require.Nil(t, adaptor.ValidateRequestAndSetAction(context, info))
	info.UpstreamModelName = "wan2.5-i2v-preview"
	taskErr := adaptor.ValidateMappedRequest(context, info)

	require.NotNil(t, taskErr)
	assert.Equal(t, "invalid_seconds", taskErr.Code)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
}
