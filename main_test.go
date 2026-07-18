package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type lifecycleTestServer struct {
	listenResult <-chan error
	shutdownFn   func(context.Context) error
	closeFn      func() error
}

func (s *lifecycleTestServer) ListenAndServe() error {
	return <-s.listenResult
}

func (s *lifecycleTestServer) Shutdown(ctx context.Context) error {
	if s.shutdownFn == nil {
		return nil
	}
	return s.shutdownFn(ctx)
}

func (s *lifecycleTestServer) Close() error {
	if s.closeFn == nil {
		return nil
	}
	return s.closeFn()
}

func TestServerLifecycleStartedCallbackRunsOnceBeforeImmediateErrorCleanup(t *testing.T) {
	listenErr := errors.New("listen failed immediately")
	listenResult := make(chan error, 1)
	listenResult <- listenErr
	workerDone := make(chan struct{})
	close(workerDone)

	var eventsMu sync.Mutex
	events := make([]string, 0, 3)
	recordEvent := func(event string) {
		eventsMu.Lock()
		events = append(events, event)
		eventsMu.Unlock()
	}
	lifecycle := serverLifecycle{
		server:  &lifecycleTestServer{listenResult: listenResult},
		signals: make(chan os.Signal),
		timeout: time.Second,
		started: func() {
			recordEvent("started")
		},
		stopWorkers: func() {
			recordEvent("stop workers")
		},
		workerDone: []<-chan struct{}{workerDone},
		flush: func() error {
			recordEvent("flush")
			return nil
		},
	}

	err := lifecycle.run()

	assert.ErrorIs(t, err, listenErr)
	eventsMu.Lock()
	defer eventsMu.Unlock()
	assert.Equal(t, []string{"started", "stop workers", "flush"}, events)
}

func TestServerLifecycleDrainsWorkersBeforeFlush(t *testing.T) {
	listenResult := make(chan error, 1)
	shutdownCalled := make(chan struct{}, 1)
	finalHandlerDone := make(chan struct{})
	workerDone := make(chan struct{})
	flushCalled := make(chan struct{}, 1)
	stopCalled := make(chan struct{}, 1)
	signals := make(chan os.Signal, 1)

	server := &lifecycleTestServer{
		listenResult: listenResult,
		shutdownFn: func(context.Context) error {
			shutdownCalled <- struct{}{}
			<-finalHandlerDone
			listenResult <- http.ErrServerClosed
			return nil
		},
	}
	lifecycle := serverLifecycle{
		server:  server,
		signals: signals,
		timeout: time.Second,
		stopWorkers: func() {
			stopCalled <- struct{}{}
		},
		workerDone: []<-chan struct{}{workerDone},
		flush: func() error {
			flushCalled <- struct{}{}
			return nil
		},
	}

	result := make(chan error, 1)
	go func() {
		result <- lifecycle.run()
	}()
	signals <- os.Interrupt

	requireSignal(t, stopCalled, "worker cancellation")
	requireSignal(t, shutdownCalled, "HTTP shutdown")
	select {
	case <-flushCalled:
		t.Fatal("flush ran before the in-flight handler completed")
	default:
	}

	close(finalHandlerDone)
	select {
	case <-flushCalled:
		t.Fatal("flush ran before the worker drained")
	default:
	}

	close(workerDone)
	require.NoError(t, requireLifecycleResult(t, result))
	requireSignal(t, flushCalled, "quota flush")
}

func TestServerLifecycleUnexpectedListenErrorCleansUpWithoutSignal(t *testing.T) {
	listenErr := errors.New("listen failed")
	listenResult := make(chan error, 1)
	listenResult <- listenErr
	workerDone := make(chan struct{})
	close(workerDone)
	stopCalled := make(chan struct{}, 1)
	flushCalled := make(chan struct{}, 1)

	lifecycle := serverLifecycle{
		server:  &lifecycleTestServer{listenResult: listenResult},
		signals: make(chan os.Signal),
		timeout: time.Second,
		stopWorkers: func() {
			stopCalled <- struct{}{}
		},
		workerDone: []<-chan struct{}{workerDone},
		flush: func() error {
			flushCalled <- struct{}{}
			return nil
		},
	}

	err := lifecycle.run()

	assert.ErrorIs(t, err, listenErr)
	requireSignal(t, stopCalled, "worker cancellation")
	requireSignal(t, flushCalled, "quota flush")
}

func TestServerLifecycleShutdownDeadlineClosesServerAndFlushes(t *testing.T) {
	listenResult := make(chan error, 1)
	closeCalled := make(chan struct{}, 1)
	flushCalled := make(chan struct{}, 1)
	signals := make(chan os.Signal, 1)
	server := &lifecycleTestServer{
		listenResult: listenResult,
		shutdownFn: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
		closeFn: func() error {
			closeCalled <- struct{}{}
			listenResult <- http.ErrServerClosed
			return nil
		},
	}
	lifecycle := serverLifecycle{
		server:      server,
		signals:     signals,
		timeout:     time.Nanosecond,
		stopWorkers: func() {},
		flush: func() error {
			flushCalled <- struct{}{}
			return nil
		},
	}

	signals <- os.Interrupt
	err := lifecycle.run()

	assert.ErrorIs(t, err, context.DeadlineExceeded)
	requireSignal(t, closeCalled, "forced HTTP close")
	requireSignal(t, flushCalled, "quota flush")
}

