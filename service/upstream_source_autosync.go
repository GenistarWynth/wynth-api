package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	upstreamSourceAutoSyncTickInterval  = time.Minute
	upstreamSourceAutoSyncSourceTimeout = 3 * time.Minute
)

var (
	upstreamSourceAutoSyncOnce    sync.Once
	upstreamSourceAutoSyncRunning atomic.Bool
)

func ListDueUpstreamSourcesForAutoSync(now int64) ([]model.UpstreamSource, error) {
	var sources []model.UpstreamSource
	if err := model.DB.Where("status = ?", model.UpstreamSourceStatusEnabled).Order("id").Find(&sources).Error; err != nil {
		return nil, err
	}

	due := make([]model.UpstreamSource, 0, len(sources))
	for _, source := range sources {
		config, err := parseUpstreamSourceSyncConfig(source.SyncConfig)
		if err != nil {
			logger.LogWarn(context.Background(), fmt.Sprintf("upstream source auto-sync: skip source_id=%d invalid sync config: %v", source.Id, err))
			continue
		}
		if !upstreamSourceHasAutoSyncSchedule(config) {
			continue
		}
		if source.CurrentSyncToken != "" && source.SyncStartedAt > now-upstreamSourceSyncStaleAfterSeconds {
			continue
		}

		var mappings []model.UpstreamSourceChannelMapping
		if err := model.DB.Where("source_id = ?", source.Id).Order("id").Find(&mappings).Error; err != nil {
			return nil, err
		}
		if len(mappings) == 0 {
			intervalMinutes := upstreamSourceCoarseAutoSyncIntervalMinutes(config)
			if intervalMinutes <= 0 {
				continue
			}
			if source.LastSyncTime > 0 && now-source.LastSyncTime < int64(intervalMinutes)*60 {
				continue
			}
			due = append(due, source)
			continue
		}
		for i := range mappings {
			if upstreamSourceMappingAutoSyncDue(config, &mappings[i], now) {
				due = append(due, source)
				break
			}
		}
	}
	return due, nil
}

func (s *UpstreamSourceService) RunDueUpstreamSourceAutoSync(ctx context.Context, now int64) []dto.UpstreamSourceSyncResult {
	due, err := ListDueUpstreamSourcesForAutoSync(now)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("upstream source auto-sync: list due sources failed: %v", err))
		return nil
	}

	results := make([]dto.UpstreamSourceSyncResult, 0, len(due))
	for _, source := range due {
		sourceCtx, cancel := context.WithTimeout(ctx, upstreamSourceAutoSyncSourceTimeout)
		discoveryResult, err := s.Discover(sourceCtx, source.Id)
		if err != nil {
			cancel()
			results = append(results, dto.UpstreamSourceSyncResult{
				SourceID: source.Id,
				Status:   model.UpstreamSyncStatusFailed,
				Error:    SanitizeUpstreamSourceError(err),
			})
			logger.LogWarn(ctx, fmt.Sprintf("upstream source auto-sync: discover source_id=%d failed: %v", source.Id, err))
			continue
		}
		if discoveryResult == nil {
			cancel()
			continue
		}
		result, err := s.SyncDueAuto(sourceCtx, source.Id)
		cancel()
		if result == nil {
			result = &dto.UpstreamSourceSyncResult{
				SourceID: source.Id,
				Status:   model.UpstreamSyncStatusFailed,
			}
		}
		if err != nil && result.Error == "" {
			result.Error = SanitizeUpstreamSourceError(err)
		}
		results = append(results, *result)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("upstream source auto-sync: sync source_id=%d failed: %v", source.Id, err))
		}
	}
	return results
}

func StartUpstreamSourceAutoSyncWorker() {
	upstreamSourceAutoSyncOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), fmt.Sprintf("upstream source auto-sync worker started: tick=%s", upstreamSourceAutoSyncTickInterval))
			ticker := time.NewTicker(upstreamSourceAutoSyncTickInterval)
			defer ticker.Stop()

			runDueUpstreamSourceAutoSyncOnce()
			for range ticker.C {
				runDueUpstreamSourceAutoSyncOnce()
			}
		})
	})
}

func runDueUpstreamSourceAutoSyncOnce() {
	if !upstreamSourceAutoSyncRunning.CompareAndSwap(false, true) {
		return
	}
	defer upstreamSourceAutoSyncRunning.Store(false)

	now := common.GetTimestamp()
	(&UpstreamSourceService{}).RunDueUpstreamSourceAutoSync(context.Background(), now)
}
