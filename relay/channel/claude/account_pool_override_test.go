package claude

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRequestURLUsesAccountPoolRuntimeBaseURL(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RuntimeBaseURL: "https://api.anthropic.com/account",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://channel.example",
		},
	}

	got, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://api.anthropic.com/account/v1/messages", got)
}
