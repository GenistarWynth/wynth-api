package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustAutoPriorityOtherJSON(t *testing.T, other map[string]interface{}) string {
	t.Helper()

	data, err := common.Marshal(other)
	require.NoError(t, err)
	return string(data)
}

func TestBuildAutoPriorityUsageStats(t *testing.T) {
	t.Run("uses cache read and completion pricing", func(t *testing.T) {
		windowStart := int64(100)
		stats := buildAutoPriorityUsageStatsFromLogs([]model.Log{
			{
				CreatedAt:        150,
				Type:             model.LogTypeConsume,
				ChannelId:        7,
				Content:          "normal request",
				PromptTokens:     1000,
				CompletionTokens: 100,
				Other: mustAutoPriorityOtherJSON(t, map[string]interface{}{
					"input_tokens_total": 1000,
					"cache_tokens":       800,
					"cache_ratio":        0.1,
					"completion_ratio":   2,
					"frt":                1200,
				}),
			},
		}, windowStart)

		require.Equal(t, int64(1), stats.UsageLogCount)
		assert.InDelta(t, 1200.0, stats.NormalCostUnits, 0.0001)
		assert.InDelta(t, 480.0, stats.AdjustedCostUnits, 0.0001)
		assert.InDelta(t, 0.4, stats.CacheAdjustedCostFactor, 0.0001)
		assert.Equal(t, int64(1), stats.FirstTokenSampleCount)
		assert.Equal(t, int64(1200), stats.FirstTokenLatencyTotalMS)
		assert.Equal(t, int64(1200), stats.AverageFirstTokenLatencyMS)
	})

	t.Run("uses split cache creation buckets without double counting aggregate", func(t *testing.T) {
		stats := buildAutoPriorityUsageStatsFromLogs([]model.Log{
			{
				CreatedAt:    200,
				Type:         model.LogTypeConsume,
				ChannelId:    8,
				Content:      "split creation request",
				PromptTokens: 1000,
				Other: mustAutoPriorityOtherJSON(t, map[string]interface{}{
					"input_tokens_total":       1000,
					"cache_tokens":             300,
					"cache_ratio":              0.1,
					"cache_creation_tokens":    999,
					"cache_creation_ratio":     0.5,
					"cache_creation_tokens_5m": 200,
					"cache_creation_ratio_5m":  1.25,
					"cache_creation_tokens_1h": 100,
					"cache_creation_ratio_1h":  2.0,
					"cache_write_tokens":       300,
					"completion_ratio":         1,
				}),
			},
		}, 100)

		require.Equal(t, int64(1), stats.UsageLogCount)
		assert.InDelta(t, 1000.0, stats.NormalCostUnits, 0.0001)
		assert.InDelta(t, 880.0, stats.AdjustedCostUnits, 0.0001)
		assert.InDelta(t, 0.88, stats.CacheAdjustedCostFactor, 0.0001)
	})

	t.Run("excludes channel test logs", func(t *testing.T) {
		stats := buildAutoPriorityUsageStatsFromLogs([]model.Log{
			{
				CreatedAt: 120,
				Type:      model.LogTypeConsume,
				ChannelId: 9,
				Content:   "模型测试",
				Other: mustAutoPriorityOtherJSON(t, map[string]interface{}{
					"input_tokens_total": 1000,
					"cache_tokens":       100,
					"cache_ratio":        0.1,
				}),
			},
			{
				CreatedAt: 130,
				Type:      model.LogTypeConsume,
				ChannelId: 9,
				TokenName: " 模型测试 ",
				Other: mustAutoPriorityOtherJSON(t, map[string]interface{}{
					"input_tokens_total": 1000,
					"cache_tokens":       100,
					"cache_ratio":        0.1,
				}),
			},
			{
				CreatedAt: 140,
				Type:      model.LogTypeConsume,
				ChannelId: 9,
				Content:   "normal request",
				Other: mustAutoPriorityOtherJSON(t, map[string]interface{}{
					"input_tokens_total": 1000,
					"cache_tokens":       100,
					"cache_ratio":        0.1,
					"is_channel_test":    true,
				}),
			},
			{
				CreatedAt:        150,
				Type:             model.LogTypeConsume,
				ChannelId:        9,
				Content:          "real request",
				PromptTokens:     1000,
				CompletionTokens: 100,
				Other: mustAutoPriorityOtherJSON(t, map[string]interface{}{
					"input_tokens_total": 1000,
					"cache_tokens":       100,
					"cache_ratio":        0.1,
					"completion_ratio":   1.5,
				}),
			},
		}, 100)

		require.Equal(t, int64(1), stats.UsageLogCount)
		assert.InDelta(t, 1150.0, stats.NormalCostUnits, 0.0001)
		assert.InDelta(t, 1060.0, stats.AdjustedCostUnits, 0.0001)
		assert.InDelta(t, 1060.0/1150.0, stats.CacheAdjustedCostFactor, 0.0001)
	})
}
