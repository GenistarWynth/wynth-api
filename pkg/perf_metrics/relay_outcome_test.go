package perfmetrics

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordRelaySampleCommittedAbnormalStreamRecordsSingleFailure(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	hotBuckets = sync.Map{}
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		hotBuckets = sync.Map{}
	})

	start := time.Now().Add(-time.Second)
	info := &relaycommon.RelayInfo{
		OriginModelName:   "gpt-committed-stream-failure",
		UsingGroup:        "default",
		IsStream:          true,
		StartTime:         start,
		FirstResponseTime: start.Add(100 * time.Millisecond),
		StreamStatus:      relaycommon.NewStreamStatus(),
	}
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, errors.New("unexpected EOF"))

	CaptureRelayUsage(info, 3, 7)
	var finalizers sync.WaitGroup
	for i := 0; i < 256; i++ {
		finalizers.Add(1)
		go func(success bool) {
			defer finalizers.Done()
			FinalizeRelaySample(info, success)
		}(i%2 == 0)
	}
	finalizers.Wait()

	var snapshot counters
	hotBuckets.Range(func(key, value any) bool {
		bucket := key.(bucketKey)
		if bucket.model == info.OriginModelName && bucket.group == info.UsingGroup {
			snapshot = value.(*atomicBucket).snapshot()
		}
		return true
	})
	assert.EqualValues(t, 1, snapshot.requestCount)
	assert.Zero(t, snapshot.successCount)
	assert.EqualValues(t, 3, snapshot.inputTokens)
	assert.EqualValues(t, 7, snapshot.outputTokens)
}

func TestRelayOutcomeConcurrentUsageCaptureRetainsRichestPartialUsage(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	hotBuckets = sync.Map{}
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		hotBuckets = sync.Map{}
	})

	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-concurrent-partial-usage",
		UsingGroup:      "default",
		IsStream:        true,
		StartTime:       time.Now().Add(-time.Second),
		StreamStatus:    relaycommon.NewStreamStatus(),
	}
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, errors.New("unexpected EOF"))

	var captures sync.WaitGroup
	for i := int64(1); i <= 128; i++ {
		captures.Add(1)
		go func(input int64) {
			defer captures.Done()
			CaptureRelayUsage(info, input, input*2)
		}(i)
	}
	captures.Wait()
	FinalizeRelaySample(info, false)

	var snapshot counters
	hotBuckets.Range(func(key, value any) bool {
		if key.(bucketKey).model == info.OriginModelName {
			snapshot = value.(*atomicBucket).snapshot()
		}
		return true
	})
	assert.EqualValues(t, 1, snapshot.requestCount)
	assert.Zero(t, snapshot.successCount)
	assert.EqualValues(t, 128, snapshot.inputTokens)
	assert.EqualValues(t, 256, snapshot.outputTokens)
}

func TestRecordRelaySampleSuccessfulStreamRecordsSingleSuccess(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	hotBuckets = sync.Map{}
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		hotBuckets = sync.Map{}
	})

	start := time.Now().Add(-time.Second)
	info := &relaycommon.RelayInfo{
		OriginModelName:   "gpt-successful-stream",
		UsingGroup:        "default",
		IsStream:          true,
		StartTime:         start,
		FirstResponseTime: start.Add(100 * time.Millisecond),
		StreamStatus:      relaycommon.NewStreamStatus(),
	}
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonEOF, nil)

	CaptureRelayUsage(info, 4, 7)
	FinalizeRelaySample(info, true)

	var snapshot counters
	found := false
	hotBuckets.Range(func(key, value any) bool {
		bucket := key.(bucketKey)
		if bucket.model == info.OriginModelName && bucket.group == info.UsingGroup {
			snapshot = value.(*atomicBucket).snapshot()
			found = true
		}
		return true
	})
	require.True(t, found)
	assert.EqualValues(t, 1, snapshot.requestCount)
	assert.EqualValues(t, 1, snapshot.successCount)
	assert.EqualValues(t, 4, snapshot.inputTokens)
	assert.EqualValues(t, 7, snapshot.outputTokens)
}

func TestRecordRelaySampleClientCancellationKeepsExistingSuccessClassification(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	hotBuckets = sync.Map{}
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		hotBuckets = sync.Map{}
	})

	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-client-canceled-stream",
		UsingGroup:      "default",
		IsStream:        true,
		StartTime:       time.Now().Add(-time.Second),
		StreamStatus:    relaycommon.NewStreamStatus(),
	}
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, context.Canceled)

	CaptureRelayUsage(info, 2, 5)
	FinalizeRelaySample(info, true)

	var snapshot counters
	hotBuckets.Range(func(key, value any) bool {
		bucket := key.(bucketKey)
		if bucket.model == info.OriginModelName && bucket.group == info.UsingGroup {
			snapshot = value.(*atomicBucket).snapshot()
		}
		return true
	})
	assert.EqualValues(t, 1, snapshot.requestCount)
	assert.EqualValues(t, 1, snapshot.successCount)
	assert.EqualValues(t, 2, snapshot.inputTokens)
	assert.EqualValues(t, 5, snapshot.outputTokens)
}
