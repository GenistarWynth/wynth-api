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
	accountPoolCapabilityAutoDetectTickInterval  = time.Minute
	accountPoolCapabilityAutoDetectJobOverhead   = 10 * time.Second
	accountPoolCapabilityAutoDetectMaxJobTimeout = 15 * time.Minute
)

var (
	accountPoolCapabilityAutoDetectOnce    sync.Once
	accountPoolCapabilityAutoDetectRunning atomic.Bool
	accountPoolCapabilityAutoDetectDone    = make(chan struct{})
)

type accountPoolCapabilityAutoDetectJob struct {
	pool       model.AccountPool
	accountIDs []int
	request    AccountPoolCapabilityDetectRequest
}

type accountPoolCapabilityAutoDetectDetector func(context.Context, AccountPoolCapabilityDetectRequest) (AccountPoolCapabilityPoolResult, error)

func listDueAccountPoolCapabilityAutoDetectJobs(ctx context.Context, now int64) ([]accountPoolCapabilityAutoDetectJob, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}

	var pools []model.AccountPool
	if err := model.DB.WithContext(ctx).
		Where("status = ? AND capability_check_enabled = ?", model.AccountPoolStatusEnabled, true).
		Order("id asc").
		Find(&pools).Error; err != nil {
		return nil, err
	}

	jobs := make([]accountPoolCapabilityAutoDetectJob, 0, len(pools))
	for _, pool := range pools {
		request, err := accountPoolCapabilityAutoDetectRequest(pool)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("account pool capability auto-detect: skip pool_id=%d invalid config: %v", pool.Id, err))
			continue
		}

		intervalMinutes := normalizeAccountPoolCapabilityCheckIntervalMinutes(true, pool.CapabilityCheckIntervalMinutes)
		cutoff := now - int64(intervalMinutes)*60
		var accounts []model.AccountPoolAccount
		if err := model.DB.WithContext(ctx).
			Select("id").
			Where("pool_id = ? AND status = ? AND (last_capability_check_at = ? OR last_capability_check_at <= ?)", pool.Id, model.AccountPoolAccountStatusEnabled, 0, cutoff).
			Order("id asc").
			Find(&accounts).Error; err != nil {
			return nil, err
		}
		if len(accounts) == 0 {
			continue
		}

		accountIDs := make([]int, 0, len(accounts))
		for _, account := range accounts {
			accountIDs = append(accountIDs, account.Id)
		}
		request.AccountIDs = accountIDs
		jobs = append(jobs, accountPoolCapabilityAutoDetectJob{
			pool:       pool,
			accountIDs: accountIDs,
			request:    request,
		})
	}
	return jobs, nil
}

func accountPoolCapabilityAutoDetectRequest(pool model.AccountPool) (AccountPoolCapabilityDetectRequest, error) {
	mode, err := normalizeAccountPoolCapabilityCheckMode(pool.CapabilityCheckMode)
	if err != nil {
		return AccountPoolCapabilityDetectRequest{}, err
	}
	if pool.CapabilityCheckChannelID < 0 {
		return AccountPoolCapabilityDetectRequest{}, fmt.Errorf("capability check channel id cannot be negative")
	}
	models, err := accountPoolCapabilityAutoDetectModels(pool.CapabilityCheckModels)
	if err != nil {
		return AccountPoolCapabilityDetectRequest{}, err
	}

	return AccountPoolCapabilityDetectRequest{
		PoolID:          pool.Id,
		ChannelID:       pool.CapabilityCheckChannelID,
		Mode:            mode,
		CandidateModels: models,
		Apply:           true,
		Merge:           pool.CapabilityCheckMerge,
		TimeoutSeconds:  normalizeAccountPoolCapabilityCheckTimeoutSeconds(pool.CapabilityCheckTimeoutSeconds),
	}, nil
}

func accountPoolCapabilityAutoDetectModels(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return []string{}, nil
	}
	var models []string
	if err := common.UnmarshalJsonStr(raw, &models); err != nil {
		return nil, fmt.Errorf("parse capability check models: %w", err)
	}
	return normalizeAccountPoolCapabilityModels(models), nil
}

func (s *AccountPoolService) RunDueAccountPoolCapabilityAutoDetect(ctx context.Context, now int64) []AccountPoolCapabilityPoolResult {
	return runDueAccountPoolCapabilityAutoDetectWithDetector(ctx, now, func(ctx context.Context, req AccountPoolCapabilityDetectRequest) (AccountPoolCapabilityPoolResult, error) {
		return s.DetectPoolCapabilities(ctx, req)
	})
}

