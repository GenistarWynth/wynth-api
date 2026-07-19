package service

import (
	"context"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

type AccountPoolLocalQuotaResetParams struct {
	PoolID            int
	AccountID         int
	ClearCooldown     bool
	ResetRequestQuota bool
	ForceProbe        bool
	Now               int64
}

type AccountPoolLocalQuotaResetResult struct {
	Account           AccountPoolAccountView       `json:"account"`
	CooldownCleared   bool                         `json:"cooldown_cleared"`
	RequestQuotaReset bool                         `json:"request_quota_reset"`
	Probe             *AccountPoolXAIQuotaSnapshot `json:"probe,omitempty"`
	ProbeError        string                       `json:"probe_error,omitempty"`
}

var accountPoolLocalQuotaProbe = func(ctx context.Context, poolID int, accountID int) (AccountPoolXAIQuotaSnapshot, error) {
	return (AccountPoolService{}).ProbeXAIQuota(ctx, poolID, accountID)
}

func (s AccountPoolService) ResetAccountLocalQuota(ctx context.Context, params AccountPoolLocalQuotaResetParams) (AccountPoolLocalQuotaResetResult, error) {
	if !params.ClearCooldown && !params.ResetRequestQuota && !params.ForceProbe {
		return AccountPoolLocalQuotaResetResult{}, errors.New("at least one local quota reset option is required")
	}
	pool, err := getAccountPoolExistingPool(params.PoolID)
	if err != nil {
		return AccountPoolLocalQuotaResetResult{}, err
	}
	account, err := getAccountPoolAccountForPool(params.PoolID, params.AccountID)
	if err != nil {
		return AccountPoolLocalQuotaResetResult{}, err
	}
	if params.ForceProbe {
		credential, decryptErr := DecryptAccountPoolCredentialConfig(account.CredentialConfig)
		if decryptErr != nil {
			return AccountPoolLocalQuotaResetResult{}, decryptErr
		}
		if !accountPoolXAIQuotaCreateProbeEligible(pool.Platform, credential) {
			return AccountPoolLocalQuotaResetResult{}, errors.New("force probe is supported only for xai OAuth accounts")
		}
	}

	now := params.Now
	if now <= 0 {
		now = common.GetTimestamp()
	}
	updates := map[string]any{"updated_time": now}
	if params.ClearCooldown {
		updates["rate_limited_until"] = int64(0)
		if accountPoolQuotaRelatedReason(account.TempDisabledReason) {
			updates["temp_disabled_until"] = int64(0)
			updates["temp_disabled_reason"] = ""
		}
		if accountPoolQuotaRelatedReason(account.LastError) {
			updates["last_error"] = ""
		}
		if pool.Platform == model.AccountPoolPlatformXAI {
			options, parseErr := parseAccountPoolRuntimeOptions(account.RuntimeOptions)
			if parseErr != nil {
				return AccountPoolLocalQuotaResetResult{}, parseErr
			}
			options.XAIQuota = nil
			encoded, marshalErr := common.Marshal(options)
			if marshalErr != nil {
				return AccountPoolLocalQuotaResetResult{}, marshalErr
			}
			updates["runtime_options"] = string(encoded)
		}
	}
	if params.ResetRequestQuota {
		updates["request_quota_used"] = int64(0)
		updates["request_quota_window_start"] = now
	}

	if ctx == nil {
		ctx = context.Background()
	}
	update := model.DB.WithContext(ctx).Model(&model.AccountPoolAccount{}).
		Where("id = ? AND pool_id = ? AND status <> ?", params.AccountID, params.PoolID, model.AccountPoolAccountStatusDeleted).
		Updates(updates)
	if update.Error != nil {
		return AccountPoolLocalQuotaResetResult{}, update.Error
	}
	if update.RowsAffected == 0 {
		return AccountPoolLocalQuotaResetResult{}, errors.New("account pool account not found")
	}
	if params.ClearCooldown {
		clearAccountPoolRuntimeBlock(params.AccountID)
	}

	result := AccountPoolLocalQuotaResetResult{
		CooldownCleared:   params.ClearCooldown,
		RequestQuotaReset: params.ResetRequestQuota,
	}
	if params.ForceProbe {
		probe, probeErr := accountPoolLocalQuotaProbe(ctx, params.PoolID, params.AccountID)
		if probeErr != nil {
			result.ProbeError = "xai quota re-probe failed"
		} else {
			result.Probe = &probe
		}
	}

	account, err = getAccountPoolAccountForPool(params.PoolID, params.AccountID)
	if err != nil {
		return AccountPoolLocalQuotaResetResult{}, err
	}
	result.Account, err = buildAccountPoolAccountView(account)
	return result, err
}

func accountPoolQuotaRelatedReason(reason string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	if reason == "" {
		return false
	}
	for _, marker := range []string{"quota", "rate limit", "rate-limit", "too many requests", "429"} {
		if strings.Contains(reason, marker) {
			return true
		}
	}
	return false
}
