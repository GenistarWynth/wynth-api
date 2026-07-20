package model

import (
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const ManuallyDisabledChannelDefaultPriority = int64(-1)

type ManuallyDisabledChannelSinkResult struct {
	ChannelID   int
	LocalGroup  string
	OldPriority int64
	NewPriority int64
	Applied     bool
}

// SinkManuallyDisabledChannels moves the selected manually-disabled channels
// below every enabled peer in their local group and keeps ability priorities in
// sync. A nil channelIDs slice selects every manually-disabled channel; a
// non-nil empty slice selects none. Callers may override a group's sink priority
// when a fresh auto-priority cohort score is available.
func SinkManuallyDisabledChannels(db *gorm.DB, channelIDs []int, sinkPrioritiesByGroup map[string]int64) ([]ManuallyDisabledChannelSinkResult, error) {
	if channelIDs != nil && len(channelIDs) == 0 {
		return nil, nil
	}
	if db == nil {
		db = DB
	}

	var channels []Channel
	query := db.Select("id", "group", "priority", "settings").
		Where("status = ?", common.ChannelStatusManuallyDisabled)
	if channelIDs != nil {
		query = query.Where("id IN ?", channelIDs)
	}
	if err := query.Order("id").Find(&channels).Error; err != nil {
		return nil, err
	}
	if len(channels) == 0 {
		return nil, nil
	}

	sinkByGroup := make(map[string]int64, len(sinkPrioritiesByGroup)+len(channels))
	for group, priority := range sinkPrioritiesByGroup {
		sinkByGroup[strings.TrimSpace(group)] = priority
	}
	groupsToResolve := make([]string, 0, len(channels))
	seenGroups := make(map[string]struct{}, len(channels))
	for _, channel := range channels {
		localGroup := strings.TrimSpace(channel.Group)
		if _, configured := sinkByGroup[localGroup]; !configured {
			sinkByGroup[localGroup] = ManuallyDisabledChannelDefaultPriority
		}
		if _, seen := seenGroups[localGroup]; seen {
			continue
		}
		seenGroups[localGroup] = struct{}{}
		groupsToResolve = append(groupsToResolve, localGroup)
	}
	if len(groupsToResolve) > 0 {
		var enabledPeers []Channel
		if err := db.Select("id", "group", "priority").
			Where("status = ?", common.ChannelStatusEnabled).
			Where(commonGroupCol+" IN ?", groupsToResolve).
			Find(&enabledPeers).Error; err != nil {
			return nil, err
		}
		for _, peer := range enabledPeers {
			localGroup := strings.TrimSpace(peer.Group)
			peerPriority := peer.GetPriority()
			if peerPriority > sinkByGroup[localGroup] {
				continue
			}
			if peerPriority == math.MinInt64 {
				sinkByGroup[localGroup] = math.MinInt64
				continue
			}
			sinkByGroup[localGroup] = peerPriority - 1
		}
	}

	results := make([]ManuallyDisabledChannelSinkResult, 0, len(channels))
	for _, channel := range channels {
		localGroup := strings.TrimSpace(channel.Group)
		sinkPriority := sinkByGroup[localGroup]
		settings, settingsChanged := clearChannelAutoPriorityRunMetadata(channel.OtherSettings)
		priorityChanged := channel.GetPriority() != sinkPriority

		if priorityChanged || settingsChanged {
			updates := map[string]any{"priority": sinkPriority}
			if settingsChanged {
				updates["settings"] = settings
			}
			if err := db.Model(&Channel{}).
				Where("id = ? AND status = ?", channel.Id, common.ChannelStatusManuallyDisabled).
				Updates(updates).Error; err != nil {
				return nil, err
			}
		}

		manuallyDisabledChannelID := db.Model(&Channel{}).
			Select("id").
			Where("id = ? AND status = ?", channel.Id, common.ChannelStatusManuallyDisabled)
		var staleAbilityCount int64
		if err := db.Model(&Ability{}).
			Where("channel_id IN (?)", manuallyDisabledChannelID).
			Where("priority IS NULL OR priority <> ?", sinkPriority).
			Count(&staleAbilityCount).Error; err != nil {
			return nil, err
		}
		if staleAbilityCount > 0 {
			if err := db.Model(&Ability{}).
				Where("channel_id IN (?)", manuallyDisabledChannelID).
				Update("priority", sinkPriority).Error; err != nil {
				return nil, err
			}
		}

		results = append(results, ManuallyDisabledChannelSinkResult{
			ChannelID:   channel.Id,
			LocalGroup:  localGroup,
			OldPriority: channel.GetPriority(),
			NewPriority: sinkPriority,
			Applied:     priorityChanged || settingsChanged || staleAbilityCount > 0,
		})
	}
	return results, nil
}

func clearChannelAutoPriorityRunMetadata(encoded string) (string, bool) {
	if strings.TrimSpace(encoded) == "" {
		return encoded, false
	}
	settings := make(map[string]any)
	if err := common.UnmarshalJsonStr(encoded, &settings); err != nil {
		return encoded, false
	}
	changed := false
	for _, key := range []string{
		"channel_auto_priority_last_run_at",
		"channel_auto_priority_last_applied_at",
		"channel_auto_priority_last_score",
	} {
		if _, exists := settings[key]; !exists {
			continue
		}
		delete(settings, key)
		changed = true
	}
	if !changed {
		return encoded, false
	}
	data, err := common.Marshal(settings)
	if err != nil {
		return encoded, false
	}
	return string(data), true
}
