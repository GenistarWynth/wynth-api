package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func channelWithAutoRetryTimes(t *testing.T, retryTimes *int) *model.Channel {
	t.Helper()
	settings := map[string]any{}
	if retryTimes != nil {
		settings["auto_retry_times"] = *retryTimes
	}
	data, err := common.Marshal(settings)
	require.NoError(t, err)
	return &model.Channel{OtherSettings: string(data)}
}

func TestResolveChannelRetryTimesUsesChannelOverride(t *testing.T) {
	globalRetryTimes := 3

	assert.Equal(t, globalRetryTimes, ResolveChannelRetryTimes(globalRetryTimes, nil))
	assert.Equal(t, globalRetryTimes, ResolveChannelRetryTimes(globalRetryTimes, channelWithAutoRetryTimes(t, nil)))

	zero := 0
	assert.Equal(t, 0, ResolveChannelRetryTimes(globalRetryTimes, channelWithAutoRetryTimes(t, &zero)))

	one := 1
	assert.Equal(t, 1, ResolveChannelRetryTimes(globalRetryTimes, channelWithAutoRetryTimes(t, &one)))
}

func TestResolveChannelRetryTimesClampsUnsafeValues(t *testing.T) {
	tooHigh := 99
	negative := -1

	assert.Equal(t, ChannelAutoRetryTimesMax, ResolveChannelRetryTimes(3, channelWithAutoRetryTimes(t, &tooHigh)))
	assert.Equal(t, 3, ResolveChannelRetryTimes(3, channelWithAutoRetryTimes(t, &negative)))
	assert.Equal(t, 0, ResolveChannelRetryTimes(-1, channelWithAutoRetryTimes(t, nil)))
}
