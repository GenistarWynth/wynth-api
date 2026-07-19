package service

import (
	"context"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"gorm.io/gorm"
)

const (
	accountPoolXAIOAuthDefaultNearExpiryWindowSeconds = int64(10 * 60)
	accountPoolXAIOAuthMaxNearExpiryWindowSeconds     = int64(24 * 60 * 60)

	AccountPoolXAIOAuthReconcileActionRefresh = "refresh_credentials"
	AccountPoolXAIOAuthReconcileActionExpire  = "expire_account"

	AccountPoolXAIOAuthReconcileReasonMissingRefreshToken = "missing_refresh_token"
	AccountPoolXAIOAuthReconcileReasonAccessMissing       = "access_token_missing"
	AccountPoolXAIOAuthReconcileReasonAccessExpired       = "access_token_expired"
	AccountPoolXAIOAuthReconcileReasonAccessNearExpiry    = "access_token_near_expiry"
	AccountPoolXAIOAuthReconcileReasonCredentialRejected  = "credential_rejected"

	AccountPoolXAIOAuthReconcileOutcomeDryRun             = "dry_run"
	AccountPoolXAIOAuthReconcileOutcomeApplied            = "applied"
	AccountPoolXAIOAuthReconcileOutcomeAlreadyExpired     = "already_expired"
	AccountPoolXAIOAuthReconcileOutcomeConcurrentUpdate   = "concurrent_update"
	AccountPoolXAIOAuthReconcileOutcomeRefreshFailed      = "refresh_failed"
	AccountPoolXAIOAuthReconcileOutcomeCredentialRejected = "credential_rejected"
)

type AccountPoolXAIOAuthReconcileParams struct {
	PoolID                  int   `json:"-"`
	DryRun                  bool  `json:"dry_run"`
	NearExpiryWindowSeconds int64 `json:"near_expiry_window_seconds,omitempty"`
	Now                     int64 `json:"-"`
}

