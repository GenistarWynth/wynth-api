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
type QuotaData struct {
	Id                  int    `json:"id"`
	UserID              int    `json:"user_id" gorm:"index"`
	Username            string `json:"username" gorm:"index:idx_qdt_model_user_name,priority:2;size:64;default:''"`
	ModelName           string `json:"model_name" gorm:"index:idx_qdt_model_user_name,priority:1;size:64;default:''"`
	CreatedAt           int64  `json:"created_at" gorm:"bigint;index:idx_qdt_created_at,priority:2"`
	TokenUsed           int    `json:"token_used" gorm:"default:0"`
	InputTokens         int    `json:"input_tokens" gorm:"default:0"`
	CacheReadTokens     int    `json:"cache_read_tokens" gorm:"default:0"`
	CacheCreationTokens int    `json:"cache_creation_tokens" gorm:"default:0"`
	Count               int    `json:"count" gorm:"default:0"`
	Quota               int    `json:"quota" gorm:"default:0"`
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

func logQuotaDataCache(userId int, username string, modelName string, quota int, createdAt int64, tokenUsed int, inputTokens int, cacheReadTokens int, cacheCreationTokens int) {
	key := fmt.Sprintf("%d-%s-%s-%d", userId, username, modelName, createdAt)
	quotaData, ok := CacheQuotaData[key]
	if ok {
		quotaData.Count += 1
		quotaData.Quota += quota
		quotaData.TokenUsed += tokenUsed
		quotaData.InputTokens += inputTokens
		quotaData.CacheReadTokens += cacheReadTokens
		quotaData.CacheCreationTokens += cacheCreationTokens
	} else {
		quotaData = &QuotaData{
			UserID:              userId,
			Username:            username,
			ModelName:           modelName,
			CreatedAt:           createdAt,
			Count:               1,
			Quota:               quota,
			TokenUsed:           tokenUsed,
			InputTokens:         inputTokens,
			CacheReadTokens:     cacheReadTokens,
			CacheCreationTokens: cacheCreationTokens,
		}
	}
	CacheQuotaData[key] = quotaData
}

func LogQuotaData(userId int, username string, modelName string, quota int, createdAt int64, tokenUsed int, inputTokens int, cacheReadTokens int, cacheCreationTokens int) {
	// 只精确到小时
	createdAt = createdAt - (createdAt % 3600)

	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	logQuotaDataCache(userId, username, modelName, quota, createdAt, tokenUsed, inputTokens, cacheReadTokens, cacheCreationTokens)
}

func SaveQuotaDataCache() {
	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	size := len(CacheQuotaData)
	// 如果缓存中有数据，就保存到数据库中
	// 1. 先查询数据库中是否有数据
	// 2. 如果有数据，就更新数据
	// 3. 如果没有数据，就插入数据
	for _, quotaData := range CacheQuotaData {
		quotaDataDB := &QuotaData{}
		DB.Table("quota_data").Where("user_id = ? and username = ? and model_name = ? and created_at = ?",
			quotaData.UserID, quotaData.Username, quotaData.ModelName, quotaData.CreatedAt).First(quotaDataDB)
		if quotaDataDB.Id > 0 {
			//quotaDataDB.Count += quotaData.Count
			//quotaDataDB.Quota += quotaData.Quota
			//DB.Table("quota_data").Save(quotaDataDB)
			increaseQuotaData(quotaData.UserID, quotaData.Username, quotaData.ModelName, quotaData.Count, quotaData.Quota, quotaData.CreatedAt, quotaData.TokenUsed, quotaData.InputTokens, quotaData.CacheReadTokens, quotaData.CacheCreationTokens)
		} else {
			DB.Table("quota_data").Create(quotaData)
		}
	}
	CacheQuotaData = make(map[string]*QuotaData)
	common.SysLog(fmt.Sprintf("保存数据看板数据成功，共保存%d条数据", size))
}

func increaseQuotaData(userId int, username string, modelName string, count int, quota int, createdAt int64, tokenUsed int, inputTokens int, cacheReadTokens int, cacheCreationTokens int) {
	err := DB.Table("quota_data").Where("user_id = ? and username = ? and model_name = ? and created_at = ?",
		userId, username, modelName, createdAt).Updates(map[string]interface{}{
		"count":                 gorm.Expr("count + ?", count),
		"quota":                 gorm.Expr("quota + ?", quota),
		"token_used":            gorm.Expr("token_used + ?", tokenUsed),
		"input_tokens":          gorm.Expr("input_tokens + ?", inputTokens),
		"cache_read_tokens":     gorm.Expr("cache_read_tokens + ?", cacheReadTokens),
		"cache_creation_tokens": gorm.Expr("cache_creation_tokens + ?", cacheCreationTokens),
	}).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("increaseQuotaData error: %s", err))
	}
}

func GetQuotaDataByUsername(username string, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	err = DB.Table("quota_data").Where("username = ? and created_at >= ? and created_at <= ?", username, startTime, endTime).Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetQuotaDataByUserId(userId int, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	err = DB.Table("quota_data").Where("user_id = ? and created_at >= ? and created_at <= ?", userId, startTime, endTime).Find(&quotaDatas).Error
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
