package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const (
	AccountPoolPlatformOpenAI    = "openai"
	AccountPoolPlatformAnthropic = "anthropic"
	AccountPoolPlatformGemini    = "gemini"
	AccountPoolPlatformXAI       = "xai"

	AccountPoolStatusEnabled  = "enabled"
	AccountPoolStatusDisabled = "disabled"
	AccountPoolStatusDeleted  = "deleted"

	AccountPoolAccountStatusEnabled  = "enabled"
	AccountPoolAccountStatusDisabled = "disabled"
	AccountPoolAccountStatusExpired  = "expired"
	AccountPoolAccountStatusDeleted  = "deleted"

	AccountPoolProxyStatusEnabled  = "enabled"
	AccountPoolProxyStatusDisabled = "disabled"
	AccountPoolProxyStatusDeleted  = "deleted"

	AccountPoolBindingStatusDraft    = "draft"
	AccountPoolBindingStatusEnabled  = "enabled"
	AccountPoolBindingStatusDisabled = "disabled"
)

var ErrAccountPoolBoundChannelEnable = errors.New("account pool bound channel cannot be enabled in phase 1")

type AccountPool struct {
	Id                             int    `json:"id"`
	Name                           string `json:"name" gorm:"type:varchar(191);not null;index"`
	Platform                       string `json:"platform" gorm:"type:varchar(32);not null;index"`
	Status                         string `json:"status" gorm:"type:varchar(32);not null;default:'enabled';index"`
	DefaultProxyID                 int    `json:"default_proxy_id" gorm:"index"`
	DefaultMonitorEnabled          bool   `json:"default_monitor_enabled" gorm:"not null;default:false"`
	DefaultSchedulePolicy          string `json:"default_schedule_policy" gorm:"type:text"`
	CapabilityCheckEnabled         bool   `json:"capability_check_enabled" gorm:"not null;default:false"`
	CapabilityCheckIntervalMinutes int    `json:"capability_check_interval_minutes" gorm:"not null;default:0"`
	CapabilityCheckMode            string `json:"capability_check_mode" gorm:"type:varchar(32)"`
	CapabilityCheckChannelID       int    `json:"capability_check_channel_id" gorm:"index"`
	CapabilityCheckModels          string `json:"capability_check_models" gorm:"type:text"`
	CapabilityCheckTimeoutSeconds  int    `json:"capability_check_timeout_seconds" gorm:"not null;default:0"`
	CapabilityCheckMerge           bool   `json:"capability_check_merge" gorm:"not null;default:false"`
	Remark                         string `json:"remark" gorm:"type:varchar(1024)"`
	CreatedTime                    int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime                    int64  `json:"updated_time" gorm:"bigint"`
}

func (p *AccountPool) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	if p.CreatedTime == 0 {
		p.CreatedTime = now
	}
	if p.UpdatedTime == 0 {
		p.UpdatedTime = now
	}
	if p.Status == "" {
		p.Status = AccountPoolStatusEnabled
	}
	return nil
}

func (p *AccountPool) BeforeUpdate(tx *gorm.DB) error {
	p.UpdatedTime = common.GetTimestamp()
	return nil
}

