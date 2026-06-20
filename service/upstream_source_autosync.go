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

const upstreamSourceAutoSyncTickInterval = time.Minute

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
		if !config.AutoSyncEnabled || config.AutoSyncIntervalMinutes <= 0 {
			continue
		}
		if source.CurrentSyncToken != "" && source.SyncStartedAt > now-upstreamSourceSyncStaleAfterSeconds {
			continue
		}
		intervalSeconds := int64(config.AutoSyncIntervalMinutes) * 60
		if source.LastSyncTime > 0 && now-source.LastSyncTime < intervalSeconds {
			continue
		}
		due = append(due, source)
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
		if _, err := s.Discover(ctx, source.Id); err != nil {
			results = append(results, dto.UpstreamSourceSyncResult{
				SourceID: source.Id,
				Status:   model.UpstreamSyncStatusFailed,
				Error:    SanitizeUpstreamSourceError(err),
			})
			logger.LogWarn(ctx, fmt.Sprintf("upstream source auto-sync: discover source_id=%d failed: %v", source.Id, err))
			continue
		}
		result, err := s.Sync(ctx, source.Id)
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
