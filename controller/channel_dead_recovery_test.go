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

	mk := func(id int, status int, monitor, recovery bool, minMinutes, maxMinutes int, statusTime int64) *model.Channel {
		ch := &model.Channel{Id: id, Status: status}
		settings := dto.ChannelOtherSettings{
			ChannelMonitorEnabled:         monitor,
			ChannelDeadRecoveryEnabled:    recovery,
			ChannelDeadRecoveryMinMinutes: minMinutes,
			ChannelDeadRecoveryMaxMinutes: maxMinutes,
		}
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

	monitored := mk(1, common.ChannelStatusAutoDisabled, true, true, 30, 30, now-3600)
	manual := mk(2, common.ChannelStatusManuallyDisabled, false, true, 30, 30, now-3600)
	enabled := mk(3, common.ChannelStatusEnabled, false, true, 30, 30, now-3600)
	freshDead := mk(4, common.ChannelStatusAutoDisabled, false, true, 30, 30, now-29*60)
	oldDead := mk(5, common.ChannelStatusAutoDisabled, false, true, 30, 30, now-30*60)
	disabledRecovery := mk(6, common.ChannelStatusAutoDisabled, false, false, 30, 30, now-3600)
	longDelay := mk(7, common.ChannelStatusAutoDisabled, false, true, 60, 60, now-45*60)

	got := filterDueDeadChannelRecoveryCandidates([]*model.Channel{
		monitored, manual, enabled, freshDead, oldDead, disabledRecovery, longDelay,
	}, now)

	ids := make([]int, 0, len(got))
	for _, ch := range got {
		ids = append(ids, ch.Id)
	}
	assert.NotContains(t, ids, 1)
	assert.NotContains(t, ids, 2)
	assert.NotContains(t, ids, 3)
	assert.NotContains(t, ids, 4)
	assert.Contains(t, ids, 5)
	assert.NotContains(t, ids, 6)
	assert.NotContains(t, ids, 7)
}

func TestFilterDueDeadChannelRecoveryCandidatesUsesFixedSafetyCap(t *testing.T) {
	now := int64(1_700_000_000)
	old := now - int64(model.DefaultChannelDeadRecoveryMaxMinutes+5)*60
	channels := make([]*model.Channel, 0, 10)
	for i := 1; i <= 10; i++ {
		ch := &model.Channel{Id: i, Status: common.ChannelStatusAutoDisabled}
		ch.SetOtherSettings(dto.ChannelOtherSettings{
			ChannelMonitorEnabled:         false,
			ChannelDeadRecoveryEnabled:    true,
			ChannelDeadRecoveryMinMinutes: model.DefaultChannelDeadRecoveryMinMinutes,
			ChannelDeadRecoveryMaxMinutes: model.DefaultChannelDeadRecoveryMaxMinutes,
		})
		ch.SetOtherInfo(map[string]interface{}{"status_time": old + int64(i)})
		channels = append(channels, ch)
	}
	got := filterDueDeadChannelRecoveryCandidates(channels, now)
	require.Len(t, got, 5)
}
