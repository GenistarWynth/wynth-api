package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"

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
const accountPoolCapabilityMaxTimeout = 300 * time.Second
const accountPoolCapabilityRequestBodyLimitBytes = 4 << 10
const accountPoolCapabilityUnsupportedBodyInspectLimitBytes = 4 << 10

type AccountPoolCapabilityDetectRequest struct {
	PoolID          int               `json:"pool_id"`
	AccountID       int               `json:"account_id"`
	AccountIDs      []int             `json:"account_ids"`
	ChannelID       int               `json:"channel_id"`
	Mode            string            `json:"mode"`
	CandidateModels []string          `json:"candidate_models"`
	Apply           bool              `json:"apply"`
	Merge           bool              `json:"merge"`
	ModelMapping    map[string]string `json:"model_mapping"`
	TimeoutSeconds  int               `json:"timeout_seconds"`
}

type AccountPoolCapabilityDetectResult struct {
	AccountID      int               `json:"account_id"`
	Status         string            `json:"status"`
	Mode           string            `json:"mode"`
	DetectedModels []string          `json:"detected_models"`
	AppliedModels  []string          `json:"applied_models"`
	ModelMapping   map[string]string `json:"model_mapping"`
	Errors         []string          `json:"errors"`
}

type AccountPoolCapabilityPoolResult struct {
	Total     int                                 `json:"total"`
	Succeeded int                                 `json:"succeeded"`
	Failed    int                                 `json:"failed"`
	Results   []AccountPoolCapabilityDetectResult `json:"results"`
}

type accountPoolCapabilityModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

type accountPoolCapabilityProbeRequest struct {
	Model     string                                     `json:"model"`
	Messages  []accountPoolCapabilityProbeRequestMessage `json:"messages"`
	MaxTokens int                                        `json:"max_tokens"`
	Stream    bool                                       `json:"stream"`
}

type accountPoolCapabilityProbeRequestMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type accountPoolCapabilityErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Code    string `json:"code"`
		Type    string `json:"type"`
	} `json:"error"`
	Message string `json:"message"`
	Code    string `json:"code"`
	Type    string `json:"type"`
}