func runDueAccountPoolCapabilityAutoDetectWithDetector(ctx context.Context, now int64, detector accountPoolCapabilityAutoDetectDetector) []AccountPoolCapabilityPoolResult {
	if ctx == nil {
		ctx = context.Background()
	}
	if detector == nil {
		detector = func(ctx context.Context, req AccountPoolCapabilityDetectRequest) (AccountPoolCapabilityPoolResult, error) {
			return (&AccountPoolService{}).DetectPoolCapabilities(ctx, req)
		}
	}
	if ctx.Err() != nil {
		return nil
	}

	jobs, err := listDueAccountPoolCapabilityAutoDetectJobs(ctx, now)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		logger.LogWarn(ctx, fmt.Sprintf("account pool capability auto-detect: list due jobs failed: %v", err))
		return nil
	}

	results := make([]AccountPoolCapabilityPoolResult, 0, len(jobs))
	for _, job := range jobs {
		if ctx.Err() != nil {
			break
		}
		jobCtx, cancel := context.WithTimeout(ctx, accountPoolCapabilityAutoDetectJobTimeout(job))
		result, err := detector(jobCtx, job.request)
		cancel()
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			result = accountPoolCapabilityAutoDetectErrorResult(job, err)
			logger.LogWarn(ctx, fmt.Sprintf("account pool capability auto-detect: pool_id=%d failed: %v", job.pool.Id, err))
		}
		results = append(results, result)
	}
	return results
}

func accountPoolCapabilityAutoDetectJobTimeout(job accountPoolCapabilityAutoDetectJob) time.Duration {
	accountCount := len(job.accountIDs)
	if accountCount <= 0 {
		accountCount = 1
	}
	timeoutSeconds := normalizeAccountPoolCapabilityCheckTimeoutSeconds(job.request.TimeoutSeconds)
	timeout := time.Duration(accountCount*timeoutSeconds)*time.Second + accountPoolCapabilityAutoDetectJobOverhead
	if timeout > accountPoolCapabilityAutoDetectMaxJobTimeout {
		return accountPoolCapabilityAutoDetectMaxJobTimeout
	}
	return timeout
}

func accountPoolCapabilityAutoDetectErrorResult(job accountPoolCapabilityAutoDetectJob, err error) AccountPoolCapabilityPoolResult {
	message := sanitizeAccountPoolCapabilityError("")
	if err != nil {
		message = sanitizeAccountPoolCapabilityError(err.Error())
	}
	result := AccountPoolCapabilityPoolResult{
		Total:   len(job.accountIDs),
		Failed:  len(job.accountIDs),
		Results: make([]AccountPoolCapabilityDetectResult, 0, len(job.accountIDs)),
	}
	for _, accountID := range job.accountIDs {
		result.Results = append(result.Results, AccountPoolCapabilityDetectResult{
			AccountID:      accountID,
			Status:         AccountPoolCapabilityStatusConfigError,
			Mode:           job.request.Mode,
			DetectedModels: []string{},
			AppliedModels:  []string{},
			ModelMapping:   map[string]string{},
			Errors:         []string{message},
		})
	}
	return result
}

func StartAccountPoolCapabilityAutoDetectWorker(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		ctx = context.Background()
	}
	accountPoolCapabilityAutoDetectOnce.Do(func() {
		if !common.IsMasterNode {
			close(accountPoolCapabilityAutoDetectDone)
			return
		}
		gopool.Go(func() {
			defer close(accountPoolCapabilityAutoDetectDone)
			logger.LogInfo(ctx, fmt.Sprintf("account pool capability auto-detect worker started: tick=%s", accountPoolCapabilityAutoDetectTickInterval))
			ticker := time.NewTicker(accountPoolCapabilityAutoDetectTickInterval)
			defer ticker.Stop()

			select {
			case <-ctx.Done():
				return
			default:
			}
			runDueAccountPoolCapabilityAutoDetectOnceRecovering(ctx)
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					runDueAccountPoolCapabilityAutoDetectOnceRecovering(ctx)
				}
			}
		})
	})
	return accountPoolCapabilityAutoDetectDone
}

func runDueAccountPoolCapabilityAutoDetectOnceRecovering(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		if r := recover(); r != nil {
			logger.LogWarn(ctx, fmt.Sprintf("account pool capability auto-detect: worker tick panic: %v", r))
		}
	}()
	runDueAccountPoolCapabilityAutoDetectOnce(ctx)
}

func runDueAccountPoolCapabilityAutoDetectOnce(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return
	}
	if !accountPoolCapabilityAutoDetectRunning.CompareAndSwap(false, true) {
		return
	}
	defer accountPoolCapabilityAutoDetectRunning.Store(false)

	now := common.GetTimestamp()
	// Expiry auto-pause sweep: cheap bulk DB update, run every tick regardless of
	// per-pool capability-check config so opted-in expired accounts are persistently
	// paused + visible (runtime selection already excludes them immediately).
	if paused, err := RunAccountPoolExpiryAutoPauseContext(ctx, now); err != nil {
		if ctx.Err() != nil {
			return
		}
		logger.LogWarn(ctx, fmt.Sprintf("account pool expiry auto-pause sweep failed: %v", err))
	} else if paused > 0 {
		logger.LogInfo(ctx, fmt.Sprintf("account pool expiry auto-pause: paused %d expired account(s)", paused))
	}
	if ctx.Err() != nil {
		return
	}
	(&AccountPoolService{}).RunDueAccountPoolCapabilityAutoDetect(ctx, now)
}
