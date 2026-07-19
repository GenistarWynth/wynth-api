package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	accountPoolXAIOAuthReconcileIntervalEnv     = "ACCOUNT_POOL_XAI_OAUTH_RECONCILE_INTERVAL_MINUTES"
	accountPoolXAIOAuthReconcileDefaultInterval = 5 * time.Minute
)

type accountPoolXAIOAuthReconcileRunnerFunc func(context.Context, AccountPoolXAIOAuthReconcileParams) (AccountPoolXAIOAuthReconcileResult, error)

var (
	accountPoolXAIOAuthReconcileWorkerOnce    sync.Once
	accountPoolXAIOAuthReconcileWorkerDone    = make(chan struct{})
	accountPoolXAIOAuthReconcileWorkerRunning atomic.Bool
	accountPoolXAIOAuthReconcileRunner        accountPoolXAIOAuthReconcileRunnerFunc = func(ctx context.Context, params AccountPoolXAIOAuthReconcileParams) (AccountPoolXAIOAuthReconcileResult, error) {
		return (AccountPoolService{}).ReconcileXAIOAuthAccounts(ctx, params)
	}
)

func loadAccountPoolXAIOAuthReconcileWorkerInterval() time.Duration {
	minutes := common.GetEnvOrDefault(
		accountPoolXAIOAuthReconcileIntervalEnv,
		int(accountPoolXAIOAuthReconcileDefaultInterval/time.Minute),
	)
	if minutes <= 0 {
		return accountPoolXAIOAuthReconcileDefaultInterval
	}
	return time.Duration(minutes) * time.Minute
}

func listDueAccountPoolXAIOAuthReconcilePoolIDs(ctx context.Context, now int64, window int64) ([]int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	if window <= 0 {
		window = accountPoolXAIOAuthDefaultNearExpiryWindowSeconds
	}

	var poolIDs []int
	if err := model.DB.WithContext(ctx).Model(&model.AccountPool{}).
		Where("platform = ? AND status = ?", model.AccountPoolPlatformXAI, model.AccountPoolStatusEnabled).
		Order("id asc").
		Pluck("id", &poolIDs).Error; err != nil {
		return nil, err
	}
	if len(poolIDs) == 0 {
		return nil, nil
	}

	var accounts []model.AccountPoolAccount
	if err := model.DB.WithContext(ctx).
		Select("id", "pool_id", "status", "credential_config", "token_state", "last_error").
		Where("pool_id IN ? AND status <> ?", poolIDs, model.AccountPoolAccountStatusDeleted).
		Order("pool_id asc, id asc").
		Find(&accounts).Error; err != nil {
		return nil, err
	}

	due := make(map[int]struct{}, len(poolIDs))
	for _, account := range accounts {
		credential, err := DecryptAccountPoolCredentialConfig(account.CredentialConfig)
		if err != nil || !strings.EqualFold(strings.TrimSpace(credential.Type), AccountPoolCredentialTypeOAuth) {
			continue
		}
		state, err := DecryptAccountPoolTokenState(account.TokenState)
		if err != nil {
			continue
		}
		action, _ := classifyAccountPoolXAIOAuthReconcileCandidate(account, credential, state, now, window)
		if action == "" || (action == AccountPoolXAIOAuthReconcileActionExpire && account.Status == model.AccountPoolAccountStatusExpired) {
			continue
		}
		due[account.PoolID] = struct{}{}
	}

	duePoolIDs := make([]int, 0, len(due))
	for _, poolID := range poolIDs {
		if _, ok := due[poolID]; ok {
			duePoolIDs = append(duePoolIDs, poolID)
		}
	}
	return duePoolIDs, nil
}

func StartAccountPoolXAIOAuthReconcileWorker(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		ctx = context.Background()
	}
	accountPoolXAIOAuthReconcileWorkerOnce.Do(func() {
		if !common.IsMasterNode {
			close(accountPoolXAIOAuthReconcileWorkerDone)
			return
		}
		interval := loadAccountPoolXAIOAuthReconcileWorkerInterval()
		gopool.Go(func() {
			defer close(accountPoolXAIOAuthReconcileWorkerDone)
			logger.LogInfo(ctx, fmt.Sprintf("account pool xai oauth reconcile worker started: tick=%s", interval))
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			select {
			case <-ctx.Done():
				return
			default:
			}
			runDueAccountPoolXAIOAuthReconcileOnceRecovering(ctx, common.GetTimestamp())
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					runDueAccountPoolXAIOAuthReconcileOnceRecovering(ctx, common.GetTimestamp())
				}
			}
		})
	})
	return accountPoolXAIOAuthReconcileWorkerDone
}

func runDueAccountPoolXAIOAuthReconcileOnceRecovering(ctx context.Context, now int64) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.LogWarn(ctx, fmt.Sprintf("account pool xai oauth reconcile worker panic recovered: %v", recovered))
		}
	}()
	runDueAccountPoolXAIOAuthReconcileOnce(ctx, now)
}

func runDueAccountPoolXAIOAuthReconcileOnce(ctx context.Context, now int64) {
	if !accountPoolXAIOAuthReconcileWorkerRunning.CompareAndSwap(false, true) {
		return
	}
	defer accountPoolXAIOAuthReconcileWorkerRunning.Store(false)

	poolIDs, err := listDueAccountPoolXAIOAuthReconcilePoolIDs(ctx, now, accountPoolXAIOAuthDefaultNearExpiryWindowSeconds)
	if err != nil {
		logger.LogWarn(ctx, "account pool xai oauth reconcile worker: list due pools failed: "+err.Error())
		return
	}
	for _, poolID := range poolIDs {
		if ctx.Err() != nil {
			return
		}
		result, err := accountPoolXAIOAuthReconcileRunner(ctx, AccountPoolXAIOAuthReconcileParams{
			PoolID:                  poolID,
			DryRun:                  false,
			NearExpiryWindowSeconds: accountPoolXAIOAuthDefaultNearExpiryWindowSeconds,
			Now:                     now,
		})
		if err != nil {
			if ctx.Err() == nil {
				logger.LogWarn(ctx, fmt.Sprintf("account pool xai oauth reconcile worker: pool_id=%d failed: %v", poolID, err))
			}
			continue
		}
		if result.Candidates > 0 {
			logger.LogInfo(ctx, fmt.Sprintf("account pool xai oauth reconcile worker: pool_id=%d candidates=%d applied=%d skipped=%d", poolID, result.Candidates, result.Applied, result.Skipped))
		}
	}
}
