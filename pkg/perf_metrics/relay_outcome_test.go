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

	// Partial usage settlement currently submits a success sample before the
	// controller submits the committed stream failure sample.
	RecordRelaySample(info, true, 7)
	RecordRelaySample(info, false, 0)

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
	assert.EqualValues(t, 7, snapshot.outputTokens)
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

	RecordRelaySample(info, true, 7)

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

	RecordRelaySample(info, true, 5)

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
	assert.EqualValues(t, 5, snapshot.outputTokens)
}
