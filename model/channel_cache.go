package model

import (
	"fmt"
	"math/big"
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

var group2model2channels map[string]map[string][]int // enabled channel
var channelsIDM map[int]*Channel                     // all channels include disabled
// channel2advancedCustomConfig caches parsed Advanced Custom (type 58) configs so
// path-aware selection avoids re-parsing JSON per request. Refreshed on full sync.
var channel2advancedCustomConfig map[int]*dto.AdvancedCustomConfig
var enabledAccountPoolChannelIDsCache map[int]struct{}
var channelSyncLock sync.RWMutex

func InitChannelCache() {
	if !common.MemoryCacheEnabled {
		return
	}
	newChannelId2channel := make(map[int]*Channel)
	newChannel2advancedCustomConfig := make(map[int]*dto.AdvancedCustomConfig)
	var channels []*Channel
	DB.Find(&channels)
	enabledAccountPoolChannelIDs, err := EnabledAccountPoolRuntimeChannelIDs()
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to load enabled account pool channel ids: %v", err))
		enabledAccountPoolChannelIDs = map[int]struct{}{}
	}
	for _, channel := range channels {
		newChannelId2channel[channel.Id] = channel
		if channel.Type == constant.ChannelTypeAdvancedCustom {
			if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
				newChannel2advancedCustomConfig[channel.Id] = config
			}
		}
	}
	var abilities []*Ability
	DB.Find(&abilities)
	groups := make(map[string]bool)
	for _, ability := range abilities {
		groups[ability.Group] = true
	}
	newGroup2model2channels := make(map[string]map[string][]int)
	for group := range groups {
		newGroup2model2channels[group] = make(map[string][]int)
	}
	// Ability rows are authoritative for group/model eligibility. Channel.Models
	// alone cannot represent a disabled individual ability.
	for _, ability := range abilities {
		if !ability.Enabled {
			continue
		}
		channel, ok := newChannelId2channel[ability.ChannelId]
		if !ok {
			continue
		}
		if channel.Status != common.ChannelStatusEnabled {
			if _, enabledByAccountPool := enabledAccountPoolChannelIDs[channel.Id]; !enabledByAccountPool {
				continue // skip disabled channels unless an enabled account-pool binding exposes them
			}
		}
		if _, ok := newGroup2model2channels[ability.Group][ability.Model]; !ok {
			newGroup2model2channels[ability.Group][ability.Model] = make([]int, 0)
		}
		newGroup2model2channels[ability.Group][ability.Model] = append(
			newGroup2model2channels[ability.Group][ability.Model],
			channel.Id,
		)
	}

	// sort by priority
	for group, model2channels := range newGroup2model2channels {
		for model, channels := range model2channels {
			sort.Slice(channels, func(i, j int) bool {
				return newChannelId2channel[channels[i]].GetPriority() > newChannelId2channel[channels[j]].GetPriority()
			})
			newGroup2model2channels[group][model] = channels
		}
	}

	channelSyncLock.Lock()
	group2model2channels = newGroup2model2channels
	//channelsIDM = newChannelId2channel
	for i, channel := range newChannelId2channel {
		if channel.ChannelInfo.IsMultiKey {
			channel.Keys = channel.GetKeys()
			if channel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
				if oldChannel, ok := channelsIDM[i]; ok {
					// 存在旧的渠道，如果是多key且轮询，保留轮询索引信息
					if oldChannel.ChannelInfo.IsMultiKey && oldChannel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
						channel.ChannelInfo.MultiKeyPollingIndex = oldChannel.ChannelInfo.MultiKeyPollingIndex
					}
				}
			}
		}
	}
	channelsIDM = newChannelId2channel
	channel2advancedCustomConfig = newChannel2advancedCustomConfig
	enabledAccountPoolChannelIDsCache = enabledAccountPoolChannelIDs
	channelSyncLock.Unlock()
	common.SysLog("channels synced from database")
}

func SyncChannelCache(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		common.SysLog("syncing channels from database")
		InitChannelCache()
	}
}

func GetRandomSatisfiedChannel(group string, model string, retry int, requestPath string, attemptedChannelIDs ...map[int]struct{}) (*Channel, error) {
	attempted := firstAttemptedChannelIDs(attemptedChannelIDs...)
	// if memory cache is disabled, get channel directly from database
	if !common.MemoryCacheEnabled {
		return GetChannel(group, model, retry, requestPath, attempted)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	// First, try to find channels with the exact model name.
	channels := filterChannelsByRequestPath(group2model2channels[group][model], requestPath)
	channels = filterAttemptedChannels(channels, attempted)

	// If no channels found, try to find channels with the normalized model name.
	if len(channels) == 0 {
		normalizedModel := ratio_setting.FormatMatchingModelName(model)
		channels = filterChannelsByRequestPath(group2model2channels[group][normalizedModel], requestPath)
		channels = filterAttemptedChannels(channels, attempted)
	}

	if len(channels) == 0 {
		return nil, nil
	}

	candidates := make([]*Channel, 0, len(channels))
	for _, channelId := range channels {
		if channel, ok := channelsIDM[channelId]; ok {
			candidates = append(candidates, channel)
		} else {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelId)
		}
	}
	return selectHighestPriorityWeightedChannel(candidates)
}

