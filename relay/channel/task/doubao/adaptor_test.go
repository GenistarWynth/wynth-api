package doubao

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetVideoInputRatio(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		resolution string
		hasVideo   bool
		wantRatio  float64
		wantOK     bool
	}{
		{name: "standard default without video", model: "doubao-seedance-2-0-260128", wantRatio: 1, wantOK: true},
		{name: "standard 480p without video", model: "doubao-seedance-2-0-260128", resolution: "480p", wantRatio: 1, wantOK: true},
		{name: "standard 720p with video", model: "doubao-seedance-2-0-260128", resolution: "720p", hasVideo: true, wantRatio: 28.0 / 46.0, wantOK: true},
		{name: "standard unknown resolution uses low tier", model: "doubao-seedance-2-0-260128", resolution: "8k", hasVideo: true, wantRatio: 28.0 / 46.0, wantOK: true},
		{name: "standard 1080p without video", model: "doubao-seedance-2-0-260128", resolution: "1080p", wantRatio: 51.0 / 46.0, wantOK: true},
		{name: "standard 1080p with video", model: "doubao-seedance-2-0-260128", resolution: "1080p", hasVideo: true, wantRatio: 31.0 / 46.0, wantOK: true},
		{name: "standard 4k without video", model: "doubao-seedance-2-0-260128", resolution: "4k", wantRatio: 26.0 / 46.0, wantOK: true},
		{name: "standard 4k with video", model: "doubao-seedance-2-0-260128", resolution: "4k", hasVideo: true, wantRatio: 16.0 / 46.0, wantOK: true},
		{name: "standard trims and folds 1080p", model: "doubao-seedance-2-0-260128", resolution: " 1080P ", hasVideo: true, wantRatio: 31.0 / 46.0, wantOK: true},
		{name: "standard trims and folds 4k", model: "doubao-seedance-2-0-260128", resolution: " 4K ", hasVideo: true, wantRatio: 16.0 / 46.0, wantOK: true},
		{name: "fast default without video", model: "doubao-seedance-2-0-fast-260128", wantRatio: 1, wantOK: true},
		{name: "fast low tier with video", model: "doubao-seedance-2-0-fast-260128", resolution: "720p", hasVideo: true, wantRatio: 22.0 / 37.0, wantOK: true},
		{name: "fast 1080p without video is unconfigured", model: "doubao-seedance-2-0-fast-260128", resolution: "1080p", wantRatio: 1, wantOK: true},
		{name: "fast 1080p with video is unconfigured", model: "doubao-seedance-2-0-fast-260128", resolution: "1080p", hasVideo: true, wantRatio: 1, wantOK: true},
		{name: "fast 4k without video is unconfigured", model: "doubao-seedance-2-0-fast-260128", resolution: "4k", wantRatio: 1, wantOK: true},
		{name: "fast 4k with video is unconfigured", model: "doubao-seedance-2-0-fast-260128", resolution: "4k", hasVideo: true, wantRatio: 1, wantOK: true},
		{name: "unknown model", model: "unknown", resolution: "1080p", hasVideo: true, wantRatio: 0, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRatio, gotOK := GetVideoInputRatio(tt.model, tt.resolution, tt.hasVideo)

			assert.Equal(t, tt.wantOK, gotOK)
			assert.InDelta(t, tt.wantRatio, gotRatio, 1e-12)
		})
	}
}

func TestTaskAdaptorEstimateBilling(t *testing.T) {
	tests := []struct {
		name          string
		originModel   string
		upstreamModel string
		metadataJSON  string
		wantRatio     float64
		wantNil       bool
		noChannelMeta bool
	}{
		{
			name:         "standard 1080p without video",
			originModel:  "doubao-seedance-2-0-260128",
			metadataJSON: `{"resolution":"1080p","content":[{"type":"text","text":"prompt"}]}`,
			wantRatio:    51.0 / 46.0,
		},
		{
			name:         "standard 4k without video",
			originModel:  "doubao-seedance-2-0-260128",
			metadataJSON: `{"resolution":"4k"}`,
			wantRatio:    26.0 / 46.0,
		},
		{
			name:         "standard 4k with real video item",
			originModel:  "doubao-seedance-2-0-260128",
			metadataJSON: `{"resolution":"4k","content":[{"type":"video_url","video_url":{"url":"https://example.com/input.mp4"}}]}`,
			wantRatio:    16.0 / 46.0,
		},
		{
			name:         "fast low tier with video",
			originModel:  "doubao-seedance-2-0-fast-260128",
			metadataJSON: `{"resolution":"720p","content":[{"video_url":{"url":"https://example.com/input.mp4"}}]}`,
			wantRatio:    22.0 / 37.0,
		},
		{
			name:          "mapped alias uses upstream canonical model",
			originModel:   "public-seedance-alias",
			upstreamModel: "doubao-seedance-2-0-260128",
			metadataJSON:  `{"resolution":"1080p","content":[{"type":"video_url","video_url":{"url":"https://example.com/input.mp4"}}]}`,
			wantRatio:     31.0 / 46.0,
		},
		{
			name:         "base ratio returns nil",
			originModel:  "doubao-seedance-2-0-260128",
			metadataJSON: `{"resolution":"720p"}`,
			wantNil:      true,
		},
		{
			name:         "unknown model returns nil",
			originModel:  "unknown",
			metadataJSON: `{"resolution":"4k","content":[{"type":"video_url","video_url":{"url":"https://example.com/input.mp4"}}]}`,
			wantNil:      true,
		},
		{
			name:          "missing channel metadata falls back to origin model",
			originModel:   "doubao-seedance-2-0-260128",
			metadataJSON:  `{"resolution":"1080p"}`,
			wantRatio:     51.0 / 46.0,
			noChannelMeta: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata := map[string]interface{}{}
			require.NoError(t, common.UnmarshalJsonStr(tt.metadataJSON, &metadata))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set("task_request", relaycommon.TaskSubmitReq{Metadata: metadata})
			info := &relaycommon.RelayInfo{OriginModelName: tt.originModel}
			if !tt.noChannelMeta {
				info.ChannelMeta = &relaycommon.ChannelMeta{UpstreamModelName: tt.upstreamModel}
			}

			got := (&TaskAdaptor{}).EstimateBilling(c, info)

			if tt.wantNil {
				assert.Nil(t, got)
				return
			}
			require.Contains(t, got, "video_input")
			assert.InDelta(t, tt.wantRatio, got["video_input"], 1e-12)
		})
	}
}

