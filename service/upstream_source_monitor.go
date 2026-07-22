package service

import (
	"context"
	"errors"
	"fmt"
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
	defaultUpstreamSourceMonitorConcurrency         = 4
	defaultUpstreamSourceMonitorSourceTimeout       = 2 * time.Minute
	defaultUpstreamSourceMonitorBatchTimeout        = 5 * time.Minute
	upstreamSourceMonitorTickInterval               = time.Minute
	upstreamSourceMonitorStaleAfterSeconds    int64 = 10 * 60
	upstreamSourceRetentionCleanupInterval    int64 = 24 * 60 * 60
)

var (
	upstreamSourceMonitorOnce            sync.Once
	upstreamSourceMonitorRunning         atomic.Bool
	upstreamSourceRetentionLastCleanupAt atomic.Int64
)

type UpstreamSourceMonitorResult struct {
	SourceID    int    `json:"source_id"`
	ScanID      int    `json:"scan_id"`
	Status      string `json:"status"`
	Collected   int    `json:"collected"`
	Failed      int    `json:"failed"`
	Unsupported int    `json:"unsupported"`
	Skipped     int    `json:"skipped"`
	Error       string `json:"error,omitempty"`
}

type UpstreamSourceMonitorRunner struct {
	AdapterFactory func(sourceType string) (UpstreamSourceAdapter, error)
	NotifyScan     func(context.Context, *model.UpstreamSource, int) error
	Now            func() int64
	NewToken       func() string
	MaxConcurrency int
	SourceTimeout  time.Duration
	BatchTimeout   time.Duration
	DueLimit       int
}

type upstreamSourceMonitorCollector struct {
	name      string
	supported bool
	run       func(context.Context, *model.UpstreamSource, int, int64) (bool, int, error)
}

func (r UpstreamSourceMonitorRunner) RunDue(ctx context.Context, now int64) []UpstreamSourceMonitorResult {
	if _, err := model.ReconcileStaleUpstreamSourceMonitorRuns(now, upstreamSourceMonitorStaleAfterSeconds); err != nil {
		logger.LogWarn(ctx, "upstream source monitor: stale-run reconciliation failed: "+SanitizeUpstreamSourceError(err))
	}
	due, err := model.ListDueUpstreamSourcesForMonitor(now, r.DueLimit)
	if err != nil {
		logger.LogWarn(ctx, "upstream source monitor: list due sources failed: "+SanitizeUpstreamSourceError(err))
		return nil
	}
	if len(due) == 0 {
		return []UpstreamSourceMonitorResult{}
	}

	batchCtx, cancel := context.WithTimeout(ctx, r.batchTimeout())
	defer cancel()
	workerCount := r.maxConcurrency()
	if workerCount > len(due) {
		workerCount = len(due)
	}
	jobs := make(chan model.UpstreamSource)
	results := make(chan UpstreamSourceMonitorResult, len(due))
	var workers sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for source := range jobs {
				if batchCtx.Err() != nil {
					return
				}
				result, ran := r.runSource(batchCtx, source, now)
				if ran {
					results <- result
				}
			}
		}()
	}

sendLoop:
	for _, source := range due {
		if batchCtx.Err() != nil {
			break
		}
		select {
		case jobs <- source:
		case <-batchCtx.Done():
			break sendLoop
		}
	}
	close(jobs)
	workers.Wait()
	close(results)

	collected := make([]UpstreamSourceMonitorResult, 0, len(due))
	for result := range results {
		collected = append(collected, result)
	}
	sort.Slice(collected, func(i, j int) bool { return collected[i].SourceID < collected[j].SourceID })
	return collected
}

