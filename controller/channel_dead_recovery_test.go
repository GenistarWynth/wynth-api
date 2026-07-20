package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterDueDeadChannelRecoveryCandidates(t *testing.T) {
	now := int64(1_700_000_000)

	mk := func(id int, status int, monitor bool, statusTime int64) *model.Channel {
		ch := &model.Channel{Id: id, Status: status}
		settings := dto.ChannelOtherSettings{ChannelMonitorEnabled: monitor}
		if monitor {
			settings.ChannelMonitorIntervalMinutes = 5
		}
		ch.SetOtherSettings(settings)
		info := map[string]interface{}{}
		if statusTime > 0 {
			info["status_time"] = statusTime
			info["status_reason"] = "auto disabled"
		}
		ch.SetOtherInfo(info)
		return ch
	}

	monitored := mk(1, common.ChannelStatusAutoDisabled, true, now-3600)
	manual := mk(2, common.ChannelStatusManuallyDisabled, false, now-3600)
	enabled := mk(3, common.ChannelStatusEnabled, false, now-3600)
	freshDead := mk(4, common.ChannelStatusAutoDisabled, false, now)
	oldDead := mk(5, common.ChannelStatusAutoDisabled, false, now-int64(defaultDeadChannelRecoveryMaxMinutes+1)*60)

	got := filterDueDeadChannelRecoveryCandidates([]*model.Channel{
		monitored, manual, enabled, freshDead, oldDead,
	}, now, 10)

	ids := make([]int, 0, len(got))
	for _, ch := range got {
		ids = append(ids, ch.Id)
	}
	assert.NotContains(t, ids, 1)
	assert.NotContains(t, ids, 2)
	assert.NotContains(t, ids, 3)
	assert.NotContains(t, ids, 4)
	assert.Contains(t, ids, 5)
}

func TestDeadChannelRecoveryDelaySecondsBoundedAndStable(t *testing.T) {
	ch := &model.Channel{Id: 42, Status: common.ChannelStatusAutoDisabled}
	ch.SetOtherInfo(map[string]interface{}{"status_time": int64(1_700_000_000)})
	d1 := deadChannelRecoveryDelaySeconds(ch)
	d2 := deadChannelRecoveryDelaySeconds(ch)
	require.Equal(t, d1, d2, "delay must be stable for same channel+status_time")
	minSec := int64(defaultDeadChannelRecoveryMinMinutes * 60)
	maxSec := int64(defaultDeadChannelRecoveryMaxMinutes * 60)
	assert.GreaterOrEqual(t, d1, minSec)
	assert.LessOrEqual(t, d1, maxSec)

	ch2 := &model.Channel{Id: 42, Status: common.ChannelStatusAutoDisabled}
	ch2.SetOtherInfo(map[string]interface{}{"status_time": int64(1_700_000_001)})
	d3 := deadChannelRecoveryDelaySeconds(ch2)
	assert.NotEqual(t, d1, d3)
}

func TestFilterDueDeadChannelRecoveryCandidatesCapsAndShuffles(t *testing.T) {
	now := int64(1_700_000_000)
	old := now - int64(defaultDeadChannelRecoveryMaxMinutes+5)*60
	channels := make([]*model.Channel, 0, 10)
	for i := 1; i <= 10; i++ {
		ch := &model.Channel{Id: i, Status: common.ChannelStatusAutoDisabled}
		ch.SetOtherSettings(dto.ChannelOtherSettings{ChannelMonitorEnabled: false})
		ch.SetOtherInfo(map[string]interface{}{"status_time": old + int64(i)})
		channels = append(channels, ch)
	}
	got := filterDueDeadChannelRecoveryCandidates(channels, now, 3)
	require.Len(t, got, 3)
}
