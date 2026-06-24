package service

import (
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type AccountPoolRuntimeUsageMetrics struct {
	PromptTokens               int
	CompletionTokens           int
	CachedTokens               int
	CacheWriteTokens           int
	LatencyMS                  int64
	FirstTokenLatencyMS        int64
	HasLatencySample           bool
	HasFirstTokenLatencySample bool
}

func RecordAccountPoolRuntimeUsage(accountID int, metrics AccountPoolRuntimeUsageMetrics, now int64) error {
	if accountID <= 0 {
		return nil
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	updates := map[string]any{
		"total_prompt_tokens":      gorm.Expr("total_prompt_tokens + ?", metrics.PromptTokens),
		"total_completion_tokens":  gorm.Expr("total_completion_tokens + ?", metrics.CompletionTokens),
		"total_cached_tokens":      gorm.Expr("total_cached_tokens + ?", metrics.CachedTokens),
		"total_cache_write_tokens": gorm.Expr("total_cache_write_tokens + ?", metrics.CacheWriteTokens),
		"last_prompt_tokens":       int64(metrics.PromptTokens),
		"last_completion_tokens":   int64(metrics.CompletionTokens),
		"last_cached_tokens":       int64(metrics.CachedTokens),
		"last_cache_write_tokens":  int64(metrics.CacheWriteTokens),
	}
	if metrics.HasLatencySample && metrics.LatencyMS > 0 {
		updates["total_latency_ms"] = gorm.Expr("total_latency_ms + ?", metrics.LatencyMS)
		updates["latency_sample_count"] = gorm.Expr("latency_sample_count + ?", 1)
		updates["last_latency_ms"] = metrics.LatencyMS
	}
	if metrics.HasFirstTokenLatencySample && metrics.FirstTokenLatencyMS > 0 {
		updates["total_first_token_latency_ms"] = gorm.Expr("total_first_token_latency_ms + ?", metrics.FirstTokenLatencyMS)
		updates["first_token_latency_sample_count"] = gorm.Expr("first_token_latency_sample_count + ?", 1)
		updates["last_first_token_latency_ms"] = metrics.FirstTokenLatencyMS
	}
	return model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ? AND status <> ?", accountID, model.AccountPoolAccountStatusDeleted).
		Updates(updates).Error
}

func recordSelectedAccountPoolTextUsage(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage, summary textQuotaSummary) {
	accountID := GetSelectedAccountPoolAccountID(ctx)
	if accountID <= 0 || usage == nil {
		return
	}
	metrics := buildAccountPoolRuntimeUsageMetrics(relayInfo, usage, summary, time.Now())
	if err := RecordAccountPoolRuntimeUsage(accountID, metrics, common.GetTimestamp()); err != nil {
		logger.LogError(ctx, "error recording account pool usage: "+err.Error())
	}
}

func buildAccountPoolRuntimeUsageMetrics(relayInfo *relaycommon.RelayInfo, usage *dto.Usage, summary textQuotaSummary, observedAt time.Time) AccountPoolRuntimeUsageMetrics {
	metrics := AccountPoolRuntimeUsageMetrics{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		CachedTokens:     usage.PromptTokensDetails.CachedTokens,
		CacheWriteTokens: cacheWriteTokensTotal(summary),
	}
	if relayInfo == nil || relayInfo.StartTime.IsZero() {
		return metrics
	}
	if observedAt.After(relayInfo.StartTime) {
		metrics.LatencyMS = observedAt.Sub(relayInfo.StartTime).Milliseconds()
		metrics.HasLatencySample = metrics.LatencyMS > 0
	}
	if relayInfo.FirstResponseTime.After(relayInfo.StartTime) {
		metrics.FirstTokenLatencyMS = relayInfo.FirstResponseTime.Sub(relayInfo.StartTime).Milliseconds()
		metrics.HasFirstTokenLatencySample = metrics.FirstTokenLatencyMS > 0
	}
	return metrics
}
