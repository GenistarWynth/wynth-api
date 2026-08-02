package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetUpstreamSourceBillingProbeWorkerForTest(t *testing.T) {
	t.Helper()
	upstreamSourceBillingProbeWorkerOnce = sync.Once{}
	upstreamSourceBillingProbeWorkerDone = make(chan struct{})
	t.Cleanup(func() {
		upstreamSourceBillingProbeWorkerOnce = sync.Once{}
		upstreamSourceBillingProbeWorkerDone = make(chan struct{})
	})
}

func TestUpstreamSourceBillingProbeSchedulerBoundsBatchAndConcurrency(t *testing.T) {
	setupUpstreamSourceBillingProbeServiceTest(t)
	now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	entered := make(chan struct{}, 6)
	unblock := make(chan struct{})
	var calls atomic.Int64
	var active atomic.Int64
	var maxActive atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		current := active.Add(1)
		defer active.Add(-1)
		for {
			peak := maxActive.Load()
			if current <= peak || maxActive.CompareAndSwap(peak, current) {
				break
			}
		}
		entered <- struct{}{}
		<-unblock
		_, _ = w.Write(upstreamSourceBillingProbeSuccessBody(t, now))
	}))
	defer server.Close()
	for i := 0; i < 6; i++ {
		createUpstreamSourceBillingProbeFixture(t, server.URL, true)
	}
	svc := newUpstreamSourceBillingProbeTestService(now)
	svc.batchSize = 3
	svc.concurrency = 2

	done := make(chan error, 1)
	go func() {
		_, err := svc.RunDue(context.Background())
		done <- err
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("scheduler did not fill the configured concurrency")
		}
	}
	select {
	case <-entered:
		t.Fatal("scheduler exceeded configured concurrency")
	case <-time.After(50 * time.Millisecond):
	}
	close(unblock)
	require.NoError(t, <-done)
	assert.Equal(t, int64(3), calls.Load())
	assert.Equal(t, int64(2), maxActive.Load())
}

func TestStartUpstreamSourceBillingProbeWorkerCancelsBlockedRequestAndClosesStableDone(t *testing.T) {
	setupUpstreamSourceBillingProbeServiceTest(t)
	resetUpstreamSourceBillingProbeWorkerForTest(t)
	oldMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = oldMaster })
	requestStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-r.Context().Done()
	}))
	defer server.Close()
	createUpstreamSourceBillingProbeFixture(t, server.URL, true)

	ctx, cancel := context.WithCancel(context.Background())
	done := StartUpstreamSourceBillingProbeWorker(ctx)
	assert.Equal(t, done, StartUpstreamSourceBillingProbeWorker(context.Background()))
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("worker did not start due probe")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("billing probe worker did not stop after cancellation")
	}
}

func TestUpstreamSourceBillingProbeRunDueContainsChildPanicAndContinues(t *testing.T) {
	setupUpstreamSourceBillingProbeServiceTest(t)
	now := time.Date(2026, time.July, 13, 2, 0, 0, 0, time.UTC)
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write(upstreamSourceBillingProbeSuccessBody(t, now))
	}))
	defer server.Close()
	first := createUpstreamSourceBillingProbeFixture(t, server.URL, true)
	second := createUpstreamSourceBillingProbeFixture(t, server.URL, true)
	svc := newUpstreamSourceBillingProbeTestService(now)
	svc.batchSize = 2
	svc.concurrency = 1
	var jitterCalls atomic.Int64
	svc.jitter = func(interval time.Duration) time.Duration {
		if jitterCalls.Add(1) == 1 {
			panic("injected billing probe jitter panic")
		}
		return interval
	}

	done := make(chan struct{})
	var responses []dto.UpstreamSourceBillingProbeResponse
	var runErr error
	go func() {
		responses, runErr = svc.RunDue(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunDue deadlocked after a child panic")
	}

	require.Error(t, runErr)
	assert.NotContains(t, runErr.Error(), first.channel.Key)
	assert.Len(t, responses, 1)
	assert.Equal(t, second.mapping.Id, responses[0].MappingID)
	assert.Equal(t, int64(2), calls.Load())
	var firstProbe model.UpstreamSourceBillingProbe
	require.NoError(t, model.DB.Where("mapping_id = ?", first.mapping.Id).First(&firstProbe).Error)
	assert.Empty(t, firstProbe.LeaseToken)
	assert.Zero(t, firstProbe.LeaseStartedAt)
	assert.Equal(t, now.Add(5*time.Minute).Unix(), firstProbe.NextProbeAt)
	var secondProbe model.UpstreamSourceBillingProbe
	require.NoError(t, model.DB.Where("mapping_id = ?", second.mapping.Id).First(&secondProbe).Error)
	assert.Equal(t, model.UpstreamSourceBillingProbeStatusOK, secondProbe.Status)
}

func TestUpstreamSourceBillingProbeLocalDelayHasAbsolute24HourCadenceCap(t *testing.T) {
	tests := []struct {
		name            string
		intervalMinutes int
		jittered        time.Duration
		want            time.Duration
	}{
		{name: "below boundary", intervalMinutes: model.MaxUpstreamSourceBillingProbeIntervalMinutes - 6, jittered: 23*time.Hour + 59*time.Minute, want: 23*time.Hour + 59*time.Minute},
		{name: "at boundary", intervalMinutes: model.MaxUpstreamSourceBillingProbeIntervalMinutes - 5, jittered: 24 * time.Hour, want: 24 * time.Hour},
		{name: "adjacent interval crosses boundary", intervalMinutes: model.MaxUpstreamSourceBillingProbeIntervalMinutes - 1, jittered: 24*time.Hour + 4*time.Minute, want: 24 * time.Hour},
		{name: "maximum with positive jitter", intervalMinutes: model.MaxUpstreamSourceBillingProbeIntervalMinutes, jittered: 24*time.Hour + 5*time.Minute, want: 24 * time.Hour},
		{name: "overflow-sized injected jitter", intervalMinutes: model.MaxUpstreamSourceBillingProbeIntervalMinutes, jittered: time.Duration(1<<63 - 1), want: 24 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewUpstreamSourceBillingProbeService()
			svc.jitter = func(time.Duration) time.Duration { return tt.jittered }
			assert.Equal(t, tt.want, svc.nextDelay(tt.intervalMinutes, 0))
		})
	}
}

func TestUpstreamSourceBillingProbeLocalDelayCapDoesNotShortenRetryAfter(t *testing.T) {
	svc := NewUpstreamSourceBillingProbeService()
	svc.jitter = func(interval time.Duration) time.Duration { return interval + 5*time.Minute }
	assert.Equal(t, 25*time.Hour, svc.nextDelay(model.MaxUpstreamSourceBillingProbeIntervalMinutes, 25*time.Hour))
}
