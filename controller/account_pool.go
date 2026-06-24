package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

const maxAccountPoolImportRequestBodyBytes = 16 << 20

func ListAccountPools(c *gin.Context) {
	pools, err := (&service.AccountPoolService{}).ListPools()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	responses := make([]dto.AccountPoolResponse, 0, len(pools))
	for _, pool := range pools {
		responses = append(responses, accountPoolResponse(pool))
	}
	common.ApiSuccess(c, responses)
}

func CreateAccountPool(c *gin.Context) {
	var req dto.AccountPoolCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	pool, err := (&service.AccountPoolService{}).CreatePool(accountPoolCreateParams(req))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.create", map[string]interface{}{
		"id":   pool.Id,
		"name": pool.Name,
	})
	common.ApiSuccess(c, accountPoolResponse(pool))
}

func GetAccountPool(c *gin.Context) {
	id, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	pool, err := (&service.AccountPoolService{}).GetPool(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, accountPoolResponse(pool))
}

func UpdateAccountPool(c *gin.Context) {
	id, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	var req dto.AccountPoolUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	pool, err := (&service.AccountPoolService{}).UpdatePool(id, accountPoolCreateParams(req))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.update", map[string]interface{}{
		"id":   pool.Id,
		"name": pool.Name,
	})
	common.ApiSuccess(c, accountPoolResponse(pool))
}

func DeleteAccountPool(c *gin.Context) {
	id, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	pool, err := (&service.AccountPoolService{}).GetPool(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := (&service.AccountPoolService{}).DeletePool(id); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.delete", map[string]interface{}{
		"id":   pool.Id,
		"name": pool.Name,
	})
	common.ApiSuccess(c, nil)
}

func ListAccountPoolAccounts(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	accounts, err := (&service.AccountPoolService{}).ListAccounts(poolID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	responses := make([]dto.AccountPoolAccountResponse, 0, len(accounts))
	for _, account := range accounts {
		responses = append(responses, accountPoolAccountResponse(account))
	}
	common.ApiSuccess(c, responses)
}

func CreateAccountPoolAccount(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	var req dto.AccountPoolAccountCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	account, err := (&service.AccountPoolService{}).CreateAccount(accountPoolAccountCreateParams(poolID, req))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.account_create", map[string]interface{}{
		"id":      account.Id,
		"name":    account.Name,
		"pool_id": poolID,
	})
	common.ApiSuccess(c, accountPoolAccountResponse(account))
}

func ImportAccountPoolAccounts(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAccountPoolImportRequestBodyBytes)
	var req dto.AccountPoolAccountImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	result, err := (&service.AccountPoolService{}).ImportAccounts(accountPoolAccountImportParams(poolID, req))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.account_import", map[string]interface{}{
		"pool_id":  poolID,
		"format":   req.Format,
		"imported": result.Imported,
		"skipped":  result.Skipped,
		"failed":   result.Failed,
	})
	common.ApiSuccess(c, accountPoolAccountImportResponse(result))
}

func UpdateAccountPoolAccount(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	accountID, ok := accountPoolAccountIDFromParam(c)
	if !ok {
		return
	}
	var req dto.AccountPoolAccountCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	account, err := (&service.AccountPoolService{}).UpdateAccount(poolID, accountID, accountPoolAccountCreateParams(poolID, req))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.account_update", map[string]interface{}{
		"id":      account.Id,
		"name":    account.Name,
		"pool_id": poolID,
	})
	common.ApiSuccess(c, accountPoolAccountResponse(account))
}

func DeleteAccountPoolAccount(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	accountID, ok := accountPoolAccountIDFromParam(c)
	if !ok {
		return
	}
	if err := (&service.AccountPoolService{}).DeleteAccount(poolID, accountID); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.account_delete", map[string]interface{}{
		"id":      accountID,
		"pool_id": poolID,
	})
	common.ApiSuccess(c, nil)
}

func ListAccountPoolProxies(c *gin.Context) {
	proxies, err := (&service.AccountPoolService{}).ListProxies()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	responses := make([]dto.AccountPoolProxyResponse, 0, len(proxies))
	for _, proxy := range proxies {
		responses = append(responses, accountPoolProxyResponse(proxy))
	}
	common.ApiSuccess(c, responses)
}

func CreateAccountPoolProxy(c *gin.Context) {
	var req dto.AccountPoolProxyCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	proxy, err := (&service.AccountPoolService{}).CreateProxy(accountPoolProxyCreateParams(req))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.proxy_create", map[string]interface{}{
		"id":   proxy.Id,
		"name": proxy.Name,
	})
	common.ApiSuccess(c, accountPoolProxyResponse(proxy))
}

