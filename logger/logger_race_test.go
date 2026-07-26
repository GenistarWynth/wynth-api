package logger

import (
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestConcurrentLoggingDoesNotRaceRotationCounter(t *testing.T) {
	common.LogWriterMu.Lock()
	previousWriter := gin.DefaultWriter
	previousErrorWriter := gin.DefaultErrorWriter
	gin.DefaultWriter = io.Discard
	gin.DefaultErrorWriter = io.Discard
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = previousWriter
		gin.DefaultErrorWriter = previousErrorWriter
		common.LogWriterMu.Unlock()
	})

	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for range 4 {
				LogInfo(nil, "concurrent rotation counter probe")
			}
		}()
	}

	close(start)
	workers.Wait()
}

func TestConcurrentLoggingCrossesInjectedRotationThresholdOnce(t *testing.T) {
	common.LogWriterMu.Lock()
	previousWriter := gin.DefaultWriter
	previousErrorWriter := gin.DefaultErrorWriter
	gin.DefaultWriter = io.Discard
	gin.DefaultErrorWriter = io.Discard
	common.LogWriterMu.Unlock()

	previousThreshold := logRotationThreshold.Load()
	logRotationHookMu.Lock()
	previousHook := logRotationHook
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	var hookOnce sync.Once
	var hookCalls atomic.Int64
	var active atomic.Int64
	var maxActive atomic.Int64
	logRotationHook = func() {
		hookCalls.Add(1)
		current := active.Add(1)
		for {
			previous := maxActive.Load()
			if current <= previous || maxActive.CompareAndSwap(previous, current) {
				break
			}
		}
		hookOnce.Do(func() { close(started) })
		<-release
		active.Add(-1)
		close(finished)
	}
	logRotationHookMu.Unlock()
	logRotationThreshold.Store(8)
	logCount.Store(0)
	setupLogWorking.Store(false)

	t.Cleanup(func() {
		<-finished
		logRotationHookMu.Lock()
		logRotationHook = previousHook
		logRotationHookMu.Unlock()
		logRotationThreshold.Store(previousThreshold)
		logCount.Store(0)
		setupLogWorking.Store(false)
		common.LogWriterMu.Lock()
		gin.DefaultWriter = previousWriter
		gin.DefaultErrorWriter = previousErrorWriter
		common.LogWriterMu.Unlock()
	})

	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 64 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			LogInfo(nil, "rotation threshold probe")
		}()
	}
	close(start)
	<-started
	workers.Wait()

	assert.EqualValues(t, 1, hookCalls.Load())
	assert.EqualValues(t, 1, maxActive.Load())
	close(release)
	<-finished
}
