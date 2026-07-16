package service

import (
	"context"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

// accountPoolProxyHealthConsecutiveFailThreshold is the number of consecutive probe
// failures required before a proxy is marked unhealthy. A single success always
// restores health immediately.
const accountPoolProxyHealthConsecutiveFailThreshold = 2

// accountPoolProxyProberTickInterval is the default probe interval. Exposed as a var
// so tests that exercise the ticker can override it; background worker usage is
// guarded by sync.Once so the override must be set before StartAccountPoolProxyProber.
var accountPoolProxyProberTickInterval = 5 * time.Minute

// proxyHealth holds the most recent observation for a single proxy.
type proxyHealth struct {
	healthy             bool
	lastLatencyMS       int64
	lastProbeAt         int64
	consecutiveFailures int
}

// accountPoolProxyHealthStore is the in-memory health map, keyed by proxy ID.
type accountPoolProxyHealthStore struct {
	mu    sync.Mutex
	store map[int]proxyHealth
}

func newAccountPoolProxyHealthStore() *accountPoolProxyHealthStore {
	return &accountPoolProxyHealthStore{store: make(map[int]proxyHealth)}
}

var accountPoolProxyHealth = newAccountPoolProxyHealthStore()

// recordAccountPoolProxyProbe updates the health store for proxyID.
// healthy=true resets consecutive failures and marks the proxy healthy immediately.
// healthy=false increments consecutive failures; the proxy is marked unhealthy only
// after reaching accountPoolProxyHealthConsecutiveFailThreshold.
// now is a Unix timestamp (seconds); pass 0 to use the current time.
func recordAccountPoolProxyProbe(proxyID int, healthy bool, latencyMS int64, now int64) {
	if proxyID <= 0 {
		return
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	accountPoolProxyHealth.mu.Lock()
	defer accountPoolProxyHealth.mu.Unlock()

	h := accountPoolProxyHealth.store[proxyID]
	h.lastProbeAt = now
	h.lastLatencyMS = latencyMS
	if healthy {
		h.healthy = true
		h.consecutiveFailures = 0
	} else {
		h.consecutiveFailures++
		if h.consecutiveFailures >= accountPoolProxyHealthConsecutiveFailThreshold {
			h.healthy = false
		}
	}
	accountPoolProxyHealth.store[proxyID] = h
}

// accountPoolProxyHealthy reports whether the proxy with the given ID is considered
// healthy at the given now timestamp. Unknown (never-probed) proxies return true
// (fail-open), so health probing being disabled or not-yet-run never blocks resolution.
func accountPoolProxyHealthy(proxyID int, _ int64) bool {
	if proxyID <= 0 {
		return true
	}
	accountPoolProxyHealth.mu.Lock()
	defer accountPoolProxyHealth.mu.Unlock()

	h, ok := accountPoolProxyHealth.store[proxyID]
	if !ok {
		// Never probed — treat as healthy (fail-open).
		return true
	}
	// Once the threshold has been reached consecutiveFailures reflects unhealthy;
	// otherwise the proxy is still healthy.
	return h.healthy || h.consecutiveFailures < accountPoolProxyHealthConsecutiveFailThreshold
}

// accountPoolProxyLatency returns the last recorded latency in milliseconds for the
// given proxyID. Returns 0 if the proxy has never been probed.
func accountPoolProxyLatency(proxyID int) int64 {
	if proxyID <= 0 {
		return 0
	}
	accountPoolProxyHealth.mu.Lock()
	defer accountPoolProxyHealth.mu.Unlock()
	return accountPoolProxyHealth.store[proxyID].lastLatencyMS
}

// resetAccountPoolProxyHealthForTest replaces the health store with a fresh one.
// Must only be called from tests.
func resetAccountPoolProxyHealthForTest() {
	accountPoolProxyHealth = newAccountPoolProxyHealthStore()
}

// --- probe seam ---

// accountPoolProxyProbeFunc is the injectable probe implementation. Tests replace it
// with a fake; production uses tcpProbeAccountPoolProxy.
// Signature: (ctx, proxy, timeoutSeconds) → (healthy, latencyMS).
var accountPoolProxyProbeFunc = tcpProbeAccountPoolProxy

// tcpProbeAccountPoolProxy performs a lightweight TCP dial to the proxy's host:port
// to check reachability. It returns healthy=true if the connection succeeds within
// timeoutSeconds, and the round-trip latency in milliseconds.
func tcpProbeAccountPoolProxy(ctx context.Context, proxy model.AccountPoolProxy, timeoutSeconds int) (bool, int64) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 10
	}
	addr := net.JoinHostPort(proxy.Host, strconv.Itoa(proxy.Port))
	start := time.Now()
	dialer := net.Dialer{Timeout: time.Duration(timeoutSeconds) * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	latencyMS := time.Since(start).Milliseconds()
	if err != nil {
		return false, latencyMS
	}
	_ = conn.Close()
	return true, latencyMS
}

// runAccountPoolProxyProbeAndRecord calls the injectable probe function for proxy and
// records the result in the health store with now as the observation timestamp.
func runAccountPoolProxyProbeAndRecord(ctx context.Context, proxy model.AccountPoolProxy, timeoutSeconds int, now int64) {
	if ctx == nil {
		ctx = context.Background()
	}
	healthy, latencyMS := accountPoolProxyProbeFunc(ctx, proxy, timeoutSeconds)
	if ctx.Err() != nil {
		return
	}
	recordAccountPoolProxyProbe(proxy.Id, healthy, latencyMS, now)
}

// --- background prober ---

var (
	accountPoolProxyProberOnce sync.Once
	accountPoolProxyProberDone = make(chan struct{})
)

// StartAccountPoolProxyProber starts a background worker that probes all enabled
// account-pool proxies on each tick of the configured interval, updating the in-memory
// health store. It is a no-op on non-master nodes. It can be called multiple times
// safely; the prober runs at most once per process.
func StartAccountPoolProxyProber(ctx context.Context, interval time.Duration) <-chan struct{} {
	if ctx == nil {
		ctx = context.Background()
	}
	if interval <= 0 {
		interval = accountPoolProxyProberTickInterval
	}
	accountPoolProxyProberOnce.Do(func() {
		if !common.IsMasterNode {
			close(accountPoolProxyProberDone)
			return
		}
		gopool.Go(func() {
			defer close(accountPoolProxyProberDone)
			logger.LogInfo(ctx, "account pool proxy prober started")
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			runAccountPoolProxyProberOnceRecovering(ctx)
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					runAccountPoolProxyProberOnceRecovering(ctx)
				}
			}
		})
	})
	return accountPoolProxyProberDone
}

func runAccountPoolProxyProberOnceRecovering(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			logger.LogWarn(ctx, "account pool proxy prober panic recovered")
		}
	}()
	runAccountPoolProxyProberOnce(ctx)
}

func runAccountPoolProxyProberOnce(ctx context.Context) {
	// Guard: if the table doesn't exist yet (e.g. migration not applied), skip quietly.
	var proxies []model.AccountPoolProxy
	err := model.DB.WithContext(ctx).
		Where("status = ?", model.AccountPoolProxyStatusEnabled).
		Order("id asc").
		Find(&proxies).Error
	if err != nil {
		logger.LogWarn(ctx, "account pool proxy prober: list proxies failed: "+err.Error())
		return
	}

	now := common.GetTimestamp()
	// Probe each proxy with bounded concurrency (one goroutine per proxy, capped by gopool).
	var wg sync.WaitGroup
	for _, p := range proxies {
		p := p
		wg.Add(1)
		gopool.Go(func() {
			defer wg.Done()
			runAccountPoolProxyProbeAndRecord(ctx, p, 10, now)
		})
	}
	wg.Wait()
}