func UpdateAccountPoolProxy(c *gin.Context) {
	proxyID, ok := accountPoolProxyIDFromParam(c)
	if !ok {
		return
	}
	var req dto.AccountPoolProxyCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	proxy, err := (&service.AccountPoolService{}).UpdateProxy(proxyID, accountPoolProxyCreateParams(req))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.proxy_update", map[string]interface{}{
		"id":   proxy.Id,
		"name": proxy.Name,
	})
	common.ApiSuccess(c, accountPoolProxyResponse(proxy))
}

func DeleteAccountPoolProxy(c *gin.Context) {
	proxyID, ok := accountPoolProxyIDFromParam(c)
	if !ok {
		return
	}
	if err := (&service.AccountPoolService{}).DeleteProxy(proxyID); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.proxy_delete", map[string]interface{}{
		"id": proxyID,
	})
	common.ApiSuccess(c, nil)
}

func ListAccountPoolBindings(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	bindings, err := (&service.AccountPoolService{}).ListBindings(poolID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	responses := make([]dto.AccountPoolBindingResponse, 0, len(bindings))
	for _, binding := range bindings {
		responses = append(responses, accountPoolBindingResponse(binding))
	}
	common.ApiSuccess(c, responses)
}

func CreateAccountPoolBinding(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	var req dto.AccountPoolBindingCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	binding, err := (&service.AccountPoolService{}).CreateBinding(accountPoolBindingCreateParams(poolID, req))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.binding_create", map[string]interface{}{
		"id":         binding.Id,
		"pool_id":    poolID,
		"channel_id": binding.ChannelID,
	})
	common.ApiSuccess(c, accountPoolBindingResponse(binding))
}

func CreateAccountPoolBoundChannel(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	var req dto.AccountPoolBoundChannelCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	binding, err := (&service.AccountPoolService{}).CreateBoundChannel(accountPoolBoundChannelCreateParams(poolID, req))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.bound_channel_create", map[string]interface{}{
		"id":         binding.Id,
		"pool_id":    poolID,
		"channel_id": binding.ChannelID,
		"name":       binding.ChannelName,
	})
	common.ApiSuccess(c, accountPoolBindingResponse(binding))
}

func UpdateAccountPoolBinding(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	bindingID, ok := accountPoolBindingIDFromParam(c)
	if !ok {
		return
	}
	var req dto.AccountPoolBindingCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	binding, err := (&service.AccountPoolService{}).UpdateBinding(poolID, bindingID, accountPoolBindingCreateParams(poolID, req))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.binding_update", map[string]interface{}{
		"id":         binding.Id,
		"pool_id":    poolID,
		"channel_id": binding.ChannelID,
	})
	common.ApiSuccess(c, accountPoolBindingResponse(binding))
}

func DeleteAccountPoolBinding(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	bindingID, ok := accountPoolBindingIDFromParam(c)
	if !ok {
		return
	}
	if err := (&service.AccountPoolService{}).DeleteBinding(poolID, bindingID); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.binding_delete", map[string]interface{}{
		"id":      bindingID,
		"pool_id": poolID,
	})
	common.ApiSuccess(c, nil)
}

func ActivateAccountPoolBinding(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	bindingID, ok := accountPoolBindingIDFromParam(c)
	if !ok {
		return
	}
	binding, err := (&service.AccountPoolService{}).ActivateBinding(poolID, bindingID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.binding_activate", map[string]interface{}{
		"id":         binding.Id,
		"pool_id":    poolID,
		"channel_id": binding.ChannelID,
	})
	common.ApiSuccess(c, accountPoolBindingResponse(binding))
}

func DisableAccountPoolBinding(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	bindingID, ok := accountPoolBindingIDFromParam(c)
	if !ok {
		return
	}
	binding, err := (&service.AccountPoolService{}).DisableBinding(poolID, bindingID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.binding_disable", map[string]interface{}{
		"id":         binding.Id,
		"pool_id":    poolID,
		"channel_id": binding.ChannelID,
	})
	common.ApiSuccess(c, accountPoolBindingResponse(binding))
}

func accountPoolIDFromParam(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id == 0 {
		common.ApiError(c, errors.New("invalid account pool id"))
		return 0, false
	}
	return id, true
}

func accountPoolBindingIDFromParam(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("binding_id"))
	if err != nil || id == 0 {
		common.ApiError(c, errors.New("invalid account pool binding id"))
		return 0, false
	}
	return id, true
}

func accountPoolAccountIDFromParam(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("account_id"))
	if err != nil || id == 0 {
		common.ApiError(c, errors.New("invalid account pool account id"))
		return 0, false
	}
	return id, true
}

func accountPoolProxyIDFromParam(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("proxy_id"))
	if err != nil || id == 0 {
		common.ApiError(c, errors.New("invalid account pool proxy id"))
		return 0, false
	}
	return id, true
}

