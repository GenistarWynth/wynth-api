package model

import (
	"math"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupQuotaDataCacheTestDB(t *testing.T) {
	t.Helper()

	oldDB := DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	DB = db
	t.Cleanup(func() {
		DB = oldDB
		CacheQuotaDataLock.Lock()
		CacheQuotaData = make(map[string]*QuotaData)
		CacheQuotaDataLock.Unlock()
	})

	require.NoError(t, db.AutoMigrate(&QuotaData{}))
}

func TestLogQuotaDataAggregatesCacheTokens(t *testing.T) {
	setupQuotaDataCacheTestDB(t)

	LogQuotaData(QuotaDataLogParams{UserID: 1, Username: "alice", ModelName: "gpt-5", Quota: 10, CreatedAt: 7201, TokenUsed: 100, InputTokens: 70, CacheReadTokens: 40, CacheCreationTokens: 5})
	LogQuotaData(QuotaDataLogParams{UserID: 1, Username: "alice", ModelName: "gpt-5", Quota: 15, CreatedAt: 7300, TokenUsed: 200, InputTokens: 130, CacheReadTokens: 60, CacheCreationTokens: 7})
	SaveQuotaDataCache()

	var row QuotaData
	require.NoError(t, DB.Table("quota_data").First(&row).Error)
	assert.Equal(t, 2, row.Count)
	assert.Equal(t, 25, row.Quota)
	assert.Equal(t, 300, row.TokenUsed)
	assert.Equal(t, 200, row.InputTokens)
	assert.Equal(t, 100, row.CacheReadTokens)
	assert.Equal(t, 12, row.CacheCreationTokens)
}

func TestQuotaDataGroupQueriesIncludeCacheTokens(t *testing.T) {
	setupQuotaDataCacheTestDB(t)

	require.NoError(t, DB.Table("quota_data").Create(&QuotaData{
		UserID:              1,
		Username:            "alice",
		ModelName:           "gpt-5",
		CreatedAt:           3600,
		Count:               1,
		Quota:               10,
		TokenUsed:           100,
		InputTokens:         70,
		CacheReadTokens:     40,
		CacheCreationTokens: 5,
	}).Error)
	require.NoError(t, DB.Table("quota_data").Create(&QuotaData{
		UserID:              2,
		Username:            "bob",
		ModelName:           "gpt-5",
		CreatedAt:           3600,
		Count:               2,
		Quota:               20,
		TokenUsed:           200,
		InputTokens:         130,
		CacheReadTokens:     60,
		CacheCreationTokens: 7,
	}).Error)

	rows, err := GetAllQuotaDates(0, 7200, "")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 200, rows[0].InputTokens)
	assert.Equal(t, 100, rows[0].CacheReadTokens)
	assert.Equal(t, 12, rows[0].CacheCreationTokens)

	userRows, err := GetQuotaDataGroupByUser(0, 7200)
	require.NoError(t, err)
	require.Len(t, userRows, 2)

	cacheReadByUser := map[string]int{}
	inputByUser := map[string]int{}
	cacheCreationByUser := map[string]int{}
	for _, row := range userRows {
		inputByUser[row.Username] = row.InputTokens
		cacheReadByUser[row.Username] = row.CacheReadTokens
		cacheCreationByUser[row.Username] = row.CacheCreationTokens
	}
	assert.Equal(t, map[string]int{"alice": 70, "bob": 130}, inputByUser)
	assert.Equal(t, map[string]int{"alice": 40, "bob": 60}, cacheReadByUser)
	assert.Equal(t, map[string]int{"alice": 5, "bob": 7}, cacheCreationByUser)
}

func TestQuotaDataCacheTokensExtractedFromConsumeLogOther(t *testing.T) {
	setupQuotaDataCacheTestDB(t)

	other := map[string]interface{}{
		"cache_tokens":             40,
		"cache_creation_tokens":    99,
		"cache_write_tokens":       12,
		"cache_creation_tokens_5m": 5,
		"cache_creation_tokens_1h": 7,
	}

	cacheRead, cacheCreation := quotaDataCacheTokensFromOther(other)
	assert.Equal(t, 40, cacheRead)
	assert.Equal(t, 12, cacheCreation)

	fallbackRead, fallbackCreation := quotaDataCacheTokensFromOther(map[string]interface{}{
		"cache_tokens":          float64(3),
		"cache_creation_tokens": float64(4),
	})
	assert.Equal(t, 3, fallbackRead)
	assert.Equal(t, 4, fallbackCreation)
}

func TestQuotaDataCacheTokenExtractionIgnoresInvalidValues(t *testing.T) {
	setupQuotaDataCacheTestDB(t)

	cacheRead, cacheCreation := quotaDataCacheTokensFromOther(map[string]interface{}{
		"cache_tokens":          "bad",
		"cache_creation_tokens": -4,
	})

	assert.Equal(t, 0, cacheRead)
	assert.Equal(t, 0, cacheCreation)
}

func TestQuotaDataCacheTokenExtractionIgnoresOverflowValues(t *testing.T) {
	setupQuotaDataCacheTestDB(t)

	cacheRead, cacheCreation := quotaDataCacheTokensFromOther(map[string]interface{}{
		"cache_tokens":          uint64(math.MaxInt) + 1,
		"cache_creation_tokens": float64(math.MaxInt) * 2,
	})

	assert.Equal(t, 0, cacheRead)
	assert.Equal(t, 0, cacheCreation)
}

func TestQuotaDataInputTokensForDashboardNormalizesCacheTokens(t *testing.T) {
	tests := []struct {
		name                string
		promptTokens        int
		other               map[string]interface{}
		cacheReadTokens     int
		cacheCreationTokens int
		want                int
	}{
		{
			name:                "openai prompt tokens include cache tokens",
			promptTokens:        1000,
			other:               map[string]interface{}{},
			cacheReadTokens:     300,
			cacheCreationTokens: 200,
			want:                500,
		},
		{
			name:                "anthropic prompt tokens are already uncached input",
			promptTokens:        500,
			other:               map[string]interface{}{"usage_semantic": "anthropic"},
			cacheReadTokens:     300,
			cacheCreationTokens: 200,
			want:                500,
		},
		{
			name:                "explicit total input overrides prompt token display",
			promptTokens:        1200,
			other:               map[string]interface{}{"input_tokens_total": 1000},
			cacheReadTokens:     300,
			cacheCreationTokens: 200,
			want:                500,
		},
		{
			name:                "cache tokens cannot make input negative",
			promptTokens:        100,
			other:               map[string]interface{}{},
			cacheReadTokens:     300,
			cacheCreationTokens: 200,
			want:                0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := quotaDataInputTokensForDashboard(tt.promptTokens, tt.other, tt.cacheReadTokens, tt.cacheCreationTokens)
			assert.Equal(t, tt.want, got)
		})
	}
}
