package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

const (
	upstreamSourceAutoPriorityTickInterval  = time.Minute
	upstreamSourceAutoPrioritySourceTimeout = 2 * time.Minute
)

var (
	upstreamSourceAutoPriorityOnce    sync.Once
	upstreamSourceAutoPriorityRunning atomic.Bool
)

func ListDueUpstreamSourcesForAutoPriority(now int64) ([]model.UpstreamSource, error) {
	var sources []model.UpstreamSource
	if err := model.DB.Where("status = ?", model.UpstreamSourceStatusEnabled).Order("id").Find(&sources).Error; err != nil {
		return nil, err
	}

	due := make([]model.UpstreamSource, 0, len(sources))
	for _, source := range sources {
		config, err := parseUpstreamSourceSyncConfig(source.SyncConfig)
		if err != nil {
			logger.LogWarn(context.Background(), fmt.Sprintf("upstream source auto-priority: skip source_id=%d invalid sync config: %v", source.Id, err))
			continue
		}

		var mappings []model.UpstreamSourceChannelMapping
		if err := model.DB.Where("source_id = ?", source.Id).Order("id").Find(&mappings).Error; err != nil {
			return nil, err
		}
		for i := range mappings {
			if upstreamSourceMappingAutoPriorityDue(source, config, &mappings[i], now) {
				due = append(due, source)
				break
			}
		}
	}
	return due, nil
}

func (s *UpstreamSourceService) RunDueUpstreamSourceAutoPriority(ctx context.Context, now int64) []dto.UpstreamSourceAutoPriorityResult {
	due, err := ListDueUpstreamSourcesForAutoPriority(now)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("upstream source auto-priority: list due sources failed: %v", err))
		return nil
	}

	results := make([]dto.UpstreamSourceAutoPriorityResult, 0, len(due))
	for _, source := range due {
		sourceCtx, cancel := context.WithTimeout(ctx, upstreamSourceAutoPrioritySourceTimeout)
		result, err := s.RunAutoPriority(sourceCtx, source.Id, now)
		cancel()
		if result == nil {
			result = &dto.UpstreamSourceAutoPriorityResult{
				SourceID: source.Id,
			}
		}
		if err != nil {
			result.Failed++
			if result.Error == "" {
				result.Error = SanitizeUpstreamSourceError(err)
			}
			logger.LogWarn(ctx, fmt.Sprintf("upstream source auto-priority: run source_id=%d failed: %v", source.Id, err))
		}
		results = append(results, *result)
	}
	return results
}

func StartUpstreamSourceAutoPriorityWorker() {
	upstreamSourceAutoPriorityOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), fmt.Sprintf("upstream source auto-priority worker started: tick=%s", upstreamSourceAutoPriorityTickInterval))
			ticker := time.NewTicker(upstreamSourceAutoPriorityTickInterval)
			defer ticker.Stop()

			runDueUpstreamSourceAutoPriorityOnce()
			for range ticker.C {
				runDueUpstreamSourceAutoPriorityOnce()
			}
		})
	})
}

func runDueUpstreamSourceAutoPriorityOnce() {
	if !upstreamSourceAutoPriorityRunning.CompareAndSwap(false, true) {
		return
	}
	defer upstreamSourceAutoPriorityRunning.Store(false)

	now := common.GetTimestamp()
	(&UpstreamSourceService{}).RunDueUpstreamSourceAutoPriority(context.Background(), now)
}

func upstreamSourceMappingAutoPriorityDue(source model.UpstreamSource, config upstreamSourceSyncConfig, mapping *model.UpstreamSourceChannelMapping, now int64) bool {
	resolution := resolveUpstreamSourceRule(config, mapping)
	if !resolution.SyncEligible || !resolution.AutoPriorityEnabled {
		return false
	}
	if mapping == nil || mapping.LocalChannelID == 0 {
		return false
	}

	channel, err := loadChannelByID(mapping.LocalChannelID)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			logger.LogWarn(context.Background(), fmt.Sprintf("upstream source auto-priority: load channel_id=%d source_id=%d failed: %v", mapping.LocalChannelID, source.Id, err))
		}
		return false
	}
	settings := channel.GetOtherSettings()
	if !isGeneratedChannelMetadataMatching(&settings, source.Id, mapping.Id) {
		return false
	}

	intervalMinutes := resolution.AutoPriorityIntervalMinutes
	if intervalMinutes == 0 {
		return true
	}
	lastRunAt := settings.ChannelAutoPriorityLastRunAt
	if lastRunAt == 0 {
		return true
	}
	return now-lastRunAt >= int64(intervalMinutes)*60
}
