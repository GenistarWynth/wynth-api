package service

import (
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

type AutoPriorityUsageStats struct {
	WindowStart                int64   `json:"window_start"`
	UsageLogCount              int64   `json:"usage_log_count"`
	NormalCostUnits            float64 `json:"normal_cost_units"`
	AdjustedCostUnits          float64 `json:"adjusted_cost_units"`
	CacheAdjustedCostFactor    float64 `json:"cache_adjusted_cost_factor"`
	FirstTokenSampleCount      int64   `json:"first_token_sample_count"`
	FirstTokenLatencyTotalMS   int64   `json:"first_token_latency_total_ms"`
	AverageFirstTokenLatencyMS int64   `json:"average_first_token_latency_ms"`
}

type autoPriorityUsageLogOther struct {
	CompletionRatio       float64 `json:"completion_ratio"`
	CacheTokens           int     `json:"cache_tokens"`
	CacheRatio            float64 `json:"cache_ratio"`
	CacheCreationTokens   int     `json:"cache_creation_tokens"`
	CacheCreationRatio    float64 `json:"cache_creation_ratio"`
	CacheCreationTokens5m int     `json:"cache_creation_tokens_5m"`
	CacheCreationRatio5m  float64 `json:"cache_creation_ratio_5m"`
	CacheCreationTokens1h int     `json:"cache_creation_tokens_1h"`
	CacheCreationRatio1h  float64 `json:"cache_creation_ratio_1h"`
	CacheWriteTokens      int     `json:"cache_write_tokens"`
	InputTokensTotal      int     `json:"input_tokens_total"`
	FRT                   float64 `json:"frt"`
	IsChannelTest         bool    `json:"is_channel_test"`
}

func CollectAutoPriorityUsageStats(channelIDs []int, windowStart int64) (AutoPriorityUsageStats, error) {
	stats := AutoPriorityUsageStats{
		WindowStart: windowStart,
	}
	validChannelIDs := make([]int, 0, len(channelIDs))
	for _, channelID := range channelIDs {
		if channelID > 0 {
			validChannelIDs = append(validChannelIDs, channelID)
		}
	}
	if len(validChannelIDs) == 0 {
		return stats, nil
	}

	var logs []model.Log
	if err := model.LOG_DB.
		Where("type = ?", model.LogTypeConsume).
		Where("channel_id IN ?", validChannelIDs).
		Where("created_at >= ?", windowStart).
		Order("created_at asc, id asc").
		Find(&logs).Error; err != nil {
		return AutoPriorityUsageStats{}, err
	}

	return buildAutoPriorityUsageStatsFromLogs(logs, windowStart), nil
}

func buildAutoPriorityUsageStatsFromLogs(logs []model.Log, windowStart int64) AutoPriorityUsageStats {
	stats := AutoPriorityUsageStats{
		WindowStart: windowStart,
	}

	for _, log := range logs {
		if log.Type != model.LogTypeConsume {
			continue
		}
		if windowStart > 0 && log.CreatedAt < windowStart {
			continue
		}

		other := parseAutoPriorityUsageLogOther(log.Other)
		if isAutoPriorityUsageTestLog(log, other) {
			continue
		}

		stats.UsageLogCount++

		inputTotal := other.InputTokensTotal
		if inputTotal <= 0 {
			inputTotal = log.PromptTokens
		}
		if inputTotal < 0 {
			inputTotal = 0
		}

		completionTokens := log.CompletionTokens
		if completionTokens < 0 {
			completionTokens = 0
		}
		completionRatio := other.CompletionRatio
		if completionRatio <= 0 {
			completionRatio = 1
		}
		normalCost := float64(inputTotal) + float64(completionTokens)*completionRatio
		stats.NormalCostUnits += normalCost

		cacheReadTokens := clampToZero(other.CacheTokens)
		cacheReadRatio := other.CacheRatio
		if cacheReadRatio < 0 {
			cacheReadRatio = 0
		}

		cacheWriteTokensUsedForCost, cacheWriteUnits := autoPriorityCacheWriteCost(other)
		uncachedInput := inputTotal - cacheReadTokens - cacheWriteTokensUsedForCost
		if uncachedInput < 0 {
			uncachedInput = 0
		}

		adjustedCost := float64(uncachedInput) + float64(cacheReadTokens)*cacheReadRatio + cacheWriteUnits + float64(completionTokens)*completionRatio
		stats.AdjustedCostUnits += adjustedCost

		if other.FRT > 0 {
			stats.FirstTokenSampleCount++
			stats.FirstTokenLatencyTotalMS += int64(math.Round(other.FRT))
		}
	}

	if stats.FirstTokenSampleCount > 0 {
		stats.AverageFirstTokenLatencyMS = int64(math.Round(float64(stats.FirstTokenLatencyTotalMS) / float64(stats.FirstTokenSampleCount)))
	}

	if stats.NormalCostUnits > 0 {
		stats.CacheAdjustedCostFactor = stats.AdjustedCostUnits / stats.NormalCostUnits
	} else {
		stats.CacheAdjustedCostFactor = 1
	}
	return stats
}

func parseAutoPriorityUsageLogOther(raw string) autoPriorityUsageLogOther {
	var other autoPriorityUsageLogOther
	if strings.TrimSpace(raw) == "" {
		return other
	}
	_ = common.UnmarshalJsonStr(raw, &other)
	return other
}

func isAutoPriorityUsageTestLog(log model.Log, other autoPriorityUsageLogOther) bool {
	if other.IsChannelTest {
		return true
	}
	if strings.TrimSpace(log.Content) == "模型测试" {
		return true
	}
	if strings.TrimSpace(log.TokenName) == "模型测试" {
		return true
	}
	return false
}

func autoPriorityCacheWriteCost(other autoPriorityUsageLogOther) (int, float64) {
	if other.CacheCreationTokens5m > 0 || other.CacheCreationTokens1h > 0 {
		units := 0.0
		tokens := 0
		if other.CacheCreationTokens5m > 0 {
			ratio := other.CacheCreationRatio5m
			if ratio <= 0 {
				ratio = 1
			}
			units += float64(other.CacheCreationTokens5m) * ratio
			tokens += other.CacheCreationTokens5m
		}
		if other.CacheCreationTokens1h > 0 {
			ratio := other.CacheCreationRatio1h
			if ratio <= 0 {
				ratio = 1
			}
			units += float64(other.CacheCreationTokens1h) * ratio
			tokens += other.CacheCreationTokens1h
		}
		return tokens, units
	}

	if other.CacheCreationTokens > 0 {
		ratio := other.CacheCreationRatio
		if ratio <= 0 {
			ratio = 1
		}
		return other.CacheCreationTokens, float64(other.CacheCreationTokens) * ratio
	}

	if other.CacheWriteTokens > 0 {
		return other.CacheWriteTokens, float64(other.CacheWriteTokens)
	}

	return 0, 0
}

func clampToZero(v int) int {
	if v > 0 {
		return v
	}
	return 0
}
