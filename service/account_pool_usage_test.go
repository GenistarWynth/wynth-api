package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordAccountPoolRuntimeUsageAggregatesTokensAndLatency(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{Name: "metered"})

	require.NoError(t, RecordAccountPoolRuntimeUsage(account.Id, AccountPoolRuntimeUsageMetrics{
		PromptTokens:               100,
		CompletionTokens:           40,
		CachedTokens:               25,
		CacheWriteTokens:           10,
		LatencyMS:                  1200,
		FirstTokenLatencyMS:        350,
		HasLatencySample:           true,
		HasFirstTokenLatencySample: true,
	}, 2000))
	require.NoError(t, RecordAccountPoolRuntimeUsage(account.Id, AccountPoolRuntimeUsageMetrics{
		PromptTokens:     60,
		CompletionTokens: 20,
		CachedTokens:     5,
	}, 2001))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Equal(t, int64(160), reloaded.TotalPromptTokens)
	assert.Equal(t, int64(60), reloaded.TotalCompletionTokens)
	assert.Equal(t, int64(30), reloaded.TotalCachedTokens)
	assert.Equal(t, int64(10), reloaded.TotalCacheWriteTokens)
	assert.Equal(t, int64(60), reloaded.LastPromptTokens)
	assert.Equal(t, int64(20), reloaded.LastCompletionTokens)
	assert.Equal(t, int64(5), reloaded.LastCachedTokens)
	assert.Equal(t, int64(0), reloaded.LastCacheWriteTokens)
	assert.Equal(t, int64(1200), reloaded.TotalLatencyMS)
	assert.Equal(t, int64(1), reloaded.LatencySampleCount)
	assert.Equal(t, int64(1200), reloaded.LastLatencyMS)
	assert.Equal(t, int64(350), reloaded.TotalFirstTokenLatencyMS)
	assert.Equal(t, int64(1), reloaded.FirstTokenLatencySampleCount)
	assert.Equal(t, int64(350), reloaded.LastFirstTokenLatencyMS)
}

func TestRecordAccountPoolRuntimeUsageRecordsCompletedDisabledAccount(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{Name: "disabled-after-selection"})
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", account.Id).
		Update("status", model.AccountPoolAccountStatusDisabled).Error)

	require.NoError(t, RecordAccountPoolRuntimeUsage(account.Id, AccountPoolRuntimeUsageMetrics{
		PromptTokens:     12,
		CompletionTokens: 3,
	}, 2000))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Equal(t, model.AccountPoolAccountStatusDisabled, reloaded.Status)
	assert.Equal(t, int64(12), reloaded.TotalPromptTokens)
	assert.Equal(t, int64(3), reloaded.TotalCompletionTokens)
}

func TestPostTextConsumeQuotaRecordsSelectedAccountUsageMetrics(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.User{}))
	oldBatchUpdateEnabled := common.BatchUpdateEnabled
	oldLogConsumeEnabled := common.LogConsumeEnabled
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = false
	t.Cleanup(func() {
		common.BatchUpdateEnabled = oldBatchUpdateEnabled
		common.LogConsumeEnabled = oldLogConsumeEnabled
	})

	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{Name: "settled"})
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	user := model.User{Username: "usage-metrics", Password: "password123"}
	require.NoError(t, model.DB.Create(&user).Error)

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	ctx.Set(accountPoolSelectedAccountIDContextKey, account.Id)
	start := time.Now().Add(-1500 * time.Millisecond)
	relayInfo := &relaycommon.RelayInfo{
		UserId:            user.Id,
		OriginModelName:   "gpt-5",
		StartTime:         start,
		FirstResponseTime: start.Add(275 * time.Millisecond),
		PriceData: types.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			CacheRatio:      0.1,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
		},
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id},
	}
	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         25,
			CachedCreationTokens: 7,
		},
	}

	PostTextConsumeQuota(ctx, relayInfo, usage, nil)

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Equal(t, int64(100), reloaded.TotalPromptTokens)
	assert.Equal(t, int64(50), reloaded.TotalCompletionTokens)
	assert.Equal(t, int64(25), reloaded.TotalCachedTokens)
	assert.Equal(t, int64(7), reloaded.TotalCacheWriteTokens)
	assert.Equal(t, int64(275), reloaded.LastFirstTokenLatencyMS)
	assert.Equal(t, int64(1), reloaded.FirstTokenLatencySampleCount)
	assert.Equal(t, int64(1), reloaded.LatencySampleCount)
	assert.Greater(t, reloaded.LastLatencyMS, int64(0))
}
