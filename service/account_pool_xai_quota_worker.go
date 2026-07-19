package service

import (
	"context"
	"fmt"
	"math"
	"sort"
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
	accountPoolXAIQuotaProbeIntervalEnv   = "ACCOUNT_POOL_XAI_QUOTA_PROBE_INTERVAL_MINUTES"
	accountPoolXAIQuotaProbeStaleEnv      = "ACCOUNT_POOL_XAI_QUOTA_PROBE_STALE_MINUTES"
	accountPoolXAIQuotaProbeMaxPerTickEnv = "ACCOUNT_POOL_XAI_QUOTA_PROBE_MAX_PER_TICK"

	accountPoolXAIQuotaProbeDefaultInterval   = 15 * time.Minute
	accountPoolXAIQuotaProbeDefaultStaleAge   = 60 * time.Minute
	accountPoolXAIQuotaProbeDefaultMaxPerTick = 10
)

type accountPoolXAIQuotaProbeWorkerConfig struct {
	Interval   time.Duration
	StaleAge   time.Duration
	MaxPerTick int
}

type accountPoolXAIQuotaProbeCandidate struct {
	PoolID    int
	AccountID int
	FetchedAt int64
}

type accountPoolXAIQuotaProbeRunnerFunc func(context.Context, int, int) (AccountPoolXAIQuotaSnapshot, error)

var (
	accountPoolXAIQuotaProbeWorkerOnce    sync.Once
	accountPoolXAIQuotaProbeWorkerDone    = make(chan struct{})
	accountPoolXAIQuotaProbeWorkerRunning atomic.Bool
	accountPoolXAIQuotaCreateProbeEnabled atomic.Bool
	accountPoolXAIQuotaProbeRunner        accountPoolXAIQuotaProbeRunnerFunc = func(ctx context.Context, poolID int, accountID int) (AccountPoolXAIQuotaSnapshot, error) {
		return (AccountPoolService{}).ProbeXAIQuota(ctx, poolID, accountID)
	}
)

func loadAccountPoolXAIQuotaProbeWorkerConfig() accountPoolXAIQuotaProbeWorkerConfig {
	maxPerTick := common.GetEnvOrDefault(accountPoolXAIQuotaProbeMaxPerTickEnv, accountPoolXAIQuotaProbeDefaultMaxPerTick)
	if maxPerTick <= 0 {
		maxPerTick = accountPoolXAIQuotaProbeDefaultMaxPerTick
	}
	return accountPoolXAIQuotaProbeWorkerConfig{
		Interval:   accountPoolWorkerDurationMinutesFromEnv(accountPoolXAIQuotaProbeIntervalEnv, accountPoolXAIQuotaProbeDefaultInterval),
		StaleAge:   accountPoolWorkerDurationMinutesFromEnv(accountPoolXAIQuotaProbeStaleEnv, accountPoolXAIQuotaProbeDefaultStaleAge),
		MaxPerTick: maxPerTick,
	}
}

func accountPoolWorkerDurationMinutesFromEnv(name string, fallback time.Duration) time.Duration {
	fallbackMinutes := int(fallback / time.Minute)
	minutes := common.GetEnvOrDefault(name, fallbackMinutes)
	maxMinutes := int64(math.MaxInt64) / int64(time.Minute)
	if minutes <= 0 || int64(minutes) > maxMinutes {
		return fallback
	}
	return time.Duration(minutes) * time.Minute
}

func accountPoolXAIQuotaCreateProbeEligible(platform string, credential AccountPoolCredentialConfig) bool {
	return strings.EqualFold(strings.TrimSpace(platform), model.AccountPoolPlatformXAI) &&
		strings.EqualFold(strings.TrimSpace(credential.Type), AccountPoolCredentialTypeOAuth)
}

func scheduleAccountPoolXAIQuotaProbe(poolID int, accountID int) {
	if poolID <= 0 || accountID <= 0 || !accountPoolXAIQuotaCreateProbeEnabled.Load() {
		return
	}
	gopool.Go(func() {
		if _, err := accountPoolXAIQuotaProbeRunner(context.Background(), poolID, accountID); err != nil {
			message := sanitizeAccountPoolRuntimeErrorMessage(err.Error(), 240)
			logger.LogWarn(context.Background(), fmt.Sprintf("account pool xai quota create probe failed: pool_id=%d account_id=%d error=%s", poolID, accountID, message))
		}
	})
}

