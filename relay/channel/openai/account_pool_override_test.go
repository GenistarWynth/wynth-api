package openai

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRequestURLUsesAccountPoolRuntimeBaseURL(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RequestURLPath: "/v1/chat/completions",
		RuntimeBaseURL: "https://api.openai.com/account",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeOpenAI,
			ChannelBaseUrl: "https://channel.example",
		},
	}

	got, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://api.openai.com/account/v1/chat/completions", got)
}
