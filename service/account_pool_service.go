package service

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

type AccountPoolService struct{}

type AccountPoolCreateParams struct {
	Name                  string
	Platform              string
	DefaultProxyID        int
	DefaultMonitorEnabled bool
	DefaultSchedulePolicy string
	Remark                string
}

type AccountPoolAccountCreateParams struct {
	PoolID             int
	Name               string
	AccountIdentifier  string
	Credential         AccountPoolCredentialConfig
	TokenState         AccountPoolTokenState
	Status             string
	Priority           int64
	Weight             uint
	MaxConcurrency     int
	ProxyID            int
	SupportedModels    []string
	ModelMapping       map[string]string
	LastUsedAt         int64
	RateLimitedUntil   int64
	TempDisabledUntil  int64
	TempDisabledReason string
	LastError          string
}

type AccountPoolBindingCreateParams struct {
	PoolID              int
	ChannelID           int
	AccountFilterConfig AccountPoolAccountFilterConfig
	ModelPolicy         AccountPoolModelPolicy
	SchedulePolicy      string
	AccountRetryTimes   int
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
	Id                 int               `json:"id"`
	PoolID             int               `json:"pool_id"`
	Name               string            `json:"name"`
	AccountIdentifier  string            `json:"account_identifier"`
	Status             string            `json:"status"`
	Priority           int64             `json:"priority"`
	Weight             uint              `json:"weight"`
	MaxConcurrency     int               `json:"max_concurrency"`
	ProxyID            int               `json:"proxy_id"`
	SupportedModels    []string          `json:"supported_models"`
	ModelMapping       map[string]string `json:"model_mapping"`
	LastUsedAt         int64             `json:"last_used_at"`
	RateLimitedUntil   int64             `json:"rate_limited_until"`
	TempDisabledUntil  int64             `json:"temp_disabled_until"`
	TempDisabledReason string            `json:"temp_disabled_reason"`
	LastError          string            `json:"last_error"`
	HasCredential      bool              `json:"has_credential"`
	HasToken           bool              `json:"has_token"`
	CredentialPreview  string            `json:"credential_preview"`
	CreatedTime        int64             `json:"created_time"`
	UpdatedTime        int64             `json:"updated_time"`
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
	Status              string `json:"status"`
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
	pool := model.AccountPool{
		Name:                  name,
		Platform:              platform,
		DefaultProxyID:        params.DefaultProxyID,
		DefaultMonitorEnabled: params.DefaultMonitorEnabled,
		DefaultSchedulePolicy: params.DefaultSchedulePolicy,
		Remark:                params.Remark,
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
	err = model.DB.Model(&pool).Updates(map[string]any{
		"name":                    name,
		"platform":                platform,
		"default_proxy_id":        params.DefaultProxyID,
		"default_monitor_enabled": params.DefaultMonitorEnabled,
		"default_schedule_policy": params.DefaultSchedulePolicy,
		"remark":                  params.Remark,
	}).Error
	if err != nil {
		return model.AccountPool{}, err
	}
	return pool, model.DB.First(&pool, id).Error
}

func (s AccountPoolService) DeletePool(id int) error {
	return model.DB.Model(&model.AccountPool{}).Where("id = ?", id).Update("status", model.AccountPoolStatusDeleted).Error
}

func (s AccountPoolService) CreateAccount(params AccountPoolAccountCreateParams) (AccountPoolAccountView, error) {
	if _, err := getAccountPoolExistingPool(params.PoolID); err != nil {
		return AccountPoolAccountView{}, err
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return AccountPoolAccountView{}, errors.New("account pool account name is required")
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
		PoolID:             params.PoolID,
		Name:               name,
		AccountIdentifier:  params.AccountIdentifier,
		CredentialConfig:   credentialConfig,
		TokenState:         tokenState,
		Status:             params.Status,
		Priority:           params.Priority,
		Weight:             params.Weight,
		MaxConcurrency:     params.MaxConcurrency,
		ProxyID:            params.ProxyID,
		SupportedModels:    supportedModels,
		ModelMapping:       modelMapping,
		LastUsedAt:         params.LastUsedAt,
		RateLimitedUntil:   params.RateLimitedUntil,
		TempDisabledUntil:  params.TempDisabledUntil,
		TempDisabledReason: params.TempDisabledReason,
		LastError:          params.LastError,
	}
	if err := model.DB.Create(&account).Error; err != nil {
		return AccountPoolAccountView{}, err
	}
	return buildAccountPoolAccountView(account)
}

func (s AccountPoolService) ListAccounts(poolID int) ([]AccountPoolAccountView, error) {
	if _, err := getAccountPoolExistingPool(poolID); err != nil {
		return nil, err
	}
	var accounts []model.AccountPoolAccount
	if err := model.DB.Where("pool_id = ?", poolID).Order("id asc").Find(&accounts).Error; err != nil {
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
	binding := model.AccountPoolChannelBinding{
		PoolID:              params.PoolID,
		ChannelID:           params.ChannelID,
		AccountFilterConfig: accountFilterConfig,
		ModelPolicy:         modelPolicy,
		SchedulePolicy:      params.SchedulePolicy,
		AccountRetryTimes:   params.AccountRetryTimes,
		Status:              model.AccountPoolBindingStatusDraft,
	}
	if err := model.DB.Create(&binding).Error; err != nil {
		return AccountPoolBindingView{}, err
	}
	return buildAccountPoolBindingView(binding, channel), nil
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

func normalizeAccountPoolPlatform(platform string) (string, error) {
	platform = strings.TrimSpace(platform)
	if platform == "" {
		return model.AccountPoolPlatformOpenAI, nil
	}
	if platform != model.AccountPoolPlatformOpenAI {
		return "", errors.New("unsupported account pool platform")
	}
	return platform, nil
}

func getAccountPoolExistingPool(poolID int) (model.AccountPool, error) {
	var pool model.AccountPool
	err := model.DB.Where("status <> ?", model.AccountPoolStatusDeleted).First(&pool, poolID).Error
	return pool, err
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
	credentialConfig, err := DecryptAccountPoolCredentialConfig(account.CredentialConfig)
	if err != nil {
		return AccountPoolAccountView{}, err
	}
	tokenState, err := DecryptAccountPoolTokenState(account.TokenState)
	if err != nil {
		return AccountPoolAccountView{}, err
	}
	return AccountPoolAccountView{
		Id:                 account.Id,
		PoolID:             account.PoolID,
		Name:               account.Name,
		AccountIdentifier:  account.AccountIdentifier,
		Status:             account.Status,
		Priority:           account.Priority,
		Weight:             account.Weight,
		MaxConcurrency:     account.MaxConcurrency,
		ProxyID:            account.ProxyID,
		SupportedModels:    supportedModels,
		ModelMapping:       modelMapping,
		LastUsedAt:         account.LastUsedAt,
		RateLimitedUntil:   account.RateLimitedUntil,
		TempDisabledUntil:  account.TempDisabledUntil,
		TempDisabledReason: account.TempDisabledReason,
		LastError:          account.LastError,
		HasCredential:      accountPoolCredentialHasSecret(credentialConfig),
		HasToken:           tokenState.AccessToken != "" || tokenState.RefreshToken != "",
		CredentialPreview:  accountPoolCredentialPreview(credentialConfig),
		CreatedTime:        account.CreatedTime,
		UpdatedTime:        account.UpdatedTime,
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
		Status:              binding.Status,
		CreatedTime:         binding.CreatedTime,
		UpdatedTime:         binding.UpdatedTime,
	}
}

func buildAccountPoolProxyView(proxy model.AccountPoolProxy) (AccountPoolProxyView, error) {
	authConfig, err := DecryptAccountPoolProxyAuthConfig(proxy.Password)
	if err != nil {
		return AccountPoolProxyView{}, err
	}
	return AccountPoolProxyView{
		Id:              proxy.Id,
		Name:            proxy.Name,
		Protocol:        proxy.Protocol,
		Host:            proxy.Host,
		Port:            proxy.Port,
		Username:        proxy.Username,
		Status:          proxy.Status,
		FallbackProxyID: proxy.FallbackProxyID,
		HasPassword:     strings.TrimSpace(authConfig.Password) != "",
		CreatedTime:     proxy.CreatedTime,
		UpdatedTime:     proxy.UpdatedTime,
	}, nil
}

func accountPoolCredentialHasSecret(config AccountPoolCredentialConfig) bool {
	return config.APIKey != "" || config.RefreshToken != "" || config.Email != ""
}

func accountPoolCredentialPreview(config AccountPoolCredentialConfig) string {
	switch {
	case config.APIKey != "":
		return MaskAccountPoolSecretValue(config.APIKey)
	case config.RefreshToken != "":
		return MaskAccountPoolSecretValue(config.RefreshToken)
	case config.Email != "":
		return MaskAccountPoolSecretValue(config.Email)
	default:
		return ""
	}
}

func marshalAccountPoolOptionalJSON(value any) (string, error) {
	data, err := common.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