func listDueAccountPoolXAIQuotaProbeCandidates(
	ctx context.Context,
	now time.Time,
	staleAge time.Duration,
	maxPerTick int,
) ([]accountPoolXAIQuotaProbeCandidate, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if staleAge <= 0 {
		staleAge = accountPoolXAIQuotaProbeDefaultStaleAge
	}
	if maxPerTick <= 0 {
		maxPerTick = accountPoolXAIQuotaProbeDefaultMaxPerTick
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
		Select("id", "pool_id", "credential_config", "runtime_options").
		Where("pool_id IN ? AND status = ?", poolIDs, model.AccountPoolAccountStatusEnabled).
		Order("pool_id asc, id asc").
		Find(&accounts).Error; err != nil {
		return nil, err
	}

	staleBefore := now.Add(-staleAge).Unix()
	candidates := make([]accountPoolXAIQuotaProbeCandidate, 0, len(accounts))
	for _, account := range accounts {
		credential, err := DecryptAccountPoolCredentialConfig(account.CredentialConfig)
		if err != nil || !strings.EqualFold(strings.TrimSpace(credential.Type), AccountPoolCredentialTypeOAuth) {
			continue
		}
		options, err := parseAccountPoolRuntimeOptions(account.RuntimeOptions)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("account pool xai quota worker: skip account_id=%d invalid runtime options", account.Id))
			continue
		}
		fetchedAt := int64(0)
		if options.XAIQuota != nil {
			fetchedAt = options.XAIQuota.FetchedAt
		}
		if fetchedAt > staleBefore {
			continue
		}
		candidates = append(candidates, accountPoolXAIQuotaProbeCandidate{
			PoolID:    account.PoolID,
			AccountID: account.Id,
			FetchedAt: fetchedAt,
		})
	}

	sort.Slice(candidates, func(i int, j int) bool {
		if candidates[i].FetchedAt != candidates[j].FetchedAt {
			return candidates[i].FetchedAt < candidates[j].FetchedAt
		}
		if candidates[i].PoolID != candidates[j].PoolID {
			return candidates[i].PoolID < candidates[j].PoolID
		}
		return candidates[i].AccountID < candidates[j].AccountID
	})
	if len(candidates) > maxPerTick {
		candidates = candidates[:maxPerTick]
	}
	return candidates, nil
}

func StartAccountPoolXAIQuotaProbeWorker(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		ctx = context.Background()
	}
	accountPoolXAIQuotaProbeWorkerOnce.Do(func() {
		accountPoolXAIQuotaCreateProbeEnabled.Store(true)
		if !common.IsMasterNode {
			close(accountPoolXAIQuotaProbeWorkerDone)
			return
		}
		config := loadAccountPoolXAIQuotaProbeWorkerConfig()
		gopool.Go(func() {
			defer close(accountPoolXAIQuotaProbeWorkerDone)
			logger.LogInfo(ctx, fmt.Sprintf("account pool xai quota worker started: tick=%s stale=%s max_per_tick=%d", config.Interval, config.StaleAge, config.MaxPerTick))
			ticker := time.NewTicker(config.Interval)
			defer ticker.Stop()

			select {
			case <-ctx.Done():
				return
			default:
			}
			runDueAccountPoolXAIQuotaProbeOnceRecovering(ctx, config)
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					runDueAccountPoolXAIQuotaProbeOnceRecovering(ctx, config)
				}
			}
		})
	})
	return accountPoolXAIQuotaProbeWorkerDone
}

func runDueAccountPoolXAIQuotaProbeOnceRecovering(ctx context.Context, config accountPoolXAIQuotaProbeWorkerConfig) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.LogWarn(ctx, fmt.Sprintf("account pool xai quota worker panic recovered: %v", recovered))
		}
	}()
	runDueAccountPoolXAIQuotaProbeOnce(ctx, config)
}

func runDueAccountPoolXAIQuotaProbeOnce(ctx context.Context, config accountPoolXAIQuotaProbeWorkerConfig) {
	if !accountPoolXAIQuotaProbeWorkerRunning.CompareAndSwap(false, true) {
		return
	}
	defer accountPoolXAIQuotaProbeWorkerRunning.Store(false)

	runAccountPoolWorkerWithLease(ctx, accountPoolXAIQuotaProbeLeaseKey, func(leaseCtx context.Context) {
		candidates, err := listDueAccountPoolXAIQuotaProbeCandidates(leaseCtx, time.Now().UTC(), config.StaleAge, config.MaxPerTick)
		if err != nil {
			logger.LogWarn(leaseCtx, "account pool xai quota worker: list candidates failed: "+err.Error())
			return
		}
		for _, candidate := range candidates {
			if leaseCtx.Err() != nil {
				return
			}
			if _, err := accountPoolXAIQuotaProbeRunner(leaseCtx, candidate.PoolID, candidate.AccountID); err != nil && leaseCtx.Err() == nil {
				message := sanitizeAccountPoolRuntimeErrorMessage(err.Error(), 240)
				logger.LogWarn(leaseCtx, fmt.Sprintf("account pool xai quota worker: probe failed pool_id=%d account_id=%d error=%s", candidate.PoolID, candidate.AccountID, message))
			}
		}
	})
}
