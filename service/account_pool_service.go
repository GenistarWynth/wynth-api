package service

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/samber/hot"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AccountPoolService struct{}

const (
	accountPoolRuntimeEnabledCacheNamespace = "new-api:account_pool_runtime_enabled:v1"
	accountPoolRuntimeEnabledCacheTTL       = 30 * time.Second

	accountPoolRuntimeConcurrencyCacheNamespace = "new-api:account_pool_runtime_concurrency:v1"

	AccountPoolSchedulePolicyRoundRobin = "round_robin"
	AccountPoolSchedulePolicyRandom     = "random"

	DefaultAccountPoolCapabilityCheckIntervalMinutes = 1440
	MinimumAccountPoolCapabilityCheckIntervalMinutes = 5
	DefaultAccountPoolCapabilityCheckTimeoutSeconds  = 30
)

// accountPoolConcurrencyConfig is the cached value for GetAccountPoolRuntimeUserConcurrencyConfig.
type accountPoolConcurrencyConfig struct {
	BindingID          int `json:"binding_id"`
	MaxUserConcurrency int `json:"max_user_concurrency"`
}

// accountPoolConcurrencyAbsent is the sentinel stored when no enabled binding exists,
// so subsequent cache hits skip the DB without returning a result.
const accountPoolConcurrencyAbsent = -1

var (
	accountPoolRuntimeEnabledCacheOnce     sync.Once
	accountPoolRuntimeEnabledCache         *cachex.HybridCache[bool]
	accountPoolRuntimeConcurrencyCacheOnce sync.Once
	accountPoolRuntimeConcurrencyCache     *cachex.HybridCache[accountPoolConcurrencyConfig]
)

type AccountPoolCreateParams struct {
	Name                           string
	Platform                       string
	DefaultProxyID                 int
	DefaultMonitorEnabled          bool
	DefaultSchedulePolicy          string
	CapabilityCheckEnabled         bool
	CapabilityCheckIntervalMinutes int
	CapabilityCheckMode            string
	CapabilityCheckChannelID       int
	CapabilityCheckModels          []string
	CapabilityCheckTimeoutSeconds  int
	CapabilityCheckMerge           bool
	Remark                         string
}

type AccountPoolAccountCreateParams struct {
	PoolID                    int
	Name                      string
	AccountIdentifier         string
	OAuthType                 string
	Credential                AccountPoolCredentialConfig
	TokenState                AccountPoolTokenState
	Status                    string
	Priority                  int64
	Weight                    uint
	MaxConcurrency            int
	MaxConcurrencySet         bool
	ProxyID                   int
	SupportedModels           []string
	ModelMapping              map[string]string
	LastUsedAt                int64
	RateLimitedUntil          int64
	TempDisabledUntil         int64
	TempDisabledReason        string
	LastError                 string
	ExpiresAt                 int64
	AutoPauseOnExpired        bool
	RequestQuota              int64
	RequestQuotaWindowSeconds int64
}

type AccountPoolBindingCreateParams struct {
	PoolID              int
	ChannelID           int
	AccountFilterConfig AccountPoolAccountFilterConfig
	ModelPolicy         AccountPoolModelPolicy
	SchedulePolicy      string
	AccountRetryTimes   int
	MaxUserConcurrency  int
	Status              string
}

type AccountPoolBoundChannelCreateParams struct {
	PoolID              int
	Name                string
	ChannelType         int
	AccountFilterConfig AccountPoolAccountFilterConfig
	ModelPolicy         AccountPoolModelPolicy
	SchedulePolicy      string
	AccountRetryTimes   int
	MaxUserConcurrency  int
}

type AccountPoolProxyCreateParams struct {
	Name            string
	Protocol        string
	Host            string
	Port            int
	Username        string
	Password        string
	Status          string
	FallbackProxyID int
}

type AccountPoolAccountView struct {
	Id                           int               `json:"id"`
	PoolID                       int               `json:"pool_id"`
	Name                         string            `json:"name"`
	AccountIdentifier            string            `json:"account_identifier"`
	OAuthType                    string            `json:"oauth_type"`
	Status                       string            `json:"status"`
	Priority                     int64             `json:"priority"`
	Weight                       uint              `json:"weight"`
	MaxConcurrency               int               `json:"max_concurrency"`
	ProxyID                      int               `json:"proxy_id"`
	SupportedModels              []string          `json:"supported_models"`
	ModelMapping                 map[string]string `json:"model_mapping"`
	LastUsedAt                   int64             `json:"last_used_at"`
	LastSuccessAt                int64             `json:"last_success_at"`
	LastFailureAt                int64             `json:"last_failure_at"`
	SuccessCount                 int64             `json:"success_count"`
	FailureCount                 int64             `json:"failure_count"`
	TotalPromptTokens            int64             `json:"total_prompt_tokens"`
	TotalCompletionTokens        int64             `json:"total_completion_tokens"`
	TotalCachedTokens            int64             `json:"total_cached_tokens"`
	TotalCacheWriteTokens        int64             `json:"total_cache_write_tokens"`
	LastPromptTokens             int64             `json:"last_prompt_tokens"`
	LastCompletionTokens         int64             `json:"last_completion_tokens"`
	LastCachedTokens             int64             `json:"last_cached_tokens"`
	LastCacheWriteTokens         int64             `json:"last_cache_write_tokens"`
	TotalLatencyMS               int64             `json:"total_latency_ms"`
	LatencySampleCount           int64             `json:"latency_sample_count"`
	LastLatencyMS                int64             `json:"last_latency_ms"`
	TotalFirstTokenLatencyMS     int64             `json:"total_first_token_latency_ms"`
	FirstTokenLatencySampleCount int64             `json:"first_token_latency_sample_count"`
	LastFirstTokenLatencyMS      int64             `json:"last_first_token_latency_ms"`
	RateLimitedUntil             int64             `json:"rate_limited_until"`
	TempDisabledUntil            int64             `json:"temp_disabled_until"`
	TempDisabledReason           string            `json:"temp_disabled_reason"`
	LastError                    string            `json:"last_error"`
	ExpiresAt                    int64             `json:"expires_at"`
	AutoPauseOnExpired           bool              `json:"auto_pause_on_expired"`
	LastCapabilityCheckAt        int64             `json:"last_capability_check_at"`
	LastCapabilityCheckStatus    string            `json:"last_capability_check_status"`
	LastCapabilityCheckError     string            `json:"last_capability_check_error"`
	LastCapabilityCheckModels    []string          `json:"last_capability_check_models"`
	HasCredential                bool              `json:"has_credential"`
	HasToken                     bool              `json:"has_token"`
	RequestQuota                 int64             `json:"request_quota"`
	RequestQuotaUsed             int64             `json:"request_quota_used"`
	RequestQuotaWindowStart      int64             `json:"request_quota_window_start"`
	RequestQuotaWindowSeconds    int64             `json:"request_quota_window_seconds"`
	CreatedTime                  int64             `json:"created_time"`
	UpdatedTime                  int64             `json:"updated_time"`
}

type AccountPoolBindingView struct {
	Id                  int    `json:"id"`
	PoolID              int    `json:"pool_id"`
	ChannelID           int    `json:"channel_id"`
	ChannelName         string `json:"channel_name"`
	ChannelStatus       int    `json:"channel_status"`
	AccountFilterConfig string `json:"account_filter_config"`
	ModelPolicy         string `json:"model_policy"`
	SchedulePolicy      string `json:"schedule_policy"`
	AccountRetryTimes   int    `json:"account_retry_times"`
	MaxUserConcurrency  int    `json:"max_user_concurrency"`
	Status              string `json:"status"`
	RuntimeEnabled      bool   `json:"runtime_enabled"`
	CreatedTime         int64  `json:"created_time"`
	UpdatedTime         int64  `json:"updated_time"`
}