func (r UpstreamSourceMonitorRunner) runSource(ctx context.Context, source model.UpstreamSource, scheduledAt int64) (result UpstreamSourceMonitorResult, ran bool) {
	result.SourceID = source.Id
	token := r.newToken()
	claimed, err := model.ClaimUpstreamSourceMonitor(source.Id, token, scheduledAt, upstreamSourceMonitorStaleAfterSeconds)
	if err != nil {
		result.Status = model.UpstreamSourceScanStatusFailed
		result.Error = SanitizeUpstreamSourceError(err)
		return result, true
	}
	if !claimed {
		return result, false
	}
	ran = true
	scan, err := model.CreateUpstreamSourceScan(source.Id, model.UpstreamSourceScanTypeMonitor, scheduledAt)
	if err != nil {
		result.Status = model.UpstreamSourceScanStatusFailed
		result.Error = SanitizeUpstreamSourceError(err)
		_ = model.ReleaseUpstreamSourceMonitor(source.Id, token, r.now())
		return result, true
	}
	result.ScanID = scan.Id
	result.Status = model.UpstreamSourceScanStatusFailed
	scanBaseline := false
	defer func() {
		if recovered := recover(); recovered != nil {
			result.Status = model.UpstreamSourceScanStatusFailed
			result.Error = SanitizeUpstreamSourceError(fmt.Errorf("monitor collector panic: %v", recovered))
		}
		finishedAt := r.now()
		if finishErr := model.FinishUpstreamSourceScanTx(model.DB, scan.Id, result.Status, scanBaseline, finishedAt, result.Error); finishErr != nil {
			logger.LogWarn(ctx, fmt.Sprintf("upstream source monitor: finalize source_id=%d scan_id=%d failed: %s", source.Id, scan.Id, SanitizeUpstreamSourceError(finishErr)))
		}
		if releaseErr := model.ReleaseUpstreamSourceMonitor(source.Id, token, finishedAt); releaseErr != nil {
			logger.LogWarn(ctx, fmt.Sprintf("upstream source monitor: release source_id=%d failed: %s", source.Id, SanitizeUpstreamSourceError(releaseErr)))
		}
	}()

	sourceCtx, cancel := context.WithTimeout(ctx, r.sourceTimeout())
	defer cancel()
	if _, err := loadUpstreamSourceRuntimeAuth(&source); err != nil {
		result.Error = SanitizeUpstreamSourceError(err)
		return result, true
	}
	originalAuth := source.AuthConfig
	adapter, err := r.adapterFactory()(source.Type)
	if err != nil {
		result.Error = SanitizeUpstreamSourceError(err)
		return result, true
	}
	if adapter == nil {
		result.Error = "upstream source adapter is unavailable"
		return result, true
	}

	collectors := monitorCollectorsForAdapter(adapter)
	errorsFound := make([]string, 0)
	authUnsafe := false
	contextUnsafe := false
	for _, collector := range collectors {
		startedAt := r.now()
		outcome := model.UpstreamSourceCapabilityOutcome{
			SourceID:   source.Id,
			ScanID:     scan.Id,
			Capability: collector.name,
			StartedAt:  startedAt,
		}
		if !collector.supported {
			result.Unsupported++
			outcome.Status = model.UpstreamSourceCapabilityStatusUnsupported
			outcome.FinishedAt = r.now()
			if err := model.DB.Create(&outcome).Error; err != nil {
				result.Failed++
				errorsFound = append(errorsFound, collector.name+" outcome: "+SanitizeUpstreamSourceError(err))
			}
			continue
		}
		if authUnsafe || contextUnsafe {
			result.Skipped++
			outcome.Status = model.UpstreamSourceCapabilityStatusSkipped
			if authUnsafe {
				outcome.ErrorSummary = "skipped after authentication failure"
			} else {
				outcome.ErrorSummary = SanitizeUpstreamSourceError(sourceCtx.Err())
			}
			outcome.FinishedAt = r.now()
			if err := model.DB.Create(&outcome).Error; err != nil {
				result.Failed++
				errorsFound = append(errorsFound, collector.name+" outcome: "+SanitizeUpstreamSourceError(err))
			}
			continue
		}
		if err := sourceCtx.Err(); err != nil {
			result.Failed++
			contextUnsafe = true
			sanitized := SanitizeUpstreamSourceError(err)
			outcome.Status = model.UpstreamSourceCapabilityStatusFailed
			outcome.ErrorSummary = sanitized
			outcome.FinishedAt = r.now()
			errorsFound = append(errorsFound, collector.name+": "+sanitized)
			if createErr := model.DB.Create(&outcome).Error; createErr != nil {
				errorsFound = append(errorsFound, collector.name+" outcome: "+SanitizeUpstreamSourceError(createErr))
			}
			continue
		}
		baseline, itemCount, collectErr := collector.run(sourceCtx, &source, scan.Id, r.now())
		outcome.FinishedAt = r.now()
		outcome.Baseline = baseline
		outcome.ItemCount = itemCount
		if collectErr != nil {
			result.Failed++
			sanitized := SanitizeUpstreamSourceError(collectErr)
			outcome.Status = model.UpstreamSourceCapabilityStatusFailed
			outcome.ErrorSummary = sanitized
			errorsFound = append(errorsFound, collector.name+": "+sanitized)
			_, authFailure := classifyUpstreamSourceAuthError(collectErr)
			if authFailure {
				authUnsafe = true
				recordUpstreamSourceAuthFailure(&source, collectErr, r.now())
			}
			if errors.Is(collectErr, context.Canceled) || errors.Is(collectErr, context.DeadlineExceeded) {
				contextUnsafe = true
			}
		} else {
			result.Collected++
			outcome.Status = model.UpstreamSourceCapabilityStatusSuccess
			if baseline {
				scanBaseline = true
			}
		}
		if err := model.DB.Create(&outcome).Error; err != nil {
			result.Failed++
			errorsFound = append(errorsFound, collector.name+" outcome: "+SanitizeUpstreamSourceError(err))
		}
	}
	// A later authentication failure is authoritative for the shared session,
	// even when an earlier capability succeeded. Do not let the normal
	// successful-validation write overwrite the failed auth health recorded
	// above.
	if !authUnsafe {
		persistUpstreamSourceAuthState(&source, originalAuth, r.now(), result.Collected > 0)
	}
	if len(errorsFound) > 0 {
		result.Error = SanitizeUpstreamSourceError(errors.New(strings.Join(errorsFound, "; ")))
	}
	switch {
	case result.Failed == 0:
		result.Status = model.UpstreamSourceScanStatusSuccess
	case result.Collected > 0:
		result.Status = model.UpstreamSourceScanStatusPartial
	default:
		result.Status = model.UpstreamSourceScanStatusFailed
	}
	notifyScan := r.NotifyScan
	if notifyScan == nil {
		notifyScan = NotifyUpstreamSourceMonitorScan
	}
	if err := notifyScan(ctx, &source, scan.Id); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("upstream source monitor: notify source_id=%d scan_id=%d failed: %s", source.Id, scan.Id, SanitizeUpstreamSourceError(err)))
	}
	return result, true
}