type AccountPoolAccount struct {
	Id                           int    `json:"id"`
	PoolID                       int    `json:"pool_id" gorm:"not null;index:idx_account_pool_status,priority:1"`
	Name                         string `json:"name" gorm:"type:varchar(191);not null;index"`
	AccountIdentifier            string `json:"account_identifier" gorm:"type:varchar(191);index"`
	OAuthType                    string `json:"oauth_type" gorm:"column:oauth_type;type:varchar(32);not null;default:''"`
	CredentialConfig             string `json:"-" gorm:"type:text"`
	TokenState                   string `json:"-" gorm:"type:text"`
	Status                       string `json:"status" gorm:"type:varchar(32);not null;default:'enabled';index:idx_account_pool_status,priority:2"`
	Priority                     int64  `json:"priority" gorm:"bigint;not null;default:0;index"`
	Weight                       uint   `json:"weight" gorm:"not null;default:0"`
	MaxConcurrency               int    `json:"max_concurrency" gorm:"not null"`
	ProxyID                      int    `json:"proxy_id" gorm:"index"`
	SupportedModels              string `json:"supported_models" gorm:"type:text"`
	ModelMapping                 string `json:"model_mapping" gorm:"type:text"`
	LastUsedAt                   int64  `json:"last_used_at" gorm:"bigint;index"`
	LastSuccessAt                int64  `json:"last_success_at" gorm:"bigint;index"`
	LastFailureAt                int64  `json:"last_failure_at" gorm:"bigint;index"`
	SuccessCount                 int64  `json:"success_count" gorm:"bigint;not null;default:0"`
	FailureCount                 int64  `json:"failure_count" gorm:"bigint;not null;default:0"`
	TotalPromptTokens            int64  `json:"total_prompt_tokens" gorm:"bigint;not null;default:0"`
	TotalCompletionTokens        int64  `json:"total_completion_tokens" gorm:"bigint;not null;default:0"`
	TotalCachedTokens            int64  `json:"total_cached_tokens" gorm:"bigint;not null;default:0"`
	TotalCacheWriteTokens        int64  `json:"total_cache_write_tokens" gorm:"bigint;not null;default:0"`
	LastPromptTokens             int64  `json:"last_prompt_tokens" gorm:"bigint;not null;default:0"`
	LastCompletionTokens         int64  `json:"last_completion_tokens" gorm:"bigint;not null;default:0"`
	LastCachedTokens             int64  `json:"last_cached_tokens" gorm:"bigint;not null;default:0"`
	LastCacheWriteTokens         int64  `json:"last_cache_write_tokens" gorm:"bigint;not null;default:0"`
	TotalLatencyMS               int64  `json:"total_latency_ms" gorm:"bigint;not null;default:0"`
	LatencySampleCount           int64  `json:"latency_sample_count" gorm:"bigint;not null;default:0"`
	LastLatencyMS                int64  `json:"last_latency_ms" gorm:"bigint;not null;default:0"`
	TotalFirstTokenLatencyMS     int64  `json:"total_first_token_latency_ms" gorm:"bigint;not null;default:0"`
	FirstTokenLatencySampleCount int64  `json:"first_token_latency_sample_count" gorm:"bigint;not null;default:0"`
	LastFirstTokenLatencyMS      int64  `json:"last_first_token_latency_ms" gorm:"bigint;not null;default:0"`
	RateLimitedUntil             int64  `json:"rate_limited_until" gorm:"bigint;index"`
	TempDisabledUntil            int64  `json:"temp_disabled_until" gorm:"bigint;index"`
	OverloadUntil                int64  `json:"overload_until" gorm:"bigint;index"`
	ExpiresAt                    int64  `json:"expires_at" gorm:"bigint;not null;default:0;index"`
	AutoPauseOnExpired           bool   `json:"auto_pause_on_expired" gorm:"not null;default:false"`
	FailureState                 string `json:"-" gorm:"type:text"`
	ModelRateLimits              string `json:"-" gorm:"type:text"`
	RuntimeOptions               string `json:"runtime_options" gorm:"type:text"`
	RequestQuota                 int64  `json:"request_quota" gorm:"bigint;not null;default:0"`
	RequestQuotaUsed             int64  `json:"request_quota_used" gorm:"bigint;not null;default:0"`
	RequestQuotaWindowStart      int64  `json:"request_quota_window_start" gorm:"bigint;not null;default:0"`
	RequestQuotaWindowSeconds    int64  `json:"request_quota_window_seconds" gorm:"bigint;not null;default:0"`
	TempDisabledReason           string `json:"temp_disabled_reason" gorm:"type:varchar(512)"`
	LastError                    string `json:"last_error" gorm:"type:varchar(1024)"`
	LastCapabilityCheckAt        int64  `json:"last_capability_check_at" gorm:"bigint;index"`
	LastCapabilityCheckStatus    string `json:"last_capability_check_status" gorm:"type:varchar(32)"`
	LastCapabilityCheckError     string `json:"last_capability_check_error" gorm:"type:varchar(1024)"`
	LastCapabilityCheckModels    string `json:"last_capability_check_models" gorm:"type:text"`
	CreatedTime                  int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime                  int64  `json:"updated_time" gorm:"bigint"`
}

