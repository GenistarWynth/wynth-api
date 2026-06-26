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
	AffinityKey          string
	Now                  int64
}

type AccountPoolSelectionResult struct {
	PoolID            int
	BindingID         int
	AccountID         int
	AccountName       string
	AccountIdentifier string
	MaxConcurrency    int
	AccountRetryTimes int
	UpstreamModelName string
	ProxyURL          string
	Credential        AccountPoolCredentialConfig
	TokenState        AccountPoolTokenState
	RuntimeOptions    string
	RequestQuota      int64
}

type accountPoolAccountCandidate struct {
	account           model.AccountPoolAccount
	upstreamModelName string
}

// accountPoolSelectionContext holds the pre-loaded, pre-filtered candidate set for one selection
// request. It is produced by loadAccountPoolSelectionContext and consumed by
// pickAccountPoolCandidate and buildAccountPoolSelectionResult.
type accountPoolSelectionContext struct {
	binding    model.AccountPoolChannelBinding
	pool       model.AccountPool
	candidates []accountPoolAccountCandidate // all schedulable candidates; no AttemptedAccountIDs filter applied
}

// loadAccountPoolSelectionContext runs the expensive part of the pipeline once: it loads the
// binding, pool, config, and queries the DB for enabled accounts, then builds the full candidate
// slice applying every filter EXCEPT AttemptedAccountIDs (which is handled per-pick in
// pickAccountPoolCandidate). Per-account parse errors keep the existing skip+continue behavior.
func loadAccountPoolSelectionContext(req AccountPoolSelectionRequest) (accountPoolSelectionContext, error) {
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
		return accountPoolSelectionContext{}, err
	}
	pool, err := loadEnabledAccountPool(binding.PoolID)
	if err != nil {
		return accountPoolSelectionContext{}, err
	}

	filterConfig, err := parseAccountPoolAccountFilterConfig(binding.AccountFilterConfig)
	if err != nil {
		return accountPoolSelectionContext{}, err
	}
	modelPolicy, err := parseAccountPoolModelPolicy(binding.ModelPolicy)
	if err != nil {
		return accountPoolSelectionContext{}, err
	}
	if !accountPoolModelPolicyAllows(modelPolicy, req.RequestModel, upstreamModelName) {
		return accountPoolSelectionContext{}, ErrAccountPoolNoSchedulableAccount
	}

	var accounts []model.AccountPoolAccount
	if err := model.DB.Where("pool_id = ? AND status = ?", binding.PoolID, model.AccountPoolAccountStatusEnabled).
		Order("id asc").
		Find(&accounts).Error; err != nil {
		return accountPoolSelectionContext{}, err
	}

	allowedAccountIDs := accountPoolAccountFilterSet(filterConfig)
	candidates := make([]accountPoolAccountCandidate, 0, len(accounts))
	for _, account := range accounts {
		// NOTE: AttemptedAccountIDs is intentionally NOT filtered here; it is applied per-pick
		// in pickAccountPoolCandidate so the loaded context can be reused across the lease loop.
		if len(allowedAccountIDs) > 0 {
			if _, allowed := allowedAccountIDs[account.Id]; !allowed {
				continue
			}
		}
		if !account.IsSchedulableAt(now) {
			continue
		}
		if account.QuotaExceededAt(now) {
			continue
		}
		// Fast-path: exclude accounts that are in-process blocked due to a recent failure,
		// bridging the DB cooldown read-propagation window without a DB round-trip.
		if accountPoolRuntimeBlocked(account.Id, now) {
			continue
		}
		supportedModels, err := parseAccountPoolSupportedModels(account.SupportedModels)
		if err != nil {
			common.SysLog(fmt.Sprintf("account pool: skipping account id=%d name=%q due to invalid supported_models: %v", account.Id, account.Name, err))
			continue
		}
		if !accountPoolModelListContainsOrEmpty(supportedModels, upstreamModelName) {
			continue
		}
		accountUpstreamModelName, err := mapAccountPoolUpstreamModel(account.ModelMapping, upstreamModelName)
		if err != nil {
			common.SysLog(fmt.Sprintf("account pool: skipping account id=%d name=%q due to invalid model_mapping: %v", account.Id, account.Name, err))
			continue
		}
		candidates = append(candidates, accountPoolAccountCandidate{
			account:           account,
			upstreamModelName: accountUpstreamModelName,
		})
	}

	return accountPoolSelectionContext{
		binding:    binding,
		pool:       pool,
		candidates: candidates,
	}, nil
}