// StartUpstreamSourceMonitorWorker is the master-only background entry point.
// Database claims remain authoritative if more than one instance invokes it.
func StartUpstreamSourceMonitorWorker() {
	upstreamSourceMonitorOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), fmt.Sprintf("upstream source monitor worker started: tick=%s", upstreamSourceMonitorTickInterval))
			ticker := time.NewTicker(upstreamSourceMonitorTickInterval)
			defer ticker.Stop()
			runDueUpstreamSourceMonitorsOnce()
			for range ticker.C {
				runDueUpstreamSourceMonitorsOnce()
			}
		})
	})
}

func runDueUpstreamSourceMonitorsOnce() {
	if !upstreamSourceMonitorRunning.CompareAndSwap(false, true) {
		return
	}
	defer upstreamSourceMonitorRunning.Store(false)
	ctx := context.Background()
	now := common.GetTimestamp()
	lastCleanupAt := upstreamSourceRetentionLastCleanupAt.Load()
	if lastCleanupAt == 0 || now-lastCleanupAt >= upstreamSourceRetentionCleanupInterval {
		if _, err := model.CleanupUpstreamSourceMonitorHistory(now, model.DefaultUpstreamSourceRetentionPolicy()); err != nil {
			logger.LogWarn(ctx, "upstream source monitor: retention cleanup failed: "+SanitizeUpstreamSourceError(err))
		} else {
			upstreamSourceRetentionLastCleanupAt.Store(now)
		}
	}
	(&UpstreamSourceMonitorRunner{}).RunDue(ctx, now)
}

func UpdateUpstreamSourceMonitorSettings(sourceID int, enabled bool, intervalMinutes int, now int64) (*model.UpstreamSource, error) {
	if sourceID == 0 {
		return nil, errors.New("source ID is required")
	}
	var source model.UpstreamSource
	if err := model.DB.Where("id = ? AND status <> ?", sourceID, model.UpstreamSourceStatusDeleted).First(&source).Error; err != nil {
		return nil, err
	}
	if intervalMinutes <= 0 {
		if enabled {
			return nil, errors.New("monitor interval minutes must be positive when monitoring is enabled")
		}
		intervalMinutes = source.MonitorIntervalMinutes
	}
	if intervalMinutes > 7*24*60 {
		return nil, errors.New("monitor interval minutes cannot exceed 10080")
	}
	nextMonitorAt := int64(0)
	if enabled {
		nextMonitorAt = now
	}
	if err := model.DB.Model(&model.UpstreamSource{}).Where("id = ?", sourceID).Updates(map[string]interface{}{
		"monitor_enabled":          enabled,
		"monitor_interval_minutes": intervalMinutes,
		"next_monitor_at":          nextMonitorAt,
		"updated_time":             now,
	}).Error; err != nil {
		return nil, err
	}
	if err := model.DB.First(&source, sourceID).Error; err != nil {
		return nil, err
	}
	return &source, nil
}