func accountPoolCreateParams(req dto.AccountPoolCreateRequest) service.AccountPoolCreateParams {
	return service.AccountPoolCreateParams{
		Name:                  req.Name,
		Platform:              req.Platform,
		DefaultProxyID:        req.DefaultProxyID,
		DefaultMonitorEnabled: req.DefaultMonitorEnabled,
		DefaultSchedulePolicy: req.DefaultSchedulePolicy,
		Remark:                req.Remark,
	}
}

func accountPoolAccountCreateParams(poolID int, req dto.AccountPoolAccountCreateRequest) service.AccountPoolAccountCreateParams {
	maxConcurrency, maxConcurrencySet := accountPoolMaxConcurrencyRequestValue(req.MaxConcurrency)
	return service.AccountPoolAccountCreateParams{
		PoolID:            poolID,
		Name:              req.Name,
		AccountIdentifier: req.AccountIdentifier,
		Credential: service.AccountPoolCredentialConfig{
			Type:         req.Credential.Type,
			APIKey:       req.Credential.APIKey,
			Email:        req.Credential.Email,
			RefreshToken: req.Credential.RefreshToken,
		},
		TokenState: service.AccountPoolTokenState{
			AccessToken:  req.TokenState.AccessToken,
			RefreshToken: req.TokenState.RefreshToken,
			ExpiresAt:    req.TokenState.ExpiresAt,
			Version:      req.TokenState.Version,
		},
		Status:             req.Status,
		Priority:           req.Priority,
		Weight:             req.Weight,
		MaxConcurrency:     maxConcurrency,
		MaxConcurrencySet:  maxConcurrencySet,
		ProxyID:            req.ProxyID,
		SupportedModels:    req.SupportedModels,
		ModelMapping:       req.ModelMapping,
		LastUsedAt:         req.LastUsedAt,
		RateLimitedUntil:   req.RateLimitedUntil,
		TempDisabledUntil:  req.TempDisabledUntil,
		TempDisabledReason: req.TempDisabledReason,
		LastError:          req.LastError,
	}
}

func accountPoolAccountImportParams(poolID int, req dto.AccountPoolAccountImportRequest) service.AccountPoolAccountImportParams {
	maxConcurrency, maxConcurrencySet := accountPoolMaxConcurrencyRequestValue(req.Defaults.MaxConcurrency)
	return service.AccountPoolAccountImportParams{
		PoolID:  poolID,
		Format:  req.Format,
		Content: req.Content,
		Defaults: service.AccountPoolAccountImportDefaults{
			Status:            req.Defaults.Status,
			Priority:          req.Defaults.Priority,
			Weight:            req.Defaults.Weight,
			MaxConcurrency:    maxConcurrency,
			MaxConcurrencySet: maxConcurrencySet,
			ProxyID:           req.Defaults.ProxyID,
			SupportedModels:   req.Defaults.SupportedModels,
			ModelMapping:      req.Defaults.ModelMapping,
		},
		DryRun: req.DryRun,
	}
}

func accountPoolMaxConcurrencyRequestValue(value *int) (int, bool) {
	if value == nil {
		return 0, false
	}
	return *value, true
}

func accountPoolBindingCreateParams(poolID int, req dto.AccountPoolBindingCreateRequest) service.AccountPoolBindingCreateParams {
	return service.AccountPoolBindingCreateParams{
		PoolID:    poolID,
		ChannelID: req.ChannelID,
		AccountFilterConfig: service.AccountPoolAccountFilterConfig{
			AccountIDs: req.AccountIDs,
		},
		ModelPolicy: service.AccountPoolModelPolicy{
			Strategy:    req.ModelStrategy,
			FixedModels: req.FixedModels,
		},
		SchedulePolicy:    req.SchedulePolicy,
		AccountRetryTimes: req.AccountRetryTimes,
	}
}

func accountPoolBoundChannelCreateParams(poolID int, req dto.AccountPoolBoundChannelCreateRequest) service.AccountPoolBoundChannelCreateParams {
	return service.AccountPoolBoundChannelCreateParams{
		PoolID:      poolID,
		Name:        req.Name,
		ChannelType: req.ChannelType,
		AccountFilterConfig: service.AccountPoolAccountFilterConfig{
			AccountIDs: req.AccountIDs,
		},
		ModelPolicy: service.AccountPoolModelPolicy{
			Strategy:    req.ModelStrategy,
			FixedModels: req.FixedModels,
		},
		SchedulePolicy:    req.SchedulePolicy,
		AccountRetryTimes: req.AccountRetryTimes,
	}
}

