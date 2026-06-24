package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

const (
	AccountPoolCapabilityModeAuto           = "auto"
	AccountPoolCapabilityModeModelsEndpoint = "models_endpoint"
	AccountPoolCapabilityModeProbeModels    = "probe_models"

	AccountPoolCapabilityStatusSuccess       = "success"
	AccountPoolCapabilityStatusPartial       = "partial"
	AccountPoolCapabilityStatusUnsupported   = "unsupported"
	AccountPoolCapabilityStatusAuthError     = "auth_error"
	AccountPoolCapabilityStatusNetworkError  = "network_error"
	AccountPoolCapabilityStatusUpstreamError = "upstream_error"
	AccountPoolCapabilityStatusConfigError   = "config_error"
)

const accountPoolCapabilityDefaultTimeout = 30 * time.Second

type AccountPoolCapabilityDetectRequest struct {
	PoolID          int
	AccountID       int
	AccountIDs      []int
	ChannelID       int
	Mode            string
	CandidateModels []string
	Apply           bool
	Merge           bool
	ModelMapping    map[string]string
	TimeoutSeconds  int
}

type AccountPoolCapabilityDetectResult struct {
	AccountID      int
	Status         string
	Mode           string
	DetectedModels []string
	AppliedModels  []string
	ModelMapping   map[string]string
	Errors         []string
}

type AccountPoolCapabilityPoolResult struct {
	Total     int
	Succeeded int
	Failed    int
	Results   []AccountPoolCapabilityDetectResult
}

type accountPoolCapabilityModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func (s AccountPoolService) DetectAccountCapability(ctx context.Context, req AccountPoolCapabilityDetectRequest) (AccountPoolCapabilityDetectResult, error) {
	result := AccountPoolCapabilityDetectResult{
		AccountID:    req.AccountID,
		Mode:         normalizeAccountPoolCapabilityMode(req.Mode),
		ModelMapping: req.ModelMapping,
	}

	if req.PoolID <= 0 {
		return accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusConfigError, "account pool id is required"), nil
	}
	if req.AccountID <= 0 {
		return accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusConfigError, "account pool account id is required"), nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	pool, err := getAccountPoolExistingPool(req.PoolID)
	if err != nil {
		return accountPoolCapabilityHandleLookupError(result, "account pool", err)
	}
	account, err := getAccountPoolAccountForPool(req.PoolID, req.AccountID)
	if err != nil {
		return accountPoolCapabilityHandleLookupError(result, "account pool account", err)
	}

	channel, detectionResult, err := resolveAccountPoolCapabilityChannel(req.PoolID, req.ChannelID, result)
	if err != nil {
		return result, err
	}
	if detectionResult != nil {
		detectionResult.AccountID = account.Id
		return *detectionResult, nil
	}

	if err := validateAccountPoolRuntimeChannel(channel); err != nil {
		result = accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusConfigError, err.Error())
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(account.Id, result); writeErr != nil {
				return result, writeErr
			}
		}
		return result, nil
	}

	baseURL := strings.TrimSpace(channel.GetBaseURL())
	if baseURL == "" {
		baseURL = constant.ChannelBaseURLs[channel.Type]
	}
	if strings.TrimSpace(baseURL) == "" {
		result = accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusConfigError, "channel base url is required")
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(account.Id, result); writeErr != nil {
				return result, writeErr
			}
		}
		return result, nil
	}

	proxyURL, err := ResolveAccountPoolRuntimeProxyURL(account.ProxyID, pool.DefaultProxyID)
	if err != nil {
		result = accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusConfigError, err.Error())
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(account.Id, result); writeErr != nil {
				return result, writeErr
			}
		}
		return result, nil
	}
	if strings.TrimSpace(proxyURL) == "" {
		proxyURL = channel.GetSetting().Proxy
	}

	credential, err := DecryptAccountPoolCredentialConfig(account.CredentialConfig)
	if err != nil {
		return result, err
	}
	tokenState, err := DecryptAccountPoolTokenState(account.TokenState)
	if err != nil {
		return result, err
	}
	runtimeCredential, err := ResolveAccountPoolRuntimeCredential(ctx, AccountPoolRuntimeCredentialRequest{
		AccountID:         account.Id,
		Credential:        credential,
		TokenState:        tokenState,
		ProxyURL:          proxyURL,
		SkipFailureRecord: true,
	})
	if err != nil {
		result = accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusConfigError, err.Error())
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(account.Id, result); writeErr != nil {
				return result, writeErr
			}
		}
		return result, nil
	}
	if strings.TrimSpace(runtimeCredential) == "" {
		result = accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusConfigError, "account pool runtime credential is empty")
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(account.Id, result); writeErr != nil {
				return result, writeErr
			}
		}
		return result, nil
	}

	switch result.Mode {
	case AccountPoolCapabilityModeProbeModels:
		result = accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusUnsupported, "probe_models is not implemented in this phase")
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(account.Id, result); writeErr != nil {
				return result, writeErr
			}
		}
		return result, nil
	case AccountPoolCapabilityModeModelsEndpoint:
	default:
		result = accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusConfigError, "unsupported capability detection mode")
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(account.Id, result); writeErr != nil {
				return result, writeErr
			}
		}
		return result, nil
	}

	options, err := FetchChannelUpstreamModelIDsOptionsForGeneratedSource(channel.GetOtherSettings())
	if err != nil {
		return result, err
	}
	headers, err := BuildFetchModelsHeaders(&channel, runtimeCredential)
	if err != nil {
		result = accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusConfigError, err.Error())
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(account.Id, result); writeErr != nil {
				return result, writeErr
			}
		}
		return result, nil
	}
	body, statusCode, err := fetchAccountPoolCapabilityResponseBody(
		ctx,
		http.MethodGet,
		buildFetchModelsURL(channel.Type, baseURL),
		proxyURL,
		headers,
		options,
		normalizeAccountPoolCapabilityTimeout(req.TimeoutSeconds),
	)
	if err != nil {
		status := AccountPoolCapabilityStatusConfigError
		if isAccountPoolCapabilityNetworkError(err) {
			status = AccountPoolCapabilityStatusNetworkError
		}
		result = accountPoolCapabilityFailResult(result, status, err.Error())
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(account.Id, result); writeErr != nil {
				return result, writeErr
			}
		}
		return result, nil
	}

	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		result = accountPoolCapabilityFailResult(
			result,
			classifyAccountPoolCapabilityHTTPStatus(statusCode),
			accountPoolCapabilityHTTPErrorMessage(statusCode, body),
		)
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(account.Id, result); writeErr != nil {
				return result, writeErr
			}
		}
		return result, nil
	}

	var payload accountPoolCapabilityModelsResponse
	if err := common.Unmarshal(body, &payload); err != nil {
		result = accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusUpstreamError, fmt.Sprintf("decode upstream models response: %v", err))
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(account.Id, result); writeErr != nil {
				return result, writeErr
			}
		}
		return result, nil
	}

	detectedModels := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		detectedModels = append(detectedModels, item.ID)
	}
	detectedModels = normalizeAccountPoolCapabilityModels(detectedModels)
	if len(req.CandidateModels) > 0 {
		detectedModels = intersectAccountPoolCapabilityModels(detectedModels, req.CandidateModels)
	}

	result.Status = AccountPoolCapabilityStatusSuccess
	result.DetectedModels = detectedModels

	if !req.Apply {
		return result, nil
	}

	appliedModels, err := accountPoolCapabilityAppliedModels(account, detectedModels, req.Merge)
	if err != nil {
		return result, err
	}
	result.AppliedModels = appliedModels

	if err := persistAccountPoolCapabilitySuccess(account.Id, appliedModels, result.DetectedModels, req.ModelMapping, result.Status); err != nil {
		return result, err
	}
	return result, nil
}

func (s AccountPoolService) DetectPoolCapabilities(ctx context.Context, req AccountPoolCapabilityDetectRequest) (AccountPoolCapabilityPoolResult, error) {
	result := AccountPoolCapabilityPoolResult{}
	if req.PoolID <= 0 {
		return result, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	accountIDs := normalizeAccountPoolCapabilityAccountIDs(req.AccountIDs)
	if len(accountIDs) == 0 {
		var accounts []model.AccountPoolAccount
		if err := model.DB.
			Where("pool_id = ? AND status <> ?", req.PoolID, model.AccountPoolAccountStatusDeleted).
			Order("id asc").
			Find(&accounts).Error; err != nil {
			return result, err
		}
		accountIDs = make([]int, 0, len(accounts))
		for _, account := range accounts {
			accountIDs = append(accountIDs, account.Id)
		}
	}

	result.Total = len(accountIDs)
	result.Results = make([]AccountPoolCapabilityDetectResult, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		accountReq := req
		accountReq.AccountID = accountID
		accountReq.AccountIDs = nil

		detectResult, err := s.DetectAccountCapability(ctx, accountReq)
		if err != nil {
			return result, err
		}
		result.Results = append(result.Results, detectResult)
		if accountPoolCapabilitySucceeded(detectResult.Status) {
			result.Succeeded++
		} else {
			result.Failed++
		}
	}
	return result, nil
}

func normalizeAccountPoolCapabilityMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "", AccountPoolCapabilityModeAuto:
		return AccountPoolCapabilityModeModelsEndpoint
	case AccountPoolCapabilityModeModelsEndpoint:
		return AccountPoolCapabilityModeModelsEndpoint
	case AccountPoolCapabilityModeProbeModels:
		return AccountPoolCapabilityModeProbeModels
	default:
		return strings.TrimSpace(mode)
	}
}

func normalizeAccountPoolCapabilityTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return accountPoolCapabilityDefaultTimeout
	}
	return time.Duration(seconds) * time.Second
}

func normalizeAccountPoolCapabilityModels(models []string) []string {
	return normalizeFetchedModelNames(models)
}

func intersectAccountPoolCapabilityModels(detected []string, candidates []string) []string {
	normalizedCandidates := normalizeAccountPoolCapabilityModels(candidates)
	if len(normalizedCandidates) == 0 {
		return []string{}
	}
	detectedSet := make(map[string]struct{}, len(detected))
	for _, modelName := range detected {
		detectedSet[modelName] = struct{}{}
	}
	filtered := make([]string, 0, len(normalizedCandidates))
	for _, candidate := range normalizedCandidates {
		if _, ok := detectedSet[candidate]; ok {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func mergeAccountPoolCapabilityModels(existing []string, detected []string) []string {
	merged := make([]string, 0, len(existing)+len(detected))
	seen := make(map[string]struct{}, len(existing)+len(detected))
	for _, modelName := range normalizeAccountPoolCapabilityModels(existing) {
		if _, ok := seen[modelName]; ok {
			continue
		}
		seen[modelName] = struct{}{}
		merged = append(merged, modelName)
	}
	for _, modelName := range normalizeAccountPoolCapabilityModels(detected) {
		if _, ok := seen[modelName]; ok {
			continue
		}
		seen[modelName] = struct{}{}
		merged = append(merged, modelName)
	}
	return merged
}

func classifyAccountPoolCapabilityHTTPStatus(status int) string {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return AccountPoolCapabilityStatusAuthError
	case http.StatusNotFound:
		return AccountPoolCapabilityStatusUnsupported
	default:
		return AccountPoolCapabilityStatusUpstreamError
	}
}

func fetchAccountPoolCapabilityResponseBody(
	ctx context.Context,
	method string,
	requestURL string,
	proxyURL string,
	headers http.Header,
	options FetchChannelUpstreamModelIDsOptions,
	timeout time.Duration,
) ([]byte, int, error) {
	if err := validateFetchModelsURL(requestURL, options); err != nil {
		return nil, 0, err
	}

	client, err := NewProxyHttpClient(proxyURL)
	if err != nil {
		return nil, 0, err
	}
	client = fetchModelsHTTPClientWithOptions(client, options)

	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = accountPoolCapabilityDefaultTimeout
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, method, requestURL, nil)
	if err != nil {
		return nil, 0, err
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, fetchModelsResponseBodyLimitBytes+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(body)) > fetchModelsResponseBodyLimitBytes {
		return nil, 0, fmt.Errorf("response body too large: limit %d bytes", fetchModelsResponseBodyLimitBytes)
	}
	return body, resp.StatusCode, nil
}

func resolveAccountPoolCapabilityChannel(poolID int, channelID int, result AccountPoolCapabilityDetectResult) (model.Channel, *AccountPoolCapabilityDetectResult, error) {
	var binding model.AccountPoolChannelBinding
	if channelID > 0 {
		err := model.DB.Where("pool_id = ? AND channel_id = ?", poolID, channelID).First(&binding).Error
		if err != nil {
			resolvedResult, resolveErr := accountPoolCapabilityHandleLookupError(result, "account pool channel binding", err)
			return model.Channel{}, &resolvedResult, resolveErr
		}
	} else {
		var bindings []model.AccountPoolChannelBinding
		if err := model.DB.Where("pool_id = ?", poolID).Order("id asc").Find(&bindings).Error; err != nil {
			return model.Channel{}, nil, err
		}
		switch len(bindings) {
		case 0:
			resolved := accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusConfigError, "account pool has no channel binding")
			return model.Channel{}, &resolved, nil
		case 1:
			binding = bindings[0]
		default:
			resolved := accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusConfigError, "account pool channel selection is ambiguous")
			return model.Channel{}, &resolved, nil
		}
	}

	var channel model.Channel
	if err := model.DB.First(&channel, binding.ChannelID).Error; err != nil {
		resolvedResult, resolveErr := accountPoolCapabilityHandleLookupError(result, "channel", err)
		return model.Channel{}, &resolvedResult, resolveErr
	}
	return channel, nil, nil
}

