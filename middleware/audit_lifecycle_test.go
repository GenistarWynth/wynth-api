package middleware

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminAuditJobTrackerDrainsConcurrentJobs(t *testing.T) {
	tracker := newAdminAuditJobTracker()
	release := make(chan struct{})
	var completed atomic.Int64

	for range 128 {
		tracker.submit(func() {
			<-release
			completed.Add(1)
		})
	}
	close(release)
	tracker.drain()

	assert.EqualValues(t, 128, completed.Load())
}

func TestAdminAuditJobTrackerDrainGenerationDoesNotWaitForLaterJobs(t *testing.T) {
	tracker := newAdminAuditJobTracker()
	firstRelease := make(chan struct{})
	secondRelease := make(chan struct{})
	secondDone := make(chan struct{})

	tracker.submit(func() { <-firstRelease })
	target := tracker.snapshotGeneration()
	tracker.submit(func() {
		<-secondRelease
		close(secondDone)
	})

	drained := make(chan struct{})
	go func() {
		tracker.drainThrough(target)
		close(drained)
	}()
	close(firstRelease)
	<-drained

	select {
	case <-secondDone:
		require.Fail(t, "later generation completed before its release")
	default:
	}
	close(secondRelease)
	tracker.drain()
	<-secondDone
}

func TestAdminAuditJobTrackerConcurrentSubmitAndDrain(t *testing.T) {
	tracker := newAdminAuditJobTracker()
	var submitted sync.WaitGroup
	var completed atomic.Int64
	submitted.Add(64)
	for range 64 {
		go func() {
			defer submitted.Done()
			tracker.submit(func() { completed.Add(1) })
		}()
	}
	submitted.Wait()
	tracker.drain()

	assert.EqualValues(t, 64, completed.Load())
}