func TestServerLifecycleWorkerDeadlineClosesServerAndFlushes(t *testing.T) {
	listenResult := make(chan error, 1)
	closeCalled := make(chan struct{}, 1)
	flushCalled := make(chan struct{}, 1)
	signals := make(chan os.Signal, 1)
	server := &lifecycleTestServer{
		listenResult: listenResult,
		shutdownFn: func(context.Context) error {
			return nil
		},
		closeFn: func() error {
			closeCalled <- struct{}{}
			listenResult <- http.ErrServerClosed
			return nil
		},
	}
	lifecycle := serverLifecycle{
		server:      server,
		signals:     signals,
		timeout:     time.Nanosecond,
		stopWorkers: func() {},
		workerDone:  []<-chan struct{}{make(chan struct{})},
		flush: func() error {
			flushCalled <- struct{}{}
			return nil
		},
	}

	signals <- os.Interrupt
	err := lifecycle.run()

	assert.ErrorIs(t, err, context.DeadlineExceeded)
	select {
	case <-closeCalled:
	default:
		t.Fatal("worker drain deadline did not force-close the HTTP server")
	}
	requireSignal(t, flushCalled, "quota flush")
}

func TestServerLifecycleSharedDeadlineClosesAndRecordsDeadlineOnce(t *testing.T) {
	listenResult := make(chan error, 1)
	signals := make(chan os.Signal, 1)
	workerDone := make(chan struct{})
	secondCloseErr := errors.New("second close must not run")
	closeCalls := 0
	flushCalls := 0
	server := &lifecycleTestServer{
		listenResult: listenResult,
		shutdownFn: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
		closeFn: func() error {
			closeCalls++
			if closeCalls > 1 {
				return secondCloseErr
			}
			listenResult <- http.ErrServerClosed
			return nil
		},
	}
	lifecycle := serverLifecycle{
		server:      server,
		signals:     signals,
		timeout:     time.Nanosecond,
		stopWorkers: func() {},
		workerDone:  []<-chan struct{}{workerDone},
		flush: func() error {
			flushCalls++
			return nil
		},
	}

	signals <- os.Interrupt
	err := lifecycle.run()

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 1, closeCalls, "the shared shutdown/drain deadline must force-close the server only once")
	assert.Equal(t, 1, flushCalls)
	assert.Equal(t, 1, strings.Count(err.Error(), context.DeadlineExceeded.Error()))
	assert.NotContains(t, err.Error(), secondCloseErr.Error())
}

func TestNormalizeServerShutdownTimeout(t *testing.T) {
	assert.Equal(t, 120*time.Second, normalizeServerShutdownTimeout(0))
	assert.Equal(t, 120*time.Second, normalizeServerShutdownTimeout(-time.Second))
	assert.Equal(t, 3*time.Second, normalizeServerShutdownTimeout(3*time.Second))
}

func TestConfiguredServerShutdownTimeoutUsesPositiveSeconds(t *testing.T) {
	t.Setenv("SHUTDOWN_TIMEOUT_SECONDS", "17")
	assert.Equal(t, 17*time.Second, configuredServerShutdownTimeout())
}

func TestConfiguredServerShutdownTimeoutFallsBackWhenMissing(t *testing.T) {
	oldValue, wasSet := os.LookupEnv("SHUTDOWN_TIMEOUT_SECONDS")
	require.NoError(t, os.Unsetenv("SHUTDOWN_TIMEOUT_SECONDS"))
	t.Cleanup(func() {
		if wasSet {
			require.NoError(t, os.Setenv("SHUTDOWN_TIMEOUT_SECONDS", oldValue))
			return
		}
		require.NoError(t, os.Unsetenv("SHUTDOWN_TIMEOUT_SECONDS"))
	})

	assert.Equal(t, defaultServerShutdownTimeout, configuredServerShutdownTimeout())
}

func TestConfiguredServerShutdownTimeoutFallsBackForInvalidValues(t *testing.T) {
	for name, value := range map[string]string{
		"empty":    "",
		"invalid":  "not-a-number",
		"zero":     "0",
		"negative": "-5",
		"overflow": "9223372036854775807",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SHUTDOWN_TIMEOUT_SECONDS", value)
			assert.Equal(t, defaultServerShutdownTimeout, configuredServerShutdownTimeout())
		})
	}
}

func TestServerShutdownSignalConfiguration(t *testing.T) {
	assert.ElementsMatch(t, []os.Signal{os.Interrupt, syscall.SIGTERM}, serverShutdownSignals())
	assert.Equal(t, 1, cap(newServerShutdownSignalChannel()))
}

func requireSignal(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func requireLifecycleResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for lifecycle result")
		return nil
	}
}
