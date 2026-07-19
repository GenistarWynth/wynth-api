package gemini

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRequestURLUsesAccountPoolRuntimeBaseURLForGeminiSurfaces(t *testing.T) {
	tests := []struct {
		name        string
		configure   func(*relaycommon.RelayInfo)
		runtimeBase string
		want        string
	}{
		{
			name:        "gemini api",
			runtimeBase: "https://generativelanguage.googleapis.com/account",
			want:        "https://generativelanguage.googleapis.com/account/v1beta/models/gemini-2.5-pro:generateContent",
		},
		{
			name: "vertex",
			configure: func(info *relaycommon.RelayInfo) {
				info.RuntimeVertexServiceAccount = true
				info.RuntimeVertexProjectID = "project-1"
				info.RuntimeVertexLocation = "us-central1"
			},
			runtimeBase: "https://us-central1-aiplatform.googleapis.com/account",
			want:        "https://us-central1-aiplatform.googleapis.com/account/v1/projects/project-1/locations/us-central1/publishers/google/models/gemini-2.5-pro:generateContent",
		},
		{
			name: "code assist",
			configure: func(info *relaycommon.RelayInfo) {
				info.RuntimeGeminiOAuthType = service.AccountPoolGeminiOAuthTypeCodeAssist
			},
			runtimeBase: "https://cloudcode-pa.googleapis.com/account",
			want:        "https://cloudcode-pa.googleapis.com/account/v1internal:generateContent",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				RuntimeBaseURL: test.runtimeBase,
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl:    "https://channel.example",
					UpstreamModelName: "gemini-2.5-pro",
				},
			}
			if test.configure != nil {
				test.configure(info)
			}

			got, err := (&Adaptor{}).GetRequestURL(info)
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}
