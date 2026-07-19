package controller

import (
	"context"
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

var (
	accountPoolXAIOAuthSessions = service.NewXAIOAuthSessionStore()
	accountPoolXAIOAuthExchange = func(ctx context.Context, sessionID string, code string, state string) (*service.XAIOAuthTokenInfo, error) {
		return service.ExchangeXAIOAuthCode(ctx, accountPoolXAIOAuthSessions, sessionID, code, state)
	}
	accountPoolXAIOAuthRefreshAccount = func(ctx context.Context, poolID int, accountID int) (service.AccountPoolAccountView, error) {
		return (&service.AccountPoolService{}).RefreshXAIOAuthAccount(ctx, poolID, accountID)
	}
	accountPoolXAISSOImport = func(ctx context.Context, params service.AccountPoolXAISSOImportParams) (service.AccountPoolXAISSOImportResult, error) {
		return (&service.AccountPoolService{}).ImportXAISSOAccounts(ctx, params)
	}
	accountPoolXAIQuotaProbe = func(ctx context.Context, poolID int, accountID int) (service.AccountPoolXAIQuotaSnapshot, error) {
		return (&service.AccountPoolService{}).ProbeXAIQuota(ctx, poolID, accountID)
	}
)

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

// ExportAccountPoolAccounts serializes a pool's accounts (and the proxies they
// reference) into the same sub2api-data shape the importer consumes, so a full
// export round-trips through import. Secrets are REDACTED unless include_secrets=true
// is passed (admin-only route; the request is audit-logged, never the secrets).
func ExportAccountPoolAccounts(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	includeSecretsRaw := c.Query("include_secrets")
	includeSecrets := includeSecretsRaw == "true" || includeSecretsRaw == "1"
	payload, skipped, err := (&service.AccountPoolService{}).ExportAccounts(poolID, includeSecrets)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.account_export", map[string]interface{}{
		"pool_id":         poolID,
		"include_secrets": includeSecrets,
		"accounts":        len(payload.Accounts),
		"skipped":         skipped,
	})
	common.ApiSuccess(c, payload)
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

func DetectAccountPoolAccountCapability(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	accountID, ok := accountPoolAccountIDFromParam(c)
	if !ok {
		return
	}
	var req dto.AccountPoolCapabilityDetectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	result, err := (&service.AccountPoolService{}).DetectAccountCapability(c.Request.Context(), service.AccountPoolCapabilityDetectRequest{
		PoolID:          poolID,
		AccountID:       accountID,
		ChannelID:       req.ChannelID,
		Mode:            req.Mode,
		CandidateModels: req.CandidateModels,
		Apply:           req.Apply,
		Merge:           req.Merge,
		ModelMapping:    req.ModelMapping,
		TimeoutSeconds:  req.TimeoutSeconds,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.capability_detect", map[string]interface{}{
		"pool_id":    poolID,
		"account_id": accountID,
		"mode":       req.Mode,
		"apply":      req.Apply,
	})
	common.ApiSuccess(c, accountPoolCapabilityDetectResponse(result))
}