func TestTaskAdaptorBuildRequestBody(t *testing.T) {
	t.Run("sends nonempty safety identifier priority zero and explicit false booleans", func(t *testing.T) {
		metadata := map[string]interface{}{}
		require.NoError(t, common.UnmarshalJsonStr(
			`{"safety_identifier":"safety-user-123","priority":0,"camera_fixed":false,"watermark":false}`,
			&metadata,
		))
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set("task_request", relaycommon.TaskSubmitReq{Model: "doubao-seedance-2-0-260128", Metadata: metadata})
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

		body, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
		require.NoError(t, err)
		payload := map[string]interface{}{}
		require.NoError(t, common.DecodeJson(body, &payload))

		assert.Equal(t, "safety-user-123", payload["safety_identifier"])
		assert.Equal(t, float64(0), payload["priority"])
		assert.Equal(t, false, payload["camera_fixed"])
		assert.Equal(t, false, payload["watermark"])
	})

	t.Run("omits absent safety identifier and priority", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set("task_request", relaycommon.TaskSubmitReq{Model: "doubao-seedance-2-0-260128"})
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

		body, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
		require.NoError(t, err)
		payload := map[string]interface{}{}
		require.NoError(t, common.DecodeJson(body, &payload))

		assert.NotContains(t, payload, "safety_identifier")
		assert.NotContains(t, payload, "priority")
	})

	t.Run("sends explicit empty safety identifier and priority zero", func(t *testing.T) {
		metadata := map[string]interface{}{}
		require.NoError(t, common.UnmarshalJsonStr(`{"safety_identifier":"","priority":0}`, &metadata))
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set("task_request", relaycommon.TaskSubmitReq{Model: "doubao-seedance-2-0-260128", Metadata: metadata})
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

		body, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
		require.NoError(t, err)
		payload := map[string]interface{}{}
		require.NoError(t, common.DecodeJson(body, &payload))

		require.Contains(t, payload, "safety_identifier")
		assert.Equal(t, "", payload["safety_identifier"])
		require.Contains(t, payload, "priority")
		assert.Equal(t, float64(0), payload["priority"])
	})

	t.Run("mapped model stays canonical despite metadata model", func(t *testing.T) {
		metadata := map[string]interface{}{}
		require.NoError(t, common.UnmarshalJsonStr(`{"model":"metadata-override"}`, &metadata))
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set("task_request", relaycommon.TaskSubmitReq{
			Model:    "public-seedance-alias",
			Metadata: metadata,
		})
		info := &relaycommon.RelayInfo{
			OriginModelName: "public-seedance-alias",
			ChannelMeta: &relaycommon.ChannelMeta{
				IsModelMapped:     true,
				UpstreamModelName: "doubao-seedance-2-0-260128",
			},
		}

		body, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
		require.NoError(t, err)
		payload := map[string]interface{}{}
		require.NoError(t, common.DecodeJson(body, &payload))

		assert.Equal(t, "doubao-seedance-2-0-260128", payload["model"])
	})
}

func TestTaskAdaptorEstimateAndBuildConsistency(t *testing.T) {
	metadata := map[string]interface{}{}
	require.NoError(t, common.UnmarshalJsonStr(
		`{"model":"metadata-override","resolution":" 4K ","content":[{"type":"video_url","video_url":{"url":"https://example.com/input.mp4"}}]}`,
		&metadata,
	))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Model:    "public-seedance-alias",
		Prompt:   "make a video",
		Metadata: metadata,
	})
	info := &relaycommon.RelayInfo{
		OriginModelName: "public-seedance-alias",
		ChannelMeta: &relaycommon.ChannelMeta{
			IsModelMapped:     true,
			UpstreamModelName: "doubao-seedance-2-0-260128",
		},
	}
	adaptor := &TaskAdaptor{}

	estimate := adaptor.EstimateBilling(c, info)
	require.Contains(t, estimate, "video_input")
	assert.InDelta(t, 16.0/46.0, estimate["video_input"], 1e-12)

	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	var payload requestPayload
	require.NoError(t, common.DecodeJson(body, &payload))

	assert.Equal(t, "doubao-seedance-2-0-260128", payload.Model)
	assert.Equal(t, " 4K ", payload.Resolution)
	require.Len(t, payload.Content, 2)
	assert.Equal(t, "video_url", payload.Content[0].Type)
	require.NotNil(t, payload.Content[0].VideoURL)
	assert.Equal(t, "https://example.com/input.mp4", payload.Content[0].VideoURL.URL)
	assert.Equal(t, "text", payload.Content[1].Type)
	assert.Equal(t, "make a video", payload.Content[1].Text)
}
