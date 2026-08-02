package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"github.com/bytedance/gopkg/util/gopool"
)

const upstreamSourceBillingProbeWorkerTickInterval = time.Minute

var (
	upstreamSourceBillingProbeWorkerOnce sync.Once
	upstreamSourceBillingProbeWorkerDone = make(chan struct{})
)

func StartUpstreamSourceBillingProbeWorker(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		ctx = context.Background()
	}
	upstreamSourceBillingProbeWorkerOnce.Do(func() {
		if !common.IsMasterNode {
			close(upstreamSourceBillingProbeWorkerDone)
			return
		}
		gopool.Go(func() {
			defer close(upstreamSourceBillingProbeWorkerDone)
			logger.LogInfo(ctx, fmt.Sprintf("upstream source billing probe worker started: tick=%s", upstreamSourceBillingProbeWorkerTickInterval))
			ticker := time.NewTicker(upstreamSourceBillingProbeWorkerTickInterval)
			defer ticker.Stop()
			service := NewUpstreamSourceBillingProbeService()

			select {
			case <-ctx.Done():
				return
			default:
			}
			runDueUpstreamSourceBillingProbes(ctx, service)
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					runDueUpstreamSourceBillingProbes(ctx, service)
				}
			}
		})
	})
	return upstreamSourceBillingProbeWorkerDone
}

func runDueUpstreamSourceBillingProbes(ctx context.Context, service *UpstreamSourceBillingProbeService) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.LogWarn(ctx, "upstream source billing probe worker recovered from a tick panic")
		}
	}()
	if service == nil || ctx.Err() != nil {
		return
	}
	if _, err := service.RunDue(ctx); err != nil && ctx.Err() == nil {
		logger.LogWarn(ctx, "upstream source billing probe worker tick failed")
	}
}