func (a *AccountPoolAccount) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	if a.CreatedTime == 0 {
		a.CreatedTime = now
	}
	if a.UpdatedTime == 0 {
		a.UpdatedTime = now
	}
	if a.Status == "" {
		a.Status = AccountPoolAccountStatusEnabled
	}
	return nil
}

func (a *AccountPoolAccount) BeforeUpdate(tx *gorm.DB) error {
	a.UpdatedTime = common.GetTimestamp()
	return nil
}

func (a AccountPoolAccount) IsSchedulableAt(now int64) bool {
	if a.Status != AccountPoolAccountStatusEnabled {
		return false
	}
	if a.RateLimitedUntil > now {
		return false
	}
	if a.TempDisabledUntil > now {
		return false
	}
	if a.OverloadUntil > now {
		return false
	}
	if a.AutoPauseOnExpired && a.ExpiresAt > 0 && a.ExpiresAt <= now {
		return false
	}
	return true
}

// QuotaExceededAt reports whether this account has exhausted its request quota at the given
// unix timestamp. Returns false (unlimited) when RequestQuota <= 0.
// If a rolling window is configured and the window has elapsed, the counter is treated as
// reset to 0 (the actual DB reset happens in IncrementAccountPoolAccountRequestQuota).
func (a AccountPoolAccount) QuotaExceededAt(now int64) bool {
	if a.RequestQuota <= 0 {
		return false
	}
	effectiveUsed := a.RequestQuotaUsed
	if a.RequestQuotaWindowSeconds > 0 && a.RequestQuotaWindowStart > 0 &&
		now >= a.RequestQuotaWindowStart+a.RequestQuotaWindowSeconds {
		effectiveUsed = 0
	}
	return effectiveUsed >= a.RequestQuota
}

type AccountPoolProxy struct {
	Id              int    `json:"id"`
	Name            string `json:"name" gorm:"type:varchar(191);not null;index"`
	Protocol        string `json:"protocol" gorm:"type:varchar(16);not null"`
	Host            string `json:"host" gorm:"type:varchar(255);not null"`
	Port            int    `json:"port" gorm:"not null"`
	Username        string `json:"username" gorm:"type:varchar(191)"`
	Password        string `json:"-" gorm:"type:text"`
	Status          string `json:"status" gorm:"type:varchar(32);not null;default:'enabled';index"`
	FallbackProxyID int    `json:"fallback_proxy_id" gorm:"index"`
	CreatedTime     int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime     int64  `json:"updated_time" gorm:"bigint"`
}

func (p *AccountPoolProxy) BeforeSave(tx *gorm.DB) error {
	if p.Id != 0 && p.FallbackProxyID == p.Id {
		return errors.New("fallback proxy cannot reference itself")
	}
	return validateAccountPoolProxyFallbackChain(tx, p.Id, p.FallbackProxyID)
}

func validateAccountPoolProxyFallbackChain(tx *gorm.DB, proxyID int, fallbackProxyID int) error {
	visited := make(map[int]struct{})
	for fallbackProxyID != 0 {
		if proxyID != 0 && fallbackProxyID == proxyID {
			return errors.New("fallback proxy cannot form a cycle")
		}
		if _, ok := visited[fallbackProxyID]; ok {
			return errors.New("fallback proxy cannot form a cycle")
		}
		visited[fallbackProxyID] = struct{}{}

		var fallback AccountPoolProxy
		if err := tx.Select("id", "fallback_proxy_id").First(&fallback, fallbackProxyID).Error; err != nil {
			return err
		}
		fallbackProxyID = fallback.FallbackProxyID
	}
	return nil
}