type AccountPoolXAIOAuthReconcileItem struct {
	AccountID int    `json:"account_id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Reason    string `json:"reason"`
	Action    string `json:"action"`
	Applied   bool   `json:"applied"`
	Outcome   string `json:"outcome"`
}

type AccountPoolXAIOAuthReconcileResult struct {
	PoolID                  int                                `json:"pool_id"`
	DryRun                  bool                               `json:"dry_run"`
	NearExpiryWindowSeconds int64                              `json:"near_expiry_window_seconds"`
	Scanned                 int                                `json:"scanned"`
	Candidates              int                                `json:"candidates"`
	Applied                 int                                `json:"applied"`
	Skipped                 int                                `json:"skipped"`
	Items                   []AccountPoolXAIOAuthReconcileItem `json:"items"`
}

func (s AccountPoolService) ReconcileXAIOAuthAccounts(ctx context.Context, params AccountPoolXAIOAuthReconcileParams) (AccountPoolXAIOAuthReconcileResult, error) {
	pool, err := getAccountPoolExistingPool(params.PoolID)
	if err != nil {
		return AccountPoolXAIOAuthReconcileResult{}, err
	}
	if pool.Platform != model.AccountPoolPlatformXAI {
		return AccountPoolXAIOAuthReconcileResult{}, errors.New("account pool is not an xai pool")
	}
	window := params.NearExpiryWindowSeconds
	if window <= 0 {
		window = accountPoolXAIOAuthDefaultNearExpiryWindowSeconds
	}
	if window > accountPoolXAIOAuthMaxNearExpiryWindowSeconds {
		return AccountPoolXAIOAuthReconcileResult{}, errors.New("xai oauth near-expiry window must not exceed 86400 seconds")
	}
	now := params.Now
	if now <= 0 {
		now = common.GetTimestamp()
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var accounts []model.AccountPoolAccount
	if err := model.DB.Where("pool_id = ? AND status <> ?", params.PoolID, model.AccountPoolAccountStatusDeleted).
		Order("id asc").Find(&accounts).Error; err != nil {
		return AccountPoolXAIOAuthReconcileResult{}, err
	}
	result := AccountPoolXAIOAuthReconcileResult{
		PoolID:                  params.PoolID,
		DryRun:                  params.DryRun,
		NearExpiryWindowSeconds: window,
		Items:                   make([]AccountPoolXAIOAuthReconcileItem, 0),
	}
	for _, account := range accounts {
		credential, decryptErr := DecryptAccountPoolCredentialConfig(account.CredentialConfig)
		if decryptErr != nil || !strings.EqualFold(strings.TrimSpace(credential.Type), AccountPoolCredentialTypeOAuth) {
			continue
		}
		result.Scanned++
		state, decryptErr := DecryptAccountPoolTokenState(account.TokenState)
		if decryptErr != nil {
			result.Skipped++
			continue
		}
		action, reason := classifyAccountPoolXAIOAuthReconcileCandidate(account, credential, state, now, window)
		if action == "" {
			continue
		}
		item := AccountPoolXAIOAuthReconcileItem{
			AccountID: account.Id,
			Name:      account.Name,
			Status:    account.Status,
			Reason:    reason,
			Action:    action,
			Outcome:   AccountPoolXAIOAuthReconcileOutcomeDryRun,
		}
		result.Candidates++
		if params.DryRun {
			result.Items = append(result.Items, item)
			continue
		}

		switch action {
		case AccountPoolXAIOAuthReconcileActionExpire:
			if account.Status == model.AccountPoolAccountStatusExpired {
				item.Outcome = AccountPoolXAIOAuthReconcileOutcomeAlreadyExpired
				result.Skipped++
				break
			}
			rows, applyErr := expireAccountPoolXAIOAuthReconcileSnapshot(account, reason, now)
			if applyErr != nil {
				return AccountPoolXAIOAuthReconcileResult{}, applyErr
			}
			if rows == 0 {
				item.Outcome = AccountPoolXAIOAuthReconcileOutcomeConcurrentUpdate
				result.Skipped++
				break
			}
			item.Applied = true
			item.Outcome = AccountPoolXAIOAuthReconcileOutcomeApplied
			result.Applied++
		case AccountPoolXAIOAuthReconcileActionRefresh:
			_, applied, refreshErr := refreshXAIOAuthAccountSnapshot(ctx, pool, account, now)
			if refreshErr != nil {
				if IsXAIOAuthPermanentCredentialError(refreshErr) && applied {
					item.Applied = true
					item.Outcome = AccountPoolXAIOAuthReconcileOutcomeCredentialRejected
					result.Applied++
					break
				}
				item.Outcome = AccountPoolXAIOAuthReconcileOutcomeRefreshFailed
				result.Skipped++
				break
			}
			if !applied {
				item.Outcome = AccountPoolXAIOAuthReconcileOutcomeConcurrentUpdate
				result.Skipped++
				break
			}
			item.Applied = true
			item.Outcome = AccountPoolXAIOAuthReconcileOutcomeApplied
			result.Applied++
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

func classifyAccountPoolXAIOAuthReconcileCandidate(account model.AccountPoolAccount, credential AccountPoolCredentialConfig, state AccountPoolTokenState, now int64, window int64) (string, string) {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(account.LastError)), "xai oauth credential rejected") {
		return AccountPoolXAIOAuthReconcileActionExpire, AccountPoolXAIOAuthReconcileReasonCredentialRejected
	}
	if accountPoolRuntimeRefreshToken(credential, state) == "" {
		return AccountPoolXAIOAuthReconcileActionExpire, AccountPoolXAIOAuthReconcileReasonMissingRefreshToken
	}
	if strings.TrimSpace(state.AccessToken) == "" {
		return AccountPoolXAIOAuthReconcileActionRefresh, AccountPoolXAIOAuthReconcileReasonAccessMissing
	}
	if state.ExpiresAt > 0 && state.ExpiresAt <= now {
		return AccountPoolXAIOAuthReconcileActionRefresh, AccountPoolXAIOAuthReconcileReasonAccessExpired
	}
	if state.ExpiresAt > 0 && state.ExpiresAt <= now+window {
		return AccountPoolXAIOAuthReconcileActionRefresh, AccountPoolXAIOAuthReconcileReasonAccessNearExpiry
	}
	return "", ""
}

func expireAccountPoolXAIOAuthReconcileSnapshot(account model.AccountPoolAccount, reason string, now int64) (int64, error) {
	message := "xai oauth account expired by reconciliation"
	if reason == AccountPoolXAIOAuthReconcileReasonMissingRefreshToken {
		message = "account pool oauth refresh_token is required"
	} else if reason == AccountPoolXAIOAuthReconcileReasonCredentialRejected {
		message = "xai oauth credential rejected"
	}
	update := model.DB.Model(&model.AccountPoolAccount{}).
		Where(
			"id = ? AND pool_id = ? AND status = ? AND credential_config = ? AND token_state = ?",
			account.Id,
			account.PoolID,
			account.Status,
			account.CredentialConfig,
			account.TokenState,
		).
		Updates(map[string]any{
			"status":               model.AccountPoolAccountStatusExpired,
			"rate_limited_until":   int64(0),
			"temp_disabled_until":  int64(0),
			"overload_until":       int64(0),
			"temp_disabled_reason": "",
			"last_error":           message,
			"last_failure_at":      now,
			"failure_count":        gorm.Expr("failure_count + ?", 1),
			"updated_time":         now,
		})
	return update.RowsAffected, update.Error
}
