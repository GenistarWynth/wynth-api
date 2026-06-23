package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"gorm.io/gorm"
)

var (
	ErrAccountPoolBindingNotRuntimeEnabled = errors.New("account pool binding is not runtime enabled")
	ErrAccountPoolNoSchedulableAccount     = errors.New("account pool has no schedulable account")
)

type AccountPoolSelectionRequest struct {
	ChannelID            int
	BindingID            int
	RequestModel         string
	ChannelUpstreamModel string
	AttemptedAccountIDs  map[int]struct{}
	Now                  int64
}

type AccountPoolSelectionResult struct {
	PoolID            int
	BindingID         int
	AccountID         int
	AccountName       string
	MaxConcurrency    int
	UpstreamModelName string
	Credential        AccountPoolCredentialConfig
	TokenState        AccountPoolTokenState
}

type accountPoolAccountCandidate struct {
	account           model.AccountPoolAccount
	upstreamModelName string
}

func SelectAccountPoolAccount(req AccountPoolSelectionRequest) (AccountPoolSelectionResult, error) {
	now := req.Now
	if now == 0 {
		now = common.GetTimestamp()
	}
	upstreamModelName := strings.TrimSpace(req.ChannelUpstreamModel)
	if upstreamModelName == "" {
		upstreamModelName = strings.TrimSpace(req.RequestModel)
	}

	binding, err := loadRuntimeAccountPoolBinding(req)
	if err != nil {
		return AccountPoolSelectionResult{}, err
	}
	if _, err := loadEnabledAccountPool(binding.PoolID); err != nil {
		return AccountPoolSelectionResult{}, err
	}

	filterConfig, err := parseAccountPoolAccountFilterConfig(binding.AccountFilterConfig)
	if err != nil {
		return AccountPoolSelectionResult{}, err
	}
	modelPolicy, err := parseAccountPoolModelPolicy(binding.ModelPolicy)
	if err != nil {
		return AccountPoolSelectionResult{}, err
	}
	if !accountPoolModelPolicyAllows(modelPolicy, req.RequestModel, upstreamModelName) {
		return AccountPoolSelectionResult{}, ErrAccountPoolNoSchedulableAccount
	}

	var accounts []model.AccountPoolAccount
	if err := model.DB.Where("pool_id = ? AND status = ?", binding.PoolID, model.AccountPoolAccountStatusEnabled).
		Order("id asc").
		Find(&accounts).Error; err != nil {
		return AccountPoolSelectionResult{}, err
	}

	allowedAccountIDs := accountPoolAccountFilterSet(filterConfig)
	candidates := make([]accountPoolAccountCandidate, 0, len(accounts))
	for _, account := range accounts {
		if _, attempted := req.AttemptedAccountIDs[account.Id]; attempted {
			continue
		}
		if len(allowedAccountIDs) > 0 {
			if _, allowed := allowedAccountIDs[account.Id]; !allowed {
				continue
			}
		}
		if !account.IsSchedulableAt(now) {
			continue
		}
		supportedModels, err := parseAccountPoolSupportedModels(account.SupportedModels)
		if err != nil {
			return AccountPoolSelectionResult{}, err
		}
		if !accountPoolModelListContainsOrEmpty(supportedModels, upstreamModelName) {
			continue
		}
		accountUpstreamModelName, err := mapAccountPoolUpstreamModel(account.ModelMapping, upstreamModelName)
		if err != nil {
			return AccountPoolSelectionResult{}, err
		}
		candidates = append(candidates, accountPoolAccountCandidate{
			account:           account,
			upstreamModelName: accountUpstreamModelName,
		})
	}
	if len(candidates) == 0 {
		return AccountPoolSelectionResult{}, ErrAccountPoolNoSchedulableAccount
	}

	selected := selectAccountPoolCandidate(candidates)
	credential, err := DecryptAccountPoolCredentialConfig(selected.account.CredentialConfig)
	if err != nil {
		return AccountPoolSelectionResult{}, fmt.Errorf("decrypt account pool credential: %w", err)
	}
	tokenState, err := DecryptAccountPoolTokenState(selected.account.TokenState)
	if err != nil {
		return AccountPoolSelectionResult{}, fmt.Errorf("decrypt account pool token state: %w", err)
	}

	return AccountPoolSelectionResult{
		PoolID:            binding.PoolID,
		BindingID:         binding.Id,
		AccountID:         selected.account.Id,
		AccountName:       selected.account.Name,
		MaxConcurrency:    selected.account.MaxConcurrency,
		UpstreamModelName: selected.upstreamModelName,
		Credential:        credential,
		TokenState:        tokenState,
	}, nil
}

func SelectAccountPoolAccountWithLease(req AccountPoolSelectionRequest) (AccountPoolSelectionResult, accountPoolRuntimeReleaseFunc, error) {
	attempted := make(map[int]struct{}, len(req.AttemptedAccountIDs)+1)
	for accountID := range req.AttemptedAccountIDs {
		attempted[accountID] = struct{}{}
	}
	for {
		req.AttemptedAccountIDs = attempted
		selection, err := SelectAccountPoolAccount(req)
		if err != nil {
			return AccountPoolSelectionResult{}, nil, err
		}
		release, acquired := tryAcquireAccountPoolRuntimeLease(selection.AccountID, selection.MaxConcurrency)
		if acquired {
			return selection, release, nil
		}
		attempted[selection.AccountID] = struct{}{}
	}
}