func (p *AccountPoolProxy) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	if p.CreatedTime == 0 {
		p.CreatedTime = now
	}
	if p.UpdatedTime == 0 {
		p.UpdatedTime = now
	}
	if p.Status == "" {
		p.Status = AccountPoolProxyStatusEnabled
	}
	return nil
}

func (p *AccountPoolProxy) BeforeUpdate(tx *gorm.DB) error {
	p.UpdatedTime = common.GetTimestamp()
	return nil
}

type AccountPoolChannelBinding struct {
	Id                  int    `json:"id"`
	PoolID              int    `json:"pool_id" gorm:"not null;index"`
	ChannelID           int    `json:"channel_id" gorm:"not null;uniqueIndex"`
	AccountFilterConfig string `json:"account_filter_config" gorm:"type:text"`
	ModelPolicy         string `json:"model_policy" gorm:"type:text"`
	SchedulePolicy      string `json:"schedule_policy" gorm:"type:text"`
	AccountRetryTimes   int    `json:"account_retry_times" gorm:"not null;default:0"`
	MaxUserConcurrency  int    `json:"max_user_concurrency" gorm:"not null;default:0"`
	Status              string `json:"status" gorm:"type:varchar(32);not null;default:'draft';index"`
	CreatedTime         int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime         int64  `json:"updated_time" gorm:"bigint"`
}

func (b *AccountPoolChannelBinding) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	if b.CreatedTime == 0 {
		b.CreatedTime = now
	}
	if b.UpdatedTime == 0 {
		b.UpdatedTime = now
	}
	if b.Status == "" {
		b.Status = AccountPoolBindingStatusDraft
	}
	return nil
}

func (b *AccountPoolChannelBinding) BeforeUpdate(tx *gorm.DB) error {
	b.UpdatedTime = common.GetTimestamp()
	return nil
}

func RejectAccountPoolBoundChannelEnable(channelID int, status int) error {
	if status != common.ChannelStatusEnabled || channelID <= 0 || DB == nil {
		return nil
	}
	bound, err := HasAccountPoolControlledChannelBinding(channelID)
	if err != nil {
		return err
	}
	if bound {
		return ErrAccountPoolBoundChannelEnable
	}
	return nil
}

func HasAccountPoolControlledChannelBinding(channelID int) (bool, error) {
	if channelID <= 0 || DB == nil {
		return false, nil
	}
	if !DB.Migrator().HasTable(&AccountPoolChannelBinding{}) {
		return false, nil
	}
	var count int64
	err := DB.Model(&AccountPoolChannelBinding{}).
		Where("channel_id = ? AND status IN ?", channelID, []string{AccountPoolBindingStatusDraft, AccountPoolBindingStatusEnabled}).
		Count(&count).Error
	return count > 0, err
}

func AccountPoolControlledChannelIDs() ([]int, error) {
	if DB == nil || !DB.Migrator().HasTable(&AccountPoolChannelBinding{}) {
		return nil, nil
	}
	var channelIDs []int
	err := DB.Model(&AccountPoolChannelBinding{}).
		Distinct("channel_id").
		Where("status IN ?", []string{AccountPoolBindingStatusDraft, AccountPoolBindingStatusEnabled}).
		Pluck("channel_id", &channelIDs).Error
	return channelIDs, err
}

func EnabledAccountPoolRuntimeChannelIDs() (map[int]struct{}, error) {
	channelIDs := make(map[int]struct{})
	if DB == nil || !DB.Migrator().HasTable(&AccountPoolChannelBinding{}) {
		return channelIDs, nil
	}
	var ids []int
	if err := DB.Model(&AccountPoolChannelBinding{}).
		Where("status = ?", AccountPoolBindingStatusEnabled).
		Pluck("channel_id", &ids).Error; err != nil {
		return nil, err
	}
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		channelIDs[id] = struct{}{}
	}
	return channelIDs, nil
}
