package model

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// QuotaData 柱状图数据
//
// Merge note (upstream sync 2026-06): the union of upstream's per-dimension
// columns (UseGroup/TokenID/ChannelID/NodeName) and Wynth's token-breakdown
// columns (InputTokens/CacheReadTokens/CacheCreationTokens).
type QuotaData struct {
	Id                  int    `json:"id"`
	UserID              int    `json:"user_id" gorm:"index"`
	Username            string `json:"username" gorm:"index:idx_qdt_model_user_name,priority:2;size:64;default:''"`
	ModelName           string `json:"model_name" gorm:"index:idx_qdt_model_user_name,priority:1;size:64;default:''"`
	CreatedAt           int64  `json:"created_at" gorm:"bigint;index:idx_qdt_created_at,priority:2"`
	UseGroup            string `json:"use_group" gorm:"index;size:64;default:''"`
	TokenID             int    `json:"token_id" gorm:"index;default:0"`
	ChannelID           int    `json:"channel_id" gorm:"index;default:0"`
	NodeName            string `json:"node_name" gorm:"index;size:64;default:''"`
	TokenUsed           int    `json:"token_used" gorm:"default:0"`
	InputTokens         int    `json:"input_tokens" gorm:"default:0"`
	CacheReadTokens     int    `json:"cache_read_tokens" gorm:"default:0"`
	CacheCreationTokens int    `json:"cache_creation_tokens" gorm:"default:0"`
	Count               int    `json:"count" gorm:"default:0"`
	Quota               int    `json:"quota" gorm:"default:0"`
}

type QuotaDataLogParams struct {
	UserID    int
	Username  string
	ModelName string
	Quota     int
	CreatedAt int64
	TokenUsed int
	UseGroup  string
	TokenID   int
	ChannelID int
	NodeName  string
	// Wynth token-breakdown fields (kept across the upstream sync).
	InputTokens         int
	CacheReadTokens     int
	CacheCreationTokens int
}

func UpdateQuotaData() {
	for {
		if common.DataExportEnabled {
			common.SysLog("正在更新数据看板数据...")
			SaveQuotaDataCache()
		}
		time.Sleep(time.Duration(common.DataExportInterval) * time.Minute)
	}
}

var CacheQuotaData = make(map[string]*QuotaData)
var CacheQuotaDataLock = sync.Mutex{}
var quotaDataFlushLock = sync.Mutex{}

func quotaDataIntFromOther(other map[string]interface{}, key string) int {
	value, ok := other[key]
	if !ok {
		return 0
	}
	var result int
	switch v := value.(type) {
	case int:
		result = v
	case int64:
		if v > int64(math.MaxInt) {
			return 0
		}
		result = int(v)
	case int32:
		result = int(v)
	case uint:
		result = int(v)
	case uint64:
		if v > uint64(math.MaxInt) {
			return 0
		}
		result = int(v)
	case uint32:
		result = int(v)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v > float64(math.MaxInt) {
			return 0
		}
		result = int(v)
	case float32:
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) || float64(v) > float64(math.MaxInt) {
			return 0
		}
		result = int(v)
	default:
		return 0
	}
	if result < 0 {
		return 0
	}
	return result
}

func quotaDataCacheTokensFromOther(other map[string]interface{}) (cacheReadTokens int, cacheCreationTokens int) {
	if other == nil {
		return 0, 0
	}
	cacheReadTokens = quotaDataIntFromOther(other, "cache_tokens")
	if cacheWriteTokens := quotaDataIntFromOther(other, "cache_write_tokens"); cacheWriteTokens > 0 {
		return cacheReadTokens, cacheWriteTokens
	}
	cacheCreation5m := quotaDataIntFromOther(other, "cache_creation_tokens_5m")
	cacheCreation1h := quotaDataIntFromOther(other, "cache_creation_tokens_1h")
	if cacheCreation5m > 0 || cacheCreation1h > 0 {
		return cacheReadTokens, cacheCreation5m + cacheCreation1h
	}
	return cacheReadTokens, quotaDataIntFromOther(other, "cache_creation_tokens")
}

func quotaDataInputTokensForDashboard(promptTokens int, other map[string]interface{}, cacheReadTokens int, cacheCreationTokens int) int {
	if promptTokens < 0 {
		promptTokens = 0
	}
	if other == nil {
		return promptTokens
	}
	if usageSemantic, ok := other["usage_semantic"].(string); ok && usageSemantic == "anthropic" {
		return promptTokens
	}

	totalInputTokens := quotaDataIntFromOther(other, "input_tokens_total")
	if totalInputTokens <= 0 {
		totalInputTokens = promptTokens
	}
	inputTokens := totalInputTokens - cacheReadTokens - cacheCreationTokens
	if inputTokens < 0 {
		return 0
	}
	return inputTokens
}

// logQuotaDataCache aggregates a quotaData row into the in-memory cache, keyed by
// the full upstream dimension set (user/name/model/hour/group/token/channel/node).
// Token-breakdown fields are accumulated alongside upstream's count/quota/token_used.
func logQuotaDataCache(quotaData *QuotaData) {
	key := fmt.Sprintf("%d\x00%s\x00%s\x00%d\x00%s\x00%d\x00%d\x00%s",
		quotaData.UserID,
		quotaData.Username,
		quotaData.ModelName,
		quotaData.CreatedAt,
		quotaData.UseGroup,
		quotaData.TokenID,
		quotaData.ChannelID,
		quotaData.NodeName,
	)
	cachedQuotaData, ok := CacheQuotaData[key]
	if ok {
		cachedQuotaData.Count += quotaData.Count
		cachedQuotaData.Quota += quotaData.Quota
		cachedQuotaData.TokenUsed += quotaData.TokenUsed
		cachedQuotaData.InputTokens += quotaData.InputTokens
		cachedQuotaData.CacheReadTokens += quotaData.CacheReadTokens
		cachedQuotaData.CacheCreationTokens += quotaData.CacheCreationTokens
		quotaData = cachedQuotaData
	}
	CacheQuotaData[key] = quotaData
}