func DetectAccountPoolCapabilities(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	var req dto.AccountPoolCapabilityDetectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	result, err := (&service.AccountPoolService{}).DetectPoolCapabilities(c.Request.Context(), service.AccountPoolCapabilityDetectRequest{
		PoolID:          poolID,
		AccountIDs:      req.AccountIDs,
		ChannelID:       req.ChannelID,
		Mode:            req.Mode,
		CandidateModels: req.CandidateModels,
		Apply:           req.Apply,
		Merge:           req.Merge,
		ModelMapping:    req.ModelMapping,
		TimeoutSeconds:  req.TimeoutSeconds,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.capability_detect", map[string]interface{}{
		"pool_id":        poolID,
		"mode":           req.Mode,
		"apply":          req.Apply,
		"account_count":  len(req.AccountIDs),
		"result_total":   result.Total,
		"result_failed":  result.Failed,
		"result_success": result.Succeeded,
	})
	common.ApiSuccess(c, accountPoolCapabilityPoolResponse(result))
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

func GenerateAccountPoolXAIOAuthAuthorization(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	pool, err := accountPoolXAIPool(poolID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var req dto.AccountPoolXAIOAuthAuthorizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	proxyURL, err := service.ResolveAccountPoolRuntimeProxyURL(req.ProxyID, pool.DefaultProxyID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	result, err := service.GenerateXAIOAuthAuthorization(accountPoolXAIOAuthSessions, proxyURL, req.RedirectURI)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.xai_oauth_authorize", map[string]interface{}{
		"pool_id":  poolID,
		"proxy_id": req.ProxyID,
	})
	common.ApiSuccess(c, dto.AccountPoolXAIOAuthAuthorizationResponse{
		AuthURL:   result.AuthURL,
		SessionID: result.SessionID,
		State:     result.State,
	})
}

func ExchangeAccountPoolXAIOAuthCode(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	if _, err := accountPoolXAIPool(poolID); err != nil {
		common.ApiError(c, err)
		return
	}
	var req dto.AccountPoolXAIOAuthExchangeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	info, err := accountPoolXAIOAuthExchange(c.Request.Context(), req.SessionID, req.Code, req.State)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.xai_oauth_exchange", map[string]interface{}{
		"pool_id": poolID,
	})
	common.ApiSuccess(c, accountPoolXAIOAuthTokenResponse(*info))
}

func RefreshAccountPoolXAIOAuthAccount(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	if _, err := accountPoolXAIPool(poolID); err != nil {
		common.ApiError(c, err)
		return
	}
	accountID, ok := accountPoolAccountIDFromParam(c)
	if !ok {
		return
	}
	account, err := accountPoolXAIOAuthRefreshAccount(c.Request.Context(), poolID, accountID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.xai_oauth_refresh", map[string]interface{}{
		"pool_id":    poolID,
		"account_id": accountID,
	})
	common.ApiSuccess(c, accountPoolAccountResponse(account))
}

func ProbeAccountPoolXAIQuota(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	if _, err := accountPoolXAIPool(poolID); err != nil {
		common.ApiError(c, err)
		return
	}
	accountID, ok := accountPoolAccountIDFromParam(c)
	if !ok {
		return
	}
	result, err := accountPoolXAIQuotaProbe(c.Request.Context(), poolID, accountID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.xai_quota_probe", map[string]interface{}{
		"pool_id":     poolID,
		"account_id":  accountID,
		"status_code": result.StatusCode,
	})
	common.ApiSuccess(c, result)
}

