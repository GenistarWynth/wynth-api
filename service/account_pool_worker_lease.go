package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	accountPoolWorkerLeaseTTLEnv     = "ACCOUNT_POOL_WORKER_LEASE_TTL_SECONDS"
	accountPoolWorkerLeaseDefaultTTL = 120 * time.Second
	accountPoolWorkerLeaseMinTTL     = 15 * time.Second
	accountPoolWorkerLeaseMaxTTL     = time.Hour

	accountPoolXAIQuotaProbeLeaseKey     = "account_pool:xai_quota_probe"
	accountPoolXAIOAuthReconcileLeaseKey = "account_pool:xai_oauth_reconcile"
)

var accountPoolWorkerLeaseOwnerID = func() string {
	nodeName := strings.TrimSpace(common.NodeName)
	if nodeName == "" {
		nodeName = "node"
	}
	return nodeName + ":" + common.GetRandomString(16)
}()

func loadAccountPoolWorkerLeaseTTL() time.Duration {
	seconds := common.GetEnvOrDefault(accountPoolWorkerLeaseTTLEnv, int(accountPoolWorkerLeaseDefaultTTL/time.Second))
	if seconds < int(accountPoolWorkerLeaseMinTTL/time.Second) || seconds > int(accountPoolWorkerLeaseMaxTTL/time.Second) {
		return accountPoolWorkerLeaseDefaultTTL
	}
	return time.Duration(seconds) * time.Second
}

func runAccountPoolWorkerWithLease(ctx context.Context, leaseKey string, run func(context.Context)) bool {
	if run == nil {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ttl := loadAccountPoolWorkerLeaseTTL()
	acquired, err := model.AcquireAccountPoolWorkerLease(
		ctx,
		leaseKey,
		accountPoolWorkerLeaseOwnerID,
		int64(ttl/time.Second),
	)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("account pool worker lease acquire failed: key=%s error=%v", leaseKey, err))
		return false
	}
	if !acquired {
		return false
	}

	leaseCtx, cancel := context.WithCancel(ctx)
	renewDone := make(chan struct{})
	defer func() {
		cancel()
		<-renewDone
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer releaseCancel()
		if _, err := model.ReleaseAccountPoolWorkerLease(releaseCtx, leaseKey, accountPoolWorkerLeaseOwnerID); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("account pool worker lease release failed: key=%s error=%v", leaseKey, err))
		}
	}()
	gopool.Go(func() {
		defer close(renewDone)
		ticker := time.NewTicker(ttl / 3)
		defer ticker.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
				renewed, renewErr := model.RenewAccountPoolWorkerLease(
					leaseCtx,
					leaseKey,
					accountPoolWorkerLeaseOwnerID,
					int64(ttl/time.Second),
				)
				if renewErr != nil || !renewed {
					if renewErr != nil {
						logger.LogWarn(leaseCtx, fmt.Sprintf("account pool worker lease renew failed: key=%s error=%v", leaseKey, renewErr))
					} else {
						logger.LogWarn(leaseCtx, fmt.Sprintf("account pool worker lease lost: key=%s", leaseKey))
					}
					cancel()
					return
				}
			}
		}
	})

	run(leaseCtx)
	return true
}
