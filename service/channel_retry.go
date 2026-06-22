package service

import (
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

const ChannelAutoRetryTimesMax = dto.ChannelAutoRetryTimesMax

func ResolveChannelRetryTimes(globalRetryTimes int, channel *model.Channel) int {
	if globalRetryTimes < 0 {
		globalRetryTimes = 0
	}
	if channel == nil {
		return globalRetryTimes
	}
	settings := channel.GetOtherSettings()
	if settings.AutoRetryTimes == nil {
		return globalRetryTimes
	}
	autoRetryTimes := *settings.AutoRetryTimes
	if autoRetryTimes < 0 {
		return globalRetryTimes
	}
	if autoRetryTimes > ChannelAutoRetryTimesMax {
		return ChannelAutoRetryTimesMax
	}
	return autoRetryTimes
}