type AccountPoolProxyView struct {
	Id              int    `json:"id"`
	Name            string `json:"name"`
	Protocol        string `json:"protocol"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	Username        string `json:"username"`
	Status          string `json:"status"`
	FallbackProxyID int    `json:"fallback_proxy_id"`
	HasPassword     bool   `json:"has_password"`
	CreatedTime     int64  `json:"created_time"`
	UpdatedTime     int64  `json:"updated_time"`
}

func (s AccountPoolService) CreatePool(params AccountPoolCreateParams) (model.AccountPool, error) {
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return model.AccountPool{}, errors.New("account pool name is required")
	}
	platform, err := normalizeAccountPoolPlatform(params.Platform)
	if err != nil {
		return model.AccountPool{}, err
	}
	if err := validateAccountPoolProxyReference(params.DefaultProxyID); err != nil {
		return model.AccountPool{}, err
	}
	schedulePolicy, err := normalizeAccountPoolSchedulePolicy(params.DefaultSchedulePolicy)
	if err != nil {
		return model.AccountPool{}, err
	}
	capabilityCheckMode, err := normalizeAccountPoolCapabilityCheckMode(params.CapabilityCheckMode)
	if err != nil {
		return model.AccountPool{}, err
	}
	if params.CapabilityCheckChannelID < 0 {
		return model.AccountPool{}, errors.New("account pool capability check channel id cannot be negative")
	}
	capabilityCheckModels, err := marshalAccountPoolOptionalJSON(normalizeAccountPoolCapabilityModels(params.CapabilityCheckModels))
	if err != nil {
		return model.AccountPool{}, err
	}
	pool := model.AccountPool{
		Name:                           name,
		Platform:                       platform,
		DefaultProxyID:                 params.DefaultProxyID,
		DefaultMonitorEnabled:          params.DefaultMonitorEnabled,
		DefaultSchedulePolicy:          schedulePolicy,
		CapabilityCheckEnabled:         params.CapabilityCheckEnabled,
		CapabilityCheckIntervalMinutes: normalizeAccountPoolCapabilityCheckIntervalMinutes(params.CapabilityCheckEnabled, params.CapabilityCheckIntervalMinutes),
		CapabilityCheckMode:            capabilityCheckMode,
		CapabilityCheckChannelID:       params.CapabilityCheckChannelID,
		CapabilityCheckModels:          capabilityCheckModels,
		CapabilityCheckTimeoutSeconds:  normalizeAccountPoolCapabilityCheckTimeoutSeconds(params.CapabilityCheckTimeoutSeconds),
		CapabilityCheckMerge:           params.CapabilityCheckMerge,
		Remark:                         params.Remark,
	}
	return pool, model.DB.Create(&pool).Error
}

func (s AccountPoolService) ListPools() ([]model.AccountPool, error) {
	var pools []model.AccountPool
	err := model.DB.Where("status <> ?", model.AccountPoolStatusDeleted).Order("id asc").Find(&pools).Error
	return pools, err
}

func (s AccountPoolService) GetPool(id int) (model.AccountPool, error) {
	return getAccountPoolExistingPool(id)
}

func (s AccountPoolService) UpdatePool(id int, params AccountPoolCreateParams) (model.AccountPool, error) {
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return model.AccountPool{}, errors.New("account pool name is required")
	}
	platform, err := normalizeAccountPoolPlatform(params.Platform)
	if err != nil {
		return model.AccountPool{}, err
	}
	pool, err := getAccountPoolExistingPool(id)
	if err != nil {
		return model.AccountPool{}, err
	}
	if err := validateAccountPoolProxyReference(params.DefaultProxyID); err != nil {
		return model.AccountPool{}, err
	}
	schedulePolicy, err := normalizeAccountPoolSchedulePolicy(params.DefaultSchedulePolicy)
	if err != nil {
		return model.AccountPool{}, err
	}
	capabilityCheckMode, err := normalizeAccountPoolCapabilityCheckMode(params.CapabilityCheckMode)
	if err != nil {
		return model.AccountPool{}, err
	}
	if params.CapabilityCheckChannelID < 0 {
		return model.AccountPool{}, errors.New("account pool capability check channel id cannot be negative")
	}
	capabilityCheckModels, err := marshalAccountPoolOptionalJSON(normalizeAccountPoolCapabilityModels(params.CapabilityCheckModels))
	if err != nil {
		return model.AccountPool{}, err
	}
	updates := map[string]any{
		"name":                              name,
		"platform":                          platform,
		"default_proxy_id":                  params.DefaultProxyID,
		"default_monitor_enabled":           params.DefaultMonitorEnabled,
		"default_schedule_policy":           schedulePolicy,
		"capability_check_enabled":          params.CapabilityCheckEnabled,
		"capability_check_interval_minutes": normalizeAccountPoolCapabilityCheckIntervalMinutes(params.CapabilityCheckEnabled, params.CapabilityCheckIntervalMinutes),
		"capability_check_mode":             capabilityCheckMode,
		"capability_check_channel_id":       params.CapabilityCheckChannelID,
		"capability_check_models":           capabilityCheckModels,
		"capability_check_timeout_seconds":  normalizeAccountPoolCapabilityCheckTimeoutSeconds(params.CapabilityCheckTimeoutSeconds),
		"capability_check_merge":            params.CapabilityCheckMerge,
		"remark":                            params.Remark,
		"updated_time":                      common.GetTimestamp(),
	}
	// A platform change must not leave already-bound channels in an incompatible
	// platform/channel-type pair (e.g. switching an openai pool with an openai channel
	// binding to grok_web). Create/bind paths validate this, but the platform can only be
	// revisited here — so re-validate every existing binding against the new platform and
	// reject the change until incompatible bindings are removed. The pool row is locked and
	// re-read INSIDE the transaction (FOR UPDATE on MySQL/PostgreSQL; SQLite serializes
	// writes at the database level), and "changed?" is computed against the LOCKED row, not
	// the stale pre-transaction read — otherwise a concurrent platform change committed
	// after the pre-read could make this path skip validation and clobber the platform. The
	// binding-mutation paths take the same lock, so they serialize.
	var boundChannelIDs []int
	var platformChanged bool
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		lockedPool, err := lockAccountPoolRow(tx, id)
		if err != nil {
			return err
		}
		platformChanged = platform != lockedPool.Platform
		if platformChanged {
			var bindings []model.AccountPoolChannelBinding
			if err := tx.Where("pool_id = ?", id).Find(&bindings).Error; err != nil {
				return err
			}
			proposed := lockedPool
			proposed.Platform = platform
			boundChannelIDs = boundChannelIDs[:0]
			for _, binding := range bindings {
				boundChannelIDs = append(boundChannelIDs, binding.ChannelID)
				var channel model.Channel
				if err := tx.First(&channel, binding.ChannelID).Error; err != nil {
					return fmt.Errorf("cannot change account pool platform: bound channel %d is unavailable: %w", binding.ChannelID, err)
				}
				if err := validateAccountPoolRuntimeChannelForPool(proposed, channel); err != nil {
					return fmt.Errorf("cannot change account pool platform to %s while channel %d is bound: %w", platform, channel.Id, err)
				}
			}
		}
		return tx.Model(&lockedPool).Updates(updates).Error
	})
	if err != nil {
		return model.AccountPool{}, err
	}
	// The per-channel runtime caches key pool platform/selection by channel; a platform
	// change makes any cached entry for a bound channel stale, so drop them.
	if platformChanged {
		for _, channelID := range boundChannelIDs {
			invalidateAccountPoolRuntimeEnabledForChannel(channelID)
		}
	}
	return pool, model.DB.First(&pool, id).Error
}

func (s AccountPoolService) DeletePool(id int) error {
	var channelIDs []int
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		now := common.GetTimestamp()
		if err := tx.Model(&model.AccountPoolChannelBinding{}).
			Where("pool_id = ?", id).
			Pluck("channel_id", &channelIDs).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.AccountPool{}).
			Where("id = ? AND status <> ?", id, model.AccountPoolStatusDeleted).
			Updates(map[string]any{
				"status":       model.AccountPoolStatusDeleted,
				"updated_time": now,
			}).Error; err != nil {
			return err
		}
		if len(channelIDs) > 0 {
			if err := tx.Model(&model.Ability{}).
				Where("channel_id IN ?", channelIDs).
				Update("enabled", false).Error; err != nil {
				return err
			}
		}
		return tx.Where("pool_id = ?", id).Delete(&model.AccountPoolChannelBinding{}).Error
	})
	if err != nil {
		return err
	}
	for _, channelID := range channelIDs {
		invalidateAccountPoolRuntimeEnabledForChannel(channelID)
	}
	refreshAccountPoolBindingRoutingCache()
	return nil
}

func (s AccountPoolService) CreateAccount(params AccountPoolAccountCreateParams) (AccountPoolAccountView, error) {
	if _, err := getAccountPoolExistingPool(params.PoolID); err != nil {
		return AccountPoolAccountView{}, err
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return AccountPoolAccountView{}, errors.New("account pool account name is required")
	}
	if err := validateAccountPoolProxyReference(params.ProxyID); err != nil {
		return AccountPoolAccountView{}, err
	}
	credentialConfig, err := EncryptAccountPoolCredentialConfig(params.Credential)
	if err != nil {
		return AccountPoolAccountView{}, err
	}
	tokenState, err := EncryptAccountPoolTokenState(params.TokenState)
	if err != nil {
		return AccountPoolAccountView{}, err
	}
	supportedModels, err := marshalAccountPoolOptionalJSON(params.SupportedModels)
	if err != nil {
		return AccountPoolAccountView{}, err
	}
	modelMapping, err := marshalAccountPoolOptionalJSON(params.ModelMapping)
	if err != nil {
		return AccountPoolAccountView{}, err
	}
	account := model.AccountPoolAccount{
		PoolID:                    params.PoolID,
		Name:                      name,
		AccountIdentifier:         params.AccountIdentifier,
		OAuthType:                 NormalizeAccountPoolOAuthType(params.OAuthType),
		CredentialConfig:          credentialConfig,
		TokenState:                tokenState,
		Status:                    params.Status,
		Priority:                  params.Priority,
		Weight:                    params.Weight,
		MaxConcurrency:            accountPoolNormalizeMaxConcurrency(params.MaxConcurrency, params.MaxConcurrencySet),
		ProxyID:                   params.ProxyID,
		SupportedModels:           supportedModels,
		ModelMapping:              modelMapping,
		LastUsedAt:                params.LastUsedAt,
		RateLimitedUntil:          params.RateLimitedUntil,
		TempDisabledUntil:         params.TempDisabledUntil,
		TempDisabledReason:        params.TempDisabledReason,
		LastError:                 params.LastError,
		ExpiresAt:                 accountPoolNormalizeExpiresAt(params.ExpiresAt),
		AutoPauseOnExpired:        params.AutoPauseOnExpired,
		RequestQuota:              accountPoolNormalizeRequestQuota(params.RequestQuota),
		RequestQuotaWindowSeconds: accountPoolNormalizeRequestQuota(params.RequestQuotaWindowSeconds),
	}
	if err := model.DB.Create(&account).Error; err != nil {
		return AccountPoolAccountView{}, err
	}
	return buildAccountPoolAccountView(account)
}

func (s AccountPoolService) UpdateAccount(poolID int, accountID int, params AccountPoolAccountCreateParams) (AccountPoolAccountView, error) {
	if _, err := getAccountPoolExistingPool(poolID); err != nil {
		return AccountPoolAccountView{}, err
	}
	account, err := getAccountPoolAccountForPool(poolID, accountID)
	if err != nil {
		return AccountPoolAccountView{}, err
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return AccountPoolAccountView{}, errors.New("account pool account name is required")
	}
	if err := validateAccountPoolProxyReference(params.ProxyID); err != nil {
		return AccountPoolAccountView{}, err
	}
	supportedModels, err := marshalAccountPoolOptionalJSON(params.SupportedModels)
	if err != nil {
		return AccountPoolAccountView{}, err
	}
	modelMapping, err := marshalAccountPoolOptionalJSON(params.ModelMapping)
	if err != nil {
		return AccountPoolAccountView{}, err
	}
	status := strings.TrimSpace(params.Status)
	if status == "" {
		status = account.Status
	}
	if status == "" {
		status = model.AccountPoolAccountStatusEnabled
	}
	// Detect re-enable transition: account was not enabled but is being set to enabled.
	isReEnable := account.Status != model.AccountPoolAccountStatusEnabled && status == model.AccountPoolAccountStatusEnabled
	updates := map[string]any{
		"name":                         name,
		"account_identifier":           strings.TrimSpace(params.AccountIdentifier),
		"oauth_type":                   NormalizeAccountPoolOAuthType(params.OAuthType),
		"status":                       status,
		"priority":                     params.Priority,
		"weight":                       params.Weight,
		"max_concurrency":              accountPoolNormalizeMaxConcurrency(params.MaxConcurrency, params.MaxConcurrencySet),
		"proxy_id":                     params.ProxyID,
		"supported_models":             supportedModels,
		"model_mapping":                modelMapping,
		"last_used_at":                 params.LastUsedAt,
		"rate_limited_until":           params.RateLimitedUntil,
		"temp_disabled_until":          params.TempDisabledUntil,
		"temp_disabled_reason":         params.TempDisabledReason,
		"last_error":                   params.LastError,
		"expires_at":                   accountPoolNormalizeExpiresAt(params.ExpiresAt),
		"auto_pause_on_expired":        params.AutoPauseOnExpired,
		"request_quota":                accountPoolNormalizeRequestQuota(params.RequestQuota),
		"request_quota_window_seconds": accountPoolNormalizeRequestQuota(params.RequestQuotaWindowSeconds),
		"updated_time":                 common.GetTimestamp(),
	}
	if isReEnable {
		// Clear failure-escalation state so the account starts fresh after admin re-enable.
		// Quota counters are NOT reset.
		updates["failure_state"] = ""
		updates["overload_until"] = int64(0)
	}
	if accountPoolCredentialHasSecret(params.Credential) {
		// Admin edit forms send secret fields blank when the operator is not rotating
		// them, so a partial update (e.g. only cf_clearance) must merge onto the stored
		// credential rather than replace it — otherwise the blank sso token would be
		// silently erased. Rotating a secret means supplying a new non-empty value.
		existingCredential, err := DecryptAccountPoolCredentialConfig(account.CredentialConfig)
		if err != nil {
			return AccountPoolAccountView{}, err
		}
		merged := mergeAccountPoolCredentialUpdate(existingCredential, params.Credential)
		credentialConfig, err := EncryptAccountPoolCredentialConfig(merged)
		if err != nil {
			return AccountPoolAccountView{}, err
		}
		updates["credential_config"] = credentialConfig
	}
	if accountPoolTokenStateHasValue(params.TokenState) {
		tokenState, err := EncryptAccountPoolTokenState(params.TokenState)
		if err != nil {
			return AccountPoolAccountView{}, err
		}
		updates["token_state"] = tokenState
	}
	if err := model.DB.Model(&account).Updates(updates).Error; err != nil {
		return AccountPoolAccountView{}, err
	}
	if isReEnable {
		clearAccountPoolRuntimeBlock(accountID)
	}
	if err := model.DB.First(&account, accountID).Error; err != nil {
		return AccountPoolAccountView{}, err
	}
	return buildAccountPoolAccountView(account)
}

func (s AccountPoolService) DeleteAccount(poolID int, accountID int) error {
	if _, err := getAccountPoolExistingPool(poolID); err != nil {
		return err
	}
	account, err := getAccountPoolAccountForPool(poolID, accountID)
	if err != nil {
		return err
	}
	return model.DB.Model(&account).Updates(map[string]any{
		"status":       model.AccountPoolAccountStatusDeleted,
		"updated_time": common.GetTimestamp(),
	}).Error
}

func (s AccountPoolService) ListAccounts(poolID int) ([]AccountPoolAccountView, error) {
	if _, err := getAccountPoolExistingPool(poolID); err != nil {
		return nil, err
	}
	var accounts []model.AccountPoolAccount
	if err := model.DB.Where("pool_id = ? AND status <> ?", poolID, model.AccountPoolAccountStatusDeleted).Order("id asc").Find(&accounts).Error; err != nil {
		return nil, err
	}
	views := make([]AccountPoolAccountView, 0, len(accounts))
	for _, account := range accounts {
		view, err := buildAccountPoolAccountView(account)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func (s AccountPoolService) CreateBinding(params AccountPoolBindingCreateParams) (AccountPoolBindingView, error) {
	if _, err := getAccountPoolExistingPool(params.PoolID); err != nil {
		return AccountPoolBindingView{}, err
	}
	if err := validateAccountPoolBindingStatus(params.Status); err != nil {
		return AccountPoolBindingView{}, err
	}
	var channel model.Channel
	if err := model.DB.First(&channel, params.ChannelID).Error; err != nil {
		return AccountPoolBindingView{}, err
	}
	if channel.Status == common.ChannelStatusEnabled {
		return AccountPoolBindingView{}, errors.New("account pool binding requires a disabled channel in phase 1")
	}
	accountFilterConfig, err := marshalAccountPoolOptionalJSON(params.AccountFilterConfig)
	if err != nil {
		return AccountPoolBindingView{}, err
	}
	modelPolicy, err := marshalAccountPoolOptionalJSON(params.ModelPolicy)
	if err != nil {
		return AccountPoolBindingView{}, err
	}
	status := strings.TrimSpace(params.Status)
	if status == "" {
		status = model.AccountPoolBindingStatusDraft
	}
	maxUserConcurrency := params.MaxUserConcurrency
	if maxUserConcurrency < 0 {
		maxUserConcurrency = 0
	}
	// Lock the pool row, then validate platform compatibility and insert in a single
	// transaction so a concurrent UpdatePool platform change cannot commit between the
	// compatibility check and the insert (which would orphan an incompatible binding).
	var binding model.AccountPoolChannelBinding
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		pool, err := lockAccountPoolRow(tx, params.PoolID)
		if err != nil {
			return err
		}
		if err := validateAccountPoolRuntimeChannelForPool(pool, channel); err != nil {
			return err
		}
		if err := validateAccountPoolBindingChannelAvailable(tx, channel.Id, 0); err != nil {
			return err
		}
		schedulePolicy, err := resolveAccountPoolSchedulePolicy(params.SchedulePolicy, pool.DefaultSchedulePolicy)
		if err != nil {
			return err
		}
		binding = model.AccountPoolChannelBinding{
			PoolID:              params.PoolID,
			ChannelID:           params.ChannelID,
			AccountFilterConfig: accountFilterConfig,
			ModelPolicy:         modelPolicy,
			SchedulePolicy:      schedulePolicy,
			AccountRetryTimes:   params.AccountRetryTimes,
			MaxUserConcurrency:  maxUserConcurrency,
			Status:              status,
		}
		return tx.Create(&binding).Error
	}); err != nil {
		return AccountPoolBindingView{}, err
	}
	return buildAccountPoolBindingView(binding, channel), nil
}

func (s AccountPoolService) CreateBoundChannel(params AccountPoolBoundChannelCreateParams) (AccountPoolBindingView, error) {
	if _, err := getAccountPoolExistingPool(params.PoolID); err != nil {
		return AccountPoolBindingView{}, err
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return AccountPoolBindingView{}, errors.New("account pool channel name is required")
	}
	accountFilterConfig, err := marshalAccountPoolOptionalJSON(params.AccountFilterConfig)
	if err != nil {
		return AccountPoolBindingView{}, err
	}
	modelPolicy, err := marshalAccountPoolOptionalJSON(params.ModelPolicy)
	if err != nil {
		return AccountPoolBindingView{}, err
	}
	channel := model.Channel{
		Key:         "account-pool-" + common.GetUUID(),
		Status:      common.ChannelStatusManuallyDisabled,
		Name:        name,
		Group:       "default",
		Models:      accountPoolFixedModelsCSV(params.ModelPolicy),
		CreatedTime: common.GetTimestamp(),
		UpdatedTime: common.GetTimestamp(),
	}
	binding := model.AccountPoolChannelBinding{
		PoolID:              params.PoolID,
		AccountFilterConfig: accountFilterConfig,
		ModelPolicy:         modelPolicy,
		AccountRetryTimes:   params.AccountRetryTimes,
		MaxUserConcurrency:  params.MaxUserConcurrency,
		Status:              model.AccountPoolBindingStatusDraft,
	}
	// Lock the pool row, then resolve the channel type from the locked platform, validate,
	// and create the channel + binding in one transaction so a concurrent UpdatePool
	// platform change cannot leave the new channel/binding incompatible with the platform.
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		pool, err := lockAccountPoolRow(tx, params.PoolID)
		if err != nil {
			return err
		}
		channelType := params.ChannelType
		if channelType == 0 {
			switch pool.Platform {
			case model.AccountPoolPlatformAnthropic:
				channelType = constant.ChannelTypeAnthropic
			case model.AccountPoolPlatformGemini:
				channelType = constant.ChannelTypeGemini
			case model.AccountPoolPlatformXAI:
				channelType = constant.ChannelTypeXai
			case model.AccountPoolPlatformGrokWeb:
				channelType = constant.ChannelTypeGrokWeb
			default:
				channelType = constant.ChannelTypeOpenAI
			}
		}
		channel.Type = channelType
		if err := validateAccountPoolRuntimeChannelForPool(pool, channel); err != nil {
			return err
		}
		schedulePolicy, err := resolveAccountPoolSchedulePolicy(params.SchedulePolicy, pool.DefaultSchedulePolicy)
		if err != nil {
			return err
		}
		binding.SchedulePolicy = schedulePolicy
		if err := tx.Create(&channel).Error; err != nil {
			return err
		}
		if err := channel.AddAbilities(tx); err != nil {
			return err
		}
		binding.ChannelID = channel.Id
		return tx.Create(&binding).Error
	}); err != nil {
		return AccountPoolBindingView{}, err
	}
	invalidateAccountPoolRuntimeEnabledForChannel(channel.Id)
	return buildAccountPoolBindingView(binding, channel), nil
}

func (s AccountPoolService) UpdateBinding(poolID int, bindingID int, params AccountPoolBindingCreateParams) (AccountPoolBindingView, error) {
	if _, err := getAccountPoolExistingPool(poolID); err != nil {
		return AccountPoolBindingView{}, err
	}
	binding, err := getAccountPoolBindingForPool(poolID, bindingID)
	if err != nil {
		return AccountPoolBindingView{}, err
	}
	if params.ChannelID <= 0 {
		return AccountPoolBindingView{}, errors.New("account pool binding channel is required")
	}
	var channel model.Channel
	if err := model.DB.First(&channel, params.ChannelID).Error; err != nil {
		return AccountPoolBindingView{}, err
	}
	if binding.ChannelID != channel.Id && channel.Status == common.ChannelStatusEnabled {
		return AccountPoolBindingView{}, errors.New("account pool binding requires a disabled channel when changing channel")
	}
	accountFilterConfig, err := marshalAccountPoolOptionalJSON(params.AccountFilterConfig)
	if err != nil {
		return AccountPoolBindingView{}, err
	}
	modelPolicy, err := marshalAccountPoolOptionalJSON(params.ModelPolicy)
	if err != nil {
		return AccountPoolBindingView{}, err
	}
	oldChannelID := binding.ChannelID
	oldStatus := binding.Status
	maxUserConcurrency := params.MaxUserConcurrency
	if maxUserConcurrency < 0 {
		maxUserConcurrency = 0
	}
	now := common.GetTimestamp()
	// Lock the pool row, then re-validate platform compatibility and channel availability
	// inside the transaction so a concurrent UpdatePool platform change cannot orphan this
	// binding between the check and the write.
	var schedulePolicy string
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		lockedPool, err := lockAccountPoolRow(tx, poolID)
		if err != nil {
			return err
		}
		if err := validateAccountPoolRuntimeChannelForPool(lockedPool, channel); err != nil {
			return err
		}
		if err := validateAccountPoolBindingChannelAvailable(tx, channel.Id, binding.Id); err != nil {
			return err
		}
		schedulePolicy, err = resolveAccountPoolSchedulePolicy(params.SchedulePolicy, lockedPool.DefaultSchedulePolicy)
		if err != nil {
			return err
		}
		if err := tx.Model(&binding).Updates(map[string]any{
			"channel_id":            channel.Id,
			"account_filter_config": accountFilterConfig,
			"model_policy":          modelPolicy,
			"schedule_policy":       schedulePolicy,
			"account_retry_times":   params.AccountRetryTimes,
			"max_user_concurrency":  maxUserConcurrency,
			"updated_time":          now,
		}).Error; err != nil {
			return err
		}
		if err := syncAccountPoolBindingChannelModels(tx, &channel, params.ModelPolicy, oldStatus == model.AccountPoolBindingStatusEnabled); err != nil {
			return err
		}
		if oldStatus == model.AccountPoolBindingStatusEnabled && oldChannelID != channel.Id {
			if err := setAccountPoolBindingAbilityEnabled(tx, oldChannelID, false); err != nil {
				return err
			}
			if err := setAccountPoolBindingAbilityEnabled(tx, channel.Id, true); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return AccountPoolBindingView{}, err
	}
	binding.ChannelID = channel.Id
	binding.AccountFilterConfig = accountFilterConfig
	binding.ModelPolicy = modelPolicy
	binding.SchedulePolicy = schedulePolicy
	binding.AccountRetryTimes = params.AccountRetryTimes
	binding.MaxUserConcurrency = maxUserConcurrency
	binding.UpdatedTime = now
	invalidateAccountPoolRuntimeEnabledForChannel(oldChannelID)
	invalidateAccountPoolRuntimeEnabledForChannel(channel.Id)
	refreshAccountPoolBindingRoutingCache()
	return buildAccountPoolBindingView(binding, channel), nil
}

func (s AccountPoolService) DeleteBinding(poolID int, bindingID int) error {
	if _, err := getAccountPoolExistingPool(poolID); err != nil {
		return err
	}
	binding, err := getAccountPoolBindingForPool(poolID, bindingID)
	if err != nil {
		return err
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := setAccountPoolBindingAbilityEnabled(tx, binding.ChannelID, false); err != nil {
			return err
		}
		return tx.Delete(&binding).Error
	}); err != nil {
		return err
	}
	invalidateAccountPoolRuntimeEnabledForChannel(binding.ChannelID)
	refreshAccountPoolBindingRoutingCache()
	return nil
}

func (s AccountPoolService) CreateProxy(params AccountPoolProxyCreateParams) (AccountPoolProxyView, error) {
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return AccountPoolProxyView{}, errors.New("account pool proxy name is required")
	}
	protocol := strings.TrimSpace(params.Protocol)
	if protocol == "" {
		return AccountPoolProxyView{}, errors.New("account pool proxy protocol is required")
	}
	host := strings.TrimSpace(params.Host)
	if host == "" {
		return AccountPoolProxyView{}, errors.New("account pool proxy host is required")
	}
	if params.Port <= 0 {
		return AccountPoolProxyView{}, errors.New("account pool proxy port is required")
	}
	if err := validateAccountPoolProxyReference(params.FallbackProxyID); err != nil {
		return AccountPoolProxyView{}, err
	}
	authConfig, err := EncryptAccountPoolProxyAuthConfig(AccountPoolProxyAuthConfig{
		Username: strings.TrimSpace(params.Username),
		Password: params.Password,
	})
	if err != nil {
		return AccountPoolProxyView{}, err
	}
	proxy := model.AccountPoolProxy{
		Name:            name,
		Protocol:        protocol,
		Host:            host,
		Port:            params.Port,
		Username:        strings.TrimSpace(params.Username),
		Password:        authConfig,
		Status:          strings.TrimSpace(params.Status),
		FallbackProxyID: params.FallbackProxyID,
	}
	if err := model.DB.Create(&proxy).Error; err != nil {
		return AccountPoolProxyView{}, err
	}
	return buildAccountPoolProxyView(proxy)
}

func (s AccountPoolService) UpdateProxy(id int, params AccountPoolProxyCreateParams) (AccountPoolProxyView, error) {
	proxy, err := getAccountPoolExistingProxy(id)
	if err != nil {
		return AccountPoolProxyView{}, err
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return AccountPoolProxyView{}, errors.New("account pool proxy name is required")
	}
	protocol := strings.TrimSpace(params.Protocol)
	if protocol == "" {
		return AccountPoolProxyView{}, errors.New("account pool proxy protocol is required")
	}
	host := strings.TrimSpace(params.Host)
	if host == "" {
		return AccountPoolProxyView{}, errors.New("account pool proxy host is required")
	}
	if params.Port <= 0 {
		return AccountPoolProxyView{}, errors.New("account pool proxy port is required")
	}
	if err := validateAccountPoolProxyReference(params.FallbackProxyID); err != nil {
		return AccountPoolProxyView{}, err
	}
	status := strings.TrimSpace(params.Status)
	if status == "" {
		status = proxy.Status
	}
	proxy.Name = name
	proxy.Protocol = protocol
	proxy.Host = host
	proxy.Port = params.Port
	proxy.Username = strings.TrimSpace(params.Username)
	proxy.Status = status
	proxy.FallbackProxyID = params.FallbackProxyID
	if strings.TrimSpace(params.Password) != "" {
		authConfig, err := EncryptAccountPoolProxyAuthConfig(AccountPoolProxyAuthConfig{
			Username: proxy.Username,
			Password: params.Password,
		})
		if err != nil {
			return AccountPoolProxyView{}, err
		}
		proxy.Password = authConfig
	}
	if err := model.DB.Save(&proxy).Error; err != nil {
		return AccountPoolProxyView{}, err
	}
	return buildAccountPoolProxyView(proxy)
}

func (s AccountPoolService) DeleteProxy(id int) error {
	if _, err := getAccountPoolExistingProxy(id); err != nil {
		return err
	}
	now := common.GetTimestamp()
	return model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.AccountPoolProxy{}).
			Where("id = ? AND status <> ?", id, model.AccountPoolProxyStatusDeleted).
			Updates(map[string]any{
				"status":            model.AccountPoolProxyStatusDeleted,
				"fallback_proxy_id": 0,
				"updated_time":      now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.AccountPool{}).
			Where("default_proxy_id = ?", id).
			Updates(map[string]any{
				"default_proxy_id": 0,
				"updated_time":     now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.AccountPoolAccount{}).
			Where("proxy_id = ?", id).
			Updates(map[string]any{
				"proxy_id":     0,
				"updated_time": now,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&model.AccountPoolProxy{}).
			Where("fallback_proxy_id = ?", id).
			Updates(map[string]any{
				"fallback_proxy_id": 0,
				"updated_time":      now,
			}).Error
	})
}

func (s AccountPoolService) ListProxies() ([]AccountPoolProxyView, error) {
	var proxies []model.AccountPoolProxy
	if err := model.DB.Where("status <> ?", model.AccountPoolProxyStatusDeleted).Order("id asc").Find(&proxies).Error; err != nil {
		return nil, err
	}
	views := make([]AccountPoolProxyView, 0, len(proxies))
	for _, proxy := range proxies {
		view, err := buildAccountPoolProxyView(proxy)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func (s AccountPoolService) ListBindings(poolID int) ([]AccountPoolBindingView, error) {
	if _, err := getAccountPoolExistingPool(poolID); err != nil {
		return nil, err
	}
	var bindings []model.AccountPoolChannelBinding
	if err := model.DB.Where("pool_id = ?", poolID).Order("id asc").Find(&bindings).Error; err != nil {
		return nil, err
	}
	views := make([]AccountPoolBindingView, 0, len(bindings))
	for _, binding := range bindings {
		var channel model.Channel
		if err := model.DB.First(&channel, binding.ChannelID).Error; err != nil {
			return nil, err
		}
		views = append(views, buildAccountPoolBindingView(binding, channel))
	}
	return views, nil
}

func (s AccountPoolService) ActivateBinding(poolID int, bindingID int) (AccountPoolBindingView, error) {
	return s.setBindingStatus(poolID, bindingID, model.AccountPoolBindingStatusEnabled)
}

func (s AccountPoolService) DisableBinding(poolID int, bindingID int) (AccountPoolBindingView, error) {
	return s.setBindingStatus(poolID, bindingID, model.AccountPoolBindingStatusDisabled)
}

func (s AccountPoolService) setBindingStatus(poolID int, bindingID int, status string) (AccountPoolBindingView, error) {
	pool, err := getAccountPoolExistingPool(poolID)
	if err != nil {
		return AccountPoolBindingView{}, err
	}
	binding, err := getAccountPoolBindingForPool(poolID, bindingID)
	if err != nil {
		return AccountPoolBindingView{}, err
	}
	var channel model.Channel
	if err := model.DB.First(&channel, binding.ChannelID).Error; err != nil {
		return AccountPoolBindingView{}, err
	}
	if status == model.AccountPoolBindingStatusEnabled {
		if err := validateAccountPoolRuntimeChannelForPool(pool, channel); err != nil {
			return AccountPoolBindingView{}, err
		}
	}
	if err := validateAccountPoolMutableBindingStatus(status); err != nil {
		return AccountPoolBindingView{}, err
	}
	now := common.GetTimestamp()
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&binding).Updates(map[string]any{
			"status":       status,
			"updated_time": now,
		}).Error; err != nil {
			return err
		}
		if status == model.AccountPoolBindingStatusEnabled {
			policy, err := parseAccountPoolModelPolicy(binding.ModelPolicy)
			if err != nil {
				return err
			}
			if err := syncAccountPoolBindingChannelModels(tx, &channel, policy, true); err != nil {
				return err
			}
		}
		return setAccountPoolBindingAbilityEnabled(tx, binding.ChannelID, status == model.AccountPoolBindingStatusEnabled)
	}); err != nil {
		return AccountPoolBindingView{}, err
	}
	binding.Status = status
	binding.UpdatedTime = now
	invalidateAccountPoolRuntimeEnabledForChannel(binding.ChannelID)
	refreshAccountPoolBindingRoutingCache()
	return buildAccountPoolBindingView(binding, channel), nil
}

func setAccountPoolBindingAbilityEnabled(tx *gorm.DB, channelID int, enabled bool) error {
	if channelID <= 0 {
		return nil
	}
	return tx.Model(&model.Ability{}).
		Where("channel_id = ?", channelID).
		Update("enabled", enabled).Error
}

func syncAccountPoolBindingChannelModels(tx *gorm.DB, channel *model.Channel, policy AccountPoolModelPolicy, enabled bool) error {
	if tx == nil || channel == nil || channel.Id <= 0 {
		return nil
	}
	models := accountPoolFixedModelsCSV(policy)
	if models == "" {
		return nil
	}
	if strings.TrimSpace(channel.Models) != models {
		if err := tx.Model(&model.Channel{}).
			Where("id = ?", channel.Id).
			Updates(map[string]any{
				"models":       models,
				"updated_time": common.GetTimestamp(),
			}).Error; err != nil {
			return err
		}
		channel.Models = models
	}
	if err := channel.UpdateAbilities(tx); err != nil {
		return err
	}
	if enabled {
		return setAccountPoolBindingAbilityEnabled(tx, channel.Id, true)
	}
	return nil
}

func accountPoolFixedModelsCSV(policy AccountPoolModelPolicy) string {
	if !strings.EqualFold(strings.TrimSpace(policy.Strategy), "fixed") && len(policy.FixedModels) == 0 {
		return ""
	}
	models := make([]string, 0, len(policy.FixedModels))
	seen := make(map[string]struct{}, len(policy.FixedModels))
	for _, modelName := range policy.FixedModels {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			continue
		}
		if _, exists := seen[modelName]; exists {
			continue
		}
		seen[modelName] = struct{}{}
		models = append(models, modelName)
	}
	return strings.Join(models, ",")
}

func refreshAccountPoolBindingRoutingCache() {
	if common.MemoryCacheEnabled {
		model.InitChannelCache()
	}
}

func normalizeAccountPoolPlatform(platform string) (string, error) {
	platform = strings.TrimSpace(platform)
	switch platform {
	case "":
		return model.AccountPoolPlatformOpenAI, nil
	case model.AccountPoolPlatformOpenAI, model.AccountPoolPlatformAnthropic, model.AccountPoolPlatformGemini, model.AccountPoolPlatformXAI, model.AccountPoolPlatformGrokWeb:
		return platform, nil
	default:
		return "", errors.New("unsupported account pool platform")
	}
}

func normalizeAccountPoolSchedulePolicy(policy string) (string, error) {
	policy = strings.TrimSpace(policy)
	switch policy {
	case "":
		return "", nil
	case AccountPoolSchedulePolicyRoundRobin, AccountPoolSchedulePolicyRandom:
		return policy, nil
	default:
		return "", errors.New("account pool schedule policy must be round_robin or random")
	}
}

func normalizeAccountPoolCapabilityCheckMode(mode string) (string, error) {
	normalized := normalizeAccountPoolCapabilityMode(mode)
	switch normalized {
	case AccountPoolCapabilityModeModelsEndpoint, AccountPoolCapabilityModeProbeModels:
		return normalized, nil
	default:
		return "", errors.New("account pool capability check mode must be models_endpoint or probe_models")
	}
}

func normalizeAccountPoolCapabilityCheckIntervalMinutes(enabled bool, minutes int) int {
	if !enabled {
		return 0
	}
	if minutes <= 0 {
		return DefaultAccountPoolCapabilityCheckIntervalMinutes
	}
	if minutes < MinimumAccountPoolCapabilityCheckIntervalMinutes {
		return MinimumAccountPoolCapabilityCheckIntervalMinutes
	}
	return minutes
}

func normalizeAccountPoolCapabilityCheckTimeoutSeconds(seconds int) int {
	if seconds <= 0 {
		return DefaultAccountPoolCapabilityCheckTimeoutSeconds
	}
	maxSeconds := int(accountPoolCapabilityMaxTimeout / time.Second)
	if seconds > maxSeconds {
		return maxSeconds
	}
	return seconds
}

func resolveAccountPoolSchedulePolicy(policy string, fallback string) (string, error) {
	normalized, err := normalizeAccountPoolSchedulePolicy(policy)
	if err != nil {
		return "", err
	}
	if normalized != "" {
		return normalized, nil
	}
	if strings.TrimSpace(fallback) == AccountPoolSchedulePolicyRandom {
		return AccountPoolSchedulePolicyRandom, nil
	}
	return AccountPoolSchedulePolicyRoundRobin, nil
}

func validateAccountPoolRuntimeChannel(channel model.Channel) error {
	switch channel.Type {
	case constant.ChannelTypeOpenAI, constant.ChannelTypeCodex, constant.ChannelTypeAnthropic, constant.ChannelTypeGemini, constant.ChannelTypeXai, constant.ChannelTypeGrokWeb:
		return nil
	default:
		return errors.New("account pool runtime only supports OpenAI-compatible, Anthropic, Gemini, xAI, or Grok (Web) channels in this phase")
	}
}

// validateAccountPoolRuntimeChannelForPool extends validateAccountPoolRuntimeChannel with a
// platform-compatibility check: the channel type must match the pool's declared platform.
//
//   - pool platform "openai" (or empty) → channel type must be OpenAI(1) or Codex(57)
//   - pool platform "anthropic"         → channel type must be Anthropic(14)
//   - pool platform "gemini"            → channel type must be Gemini(24)
//   - pool platform "xai"               → channel type must be Xai(48)
//   - pool platform "grok_web"          → channel type must be GrokWeb(59)
func validateAccountPoolRuntimeChannelForPool(pool model.AccountPool, channel model.Channel) error {
	if err := validateAccountPoolRuntimeChannel(channel); err != nil {
		return err
	}
	switch pool.Platform {
	case model.AccountPoolPlatformAnthropic:
		if channel.Type != constant.ChannelTypeAnthropic {
			return fmt.Errorf("account pool platform %s is not compatible with channel type %d", pool.Platform, channel.Type)
		}
	case model.AccountPoolPlatformGemini:
		if channel.Type != constant.ChannelTypeGemini {
			return fmt.Errorf("account pool platform %s is not compatible with channel type %d", pool.Platform, channel.Type)
		}
	case model.AccountPoolPlatformXAI:
		if channel.Type != constant.ChannelTypeXai {
			return fmt.Errorf("account pool platform %s is not compatible with channel type %d", pool.Platform, channel.Type)
		}
	case model.AccountPoolPlatformGrokWeb:
		if channel.Type != constant.ChannelTypeGrokWeb {
			return fmt.Errorf("account pool platform %s is not compatible with channel type %d", pool.Platform, channel.Type)
		}
	default: // openai or empty
		if channel.Type == constant.ChannelTypeAnthropic || channel.Type == constant.ChannelTypeGemini || channel.Type == constant.ChannelTypeXai || channel.Type == constant.ChannelTypeGrokWeb {
			return fmt.Errorf("account pool platform %s is not compatible with channel type %d", pool.Platform, channel.Type)
		}
	}
	return nil
}

// lockAccountPoolRow re-reads the pool inside a transaction, taking a row-level write
// lock on MySQL/PostgreSQL (SELECT ... FOR UPDATE). The pool-platform change path and the
// binding mutation paths all acquire this lock before validating compatibility, so on
// MySQL/PostgreSQL they fully serialize on the same row and a binding can never be
// validated against one platform while the pool is concurrently switched to another.
//
// SQLite does not support FOR UPDATE (the clause would be a syntax error), so the lock is
// skipped there; SQLite admits at most one writer at a time, and the affected operations
// are rare single-admin config changes, so the narrow deferred-read window is acceptable
// rather than worth a BEGIN IMMEDIATE workaround.
func lockAccountPoolRow(tx *gorm.DB, poolID int) (model.AccountPool, error) {
	var pool model.AccountPool
	query := tx
	if !common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		query = tx.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.First(&pool, poolID).Error
	return pool, err
}

func validateAccountPoolBindingChannelAvailable(db *gorm.DB, channelID int, excludeBindingID int) error {
	var count int64
	query := db.Model(&model.AccountPoolChannelBinding{}).Where("channel_id = ?", channelID)
	if excludeBindingID > 0 {
		query = query.Where("id <> ?", excludeBindingID)
	}
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("account pool channel is already bound")
	}
	return nil
}

func validateAccountPoolBindingStatus(status string) error {
	status = strings.TrimSpace(status)
	if status == "" || status == model.AccountPoolBindingStatusDraft || status == model.AccountPoolBindingStatusDisabled {
		return nil
	}
	return errors.New("account pool binding status must be draft or disabled in phase 1")
}

func validateAccountPoolMutableBindingStatus(status string) error {
	switch status {
	case model.AccountPoolBindingStatusEnabled, model.AccountPoolBindingStatusDisabled:
		return nil
	default:
		return errors.New("account pool binding status must be enabled or disabled")
	}
}

func validateAccountPoolProxyReference(proxyID int) error {
	if proxyID <= 0 {
		return nil
	}
	if _, err := getAccountPoolExistingProxy(proxyID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("account pool proxy not found")
		}
		return err
	}
	return nil
}

func getAccountPoolBindingForPool(poolID int, bindingID int) (model.AccountPoolChannelBinding, error) {
	var binding model.AccountPoolChannelBinding
	err := model.DB.Where("id = ? AND pool_id = ?", bindingID, poolID).First(&binding).Error
	return binding, err
}

func getAccountPoolAccountForPool(poolID int, accountID int) (model.AccountPoolAccount, error) {
	var account model.AccountPoolAccount
	err := model.DB.Where("id = ? AND pool_id = ? AND status <> ?", accountID, poolID, model.AccountPoolAccountStatusDeleted).First(&account).Error
	return account, err
}

func getAccountPoolExistingPool(poolID int) (model.AccountPool, error) {
	var pool model.AccountPool
	err := model.DB.Where("status <> ?", model.AccountPoolStatusDeleted).First(&pool, poolID).Error
	return pool, err
}

func getAccountPoolExistingProxy(proxyID int) (model.AccountPoolProxy, error) {
	var proxy model.AccountPoolProxy
	err := model.DB.Where("status <> ?", model.AccountPoolProxyStatusDeleted).First(&proxy, proxyID).Error
	return proxy, err
}

// GetAccountPoolRuntimeUserConcurrencyConfig returns the binding ID and MaxUserConcurrency
// for the enabled account-pool binding for the given channel. Returns (0, 0, nil) when
// no enabled binding exists (channel not under account-pool control).
// Results are cached with the same TTL as AccountPoolRuntimeEnabledForChannel and
// are invalidated whenever the binding is mutated or its status changes.
func GetAccountPoolRuntimeUserConcurrencyConfig(channelID int) (bindingID int, maxUserConcurrency int, err error) {
	if channelID <= 0 || model.DB == nil {
		return 0, 0, nil
	}
	cacheKey := strconv.Itoa(channelID)
	if cached, found, cerr := getAccountPoolRuntimeConcurrencyCache().Get(cacheKey); cerr == nil && found {
		if cached.BindingID == accountPoolConcurrencyAbsent {
			return 0, 0, nil
		}
		return cached.BindingID, cached.MaxUserConcurrency, nil
	}
	var binding model.AccountPoolChannelBinding
	err = model.DB.
		Select("id", "max_user_concurrency").
		Where("channel_id = ? AND status = ?", channelID, model.AccountPoolBindingStatusEnabled).
		First(&binding).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = getAccountPoolRuntimeConcurrencyCache().SetWithTTL(cacheKey,
				accountPoolConcurrencyConfig{BindingID: accountPoolConcurrencyAbsent},
				accountPoolRuntimeEnabledCacheTTL)
			return 0, 0, nil
		}
		return 0, 0, err
	}
	_ = getAccountPoolRuntimeConcurrencyCache().SetWithTTL(cacheKey,
		accountPoolConcurrencyConfig{BindingID: binding.Id, MaxUserConcurrency: binding.MaxUserConcurrency},
		accountPoolRuntimeEnabledCacheTTL)
	return binding.Id, binding.MaxUserConcurrency, nil
}

func AccountPoolRuntimeEnabledForChannel(channelID int) (bool, error) {
	if channelID <= 0 || model.DB == nil {
		return false, nil
	}
	cacheKey := strconv.Itoa(channelID)
	if cached, found, err := getAccountPoolRuntimeEnabledCache().Get(cacheKey); err == nil && found {
		return cached, nil
	}
	var count int64
	err := model.DB.Model(&model.AccountPoolChannelBinding{}).
		Where("channel_id = ? AND status = ?", channelID, model.AccountPoolBindingStatusEnabled).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	enabled := count > 0
	_ = getAccountPoolRuntimeEnabledCache().SetWithTTL(cacheKey, enabled, accountPoolRuntimeEnabledCacheTTL)
	return enabled, nil
}

func getAccountPoolRuntimeEnabledCache() *cachex.HybridCache[bool] {
	accountPoolRuntimeEnabledCacheOnce.Do(func() {
		accountPoolRuntimeEnabledCache = cachex.NewHybridCache[bool](cachex.HybridCacheConfig[bool]{
			Namespace:  cachex.Namespace(accountPoolRuntimeEnabledCacheNamespace),
			Redis:      common.RDB,
			RedisCodec: cachex.JSONCodec[bool]{},
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			Memory: func() *hot.HotCache[string, bool] {
				return hot.NewHotCache[string, bool](hot.LRU, 1024).
					WithTTL(accountPoolRuntimeEnabledCacheTTL).
					WithJanitor().
					Build()
			},
		})
	})
	return accountPoolRuntimeEnabledCache
}

func getAccountPoolRuntimeConcurrencyCache() *cachex.HybridCache[accountPoolConcurrencyConfig] {
	accountPoolRuntimeConcurrencyCacheOnce.Do(func() {
		accountPoolRuntimeConcurrencyCache = cachex.NewHybridCache[accountPoolConcurrencyConfig](
			cachex.HybridCacheConfig[accountPoolConcurrencyConfig]{
				Namespace:  cachex.Namespace(accountPoolRuntimeConcurrencyCacheNamespace),
				Redis:      common.RDB,
				RedisCodec: cachex.JSONCodec[accountPoolConcurrencyConfig]{},
				RedisEnabled: func() bool {
					return common.RedisEnabled && common.RDB != nil
				},
				Memory: func() *hot.HotCache[string, accountPoolConcurrencyConfig] {
					return hot.NewHotCache[string, accountPoolConcurrencyConfig](hot.LRU, 1024).
						WithTTL(accountPoolRuntimeEnabledCacheTTL).
						WithJanitor().
						Build()
				},
			})
	})
	return accountPoolRuntimeConcurrencyCache
}

func invalidateAccountPoolRuntimeEnabledForChannel(channelID int) {
	if channelID <= 0 {
		return
	}
	key := strconv.Itoa(channelID)
	// HybridCache.DeleteMany accepts raw keys and applies the namespace internally.
	_, _ = getAccountPoolRuntimeEnabledCache().DeleteMany([]string{key})
	_, _ = getAccountPoolRuntimeConcurrencyCache().DeleteMany([]string{key})
}

func accountPoolNormalizeMaxConcurrency(value int, explicit bool) int {
	if value < 0 {
		return 0
	}
	if !explicit && value == 0 {
		return 1
	}
	return value
}

// accountPoolNormalizeRequestQuota clamps negative quota values to 0.
// 0 means unlimited / unset.
func accountPoolNormalizeRequestQuota(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

// accountPoolNormalizeExpiresAt clamps negative ExpiresAt to 0.
// 0 means "never expires"; positive values are Unix seconds.
func accountPoolNormalizeExpiresAt(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func buildAccountPoolAccountView(account model.AccountPoolAccount) (AccountPoolAccountView, error) {
	var supportedModels []string
	if account.SupportedModels != "" {
		if err := common.UnmarshalJsonStr(account.SupportedModels, &supportedModels); err != nil {
			return AccountPoolAccountView{}, err
		}
	}
	modelMapping := map[string]string{}
	if account.ModelMapping != "" {
		if err := common.UnmarshalJsonStr(account.ModelMapping, &modelMapping); err != nil {
			return AccountPoolAccountView{}, err
		}
	}
	lastCapabilityCheckModels := []string{}
	if strings.TrimSpace(account.LastCapabilityCheckModels) != "" {
		if err := common.UnmarshalJsonStr(account.LastCapabilityCheckModels, &lastCapabilityCheckModels); err != nil {
			return AccountPoolAccountView{}, err
		}
	}
	return AccountPoolAccountView{
		Id:                           account.Id,
		PoolID:                       account.PoolID,
		Name:                         account.Name,
		AccountIdentifier:            account.AccountIdentifier,
		Status:                       account.Status,
		Priority:                     account.Priority,
		Weight:                       account.Weight,
		MaxConcurrency:               account.MaxConcurrency,
		ProxyID:                      account.ProxyID,
		SupportedModels:              supportedModels,
		ModelMapping:                 modelMapping,
		LastUsedAt:                   account.LastUsedAt,
		LastSuccessAt:                account.LastSuccessAt,
		LastFailureAt:                account.LastFailureAt,
		SuccessCount:                 account.SuccessCount,
		FailureCount:                 account.FailureCount,
		TotalPromptTokens:            account.TotalPromptTokens,
		TotalCompletionTokens:        account.TotalCompletionTokens,
		TotalCachedTokens:            account.TotalCachedTokens,
		TotalCacheWriteTokens:        account.TotalCacheWriteTokens,
		LastPromptTokens:             account.LastPromptTokens,
		LastCompletionTokens:         account.LastCompletionTokens,
		LastCachedTokens:             account.LastCachedTokens,
		LastCacheWriteTokens:         account.LastCacheWriteTokens,
		TotalLatencyMS:               account.TotalLatencyMS,
		LatencySampleCount:           account.LatencySampleCount,
		LastLatencyMS:                account.LastLatencyMS,
		TotalFirstTokenLatencyMS:     account.TotalFirstTokenLatencyMS,
		FirstTokenLatencySampleCount: account.FirstTokenLatencySampleCount,
		LastFirstTokenLatencyMS:      account.LastFirstTokenLatencyMS,
		RateLimitedUntil:             account.RateLimitedUntil,
		TempDisabledUntil:            account.TempDisabledUntil,
		TempDisabledReason:           account.TempDisabledReason,
		LastError:                    account.LastError,
		ExpiresAt:                    account.ExpiresAt,
		AutoPauseOnExpired:           account.AutoPauseOnExpired,
		LastCapabilityCheckAt:        account.LastCapabilityCheckAt,
		LastCapabilityCheckStatus:    account.LastCapabilityCheckStatus,
		LastCapabilityCheckError:     account.LastCapabilityCheckError,
		LastCapabilityCheckModels:    lastCapabilityCheckModels,
		OAuthType:                    account.OAuthType,
		HasCredential:                strings.TrimSpace(account.CredentialConfig) != "",
		HasToken:                     strings.TrimSpace(account.TokenState) != "",
		RequestQuota:                 account.RequestQuota,
		RequestQuotaUsed:             account.RequestQuotaUsed,
		RequestQuotaWindowStart:      account.RequestQuotaWindowStart,
		RequestQuotaWindowSeconds:    account.RequestQuotaWindowSeconds,
		CreatedTime:                  account.CreatedTime,
		UpdatedTime:                  account.UpdatedTime,
	}, nil
}

func buildAccountPoolBindingView(binding model.AccountPoolChannelBinding, channel model.Channel) AccountPoolBindingView {
	return AccountPoolBindingView{
		Id:                  binding.Id,
		PoolID:              binding.PoolID,
		ChannelID:           binding.ChannelID,
		ChannelName:         channel.Name,
		ChannelStatus:       channel.Status,
		AccountFilterConfig: binding.AccountFilterConfig,
		ModelPolicy:         binding.ModelPolicy,
		SchedulePolicy:      binding.SchedulePolicy,
		AccountRetryTimes:   binding.AccountRetryTimes,
		MaxUserConcurrency:  binding.MaxUserConcurrency,
		Status:              binding.Status,
		RuntimeEnabled:      binding.Status == model.AccountPoolBindingStatusEnabled,
		CreatedTime:         binding.CreatedTime,
		UpdatedTime:         binding.UpdatedTime,
	}
}

func buildAccountPoolProxyView(proxy model.AccountPoolProxy) (AccountPoolProxyView, error) {
	return AccountPoolProxyView{
		Id:              proxy.Id,
		Name:            proxy.Name,
		Protocol:        proxy.Protocol,
		Host:            proxy.Host,
		Port:            proxy.Port,
		Username:        proxy.Username,
		Status:          proxy.Status,
		FallbackProxyID: proxy.FallbackProxyID,
		HasPassword:     strings.TrimSpace(proxy.Password) != "",
		CreatedTime:     proxy.CreatedTime,
		UpdatedTime:     proxy.UpdatedTime,
	}, nil
}

func accountPoolCredentialHasValue(config AccountPoolCredentialConfig) bool {
	return strings.TrimSpace(config.Type) != "" ||
		strings.TrimSpace(config.APIKey) != "" ||
		strings.TrimSpace(config.RefreshToken) != "" ||
		strings.TrimSpace(config.Email) != "" ||
		strings.TrimSpace(config.IDToken) != "" ||
		strings.TrimSpace(config.ClientID) != "" ||
		strings.TrimSpace(config.Scope) != "" ||
		strings.TrimSpace(config.TokenType) != "" ||
		strings.TrimSpace(config.Subject) != "" ||
		strings.TrimSpace(config.TeamID) != "" ||
		strings.TrimSpace(config.SubscriptionTier) != "" ||
		strings.TrimSpace(config.EntitlementStatus) != "" ||
		strings.TrimSpace(config.ServiceAccountJSON) != "" ||
		strings.TrimSpace(config.CFClearance) != ""
}

// mergeAccountPoolCredentialUpdate overlays the non-empty fields of an incoming
// credential update onto the existing decrypted credential. Admin edit forms leave a
// secret field blank when the operator does not want to rotate it, so a blank incoming
// secret means "keep the stored value" rather than "erase it". This prevents a partial
// update (for example setting only cf_clearance) from wiping the stored sso token. To
// rotate a secret the operator supplies a new non-empty value; clearing an individual
// secret back to empty is intentionally not expressible through this path.
//
// A credential-TYPE change is treated as a full rotation, NOT a partial edit: the
// incoming config replaces the stored one wholesale. Merging across types would retain
// secrets that do not belong to the new type — e.g. switching api_key->oauth would keep
// the old APIKey, which the token provider short-circuits on (so the account would keep
// authenticating with the stale key), and the obsolete secret would also linger in
// storage/export. Same-type edits keep the blank-means-keep partial-update semantics.
func mergeAccountPoolCredentialUpdate(existing, incoming AccountPoolCredentialConfig) AccountPoolCredentialConfig {
	incomingType := strings.TrimSpace(incoming.Type)
	existingType := strings.TrimSpace(existing.Type)
	if incomingType != "" && !strings.EqualFold(incomingType, existingType) {
		return incoming
	}
	merged := existing
	if incomingType != "" {
		merged.Type = incoming.Type
	}
	if strings.TrimSpace(incoming.APIKey) != "" {
		merged.APIKey = incoming.APIKey
	}
	if strings.TrimSpace(incoming.Email) != "" {
		merged.Email = incoming.Email
	}
	if strings.TrimSpace(incoming.RefreshToken) != "" {
		merged.RefreshToken = incoming.RefreshToken
	}
	if strings.TrimSpace(incoming.IDToken) != "" {
		merged.IDToken = incoming.IDToken
	}
	if strings.TrimSpace(incoming.ClientID) != "" {
		merged.ClientID = incoming.ClientID
	}
	if strings.TrimSpace(incoming.Scope) != "" {
		merged.Scope = incoming.Scope
	}
	if strings.TrimSpace(incoming.TokenType) != "" {
		merged.TokenType = incoming.TokenType
	}
	if strings.TrimSpace(incoming.Subject) != "" {
		merged.Subject = incoming.Subject
	}
	if strings.TrimSpace(incoming.TeamID) != "" {
		merged.TeamID = incoming.TeamID
	}
	if strings.TrimSpace(incoming.SubscriptionTier) != "" {
		merged.SubscriptionTier = incoming.SubscriptionTier
	}
	if strings.TrimSpace(incoming.EntitlementStatus) != "" {
		merged.EntitlementStatus = incoming.EntitlementStatus
	}
	if strings.TrimSpace(incoming.ServiceAccountJSON) != "" {
		merged.ServiceAccountJSON = incoming.ServiceAccountJSON
	}
	if strings.TrimSpace(incoming.Location) != "" {
		merged.Location = incoming.Location
	}
	if strings.TrimSpace(incoming.CFClearance) != "" {
		merged.CFClearance = incoming.CFClearance
	}
	return merged
}

func accountPoolCredentialHasSecret(config AccountPoolCredentialConfig) bool {
	return strings.TrimSpace(config.APIKey) != "" ||
		strings.TrimSpace(config.RefreshToken) != "" ||
		strings.TrimSpace(config.Email) != "" ||
		strings.TrimSpace(config.IDToken) != "" ||
		strings.TrimSpace(config.ClientID) != "" ||
		strings.TrimSpace(config.Scope) != "" ||
		strings.TrimSpace(config.TokenType) != "" ||
		strings.TrimSpace(config.Subject) != "" ||
		strings.TrimSpace(config.TeamID) != "" ||
		strings.TrimSpace(config.SubscriptionTier) != "" ||
		strings.TrimSpace(config.EntitlementStatus) != "" ||
		strings.TrimSpace(config.ServiceAccountJSON) != "" ||
		strings.TrimSpace(config.CFClearance) != ""
}

func accountPoolTokenStateHasValue(state AccountPoolTokenState) bool {
	return accountPoolTokenStateHasSecret(state) || state.ExpiresAt != 0 || state.Version != 0
}

func marshalAccountPoolOptionalJSON(value any) (string, error) {
	data, err := common.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ResetAccountPoolRuntimeForTest resets all process-global singletons used by the account-pool
// runtime to a clean zero state. It must only be called from tests.
func ResetAccountPoolRuntimeForTest() {
	resetAccountPoolRuntimeLeasesForTest()
	resetAccountPoolRuntimeSelectionRecencyForTest()
	resetAccountPoolRuntimeBlocksForTest()
	resetAccountPoolUserConcurrencyForTest()
	resetAccountPoolRuntimeAffinitiesForTest()
	resetAccountPoolProxyHealthForTest()
	accountPoolRuntimeEnabledCache = nil
	accountPoolRuntimeEnabledCacheOnce = sync.Once{}
	accountPoolRuntimeConcurrencyCache = nil
	accountPoolRuntimeConcurrencyCacheOnce = sync.Once{}
}
