package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- health store ---

func TestAccountPoolProxyHealthUnknownProxyIsHealthy(t *testing.T) {
	resetAccountPoolProxyHealthForTest()

	// A proxy that has never been probed must be treated as healthy (fail-open).
	healthy := accountPoolProxyHealthy(42, 1000)

	assert.True(t, healthy, "unknown proxy must be treated as healthy (fail-open)")
}

func TestAccountPoolProxyHealthReportsUnhealthyAfterConsecutiveFailureThreshold(t *testing.T) {
	resetAccountPoolProxyHealthForTest()

	proxyID := 7
	now := int64(1000)

	// Below threshold — should still be healthy.
	for i := 0; i < accountPoolProxyHealthConsecutiveFailThreshold-1; i++ {
		recordAccountPoolProxyProbe(proxyID, false, 0, now)
		assert.True(t, accountPoolProxyHealthy(proxyID, now), "should stay healthy before threshold (%d failures)", i+1)
	}

	// Hit threshold — should flip to unhealthy.
	recordAccountPoolProxyProbe(proxyID, false, 0, now)
	assert.False(t, accountPoolProxyHealthy(proxyID, now), "should be unhealthy after %d consecutive failures", accountPoolProxyHealthConsecutiveFailThreshold)
}

func TestAccountPoolProxyHealthRecordsLatency(t *testing.T) {
	resetAccountPoolProxyHealthForTest()

	proxyID := 3
	recordAccountPoolProxyProbe(proxyID, true, 42, 1000)

	assert.Equal(t, int64(42), accountPoolProxyLatency(proxyID))
}

func TestAccountPoolProxyHealthSuccessAfterFailuresRestoresHealthy(t *testing.T) {
	resetAccountPoolProxyHealthForTest()

	proxyID := 5
	now := int64(1000)

	// Drive into unhealthy state.
	for i := 0; i < accountPoolProxyHealthConsecutiveFailThreshold; i++ {
		recordAccountPoolProxyProbe(proxyID, false, 0, now)
	}
	require.False(t, accountPoolProxyHealthy(proxyID, now), "precondition: proxy must be unhealthy")

	// One success must restore health immediately.
	recordAccountPoolProxyProbe(proxyID, true, 10, now)
	assert.True(t, accountPoolProxyHealthy(proxyID, now), "single success must restore proxy health")
}

func TestAccountPoolProxyHealthResetIsolatesState(t *testing.T) {
	proxyID := 9
	now := int64(1000)

	// Drive into unhealthy state.
	for i := 0; i < accountPoolProxyHealthConsecutiveFailThreshold; i++ {
		recordAccountPoolProxyProbe(proxyID, false, 0, now)
	}
	require.False(t, accountPoolProxyHealthy(proxyID, now), "precondition: proxy must be unhealthy")

	resetAccountPoolProxyHealthForTest()

	// After reset, same proxy must appear healthy (unknown).
	assert.True(t, accountPoolProxyHealthy(proxyID, now), "reset must clear health state")
}

// --- probe seam / injected fake ---

func TestAccountPoolProxyProbeSeamRecordsHealthAndLatencyFromFakeProbe(t *testing.T) {
	resetAccountPoolProxyHealthForTest()

	proxyID := 11
	now := int64(2000)

	// Install a fake probe that reports healthy with 25 ms latency.
	old := accountPoolProxyProbeFunc
	accountPoolProxyProbeFunc = func(_ context.Context, _ model.AccountPoolProxy, _ int) (bool, int64) {
		return true, 25
	}
	t.Cleanup(func() { accountPoolProxyProbeFunc = old })

	proxy := model.AccountPoolProxy{Id: proxyID, Protocol: "http", Host: "h.local", Port: 8080}
	runAccountPoolProxyProbeAndRecord(context.Background(), proxy, 1, now)

	assert.True(t, accountPoolProxyHealthy(proxyID, now), "fake-probe result healthy must be stored")
	assert.Equal(t, int64(25), accountPoolProxyLatency(proxyID))
}

func TestAccountPoolProxyProbeSeamRecordsUnhealthyFromFakeProbe(t *testing.T) {
	resetAccountPoolProxyHealthForTest()

	proxyID := 13
	now := int64(3000)

	// Install a fake probe that reports unhealthy.
	old := accountPoolProxyProbeFunc
	accountPoolProxyProbeFunc = func(_ context.Context, _ model.AccountPoolProxy, _ int) (bool, int64) {
		return false, 0
	}
	t.Cleanup(func() { accountPoolProxyProbeFunc = old })

	proxy := model.AccountPoolProxy{Id: proxyID, Protocol: "http", Host: "h2.local", Port: 8080}
	// Drive to threshold using the seam.
	for i := 0; i < accountPoolProxyHealthConsecutiveFailThreshold; i++ {
		runAccountPoolProxyProbeAndRecord(context.Background(), proxy, 1, now)
	}

	assert.False(t, accountPoolProxyHealthy(proxyID, now), "fake-probe unhealthy result must eventually mark proxy unhealthy")
}

// --- resolution integration ---

