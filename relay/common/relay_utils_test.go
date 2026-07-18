package common

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestTaskDurationBounds guards the billing invariant that user-supplied
// video duration (a quota multiplier via OtherRatio "seconds") is bounded, so
// it can never overflow quota calculation into a negative charge.
func TestTaskDurationBounds(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newContext := func(t *testing.T, body string) (*gin.Context, *RelayInfo) {
		request := httptest.NewRequest(http.MethodPost, "/v1/video/generations", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		context, _ := gin.CreateTestContext(httptest.NewRecorder())
		context.Request = request
		return context, &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}
	}

	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name:    "huge duration is rejected",
			body:    `{"model":"sora-2","prompt":"a cat","duration":9999999999}`,
			wantErr: true,
		},
		{
			name:    "huge seconds string is rejected",
			body:    `{"model":"sora-2","prompt":"a cat","seconds":"9999999999"}`,
			wantErr: true,
		},
		{
			name:    "negative duration is rejected",
			body:    `{"model":"sora-2","prompt":"a cat","duration":-8}`,
			wantErr: true,
		},
		{
			name: "normal duration is accepted",
			body: `{"model":"sora-2","prompt":"a cat","seconds":"8"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" (multipart direct)", func(t *testing.T) {
			context, info := newContext(t, tt.body)
			taskErr := ValidateMultipartDirect(context, info)
			if tt.wantErr {
				require.NotNil(t, taskErr)
				require.Equal(t, "invalid_seconds", taskErr.Code)
			} else {
				require.Nil(t, taskErr)
			}
		})
		t.Run(tt.name+" (basic task request)", func(t *testing.T) {
			context, info := newContext(t, tt.body)
			taskErr := ValidateBasicTaskRequest(context, info, constant.TaskActionGenerate)
			if tt.wantErr {
				require.NotNil(t, taskErr)
				require.Equal(t, "invalid_seconds", taskErr.Code)
			} else {
				require.Nil(t, taskErr)
			}
		})
	}
}

func TestValidateMultipartDirectNormalizesImageInputs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		body       string
		wantImages []string
		wantAction string
	}{
		{
			name:       "single Image is trimmed into Images",
			body:       `{"model":"wan2.7-i2v","prompt":"animate","image":" https://example.com/first.png "}`,
			wantImages: []string{"https://example.com/first.png"},
			wantAction: constant.TaskActionGenerate,
		},
		{
			name:       "blank entries are removed from Images",
			body:       `{"model":"wan2.7-i2v","prompt":"animate","images":[" "," https://example.com/first.png ",""," https://example.com/last.png "]}`,
			wantImages: []string{"https://example.com/first.png", "https://example.com/last.png"},
			wantAction: constant.TaskActionGenerate,
		},
		{
			name:       "existing Images are not replaced by InputReference",
			body:       `{"model":"wan2.7-i2v","prompt":"animate","images":["https://example.com/first.png","https://example.com/last.png"],"input_reference":"https://example.com/reference.png"}`,
			wantImages: []string{"https://example.com/first.png", "https://example.com/last.png"},
			wantAction: constant.TaskActionGenerate,
		},
		{
			name:       "blank Image does not create an empty Images entry",
			body:       `{"model":"wan2.7-t2v","prompt":"animate","image":"   "}`,
			wantImages: nil,
			wantAction: constant.TaskActionTextGenerate,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/video/generations", strings.NewReader(tt.body))
			request.Header.Set("Content-Type", "application/json")
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = request
			info := &RelayInfo{TaskRelayInfo: &TaskRelayInfo{}}

			taskErr := ValidateMultipartDirect(context, info)

			require.Nil(t, taskErr)
			stored, err := GetTaskRequest(context)
			require.NoError(t, err)
			require.Equal(t, tt.wantImages, stored.Images)
			require.Equal(t, tt.wantAction, info.Action)
		})
	}
}