func LogQuotaData(params QuotaDataLogParams) {
	// 只精确到小时
	createdAt := params.CreatedAt - (params.CreatedAt % 3600)
	quotaData := &QuotaData{
		UserID:              params.UserID,
		Username:            params.Username,
		ModelName:           params.ModelName,
		CreatedAt:           createdAt,
		UseGroup:            params.UseGroup,
		TokenID:             params.TokenID,
		ChannelID:           params.ChannelID,
		NodeName:            params.NodeName,
		Count:               1,
		Quota:               params.Quota,
		TokenUsed:           params.TokenUsed,
		InputTokens:         params.InputTokens,
		CacheReadTokens:     params.CacheReadTokens,
		CacheCreationTokens: params.CacheCreationTokens,
	}

	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	logQuotaDataCache(quotaData)
}

func SaveQuotaDataCache() {
	quotaDataFlushLock.Lock()
	defer quotaDataFlushLock.Unlock()

	CacheQuotaDataLock.Lock()
	quotaDataCache := CacheQuotaData
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()

	size := len(quotaDataCache)
	// 如果缓存中有数据，就保存到数据库中
	// 1. 先查询数据库中是否有数据
	// 2. 如果有数据，就更新数据
	// 3. 如果没有数据，就插入数据
	for _, quotaData := range quotaDataCache {
		quotaDataDB := &QuotaData{}
		DB.Table("quota_data").
			Where("user_id = ? and username = ? and model_name = ? and created_at = ? and use_group = ? and token_id = ? and channel_id = ? and node_name = ?",
				quotaData.UserID, quotaData.Username, quotaData.ModelName, quotaData.CreatedAt, quotaData.UseGroup, quotaData.TokenID, quotaData.ChannelID, quotaData.NodeName).
			First(quotaDataDB)
		if quotaDataDB.Id > 0 {
			increaseQuotaData(quotaData)
		} else {
			DB.Table("quota_data").Create(quotaData)
		}
	}
	common.SysLog(fmt.Sprintf("保存数据看板数据成功，共保存%d条数据", size))
}

func increaseQuotaData(quotaData *QuotaData) {
	err := DB.Table("quota_data").
		Where("user_id = ? and username = ? and model_name = ? and created_at = ? and use_group = ? and token_id = ? and channel_id = ? and node_name = ?",
			quotaData.UserID, quotaData.Username, quotaData.ModelName, quotaData.CreatedAt, quotaData.UseGroup, quotaData.TokenID, quotaData.ChannelID, quotaData.NodeName).
		Updates(map[string]interface{}{
			"count":                 gorm.Expr("count + ?", quotaData.Count),
			"quota":                 gorm.Expr("quota + ?", quotaData.Quota),
			"token_used":            gorm.Expr("token_used + ?", quotaData.TokenUsed),
			"input_tokens":          gorm.Expr("input_tokens + ?", quotaData.InputTokens),
			"cache_read_tokens":     gorm.Expr("cache_read_tokens + ?", quotaData.CacheReadTokens),
			"cache_creation_tokens": gorm.Expr("cache_creation_tokens + ?", quotaData.CacheCreationTokens),
		}).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("increaseQuotaData error: %s", err))
	}
}

func GetQuotaDataByUsername(username string, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	err = DB.Table("quota_data").
		Select("user_id, username, model_name, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used").
		Where("username = ? and created_at >= ? and created_at <= ?", username, startTime, endTime).
		Group("user_id, username, model_name, created_at").
		Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetQuotaDataByUserId(userId int, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	err = DB.Table("quota_data").
		Select("user_id, username, model_name, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used").
		Where("user_id = ? and created_at >= ? and created_at <= ?", userId, startTime, endTime).
		Group("user_id, username, model_name, created_at").
		Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetQuotaDataGroupByUser(startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	err = DB.Table("quota_data").
		Select("username, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, sum(input_tokens) as input_tokens, sum(cache_read_tokens) as cache_read_tokens, sum(cache_creation_tokens) as cache_creation_tokens").
		Where("created_at >= ? and created_at <= ?", startTime, endTime).
		Group("username, created_at").
		Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetAllQuotaDates(startTime int64, endTime int64, username string) (quotaData []*QuotaData, err error) {
	if username != "" {
		return GetQuotaDataByUsername(username, startTime, endTime)
	}
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	// only select model_name, sum(count) as count, sum(quota) as quota, model_name, created_at from quota_data group by model_name, created_at;
	//err = DB.Table("quota_data").Where("created_at >= ? and created_at <= ?", startTime, endTime).Find(&quotaDatas).Error
	err = DB.Table("quota_data").Select("model_name, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, sum(input_tokens) as input_tokens, sum(cache_read_tokens) as cache_read_tokens, sum(cache_creation_tokens) as cache_creation_tokens, created_at").Where("created_at >= ? and created_at <= ?", startTime, endTime).Group("model_name, created_at").Find(&quotaDatas).Error
	return quotaDatas, err
}