func GetAccountPoolXAIQuota(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	if _, err := accountPoolXAIPool(poolID); err != nil {
		common.ApiError(c, err)
		return
	}
	accountID, ok := accountPoolAccountIDFromParam(c)
	if !ok {
		return
	}
	result, err := (&service.AccountPoolService{}).GetXAIQuotaSnapshot(poolID, accountID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func ImportAccountPoolXAISSOAccounts(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	if _, err := accountPoolXAIPool(poolID); err != nil {
		common.ApiError(c, err)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAccountPoolImportRequestBodyBytes)
	var req dto.AccountPoolXAISSOImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	maxConcurrency, maxConcurrencySet := accountPoolMaxConcurrencyRequestValue(req.MaxConcurrency)
	result, err := accountPoolXAISSOImport(c.Request.Context(), service.AccountPoolXAISSOImportParams{
		PoolID:            poolID,
		SSOTokens:         req.SSOTokens,
		Name:              req.Name,
		Status:            req.Status,
		Priority:          req.Priority,
		Weight:            req.Weight,
		MaxConcurrency:    maxConcurrency,
		MaxConcurrencySet: maxConcurrencySet,
		ProxyID:           req.ProxyID,
		SupportedModels:   req.SupportedModels,
		ModelMapping:      req.ModelMapping,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	created := make([]dto.AccountPoolAccountResponse, 0, len(result.Created))
	for _, account := range result.Created {
		created = append(created, accountPoolAccountResponse(account))
	}
	importErrors := make([]dto.AccountPoolAccountImportError, 0, len(result.Errors))
	for _, item := range result.Errors {
		importErrors = append(importErrors, dto.AccountPoolAccountImportError{
			Index:   item.Index,
			Name:    item.Name,
			Message: item.Message,
		})
	}
	recordManageAudit(c, "account_pool.xai_sso_import", map[string]interface{}{
		"pool_id": poolID,
		"created": len(created),
		"failed":  len(importErrors),
	})
	common.ApiSuccess(c, dto.AccountPoolXAISSOImportResponse{
		Created: created,
		Errors:  importErrors,
	})
}

func accountPoolXAIPool(poolID int) (model.AccountPool, error) {
	pool, err := (&service.AccountPoolService{}).GetPool(poolID)
	if err != nil {
		return model.AccountPool{}, err
	}
	if pool.Platform != model.AccountPoolPlatformXAI {
		return model.AccountPool{}, errors.New("account pool is not an xai pool")
	}
	return pool, nil
}

func accountPoolXAIOAuthTokenResponse(info service.XAIOAuthTokenInfo) dto.AccountPoolXAIOAuthTokenResponse {
	credential := info.AccountPoolCredential()
	tokenState := info.AccountPoolTokenState()
	return dto.AccountPoolXAIOAuthTokenResponse{
		Email:             info.Email,
		Subject:           info.Subject,
		TeamID:            info.TeamID,
		SubscriptionTier:  info.SubscriptionTier,
		EntitlementStatus: info.EntitlementStatus,
		ExpiresAt:         info.ExpiresAt,
		Credential: dto.AccountPoolCredentialConfigRequest{
			Type:              credential.Type,
			Email:             credential.Email,
			RefreshToken:      credential.RefreshToken,
			IDToken:           credential.IDToken,
			ClientID:          credential.ClientID,
			Scope:             credential.Scope,
			TokenType:         credential.TokenType,
			Subject:           credential.Subject,
			TeamID:            credential.TeamID,
			SubscriptionTier:  credential.SubscriptionTier,
			EntitlementStatus: credential.EntitlementStatus,
		},
		TokenState: dto.AccountPoolTokenStateRequest{
			AccessToken:  tokenState.AccessToken,
			RefreshToken: tokenState.RefreshToken,
			ExpiresAt:    tokenState.ExpiresAt,
			Version:      tokenState.Version,
		},
	}
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
		Name:                           req.Name,
		Platform:                       req.Platform,
		DefaultProxyID:                 req.DefaultProxyID,
		DefaultMonitorEnabled:          req.DefaultMonitorEnabled,
		DefaultSchedulePolicy:          req.DefaultSchedulePolicy,
		CapabilityCheckEnabled:         req.CapabilityCheckEnabled,
		CapabilityCheckIntervalMinutes: req.CapabilityCheckIntervalMinutes,
		CapabilityCheckMode:            req.CapabilityCheckMode,
		CapabilityCheckChannelID:       req.CapabilityCheckChannelID,
		CapabilityCheckModels:          req.CapabilityCheckModels,
		CapabilityCheckTimeoutSeconds:  req.CapabilityCheckTimeoutSeconds,
		CapabilityCheckMerge:           req.CapabilityCheckMerge,
		Remark:                         req.Remark,
	}
}

func accountPoolAccountCreateParams(poolID int, req dto.AccountPoolAccountCreateRequest) service.AccountPoolAccountCreateParams {
	maxConcurrency, maxConcurrencySet := accountPoolMaxConcurrencyRequestValue(req.MaxConcurrency)
	return service.AccountPoolAccountCreateParams{
		PoolID:            poolID,
		Name:              req.Name,
		AccountIdentifier: req.AccountIdentifier,
		OAuthType:         req.Credential.OAuthType,
		Credential: service.AccountPoolCredentialConfig{
			Type:               req.Credential.Type,
			APIKey:             req.Credential.APIKey,
			Email:              req.Credential.Email,
			RefreshToken:       req.Credential.RefreshToken,
			IDToken:            req.Credential.IDToken,
			ClientID:           req.Credential.ClientID,
			Scope:              req.Credential.Scope,
			TokenType:          req.Credential.TokenType,
			Subject:            req.Credential.Subject,
			TeamID:             req.Credential.TeamID,
			SubscriptionTier:   req.Credential.SubscriptionTier,
			EntitlementStatus:  req.Credential.EntitlementStatus,
			ServiceAccountJSON: req.Credential.ServiceAccountJSON,
			Location:           req.Credential.Location,
			CFClearance:        req.Credential.CFClearance,
		},
		TokenState: service.AccountPoolTokenState{
			AccessToken:  req.TokenState.AccessToken,
			RefreshToken: req.TokenState.RefreshToken,
			ExpiresAt:    req.TokenState.ExpiresAt,
			Version:      req.TokenState.Version,
		},
		Status:                    req.Status,
		Priority:                  req.Priority,
		Weight:                    req.Weight,
		MaxConcurrency:            maxConcurrency,
		MaxConcurrencySet:         maxConcurrencySet,
		ProxyID:                   req.ProxyID,
		SupportedModels:           req.SupportedModels,
		ModelMapping:              req.ModelMapping,
		LastUsedAt:                req.LastUsedAt,
		RateLimitedUntil:          req.RateLimitedUntil,
		TempDisabledUntil:         req.TempDisabledUntil,
		TempDisabledReason:        req.TempDisabledReason,
		LastError:                 req.LastError,
		ExpiresAt:                 req.ExpiresAt,
		AutoPauseOnExpired:        req.AutoPauseOnExpired,
		RequestQuota:              req.RequestQuota,
		RequestQuotaWindowSeconds: req.RequestQuotaWindowSeconds,
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
		SchedulePolicy:     req.SchedulePolicy,
		AccountRetryTimes:  req.AccountRetryTimes,
		MaxUserConcurrency: req.MaxUserConcurrency,
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
		SchedulePolicy:     req.SchedulePolicy,
		AccountRetryTimes:  req.AccountRetryTimes,
		MaxUserConcurrency: req.MaxUserConcurrency,
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
	capabilityCheckModels := []string{}
	if pool.CapabilityCheckModels != "" {
		if err := common.UnmarshalJsonStr(pool.CapabilityCheckModels, &capabilityCheckModels); err != nil {
			common.SysError("failed to unmarshal account pool capability check models: pool_id=" + strconv.Itoa(pool.Id) + ", error=" + err.Error())
			capabilityCheckModels = []string{}
		}
	}
	return dto.AccountPoolResponse{
		Id:                             pool.Id,
		Name:                           pool.Name,
		Platform:                       pool.Platform,
		Status:                         pool.Status,
		DefaultProxyID:                 pool.DefaultProxyID,
		DefaultMonitorEnabled:          pool.DefaultMonitorEnabled,
		DefaultSchedulePolicy:          pool.DefaultSchedulePolicy,
		CapabilityCheckEnabled:         pool.CapabilityCheckEnabled,
		CapabilityCheckIntervalMinutes: pool.CapabilityCheckIntervalMinutes,
		CapabilityCheckMode:            pool.CapabilityCheckMode,
		CapabilityCheckChannelID:       pool.CapabilityCheckChannelID,
		CapabilityCheckModels:          capabilityCheckModels,
		CapabilityCheckTimeoutSeconds:  pool.CapabilityCheckTimeoutSeconds,
		CapabilityCheckMerge:           pool.CapabilityCheckMerge,
		Remark:                         pool.Remark,
		CreatedTime:                    pool.CreatedTime,
		UpdatedTime:                    pool.UpdatedTime,
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
		CredentialType:            account.CredentialType,
		OAuthType:                 account.OAuthType,
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
		ExpiresAt:                 account.ExpiresAt,
		AutoPauseOnExpired:        account.AutoPauseOnExpired,
		LastCapabilityCheckAt:     account.LastCapabilityCheckAt,
		LastCapabilityCheckStatus: account.LastCapabilityCheckStatus,
		LastCapabilityCheckError:  account.LastCapabilityCheckError,
		LastCapabilityCheckModels: account.LastCapabilityCheckModels,
		HasCredential:             account.HasCredential,
		HasToken:                  account.HasToken,
		RequestQuota:              account.RequestQuota,
		RequestQuotaUsed:          account.RequestQuotaUsed,
		RequestQuotaWindowStart:   account.RequestQuotaWindowStart,
		RequestQuotaWindowSeconds: account.RequestQuotaWindowSeconds,
		XAIQuota:                  accountPoolXAIQuotaResponse(account.XAIQuota),
		CreatedTime:               account.CreatedTime,
		UpdatedTime:               account.UpdatedTime,
	}
}

func accountPoolXAIQuotaResponse(snapshot *service.AccountPoolXAIQuotaSnapshot) *dto.AccountPoolXAIQuotaSnapshot {
	if snapshot == nil {
		return nil
	}
	result := &dto.AccountPoolXAIQuotaSnapshot{
		Source:                 snapshot.Source,
		Model:                  snapshot.Model,
		RetryAfterSeconds:      snapshot.RetryAfterSeconds,
		StatusCode:             snapshot.StatusCode,
		HeadersObserved:        snapshot.HeadersObserved,
		MediaEligible:          snapshot.MediaEligible,
		MediaEligibilityReason: snapshot.MediaEligibilityReason,
		FetchedAt:              snapshot.FetchedAt,
		ProbeError:             snapshot.ProbeError,
	}
	if snapshot.Billing != nil {
		result.Billing = &dto.AccountPoolXAIBillingSnapshot{
			UsagePercent:      snapshot.Billing.UsagePercent,
			MonthlyLimitCents: snapshot.Billing.MonthlyLimitCents,
			UsedCents:         snapshot.Billing.UsedCents,
			UsedPercent:       snapshot.Billing.UsedPercent,
			Plan:              snapshot.Billing.Plan,
			WeeklyStatusCode:  snapshot.Billing.WeeklyStatusCode,
			MonthlyStatusCode: snapshot.Billing.MonthlyStatusCode,
			Partial:           snapshot.Billing.Partial,
		}
	}
	if snapshot.Requests != nil {
		result.Requests = &dto.AccountPoolXAIQuotaWindow{
			Limit: snapshot.Requests.Limit, Remaining: snapshot.Requests.Remaining,
			ResetUnix: snapshot.Requests.ResetUnix, ResetAt: snapshot.Requests.ResetAt,
		}
	}
	if snapshot.Tokens != nil {
		result.Tokens = &dto.AccountPoolXAIQuotaWindow{
			Limit: snapshot.Tokens.Limit, Remaining: snapshot.Tokens.Remaining,
			ResetUnix: snapshot.Tokens.ResetUnix, ResetAt: snapshot.Tokens.ResetAt,
		}
	}
	return result
}

func accountPoolCapabilityDetectResponse(result service.AccountPoolCapabilityDetectResult) dto.AccountPoolCapabilityDetectResult {
	detectedModels := result.DetectedModels
	if detectedModels == nil {
		detectedModels = []string{}
	}
	appliedModels := result.AppliedModels
	if appliedModels == nil {
		appliedModels = []string{}
	}
	modelMapping := result.ModelMapping
	if modelMapping == nil {
		modelMapping = map[string]string{}
	}
	errors := result.Errors
	if errors == nil {
		errors = []string{}
	}
	return dto.AccountPoolCapabilityDetectResult{
		AccountID:      result.AccountID,
		Status:         result.Status,
		Mode:           result.Mode,
		DetectedModels: detectedModels,
		AppliedModels:  appliedModels,
		ModelMapping:   modelMapping,
		Errors:         errors,
	}
}

func accountPoolCapabilityPoolResponse(result service.AccountPoolCapabilityPoolResult) dto.AccountPoolCapabilityPoolResult {
	responses := make([]dto.AccountPoolCapabilityDetectResult, 0, len(result.Results))
	for _, item := range result.Results {
		responses = append(responses, accountPoolCapabilityDetectResponse(item))
	}
	return dto.AccountPoolCapabilityPoolResult{
		Total:     result.Total,
		Succeeded: result.Succeeded,
		Failed:    result.Failed,
		Results:   responses,
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
		MaxUserConcurrency:  binding.MaxUserConcurrency,
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