// pickAccountPoolCandidate selects one candidate from ctx.candidates minus the attempted set.
// It applies the same policy selection (highest-priority subset → weighted-random or round-robin)
// and the same affinity override used by the original SelectAccountPoolAccount. Returns ok=false
// when no unattempted candidates remain.
func pickAccountPoolCandidate(
	ctx accountPoolSelectionContext,
	attempted map[int]struct{},
	affinityKey string,
	now int64,
) (accountPoolAccountCandidate, bool) {
	// Build the unattempted candidate slice without allocating unless necessary.
	available := ctx.candidates
	if len(attempted) > 0 {
		filtered := make([]accountPoolAccountCandidate, 0, len(ctx.candidates))
		for _, c := range ctx.candidates {
			if _, skip := attempted[c.account.Id]; !skip {
				filtered = append(filtered, c)
			}
		}
		available = filtered
	}
	if len(available) == 0 {
		return accountPoolAccountCandidate{}, false
	}

	selected := selectAccountPoolCandidate(available, ctx.binding.SchedulePolicy)
	if affinityCandidate, ok := selectAccountPoolAffinityCandidate(affinityKey, ctx.binding.Id, available, now); ok {
		selected = affinityCandidate
	}
	return selected, true
}

// buildAccountPoolSelectionResult assembles the final AccountPoolSelectionResult for a chosen
// candidate by decrypting the credential + token state and resolving the proxy URL. This is the
// expensive tail of the pipeline; it is only called once a lease has been acquired.
func buildAccountPoolSelectionResult(
	ctx accountPoolSelectionContext,
	selected accountPoolAccountCandidate,
) (AccountPoolSelectionResult, error) {
	credential, err := DecryptAccountPoolCredentialConfig(selected.account.CredentialConfig)
	if err != nil {
		return AccountPoolSelectionResult{}, fmt.Errorf("decrypt account pool credential: %w", err)
	}
	tokenState, err := DecryptAccountPoolTokenState(selected.account.TokenState)
	if err != nil {
		return AccountPoolSelectionResult{}, fmt.Errorf("decrypt account pool token state: %w", err)
	}
	proxyURL, err := ResolveAccountPoolRuntimeProxyURL(selected.account.ProxyID, ctx.pool.DefaultProxyID)
	if err != nil {
		return AccountPoolSelectionResult{}, fmt.Errorf("resolve account pool proxy: %w", err)
	}

	return AccountPoolSelectionResult{
		PoolID:            ctx.binding.PoolID,
		BindingID:         ctx.binding.Id,
		AccountID:         selected.account.Id,
		AccountName:       selected.account.Name,
		AccountIdentifier: selected.account.AccountIdentifier,
		MaxConcurrency:    selected.account.MaxConcurrency,
		AccountRetryTimes: ctx.binding.AccountRetryTimes,
		UpstreamModelName: selected.upstreamModelName,
		ProxyURL:          proxyURL,
		Credential:        credential,
		TokenState:        tokenState,
		RuntimeOptions:    selected.account.RuntimeOptions,
		RequestQuota:      selected.account.RequestQuota,
	}, nil
}

// SelectAccountPoolAccount selects one schedulable account for the given request.
// External behavior is identical to the original implementation.
func SelectAccountPoolAccount(req AccountPoolSelectionRequest) (AccountPoolSelectionResult, error) {
	now := req.Now
	if now == 0 {
		now = common.GetTimestamp()
	}

	ctx, err := loadAccountPoolSelectionContext(req)
	if err != nil {
		return AccountPoolSelectionResult{}, err
	}

	selected, ok := pickAccountPoolCandidate(ctx, req.AttemptedAccountIDs, req.AffinityKey, now)
	if !ok {
		return AccountPoolSelectionResult{}, ErrAccountPoolNoSchedulableAccount
	}

	return buildAccountPoolSelectionResult(ctx, selected)
}