func (s AccountPoolService) DetectAccountCapability(ctx context.Context, req AccountPoolCapabilityDetectRequest) (AccountPoolCapabilityDetectResult, error) {
	result := newAccountPoolCapabilityDetectResult(req)
	cleanedModelMapping := accountPoolCleanStringMap(req.ModelMapping)

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
			if writeErr := persistAccountPoolCapabilityFailure(ctx, account.Id, result); writeErr != nil {
				return result, writeErr
			}
		}
		return result, nil
	}

	var probeCandidateModels []string
	if result.Mode == AccountPoolCapabilityModeProbeModels {
		probeCandidateModels = normalizeAccountPoolCapabilityModels(req.CandidateModels)
		if len(probeCandidateModels) == 0 {
			result = accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusConfigError, "probe_models requires candidate_models")
			if req.Apply {
				if writeErr := persistAccountPoolCapabilityFailure(ctx, account.Id, result); writeErr != nil {
					return result, writeErr
				}
			}
			return result, nil
		}
	}

	baseURL := strings.TrimSpace(channel.GetBaseURL())
	if baseURL == "" {
		baseURL = constant.ChannelBaseURLs[channel.Type]
	}
	if strings.TrimSpace(baseURL) == "" {
		result = accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusConfigError, "channel base url is required")
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(ctx, account.Id, result); writeErr != nil {
				return result, writeErr
			}
		}
		return result, nil
	}

	proxyURL, err := ResolveAccountPoolRuntimeProxyURL(account.ProxyID, pool.DefaultProxyID)
	if err != nil {
		result = accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusConfigError, err.Error())
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(ctx, account.Id, result); writeErr != nil {
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
	runtimeCredential, err := resolveAccountPoolCapabilityRuntimeCredential(ctx, credential, tokenState, proxyURL)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, ctxErr
		}
		result = accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusConfigError, err.Error())
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(ctx, account.Id, result); writeErr != nil {
				return result, writeErr
			}
		}
		return result, nil
	}
	if strings.TrimSpace(runtimeCredential) == "" {
		result = accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusConfigError, "account pool runtime credential is empty")
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(ctx, account.Id, result); writeErr != nil {
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
			if writeErr := persistAccountPoolCapabilityFailure(ctx, account.Id, result); writeErr != nil {
				return result, writeErr
			}
		}
		return result, nil
	}

	switch result.Mode {
	case AccountPoolCapabilityModeProbeModels:
		supportedCandidates := make([]string, 0, len(probeCandidateModels))
		probeErrors := make([]string, 0, len(probeCandidateModels))
		for _, candidateModel := range probeCandidateModels {
			requestBody, err := buildAccountPoolCapabilityProbeRequestBody(candidateModel)
			if err != nil {
				return result, err
			}
			body, statusCode, err := fetchAccountPoolCapabilityResponseBodyWithRequestBody(
				ctx,
				http.MethodPost,
				buildAccountPoolCapabilityProbeURL(baseURL),
				proxyURL,
				headers,
				options,
				normalizeAccountPoolCapabilityTimeout(req.TimeoutSeconds),
				requestBody,
			)
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return result, ctxErr
				}
				status := AccountPoolCapabilityStatusConfigError
				if isAccountPoolCapabilityNetworkError(err) {
					status = AccountPoolCapabilityStatusNetworkError
				}
				result = accountPoolCapabilityFailResult(result, status, err.Error())
				if req.Apply {
					if writeErr := persistAccountPoolCapabilityFailure(ctx, account.Id, result); writeErr != nil {
						return result, writeErr
					}
				}
				return result, nil
			}

			switch {
			case statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices:
				supportedCandidates = append(supportedCandidates, candidateModel)
			case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
				result = accountPoolCapabilityFailResult(
					result,
					AccountPoolCapabilityStatusAuthError,
					accountPoolCapabilityProbeCandidateError(candidateModel, statusCode, body),
				)
				if req.Apply {
					if writeErr := persistAccountPoolCapabilityFailure(ctx, account.Id, result); writeErr != nil {
						return result, writeErr
					}
				}
				return result, nil
			case accountPoolCapabilityProbeResponseIndicatesUnsupportedModel(statusCode, body):
				probeErrors = append(probeErrors, accountPoolCapabilityProbeCandidateError(candidateModel, statusCode, body))
			default:
				result = accountPoolCapabilityFailResult(
					result,
					AccountPoolCapabilityStatusUpstreamError,
					accountPoolCapabilityProbeCandidateError(candidateModel, statusCode, body),
				)
				if req.Apply {
					if writeErr := persistAccountPoolCapabilityFailure(ctx, account.Id, result); writeErr != nil {
						return result, writeErr
					}
				}
				return result, nil
			}
		}

		if len(supportedCandidates) == 0 {
			result.Status = AccountPoolCapabilityStatusUnsupported
			result.Errors = probeErrors
			result.DetectedModels = []string{}
			result.AppliedModels = []string{}
			if len(result.Errors) == 0 {
				result.Errors = []string{sanitizeAccountPoolCapabilityError("no candidate models were supported by upstream account")}
			}
			if req.Apply {
				if writeErr := persistAccountPoolCapabilityFailure(ctx, account.Id, result); writeErr != nil {
					return result, writeErr
				}
			}
			return result, nil
		}

		schedulerModels, err := accountPoolCapabilitySchedulerModels(supportedCandidates, cleanedModelMapping)
		if err != nil {
			result = accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusConfigError, err.Error())
			if req.Apply {
				if writeErr := persistAccountPoolCapabilityFailure(ctx, account.Id, result); writeErr != nil {
					return result, writeErr
				}
			}
			return result, nil
		}

		if len(cleanedModelMapping) == 0 {
			result.DetectedModels = schedulerModels
		} else {
			result.DetectedModels = supportedCandidates
		}
		result.Errors = probeErrors
		if len(probeErrors) > 0 {
			result.Status = AccountPoolCapabilityStatusPartial
		} else {
			result.Status = AccountPoolCapabilityStatusSuccess
		}
		if !req.Apply {
			return result, nil
		}

		appliedModels, err := accountPoolCapabilityAppliedModels(account, schedulerModels, req.Merge)
		if err != nil {
			return result, err
		}
		result.AppliedModels = appliedModels

		if err := persistAccountPoolCapabilitySuccess(ctx, account.Id, appliedModels, result.DetectedModels, cleanedModelMapping, result.Status); err != nil {
			return result, err
		}
		return result, nil
	case AccountPoolCapabilityModeModelsEndpoint:
	default:
		result = accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusConfigError, "unsupported capability detection mode")
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(ctx, account.Id, result); writeErr != nil {
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
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, ctxErr
		}
		status := AccountPoolCapabilityStatusConfigError
		if isAccountPoolCapabilityNetworkError(err) {
			status = AccountPoolCapabilityStatusNetworkError
		}
		result = accountPoolCapabilityFailResult(result, status, err.Error())
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(ctx, account.Id, result); writeErr != nil {
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
			if writeErr := persistAccountPoolCapabilityFailure(ctx, account.Id, result); writeErr != nil {
				return result, writeErr
			}
		}
		return result, nil
	}

	var payload accountPoolCapabilityModelsResponse
	if err := common.Unmarshal(body, &payload); err != nil {
		result = accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusUpstreamError, fmt.Sprintf("decode upstream models response: %v", err))
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(ctx, account.Id, result); writeErr != nil {
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
	if len(detectedModels) == 0 {
		result = accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusUnsupported, "upstream returned no supported models")
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(ctx, account.Id, result); writeErr != nil {
				return result, writeErr
			}
		}
		return result, nil
	}

	schedulerModels, err := accountPoolCapabilitySchedulerModels(detectedModels, cleanedModelMapping)
	if err != nil {
		result = accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusConfigError, err.Error())
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(ctx, account.Id, result); writeErr != nil {
				return result, writeErr
			}
		}
		return result, nil
	}
	if len(req.CandidateModels) > 0 {
		schedulerModels = intersectAccountPoolCapabilityModels(schedulerModels, req.CandidateModels)
	}
	if len(schedulerModels) == 0 {
		result = accountPoolCapabilityFailResult(result, AccountPoolCapabilityStatusUnsupported, "no candidate models matched detected account capabilities")
		if req.Apply {
			if writeErr := persistAccountPoolCapabilityFailure(ctx, account.Id, result); writeErr != nil {
				return result, writeErr
			}
		}
		return result, nil
	}

	result.Status = AccountPoolCapabilityStatusSuccess
	if len(cleanedModelMapping) == 0 {
		result.DetectedModels = schedulerModels
	} else {
		result.DetectedModels = detectedModels
	}

	if !req.Apply {
		return result, nil
	}

	appliedModels, err := accountPoolCapabilityAppliedModels(account, schedulerModels, req.Merge)
	if err != nil {
		return result, err
	}
	result.AppliedModels = appliedModels

	if err := persistAccountPoolCapabilitySuccess(ctx, account.Id, appliedModels, result.DetectedModels, cleanedModelMapping, result.Status); err != nil {
		return result, err
	}
	return result, nil
}