func TestAccountPoolProxyHealthAwareResolutionSkipsUnhealthyProxyAndReturnsFallback(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	resetAccountPoolProxyHealthForTest()

	svc := AccountPoolService{}
	fallback := createAccountPoolRuntimeTestProxy(t, svc, AccountPoolProxyCreateParams{
		Name:     "fallback",
		Protocol: "socks5",
		Host:     "fallback.local",
		Port:     1080,
	})
	primary := createAccountPoolRuntimeTestProxy(t, svc, AccountPoolProxyCreateParams{
		Name:            "primary",
		Protocol:        "http",
		Host:            "primary.local",
		Port:            8080,
		FallbackProxyID: fallback.Id,
	})

	// Mark primary as unhealthy via threshold failures.
	now := int64(5000)
	for i := 0; i < accountPoolProxyHealthConsecutiveFailThreshold; i++ {
		recordAccountPoolProxyProbe(primary.Id, false, 0, now)
	}
	require.False(t, accountPoolProxyHealthy(primary.Id, now))

	proxyURL, err := ResolveAccountPoolRuntimeProxyURL(primary.Id, 0)

	require.NoError(t, err)
	assert.Equal(t, "socks5://fallback.local:1080", proxyURL, "unhealthy primary should be skipped; fallback must be returned")
}

func TestAccountPoolProxyHealthAwareResolutionAllUnhealthyFallsBackToLast(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	resetAccountPoolProxyHealthForTest()

	svc := AccountPoolService{}
	last := createAccountPoolRuntimeTestProxy(t, svc, AccountPoolProxyCreateParams{
		Name:     "last",
		Protocol: "http",
		Host:     "last.local",
		Port:     9090,
	})
	primary := createAccountPoolRuntimeTestProxy(t, svc, AccountPoolProxyCreateParams{
		Name:            "primary",
		Protocol:        "http",
		Host:            "primary.local",
		Port:            8080,
		FallbackProxyID: last.Id,
	})

	// Mark both as unhealthy.
	now := int64(6000)
	for i := 0; i < accountPoolProxyHealthConsecutiveFailThreshold; i++ {
		recordAccountPoolProxyProbe(primary.Id, false, 0, now)
		recordAccountPoolProxyProbe(last.Id, false, 0, now)
	}

	proxyURL, err := ResolveAccountPoolRuntimeProxyURL(primary.Id, 0)

	// Must not error and must not return empty — falls back to last candidate.
	require.NoError(t, err)
	assert.NotEmpty(t, proxyURL, "all-unhealthy chain must still return a non-empty URL (fail-open on last)")
	assert.Equal(t, "http://last.local:9090", proxyURL)
}

func TestAccountPoolProxyHealthAwareResolutionUnknownProxyResolvesAsToday(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	resetAccountPoolProxyHealthForTest()

	svc := AccountPoolService{}
	proxy := createAccountPoolRuntimeTestProxy(t, svc, AccountPoolProxyCreateParams{
		Name:     "unknown-health",
		Protocol: "http",
		Host:     "unknown.local",
		Port:     7070,
	})

	// No probes recorded — proxy is "unknown".
	proxyURL, err := ResolveAccountPoolRuntimeProxyURL(proxy.Id, 0)

	require.NoError(t, err)
	assert.Equal(t, "http://unknown.local:7070", proxyURL, "unknown proxy health must resolve identically to before (fail-open)")
}

func TestAccountPoolProxyHealthAwareResolutionHealthyProxyResolvesAsToday(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	resetAccountPoolProxyHealthForTest()

	svc := AccountPoolService{}
	proxy := createAccountPoolRuntimeTestProxy(t, svc, AccountPoolProxyCreateParams{
		Name:     "healthy-proxy",
		Protocol: "http",
		Host:     "healthy.local",
		Port:     7071,
	})

	// Record a successful probe.
	now := int64(7000)
	recordAccountPoolProxyProbe(proxy.Id, true, 5, now)

	proxyURL, err := ResolveAccountPoolRuntimeProxyURL(proxy.Id, 0)

	require.NoError(t, err)
	assert.Equal(t, "http://healthy.local:7071", proxyURL, "known-healthy proxy must resolve as normal")
}

func TestStartAccountPoolProxyProberCancellationStopsBlockingProbe(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	resetAccountPoolProxyHealthForTest()
	proxy := createAccountPoolRuntimeTestProxy(t, AccountPoolService{}, AccountPoolProxyCreateParams{
		Name:     "cancel-probe",
		Protocol: "http",
		Host:     "cancel.local",
		Port:     8080,
	})

	oldMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = oldMaster })
	accountPoolProxyProberOnce = sync.Once{}
	accountPoolProxyProberDone = make(chan struct{})

	probeStarted := make(chan struct{}, 1)
	probeCanceled := make(chan struct{}, 1)
	oldProbe := accountPoolProxyProbeFunc
	accountPoolProxyProbeFunc = func(ctx context.Context, _ model.AccountPoolProxy, _ int) (bool, int64) {
		probeStarted <- struct{}{}
		<-ctx.Done()
		probeCanceled <- struct{}{}
		return false, 0
	}
	t.Cleanup(func() { accountPoolProxyProbeFunc = oldProbe })

	ctx, cancel := context.WithCancel(context.Background())
	done := StartAccountPoolProxyProber(ctx, time.Hour)
	assert.Equal(t, done, StartAccountPoolProxyProber(context.Background(), time.Hour), "repeated starts must return the stable done channel")
	requireProxyWorkerSignal(t, probeStarted, "blocking probe start")

	cancel()
	requireProxyWorkerSignal(t, probeCanceled, "probe cancellation")
	requireProxyWorkerSignal(t, done, "proxy prober shutdown")

	accountPoolProxyHealth.mu.Lock()
	health, recorded := accountPoolProxyHealth.store[proxy.Id]
	assert.True(t, !recorded || health.consecutiveFailures == 0, "shutdown cancellation must not count as a failed proxy probe")
	accountPoolProxyHealth.mu.Unlock()
}

func requireProxyWorkerSignal(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}
