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
	t.Run("isolates channels and keeps split cache creation pricing", func(t *testing.T) {
		statsByChannel := buildAutoPriorityUsageStatsFromLogs([]model.Log{
			{
				CreatedAt:        150,
				Type:             model.LogTypeConsume,
				ChannelId:        1,
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
			{
				CreatedAt:    160,
				Type:         model.LogTypeConsume,
				ChannelId:    2,
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

		require.Len(t, statsByChannel, 2)

		channel1, ok := statsByChannel[1]
		require.True(t, ok)
		assert.Equal(t, 1, channel1.ChannelID)
		assert.Equal(t, int64(100), channel1.WindowStart)
		assert.Equal(t, int64(1), channel1.UsageLogCount)
		assert.InDelta(t, 1200.0, channel1.NormalCostUnits, 0.0001)
		assert.InDelta(t, 480.0, channel1.AdjustedCostUnits, 0.0001)
		assert.InDelta(t, 0.4, channel1.CacheAdjustedCostFactor, 0.0001)
		assert.Equal(t, int64(1), channel1.FirstTokenSampleCount)
		assert.Equal(t, int64(1200), channel1.FirstTokenLatencyTotalMS)
		assert.Equal(t, int64(1200), channel1.AverageFirstTokenLatencyMS)

		channel2, ok := statsByChannel[2]
		require.True(t, ok)
		assert.Equal(t, 2, channel2.ChannelID)
		assert.Equal(t, int64(100), channel2.WindowStart)
		assert.Equal(t, int64(1), channel2.UsageLogCount)
		assert.InDelta(t, 1000.0, channel2.NormalCostUnits, 0.0001)
		assert.InDelta(t, 880.0, channel2.AdjustedCostUnits, 0.0001)
		assert.InDelta(t, 0.88, channel2.CacheAdjustedCostFactor, 0.0001)
		assert.Equal(t, int64(0), channel2.FirstTokenSampleCount)
		assert.Equal(t, int64(0), channel2.FirstTokenLatencyTotalMS)
		assert.Equal(t, int64(0), channel2.AverageFirstTokenLatencyMS)
	})

	t.Run("skips malformed other json and allows empty other", func(t *testing.T) {
		statsByChannel := buildAutoPriorityUsageStatsFromLogs([]model.Log{
			{
				CreatedAt: 110,
				Type:      model.LogTypeConsume,
				ChannelId: 3,
				Content:   "模型测试",
				Other: mustAutoPriorityOtherJSON(t, map[string]interface{}{
					"input_tokens_total": 1000,
					"cache_tokens":       100,
					"cache_ratio":        0.1,
				}),
			},
			{
				CreatedAt: 120,
				Type:      model.LogTypeConsume,
				ChannelId: 3,
				TokenName: " 模型测试 ",
				Other: mustAutoPriorityOtherJSON(t, map[string]interface{}{
					"input_tokens_total": 1000,
					"cache_tokens":       100,
					"cache_ratio":        0.1,
				}),
			},
			{
				CreatedAt: 130,
				Type:      model.LogTypeConsume,
				ChannelId: 3,
				Content:   "real request",
				Other:     `{"input_tokens_total":`,
			},
			{
				CreatedAt:        140,
				Type:             model.LogTypeConsume,
				ChannelId:        3,
				Content:          "real request",
				PromptTokens:     80,
				CompletionTokens: 20,
				Other:            "",
			},
		}, 100)

		require.Len(t, statsByChannel, 1)
		stats, ok := statsByChannel[3]
		require.True(t, ok)
		assert.Equal(t, 3, stats.ChannelID)
		assert.Equal(t, int64(100), stats.WindowStart)
		assert.Equal(t, int64(1), stats.UsageLogCount)
		assert.InDelta(t, 100.0, stats.NormalCostUnits, 0.0001)
		assert.InDelta(t, 100.0, stats.AdjustedCostUnits, 0.0001)
		assert.InDelta(t, 1.0, stats.CacheAdjustedCostFactor, 0.0001)
		assert.Equal(t, int64(0), stats.FirstTokenSampleCount)
		assert.Equal(t, int64(0), stats.FirstTokenLatencyTotalMS)
		assert.Equal(t, int64(0), stats.AverageFirstTokenLatencyMS)
	})

	t.Run("keeps first token latency for zero denominator real logs without adding cost", func(t *testing.T) {
		statsByChannel := buildAutoPriorityUsageStatsFromLogs([]model.Log{
			{
				CreatedAt: 150,
				Type:      model.LogTypeConsume,
				ChannelId: 4,
				Content:   "real request",
				Other: mustAutoPriorityOtherJSON(t, map[string]interface{}{
					"input_tokens_total": 0,
					"frt":                222,
				}),
			},
		}, 100)

		require.Len(t, statsByChannel, 1)
		stats, ok := statsByChannel[4]
		require.True(t, ok)
		assert.Equal(t, 4, stats.ChannelID)
		assert.Equal(t, int64(100), stats.WindowStart)
		assert.Equal(t, int64(1), stats.UsageLogCount)
		assert.InDelta(t, 0.0, stats.NormalCostUnits, 0.0001)
		assert.InDelta(t, 0.0, stats.AdjustedCostUnits, 0.0001)
		assert.InDelta(t, 1.0, stats.CacheAdjustedCostFactor, 0.0001)
		assert.Equal(t, int64(1), stats.FirstTokenSampleCount)
		assert.Equal(t, int64(222), stats.FirstTokenLatencyTotalMS)
		assert.Equal(t, int64(222), stats.AverageFirstTokenLatencyMS)
	})

	t.Run("returns empty map for empty or invalid channel ids", func(t *testing.T) {
		statsByChannel, err := CollectAutoPriorityUsageStats([]int{}, 100)
		require.NoError(t, err)
		assert.Empty(t, statsByChannel)

		statsByChannel, err = CollectAutoPriorityUsageStats([]int{0, -1}, 100)
		require.NoError(t, err)
		assert.Empty(t, statsByChannel)
	})

	t.Run("collects neutral buckets for requested channels without usable logs", func(t *testing.T) {
		channelID := 51
		otherChannelID := 52
		require.NoError(t, model.LOG_DB.Exec("DELETE FROM logs").Error)
		t.Cleanup(func() {
			_ = model.LOG_DB.Exec("DELETE FROM logs").Error
		})

		require.NoError(t, model.LOG_DB.Create([]model.Log{
			{
				CreatedAt: 120,
				Type:      model.LogTypeConsume,
				ChannelId: channelID,
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
				ChannelId: otherChannelID,
				Content:   "real request",
				Other:     `{"input_tokens_total":`,
			},
		}).Error)

		statsByChannel, err := CollectAutoPriorityUsageStats([]int{channelID, otherChannelID}, 100)
		require.NoError(t, err)
		require.Len(t, statsByChannel, 2)

		channelStats, ok := statsByChannel[channelID]
		require.True(t, ok)
		assert.Equal(t, channelID, channelStats.ChannelID)
		assert.Equal(t, int64(100), channelStats.WindowStart)
		assert.Equal(t, int64(0), channelStats.UsageLogCount)
		assert.InDelta(t, 0.0, channelStats.NormalCostUnits, 0.0001)
		assert.InDelta(t, 0.0, channelStats.AdjustedCostUnits, 0.0001)
		assert.InDelta(t, 1.0, channelStats.CacheAdjustedCostFactor, 0.0001)
		assert.Equal(t, int64(0), channelStats.FirstTokenSampleCount)
		assert.Equal(t, int64(0), channelStats.FirstTokenLatencyTotalMS)
		assert.Equal(t, int64(0), channelStats.AverageFirstTokenLatencyMS)

		otherStats, ok := statsByChannel[otherChannelID]
		require.True(t, ok)
		assert.Equal(t, otherChannelID, otherStats.ChannelID)
		assert.Equal(t, int64(100), otherStats.WindowStart)
		assert.Equal(t, int64(0), otherStats.UsageLogCount)
		assert.InDelta(t, 0.0, otherStats.NormalCostUnits, 0.0001)
		assert.InDelta(t, 0.0, otherStats.AdjustedCostUnits, 0.0001)
		assert.InDelta(t, 1.0, otherStats.CacheAdjustedCostFactor, 0.0001)
		assert.Equal(t, int64(0), otherStats.FirstTokenSampleCount)
		assert.Equal(t, int64(0), otherStats.FirstTokenLatencyTotalMS)
		assert.Equal(t, int64(0), otherStats.AverageFirstTokenLatencyMS)
	})
}