func (s AccountPoolService) DetectPoolCapabilities(ctx context.Context, req AccountPoolCapabilityDetectRequest) (AccountPoolCapabilityPoolResult, error) {
	result := newAccountPoolCapabilityPoolResult()
	if req.PoolID <= 0 {
		return result, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	var pool model.AccountPool
	if err := model.DB.WithContext(ctx).
		Where("status <> ?", model.AccountPoolStatusDeleted).
		First(&pool, req.PoolID).Error; err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, ctxErr
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return result, errors.New("account pool not found")
		}
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	accountIDs := normalizeAccountPoolCapabilityAccountIDs(req.AccountIDs)
	if len(accountIDs) == 0 {
		var accounts []model.AccountPoolAccount
		if err := model.DB.WithContext(ctx).
			Where("pool_id = ? AND status <> ?", req.PoolID, model.AccountPoolAccountStatusDeleted).
			Order("id asc").
			Find(&accounts).Error; err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return result, ctxErr
			}
			return result, err
		}
		if err := ctx.Err(); err != nil {
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
		if err := ctx.Err(); err != nil {
			return result, err
		}
		accountReq := req
		accountReq.AccountID = accountID
		accountReq.AccountIDs = nil

		detectResult, err := s.DetectAccountCapability(ctx, accountReq)
		if err != nil {
			return result, err
		}
		if err := ctx.Err(); err != nil {
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
	timeout := time.Duration(seconds) * time.Second
	if timeout > accountPoolCapabilityMaxTimeout {
		return accountPoolCapabilityMaxTimeout
	}
	return timeout
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
	return fetchAccountPoolCapabilityResponseBodyWithRequestBody(ctx, method, requestURL, proxyURL, headers, options, timeout, nil)
}

func fetchAccountPoolCapabilityResponseBodyWithRequestBody(
	ctx context.Context,
	method string,
	requestURL string,
	proxyURL string,
	headers http.Header,
	options FetchChannelUpstreamModelIDsOptions,
	timeout time.Duration,
	requestBody []byte,
) ([]byte, int, error) {
	if err := validateFetchModelsURL(requestURL, options); err != nil {
		return nil, 0, err
	}
	if len(requestBody) > accountPoolCapabilityRequestBodyLimitBytes {
		return nil, 0, fmt.Errorf("request body too large: limit %d bytes", accountPoolCapabilityRequestBodyLimitBytes)
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

	var requestReader io.Reader
	if len(requestBody) > 0 {
		requestReader = bytes.NewReader(requestBody)
	}
	req, err := http.NewRequestWithContext(requestCtx, method, requestURL, requestReader)
	if err != nil {
		return nil, 0, err
	}
	if len(requestBody) > 0 && headers.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
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
	result.AppliedModels = []string{}
	return result
}

func newAccountPoolCapabilityDetectResult(req AccountPoolCapabilityDetectRequest) AccountPoolCapabilityDetectResult {
	modelMapping := accountPoolCleanStringMap(req.ModelMapping)
	if modelMapping == nil {
		modelMapping = map[string]string{}
	}
	return AccountPoolCapabilityDetectResult{
		AccountID:      req.AccountID,
		Mode:           normalizeAccountPoolCapabilityMode(req.Mode),
		DetectedModels: []string{},
		AppliedModels:  []string{},
		ModelMapping:   modelMapping,
		Errors:         []string{},
	}
}

func newAccountPoolCapabilityPoolResult() AccountPoolCapabilityPoolResult {
	return AccountPoolCapabilityPoolResult{
		Results: []AccountPoolCapabilityDetectResult{},
	}
}

func accountPoolCapabilityHTTPErrorMessage(statusCode int, body []byte) string {
	bodyText := strings.TrimSpace(string(body))
	if bodyText == "" {
		return fmt.Sprintf("status code: %d", statusCode)
	}
	return fmt.Sprintf("status code: %d, body: %s", statusCode, bodyText)
}

func buildAccountPoolCapabilityProbeURL(baseURL string) string {
	return fmt.Sprintf("%s/v1/chat/completions", strings.TrimRight(baseURL, "/"))
}

func buildAccountPoolCapabilityProbeRequestBody(modelName string) ([]byte, error) {
	return common.Marshal(accountPoolCapabilityProbeRequest{
		Model: modelName,
		Messages: []accountPoolCapabilityProbeRequestMessage{
			{
				Role:    "user",
				Content: "ping",
			},
		},
		MaxTokens: 1,
		Stream:    false,
	})
}

func accountPoolCapabilityProbeCandidateError(modelName string, statusCode int, body []byte) string {
	return sanitizeAccountPoolCapabilityError(
		fmt.Sprintf("candidate model %q probe failed: %s", modelName, accountPoolCapabilityHTTPErrorMessage(statusCode, body)),
	)
}

func accountPoolCapabilityProbeResponseIndicatesUnsupportedModel(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest && statusCode != http.StatusNotFound {
		return false
	}
	if len(body) == 0 || len(body) > accountPoolCapabilityUnsupportedBodyInspectLimitBytes {
		return false
	}

	if accountPoolCapabilityStructuredErrorIndicatesUnsupportedModel(body) {
		return true
	}

	message := strings.TrimSpace(accountPoolCapabilityErrorMessage(body))
	if message == "" {
		return false
	}
	return accountPoolCapabilityUnsupportedMessageIndicatesUnsupportedModel(message)
}

func accountPoolCapabilityStructuredErrorIndicatesUnsupportedModel(body []byte) bool {
	var payload accountPoolCapabilityErrorResponse
	if err := common.Unmarshal(body, &payload); err != nil {
		return false
	}
	return accountPoolCapabilityUnsupportedErrorSignal(payload.Error.Code) ||
		accountPoolCapabilityUnsupportedErrorSignal(payload.Error.Type) ||
		accountPoolCapabilityUnsupportedErrorSignal(payload.Code) ||
		accountPoolCapabilityUnsupportedErrorSignal(payload.Type)
}

func accountPoolCapabilityUnsupportedMessageIndicatesUnsupportedModel(message string) bool {
	normalized := accountPoolCapabilityNormalizeUnsupportedErrorSignal(message)
	if normalized == "" {
		return false
	}
	if strings.Contains(normalized, "model not found") || strings.Contains(normalized, "deployment not found") {
		return true
	}
	if strings.Contains(normalized, "model not allowed") {
		return true
	}
	if strings.Contains(normalized, "not allowed") && strings.Contains(normalized, "model") {
		return true
	}
	return false
}

func accountPoolCapabilityUnsupportedErrorSignal(value string) bool {
	normalized := accountPoolCapabilityNormalizeUnsupportedErrorSignal(value)
	switch normalized {
	case "model not found", "model not allowed", "deployment not found":
		return true
	}
	switch strings.ReplaceAll(normalized, " ", "") {
	case "modelnotfound", "modelnotallowed", "deploymentnotfound":
		return true
	}
	return false
}

func accountPoolCapabilityNormalizeUnsupportedErrorSignal(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	var builder strings.Builder
	builder.Grow(len(value))
	var previous rune
	for _, current := range value {
		switch {
		case current == '_' || current == '-' || unicode.IsSpace(current):
			builder.WriteByte(' ')
			previous = ' '
			continue
		case unicode.IsUpper(current) && (unicode.IsLower(previous) || unicode.IsDigit(previous)):
			builder.WriteByte(' ')
		}
		builder.WriteRune(unicode.ToLower(current))
		previous = current
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}

func accountPoolCapabilityErrorMessage(body []byte) string {
	var payload accountPoolCapabilityErrorResponse
	if err := common.Unmarshal(body, &payload); err == nil {
		if message := strings.TrimSpace(payload.Error.Message); message != "" {
			return message
		}
		if message := strings.TrimSpace(payload.Message); message != "" {
			return message
		}
	}
	return strings.TrimSpace(string(body))
}

func sanitizeAccountPoolCapabilityError(message string) string {
	return sanitizeAccountPoolRuntimeErrorMessage(message, accountPoolLastErrorMaxLength)
}

func resolveAccountPoolCapabilityRuntimeCredential(
	ctx context.Context,
	credential AccountPoolCredentialConfig,
	tokenState AccountPoolTokenState,
	proxyURL string,
) (string, error) {
	if token := strings.TrimSpace(credential.APIKey); token != "" {
		return token, nil
	}
	now := common.GetTimestamp()
	if accountPoolAccessTokenUsable(tokenState, now) {
		return strings.TrimSpace(tokenState.AccessToken), nil
	}
	if !accountPoolHasOAuthRuntimeCredential(credential, tokenState) {
		return "", nil
	}
	refreshToken := accountPoolRuntimeRefreshToken(credential, tokenState)
	if refreshToken == "" {
		if strings.TrimSpace(tokenState.AccessToken) != "" && tokenState.ExpiresAt == 0 {
			return strings.TrimSpace(tokenState.AccessToken), nil
		}
		return "", errors.New("account pool oauth refresh_token is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := accountPoolOAuthRefresh(ctx, refreshToken, proxyURL)
	if err != nil {
		return "", err
	}
	if result == nil || strings.TrimSpace(result.AccessToken) == "" {
		return "", errors.New("account pool oauth refresh response missing access_token")
	}
	return strings.TrimSpace(result.AccessToken), nil
}

func persistAccountPoolCapabilitySuccess(ctx context.Context, accountID int, supportedModels []string, detectedModels []string, modelMapping map[string]string, status string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
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
	return model.DB.WithContext(ctx).Model(&model.AccountPoolAccount{}).
		Where("id = ?", accountID).
		Updates(updates).Error
}

func persistAccountPoolCapabilityFailure(ctx context.Context, accountID int, result AccountPoolCapabilityDetectResult) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
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
	}
	return model.DB.WithContext(ctx).Model(&model.AccountPoolAccount{}).
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

func accountPoolCapabilitySchedulerModels(detectedModels []string, modelMapping map[string]string) ([]string, error) {
	if len(modelMapping) == 0 {
		return normalizeAccountPoolCapabilityModels(detectedModels), nil
	}

	detectedSet := make(map[string]struct{}, len(detectedModels))
	for _, detectedModel := range detectedModels {
		detectedSet[detectedModel] = struct{}{}
	}

	keys := make([]string, 0, len(modelMapping))
	for schedulerModel, accountModel := range modelMapping {
		if _, ok := detectedSet[accountModel]; !ok {
			return nil, fmt.Errorf("mapped account model %q for scheduler model %q was not detected", accountModel, schedulerModel)
		}
		keys = append(keys, schedulerModel)
	}
	sort.Strings(keys)
	return normalizeAccountPoolCapabilityModels(keys), nil
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