func monitorCollectorsForAdapter(adapter UpstreamSourceAdapter) []upstreamSourceMonitorCollector {
	collectors := []upstreamSourceMonitorCollector{
		{name: model.UpstreamSourceCapabilityBalance},
		{name: model.UpstreamSourceCapabilityCost},
		{name: model.UpstreamSourceCapabilityRateGroup},
		{name: model.UpstreamSourceCapabilityAnnouncement},
		{name: model.UpstreamSourceCapabilitySubscriptionUsage},
	}
	if collector, ok := adapter.(UpstreamBalanceCollector); ok {
		collectors[0].supported = true
		collectors[0].run = func(ctx context.Context, source *model.UpstreamSource, scanID int, now int64) (bool, int, error) {
			snapshot, err := collector.CollectBalance(ctx, source)
			if err != nil {
				return false, 0, err
			}
			return false, 1, persistUpstreamSourceBalance(source.Id, scanID, snapshot, now)
		}
	}
	if collector, ok := adapter.(UpstreamCostCollector); ok {
		collectors[1].supported = true
		collectors[1].run = func(ctx context.Context, source *model.UpstreamSource, scanID int, now int64) (bool, int, error) {
			snapshot, err := collector.CollectCost(ctx, source)
			if err != nil {
				return false, 0, err
			}
			return false, 1, persistUpstreamSourceCost(source.Id, scanID, snapshot, now)
		}
	}
	if collector, ok := adapter.(UpstreamRateGroupCollector); ok {
		collectors[2].supported = true
		collectors[2].run = func(ctx context.Context, source *model.UpstreamSource, scanID int, now int64) (bool, int, error) {
			snapshot, err := collector.CollectRateGroups(ctx, source)
			if err != nil {
				return false, 0, err
			}
			baseline, _, err := applyUpstreamSourceRateGroupSnapshot(source, scanID, snapshot, now)
			return baseline, len(snapshot.Groups), err
		}
	}
	if collector, ok := adapter.(UpstreamAnnouncementCollector); ok {
		collectors[3].supported = true
		collectors[3].run = func(ctx context.Context, source *model.UpstreamSource, scanID int, now int64) (bool, int, error) {
			snapshot, err := collector.CollectAnnouncements(ctx, source)
			if err != nil {
				return false, 0, err
			}
			baseline, _, err := persistUpstreamSourceAnnouncements(source.Id, scanID, snapshot, now)
			return baseline, len(snapshot.Items), err
		}
	}
	if collector, ok := adapter.(UpstreamSubscriptionUsageCollector); ok {
		collectors[4].supported = true
		collectors[4].run = func(ctx context.Context, source *model.UpstreamSource, scanID int, now int64) (bool, int, error) {
			snapshot, err := collector.CollectSubscriptionUsage(ctx, source)
			if err != nil {
				return false, 0, err
			}
			_, err = persistUpstreamSourceSubscriptionUsage(source.Id, scanID, snapshot, now)
			return false, len(snapshot.Subscriptions), err
		}
	}
	return collectors
}

func (r UpstreamSourceMonitorRunner) adapterFactory() func(string) (UpstreamSourceAdapter, error) {
	if r.AdapterFactory != nil {
		return r.AdapterFactory
	}
	return DefaultUpstreamSourceAdapterFactory
}

func (r UpstreamSourceMonitorRunner) maxConcurrency() int {
	if r.MaxConcurrency > 0 {
		return r.MaxConcurrency
	}
	return defaultUpstreamSourceMonitorConcurrency
}

func (r UpstreamSourceMonitorRunner) sourceTimeout() time.Duration {
	if r.SourceTimeout > 0 {
		return r.SourceTimeout
	}
	return defaultUpstreamSourceMonitorSourceTimeout
}

func (r UpstreamSourceMonitorRunner) batchTimeout() time.Duration {
	if r.BatchTimeout > 0 {
		return r.BatchTimeout
	}
	return defaultUpstreamSourceMonitorBatchTimeout
}

func (r UpstreamSourceMonitorRunner) now() int64 {
	if r.Now != nil {
		return r.Now()
	}
	return common.GetTimestamp()
}

func (r UpstreamSourceMonitorRunner) newToken() string {
	if r.NewToken != nil {
		return r.NewToken()
	}
	return common.GetUUID()
}