func accountPoolProxyCreateParams(req dto.AccountPoolProxyCreateRequest) service.AccountPoolProxyCreateParams {
	return service.AccountPoolProxyCreateParams{
		Name:            req.Name,
		Protocol:        req.Protocol,
		Host:            req.Host,
		Port:            req.Port,
		Username:        req.Username,
		Password:        req.Password,
		Status:          req.Status,
		FallbackProxyID: req.FallbackProxyID,
	}
}

func accountPoolResponse(pool model.AccountPool) dto.AccountPoolResponse {
	return dto.AccountPoolResponse{
		Id:                    pool.Id,
		Name:                  pool.Name,
		Platform:              pool.Platform,
		Status:                pool.Status,
		DefaultProxyID:        pool.DefaultProxyID,
		DefaultMonitorEnabled: pool.DefaultMonitorEnabled,
		DefaultSchedulePolicy: pool.DefaultSchedulePolicy,
		Remark:                pool.Remark,
		CreatedTime:           pool.CreatedTime,
		UpdatedTime:           pool.UpdatedTime,
	}
}

func accountPoolAccountImportResponse(result service.AccountPoolAccountImportResult) dto.AccountPoolAccountImportResponse {
	accounts := make([]dto.AccountPoolAccountResponse, 0, len(result.Accounts))
	for _, account := range result.Accounts {
		accounts = append(accounts, accountPoolAccountResponse(account))
	}
	errors := make([]dto.AccountPoolAccountImportError, 0, len(result.Errors))
	for _, item := range result.Errors {
		errors = append(errors, dto.AccountPoolAccountImportError{
			Index:   item.Index,
			Name:    item.Name,
			Message: item.Message,
		})
	}
	return dto.AccountPoolAccountImportResponse{
		Imported:     result.Imported,
		Skipped:      result.Skipped,
		Failed:       result.Failed,
		ProxyCreated: result.ProxyCreated,
		ProxyReused:  result.ProxyReused,
		Accounts:     accounts,
		Errors:       errors,
	}
}

func accountPoolAccountResponse(account service.AccountPoolAccountView) dto.AccountPoolAccountResponse {
	return dto.AccountPoolAccountResponse{
		Id:                        account.Id,
		PoolID:                    account.PoolID,
		Name:                      account.Name,
		AccountIdentifier:         account.AccountIdentifier,
		Status:                    account.Status,
		Priority:                  account.Priority,
		Weight:                    account.Weight,
		MaxConcurrency:            account.MaxConcurrency,
		ProxyID:                   account.ProxyID,
		SupportedModels:           account.SupportedModels,
		ModelMapping:              account.ModelMapping,
		LastUsedAt:                account.LastUsedAt,
		RateLimitedUntil:          account.RateLimitedUntil,
		TempDisabledUntil:         account.TempDisabledUntil,
		TempDisabledReason:        account.TempDisabledReason,
		LastError:                 account.LastError,
		LastCapabilityCheckAt:     account.LastCapabilityCheckAt,
		LastCapabilityCheckStatus: account.LastCapabilityCheckStatus,
		LastCapabilityCheckError:  account.LastCapabilityCheckError,
		LastCapabilityCheckModels: account.LastCapabilityCheckModels,
		HasCredential:             account.HasCredential,
		HasToken:                  account.HasToken,
		CreatedTime:               account.CreatedTime,
		UpdatedTime:               account.UpdatedTime,
	}
}

func accountPoolBindingResponse(binding service.AccountPoolBindingView) dto.AccountPoolBindingResponse {
	return dto.AccountPoolBindingResponse{
		Id:                  binding.Id,
		PoolID:              binding.PoolID,
		ChannelID:           binding.ChannelID,
		ChannelName:         binding.ChannelName,
		ChannelStatus:       binding.ChannelStatus,
		AccountFilterConfig: binding.AccountFilterConfig,
		ModelPolicy:         binding.ModelPolicy,
		SchedulePolicy:      binding.SchedulePolicy,
		AccountRetryTimes:   binding.AccountRetryTimes,
		Status:              binding.Status,
		RuntimeEnabled:      binding.RuntimeEnabled,
		CreatedTime:         binding.CreatedTime,
		UpdatedTime:         binding.UpdatedTime,
	}
}

func accountPoolProxyResponse(proxy service.AccountPoolProxyView) dto.AccountPoolProxyResponse {
	return dto.AccountPoolProxyResponse{
		Id:              proxy.Id,
		Name:            proxy.Name,
		Protocol:        proxy.Protocol,
		Host:            proxy.Host,
		Port:            proxy.Port,
		Username:        proxy.Username,
		Status:          proxy.Status,
		FallbackProxyID: proxy.FallbackProxyID,
		HasPassword:     proxy.HasPassword,
		CreatedTime:     proxy.CreatedTime,
		UpdatedTime:     proxy.UpdatedTime,
	}
}