func loadRuntimeAccountPoolBinding(req AccountPoolSelectionRequest) (model.AccountPoolChannelBinding, error) {
	var binding model.AccountPoolChannelBinding
	query := model.DB
	switch {
	case req.BindingID > 0:
		query = query.Where("id = ?", req.BindingID)
	case req.ChannelID > 0:
		query = query.Where("channel_id = ?", req.ChannelID)
	default:
		return binding, ErrAccountPoolBindingNotRuntimeEnabled
	}
	if err := query.First(&binding).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return binding, ErrAccountPoolBindingNotRuntimeEnabled
		}
		return binding, err
	}
	if binding.Status != model.AccountPoolBindingStatusEnabled {
		return binding, ErrAccountPoolBindingNotRuntimeEnabled
	}
	return binding, nil
}

func loadEnabledAccountPool(poolID int) (model.AccountPool, error) {
	var pool model.AccountPool
	if err := model.DB.Where("id = ? AND status = ?", poolID, model.AccountPoolStatusEnabled).First(&pool).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pool, ErrAccountPoolNoSchedulableAccount
		}
		return pool, err
	}
	return pool, nil
}

func parseAccountPoolAccountFilterConfig(raw string) (AccountPoolAccountFilterConfig, error) {
	var config AccountPoolAccountFilterConfig
	if strings.TrimSpace(raw) == "" {
		return config, nil
	}
	if err := common.UnmarshalJsonStr(raw, &config); err != nil {
		return config, fmt.Errorf("parse account pool account filter: %w", err)
	}
	return config, nil
}

func parseAccountPoolModelPolicy(raw string) (AccountPoolModelPolicy, error) {
	var policy AccountPoolModelPolicy
	if strings.TrimSpace(raw) == "" {
		return policy, nil
	}
	if err := common.UnmarshalJsonStr(raw, &policy); err != nil {
		return policy, fmt.Errorf("parse account pool model policy: %w", err)
	}
	return policy, nil
}

func parseAccountPoolSupportedModels(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var supportedModels []string
	if err := common.UnmarshalJsonStr(raw, &supportedModels); err != nil {
		return nil, fmt.Errorf("parse account pool supported models: %w", err)
	}
	return supportedModels, nil
}

func accountPoolAccountFilterSet(config AccountPoolAccountFilterConfig) map[int]struct{} {
	if len(config.AccountIDs) == 0 {
		return nil
	}
	set := make(map[int]struct{}, len(config.AccountIDs))
	for _, accountID := range config.AccountIDs {
		if accountID > 0 {
			set[accountID] = struct{}{}
		}
	}
	return set
}

func accountPoolModelPolicyAllows(policy AccountPoolModelPolicy, requestModel string, upstreamModel string) bool {
	if !strings.EqualFold(strings.TrimSpace(policy.Strategy), "fixed") || len(policy.FixedModels) == 0 {
		return true
	}
	return accountPoolModelListContainsOrEmpty(policy.FixedModels, requestModel) ||
		accountPoolModelListContainsOrEmpty(policy.FixedModels, upstreamModel)
}

func accountPoolModelListContainsOrEmpty(models []string, modelName string) bool {
	if len(models) == 0 {
		return true
	}
	modelName = strings.TrimSpace(modelName)
	for _, candidate := range models {
		if strings.TrimSpace(candidate) == modelName {
			return true
		}
	}
	return false
}

func mapAccountPoolUpstreamModel(rawMapping string, upstreamModelName string) (string, error) {
	if strings.TrimSpace(rawMapping) == "" {
		return upstreamModelName, nil
	}
	modelMapping := map[string]string{}
	if err := common.UnmarshalJsonStr(rawMapping, &modelMapping); err != nil {
		return "", fmt.Errorf("parse account pool model mapping: %w", err)
	}
	if mapped := strings.TrimSpace(modelMapping[upstreamModelName]); mapped != "" {
		return mapped, nil
	}
	return upstreamModelName, nil
}

func selectAccountPoolCandidate(candidates []accountPoolAccountCandidate) accountPoolAccountCandidate {
	highestPriority := candidates[0].account.Priority
	for _, candidate := range candidates[1:] {
		if candidate.account.Priority > highestPriority {
			highestPriority = candidate.account.Priority
		}
	}
	priorityCandidates := make([]accountPoolAccountCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.account.Priority == highestPriority {
			priorityCandidates = append(priorityCandidates, candidate)
		}
	}

	totalWeight := 0
	for _, candidate := range priorityCandidates {
		totalWeight += int(candidate.account.Weight) + 10
	}
	if totalWeight <= 0 {
		return priorityCandidates[0]
	}
	weight := common.GetRandomInt(totalWeight)
	for _, candidate := range priorityCandidates {
		weight -= int(candidate.account.Weight) + 10
		if weight < 0 {
			return candidate
		}
	}
	return priorityCandidates[len(priorityCandidates)-1]
}
