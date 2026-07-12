package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenConcurrencyAcquireReleaseAndBatch(t *testing.T) {
	setupAccountPoolRedisForTest(t)
	release1 := AcquireTokenConcurrencyLease(context.Background(), 11)
	release2 := AcquireTokenConcurrencyLease(context.Background(), 11)
	release3 := AcquireTokenConcurrencyLease(context.Background(), 12)
	require.Equal(t, map[int]int{11: 2, 12: 1, 13: 0}, GetTokenConcurrencyCounts(context.Background(), []int{11, 12, 13}))
	release1()
	release1()
	assert.Equal(t, 1, GetTokenConcurrencyCounts(context.Background(), []int{11})[11])
	release2()
	release3()
}

func TestTokenConcurrencyLeaseRefreshesUntilRelease(t *testing.T) {
	mr := setupAccountPoolRedisForTest(t)
	oldTTL := tokenConcurrencyLeaseTTL
	tokenConcurrencyLeaseTTL = 90 * time.Millisecond
	t.Cleanup(func() { tokenConcurrencyLeaseTTL = oldTTL })

	release := AcquireTokenConcurrencyLease(context.Background(), 11)
	require.Eventually(t, func() bool {
		mr.FastForward(35 * time.Millisecond)
		return GetTokenConcurrencyCounts(context.Background(), []int{11})[11] == 1
	}, time.Second, 35*time.Millisecond)
	release()
	require.Eventually(t, func() bool {
		return GetTokenConcurrencyCounts(context.Background(), []int{11})[11] == 0
	}, time.Second, 10*time.Millisecond)
}

func TestTokenConcurrencyRedisFailureIsObservational(t *testing.T) {
	mr := setupAccountPoolRedisForTest(t)
	mr.SetError("unavailable")
	release := AcquireTokenConcurrencyLease(context.Background(), 11)
	require.NotNil(t, release)
	release()
	assert.Equal(t, map[int]int{11: 0}, GetTokenConcurrencyCounts(context.Background(), []int{11}))
}