func selectHighestPriorityWeightedChannel(channels []*Channel) (*Channel, error) {
	if len(channels) == 0 {
		return nil, nil
	}

	targetPriority := channels[0].GetPriority()
	for _, channel := range channels[1:] {
		if channel.GetPriority() > targetPriority {
			targetPriority = channel.GetPriority()
		}
	}

	totalWeight := new(big.Int)
	var targetChannels []*Channel
	for _, channel := range channels {
		if channel.GetPriority() == targetPriority {
			weight := new(big.Int).SetUint64(uint64(channel.GetWeightUint()))
			totalWeight.Add(totalWeight, weight)
			targetChannels = append(targetChannels, channel)
		}
	}

	if len(targetChannels) == 0 {
		return nil, fmt.Errorf("no channel found at priority %d", targetPriority)
	}

	if totalWeight.Sign() == 0 {
		// Established zero-weight behavior: when every candidate is zero,
		// choose uniformly. A zero-weight candidate remains unselectable while
		// any positive-weight candidate exists at the same priority.
		return targetChannels[rand.Intn(len(targetChannels))], nil
	}

	// Use arbitrary-precision accumulation so every uint weight and practical
	// candidate count is representable. Rejection sampling supplies an exactly
	// uniform integer below the total without modulo bias. The former smoothing
	// multiplier was common to all nonzero weights, so omitting it preserves the
	// same proportional policy while avoiding unnecessary large products.
	randomWeight := randomBigIntBelow(totalWeight)
	for _, channel := range targetChannels {
		weight := new(big.Int).SetUint64(uint64(channel.GetWeightUint()))
		if randomWeight.Cmp(weight) < 0 {
			return channel, nil
		}
		randomWeight.Sub(randomWeight, weight)
	}
	return nil, fmt.Errorf("channel not found at priority %d", targetPriority)
}

func randomBigIntBelow(limit *big.Int) *big.Int {
	bitLength := limit.BitLen()
	byteLength := (bitLength + 7) / 8
	excessBits := uint(byteLength*8 - bitLength)
	randomBytes := make([]byte, byteLength)

	for {
		_, _ = rand.Read(randomBytes)
		randomBytes[0] &= byte(0xff >> excessBits)
		candidate := new(big.Int).SetBytes(randomBytes)
		if candidate.Cmp(limit) < 0 {
			return candidate
		}
	}
}

func firstAttemptedChannelIDs(attemptedChannelIDs ...map[int]struct{}) map[int]struct{} {
	if len(attemptedChannelIDs) == 0 {
		return nil
	}
	return attemptedChannelIDs[0]
}

func filterAttemptedChannels(channels []int, attempted map[int]struct{}) []int {
	if len(channels) == 0 || len(attempted) == 0 {
		return channels
	}
	filtered := make([]int, 0, len(channels))
	for _, channelId := range channels {
		if _, ok := attempted[channelId]; ok {
			continue
		}
		filtered = append(filtered, channelId)
	}
	return filtered
}

// filterChannelsByRequestPath restricts candidates by request path. Only Advanced
// Custom (type 58) channels are path-checked: they are kept only when one of their
// configured routes matches requestPath. All other channel types always pass.
// When requestPath is empty (non-relay callers) filtering is skipped.
// Caller must hold channelSyncLock (read lock). The cached slice is never mutated.
func filterChannelsByRequestPath(channels []int, requestPath string) []int {
	if requestPath == "" || len(channels) == 0 {
		return channels
	}
	filtered := make([]int, 0, len(channels))
	for _, channelId := range channels {
		channel, ok := channelsIDM[channelId]
		if !ok {
			// keep it so the downstream consistency error is raised as before
			filtered = append(filtered, channelId)
			continue
		}
		if channel.Type != constant.ChannelTypeAdvancedCustom {
			filtered = append(filtered, channelId)
			continue
		}
		if config := channel2advancedCustomConfig[channelId]; config != nil && config.SupportsPath(requestPath) {
			filtered = append(filtered, channelId)
		}
	}
	return filtered
}

func CacheGetChannel(id int) (*Channel, error) {
	if !common.MemoryCacheEnabled {
		return GetChannelById(id, true)
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("渠道# %d，已不存在", id)
	}
	return c, nil
}

func CacheGetChannelInfo(id int) (*ChannelInfo, error) {
	if !common.MemoryCacheEnabled {
		channel, err := GetChannelById(id, true)
		if err != nil {
			return nil, err
		}
		return &channel.ChannelInfo, nil
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("渠道# %d，已不存在", id)
	}
	return &c.ChannelInfo, nil
}

func CacheUpdateChannelStatus(id int, status int) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	if channel, ok := channelsIDM[id]; ok {
		channel.Status = status
	}
	if status != common.ChannelStatusEnabled {
		if isEnabledAccountPoolRuntimeChannelID(id) {
			return
		}
		// delete the channel from group2model2channels
		for group, model2channels := range group2model2channels {
			for model, channels := range model2channels {
				for i, channelId := range channels {
					if channelId == id {
						// remove the channel from the slice
						group2model2channels[group][model] = append(channels[:i], channels[i+1:]...)
						break
					}
				}
			}
		}
	}
}

func isEnabledAccountPoolRuntimeChannelID(id int) bool {
	if enabledAccountPoolChannelIDsCache == nil {
		return false
	}
	_, ok := enabledAccountPoolChannelIDsCache[id]
	return ok
}

func CacheUpdateChannel(channel *Channel) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	if channel == nil {
		return
	}

	if channelsIDM == nil {
		channelsIDM = make(map[int]*Channel)
	}
	if oldChannel, ok := channelsIDM[channel.Id]; ok {
		logger.LogDebug(nil, "CacheUpdateChannel before: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, oldChannel.ChannelInfo.MultiKeyPollingIndex)
	}
	channelsIDM[channel.Id] = channel
	logger.LogDebug(nil, "CacheUpdateChannel after: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, channel.ChannelInfo.MultiKeyPollingIndex)
}