func accountPoolCapabilityHandleLookupError(result AccountPoolCapabilityDetectResult, subject string, err error) (AccountPoolCapabilityDetectResult, error) {
	if err == nil {
		return result, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusConfigError, fmt.Sprintf("%s not found", subject)), nil
	}
	return result, err
}

func accountPoolCapabilityFailResult(result AccountPoolCapabilityDetectResult, status string, message string) AccountPoolCapabilityDetectResult {
	sanitized := sanitizeAccountPoolCapabilityError(message)
	result.Status = status
	result.Errors = []string{sanitized}
	result.DetectedModels = []string{}
	result.AppliedModels = nil
	return result
}

func accountPoolCapabilityHTTPErrorMessage(statusCode int, body []byte) string {
	bodyText := strings.TrimSpace(string(body))
	if bodyText == "" {
		return fmt.Sprintf("status code: %d", statusCode)
	}
	return fmt.Sprintf("status code: %d, body: %s", statusCode, bodyText)
}

func sanitizeAccountPoolCapabilityError(message string) string {
	return sanitizeAccountPoolRuntimeErrorMessage(message, accountPoolLastErrorMaxLength)
}

func persistAccountPoolCapabilitySuccess(accountID int, supportedModels []string, detectedModels []string, modelMapping map[string]string, status string) error {
	supportedModelsJSON, err := common.Marshal(supportedModels)
	if err != nil {
		return err
	}
	detectedModelsJSON, err := common.Marshal(detectedModels)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"supported_models":             string(supportedModelsJSON),
		"last_capability_check_at":     common.GetTimestamp(),
		"last_capability_check_status": status,
		"last_capability_check_error":  "",
		"last_capability_check_models": string(detectedModelsJSON),
	}
	if modelMapping != nil {
		modelMappingJSON, err := common.Marshal(modelMapping)
		if err != nil {
			return err
		}
		updates["model_mapping"] = string(modelMappingJSON)
	}
	return model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", accountID).
		Updates(updates).Error
}

func persistAccountPoolCapabilityFailure(accountID int, result AccountPoolCapabilityDetectResult) error {
	errorText := ""
	if len(result.Errors) > 0 {
		errorText = sanitizeAccountPoolCapabilityError(result.Errors[0])
	}
	detectedModelsJSON, err := common.Marshal([]string{})
	if err != nil {
		return err
	}
	updates := map[string]any{
		"last_capability_check_at":     common.GetTimestamp(),
		"last_capability_check_status": result.Status,
		"last_capability_check_error":  errorText,
		"last_capability_check_models": string(detectedModelsJSON),
		"last_error":                   errorText,
	}
	return model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", accountID).
		Updates(updates).Error
}

func accountPoolCapabilityAppliedModels(account model.AccountPoolAccount, detectedModels []string, merge bool) ([]string, error) {
	var existing []string
	if strings.TrimSpace(account.SupportedModels) != "" {
		if err := common.UnmarshalJsonStr(account.SupportedModels, &existing); err != nil {
			return nil, err
		}
	}
	if merge {
		return mergeAccountPoolCapabilityModels(existing, detectedModels), nil
	}
	return normalizeAccountPoolCapabilityModels(detectedModels), nil
}

func accountPoolCapabilitySucceeded(status string) bool {
	return status == AccountPoolCapabilityStatusSuccess || status == AccountPoolCapabilityStatusPartial
}

func normalizeAccountPoolCapabilityAccountIDs(accountIDs []int) []int {
	if len(accountIDs) == 0 {
		return nil
	}
	normalized := make([]int, 0, len(accountIDs))
	seen := make(map[int]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID <= 0 {
			continue
		}
		if _, ok := seen[accountID]; ok {
			continue
		}
		seen[accountID] = struct{}{}
		normalized = append(normalized, accountID)
	}
	return normalized
}

func isAccountPoolCapabilityNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}