func selectAccountPoolAffinityCandidate(key string, bindingID int, candidates []accountPoolAccountCandidate, now int64) (accountPoolAccountCandidate, bool) {
	accountID, ok := lookupAccountPoolRuntimeAffinity(key, bindingID, now)
	if !ok {
		return accountPoolAccountCandidate{}, false
	}
	for _, candidate := range candidates {
		if candidate.account.Id == accountID {
			return candidate, true
		}
	}
	// Do NOT forget the pin here. The pinned account may be absent from candidates only
	// transiently (e.g., it is in the per-request AttemptedAccountIDs set, or it failed a
	// concurrent lease/capacity check). Dropping the pin now would cause the next request to
	// re-pin to a DIFFERENT account — which for stateful sessions (previous_response_id,
	// conversation id) does not hold the server-side state and triggers an upstream error.
	//
	// Eviction ownership:
	//   • Relay failure path: ForgetSelectedAccountPoolRuntimeAffinity (relay/account_pool_runtime.go)
	//     fires when the pinned account actually fails a request — this is the correct trigger.
	//   • Idle TTL: entries expire after accountPoolRuntimeAffinityTTLSeconds of inactivity.
	//   • Hard cap: entries expire after accountPoolRuntimeAffinityHardCapSeconds regardless.
	//
	// A stale pin to a genuinely dead account is harmless: the dead account never passes
	// IsSchedulableAt so it never appears in candidates, the pin is never honored, and the
	// entry expires via TTL. The relay already cleared the pin on the failure that killed it.
	return accountPoolAccountCandidate{}, false
}

func SelectAccountPoolAccountWithLease(req AccountPoolSelectionRequest) (AccountPoolSelectionResult, accountPoolRuntimeReleaseFunc, error) {
	return selectAccountPoolAccountWithLease(req, true)
}

// selectAccountPoolAccountWithLease loads the candidate pool ONCE, then loops entirely in memory:
// pick an unattempted candidate → try to acquire its lease → on success decrypt and return;
// on failure (at capacity) add to local attempted and loop. No additional DB queries occur
// after the initial loadAccountPoolSelectionContext call.
func selectAccountPoolAccountWithLease(req AccountPoolSelectionRequest, rememberSelection bool) (AccountPoolSelectionResult, accountPoolRuntimeReleaseFunc, error) {
	now := req.Now
	if now == 0 {
		now = common.GetTimestamp()
	}

	ctx, err := loadAccountPoolSelectionContext(req)
	if err != nil {
		return AccountPoolSelectionResult{}, nil, err
	}

	// Seed the local attempted set from the caller's set; we accumulate capacity failures here.
	attempted := make(map[int]struct{}, len(req.AttemptedAccountIDs)+len(ctx.candidates))
	for accountID := range req.AttemptedAccountIDs {
		attempted[accountID] = struct{}{}
	}

	for {
		selected, ok := pickAccountPoolCandidate(ctx, attempted, req.AffinityKey, now)
		if !ok {
			return AccountPoolSelectionResult{}, nil, ErrAccountPoolNoSchedulableAccount
		}

		release, acquired := tryAcquireAccountPoolRuntimeLease(selected.account.Id, selected.account.MaxConcurrency)
		if acquired {
			if rememberSelection {
				rememberAccountPoolRuntimeSelection(selected.account.Id, now)
			}
			result, err := buildAccountPoolSelectionResult(ctx, selected)
			if err != nil {
				release()
				return AccountPoolSelectionResult{}, nil, err
			}
			return result, release, nil
		}
		// Lease at capacity: exclude this account from subsequent picks in this request.
		attempted[selected.account.Id] = struct{}{}
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

func selectAccountPoolCandidate(candidates []accountPoolAccountCandidate, schedulePolicy string) accountPoolAccountCandidate {
	priorityCandidates := highestPriorityAccountPoolCandidates(candidates)
	if strings.EqualFold(strings.TrimSpace(schedulePolicy), AccountPoolSchedulePolicyRandom) {
		return selectWeightedRandomAccountPoolCandidate(priorityCandidates)
	}
	return selectRoundRobinAccountPoolCandidate(priorityCandidates)
}

func highestPriorityAccountPoolCandidates(candidates []accountPoolAccountCandidate) []accountPoolAccountCandidate {
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
	return priorityCandidates
}

func selectRoundRobinAccountPoolCandidate(candidates []accountPoolAccountCandidate) accountPoolAccountCandidate {
	selected := candidates[0]
	selectedRank := accountPoolRuntimeSelectionRank(selected.account.Id, selected.account.LastUsedAt)
	for _, candidate := range candidates[1:] {
		candidateRank := accountPoolRuntimeSelectionRank(candidate.account.Id, candidate.account.LastUsedAt)
		if candidateRank < selectedRank {
			selected = candidate
			selectedRank = candidateRank
		}
	}
	return selected
}

func selectWeightedRandomAccountPoolCandidate(priorityCandidates []accountPoolAccountCandidate) accountPoolAccountCandidate {
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
