package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupAccountPoolRedisForTest starts an in-memory miniredis, points the global
// Redis client at it, enables the Redis path, and restores prior state on
// cleanup. It also resets the in-memory account-pool managers so the Redis path
// is exercised in isolation. Returns the miniredis handle for TTL fast-forward.
func setupAccountPoolRedisForTest(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)

	prevRDB := common.RDB
	prevEnabled := common.RedisEnabled
	common.RDB = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	common.RedisEnabled = true
	require.NoError(t, common.RDB.Ping(context.Background()).Err())

	ResetAccountPoolRuntimeForTest()
	t.Cleanup(func() {
		_ = common.RDB.Close()
		common.RDB = prevRDB
		common.RedisEnabled = prevEnabled
		mr.Close()
		ResetAccountPoolRuntimeForTest()
	})
	return mr
}

// ---- Block (屏蔽) ----------------------------------------------------------

func TestAccountPoolRedisBlockBasicAndMaxSemantics(t *testing.T) {
	setupAccountPoolRedisForTest(t)
	now := common.GetTimestamp()
	const id = 7

	assert.False(t, accountPoolRuntimeBlocked(id, now), "no block initially")

	blockAccountPoolRuntime(id, now+300)
	assert.True(t, accountPoolRuntimeBlocked(id, now+10), "blocked within window")
	assert.False(t, accountPoolRuntimeBlocked(id, now+400), "not blocked past until")

	// Max-semantics: a shorter block must not shrink the window.
	blockAccountPoolRuntime(id, now+100)
	assert.True(t, accountPoolRuntimeBlocked(id, now+200), "shorter block must not lower the existing until")

	// A longer block widens the window.
	blockAccountPoolRuntime(id, now+600)
	assert.True(t, accountPoolRuntimeBlocked(id, now+500), "longer block widens the window")

	clearAccountPoolRuntimeBlock(id)
	assert.False(t, accountPoolRuntimeBlocked(id, now+10), "cleared block is gone")
}

func TestAccountPoolRedisBlockTTLExpiry(t *testing.T) {
	mr := setupAccountPoolRedisForTest(t)
	now := common.GetTimestamp()
	const id = 8

	blockAccountPoolRuntime(id, now+2)
	assert.True(t, accountPoolRuntimeBlocked(id, now+1))

	// Past the block window the Redis key should self-expire (TTL = until-now).
	mr.FastForward(3 * time.Second)
	assert.False(t, accountPoolRuntimeBlocked(id, now+1), "expired block key must be gone")
}

func TestAccountPoolRedisBlockSharedAcrossClients(t *testing.T) {
	mr := setupAccountPoolRedisForTest(t)
	now := common.GetTimestamp()
	const id = 9

	// Block via the manager (instance A), observe via a second client (instance B).
	blockAccountPoolRuntime(id, now+300)
	clientB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer clientB.Close()
	v, err := clientB.Get(context.Background(), accountPoolBlockKey(id)).Result()
	require.NoError(t, err)
	assert.NotEmpty(t, v, "block visible to another instance")
}

// ---- Affinity (亲和) -------------------------------------------------------

func TestAccountPoolRedisAffinityRememberLookupForget(t *testing.T) {
	setupAccountPoolRedisForTest(t)
	now := common.GetTimestamp()
	const key, binding, account = "sig-abc", 3, 42

	_, ok := lookupAccountPoolRuntimeAffinity(key, binding, now)
	assert.False(t, ok, "no pin initially")

	rememberAccountPoolRuntimeAffinity(key, binding, account, now)
	got, ok := lookupAccountPoolRuntimeAffinity(key, binding, now)
	require.True(t, ok, "pin found")
	assert.Equal(t, account, got)

	// Binding mismatch is a miss and evicts the stale pin.
	_, ok = lookupAccountPoolRuntimeAffinity(key, binding+1, now)
	assert.False(t, ok, "different binding is a miss")
	_, ok = lookupAccountPoolRuntimeAffinity(key, binding, now)
	assert.False(t, ok, "mismatch lookup evicted the pin")

	rememberAccountPoolRuntimeAffinity(key, binding, account, now)
	forgetAccountPoolRuntimeAffinity(key)
	_, ok = lookupAccountPoolRuntimeAffinity(key, binding, now)
	assert.False(t, ok, "forgotten pin is gone")
}

func TestAccountPoolRedisAffinityHardCapAnchoredToFirstPin(t *testing.T) {
	setupAccountPoolRedisForTest(t)
	now := common.GetTimestamp()
	const key, binding, account = "sig-hardcap", 3, 42

	rememberAccountPoolRuntimeAffinity(key, binding, account, now)
	// Refresh much later; createdAt must stay anchored to the FIRST pin.
	rememberAccountPoolRuntimeAffinity(key, binding, account, now+100)

	// Just before the hard cap (measured from the first pin) → still a hit.
	got, ok := lookupAccountPoolRuntimeAffinity(key, binding, now+accountPoolRuntimeAffinityHardCapSeconds-1)
	require.True(t, ok, "within hard cap from first pin")
	assert.Equal(t, account, got)

	// At/after the hard cap from the first pin → evicted, proving the refresh did
	// not re-anchor createdAt (else this would still be within the window).
	_, ok = lookupAccountPoolRuntimeAffinity(key, binding, now+accountPoolRuntimeAffinityHardCapSeconds+1)
	assert.False(t, ok, "hard cap anchored to first pin, not the refresh")
}

func TestAccountPoolRedisAffinityIdleTTLExpiry(t *testing.T) {
	mr := setupAccountPoolRedisForTest(t)
	now := common.GetTimestamp()
	const key, binding, account = "sig-idle", 3, 42

	rememberAccountPoolRuntimeAffinity(key, binding, account, now)
	mr.FastForward(time.Duration(accountPoolRuntimeAffinityTTLSeconds+1) * time.Second)
	_, ok := lookupAccountPoolRuntimeAffinity(key, binding, now)
	assert.False(t, ok, "idle pin expires via the sliding Redis TTL")
}

// ---- Concurrency lease (并发) ----------------------------------------------

func TestAccountPoolRedisLeaseCapAndRelease(t *testing.T) {
	setupAccountPoolRedisForTest(t)
	const id = 11

	r1, ok1 := tryAcquireAccountPoolRuntimeLease(id, 2)
	require.True(t, ok1)
	r2, ok2 := tryAcquireAccountPoolRuntimeLease(id, 2)
	require.True(t, ok2)
	_, ok3 := tryAcquireAccountPoolRuntimeLease(id, 2)
	assert.False(t, ok3, "third acquire denied at cap=2")

	r1() // release one slot
	r3, ok4 := tryAcquireAccountPoolRuntimeLease(id, 2)
	require.True(t, ok4, "slot freed by release is reusable")

	// Idempotent release: double-calling must not over-free.
	r1()
	_, okDup := tryAcquireAccountPoolRuntimeLease(id, 2)
	assert.False(t, okDup, "double release of r1 must not create a phantom slot")

	r2()
	r3()
}

func TestAccountPoolRedisLeaseGuards(t *testing.T) {
	setupAccountPoolRedisForTest(t)

	_, ok := tryAcquireAccountPoolRuntimeLease(0, 2)
	assert.False(t, ok, "accountID<=0 denied")

	release, ok := tryAcquireAccountPoolRuntimeLease(12, 0)
	require.True(t, ok, "maxConcurrency<=0 is unlimited")
	require.NotNil(t, release)
	// Unlimited path must not create a Redis ZSET key.
	exists, err := common.RDB.Exists(context.Background(), accountPoolLeaseKey(12)).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists, "unlimited lease creates no Redis key")
}

func TestAccountPoolRedisLeaseSelfHealsExpiredHolders(t *testing.T) {
	setupAccountPoolRedisForTest(t)
	const id = 13
	key := accountPoolLeaseKey(id)
	ctx := context.Background()

	// Seed a stale holder whose lease expiry is far in the past (simulating a
	// crashed instance that never released). cap=1 would normally be full.
	require.NoError(t, common.RDB.ZAdd(ctx, key, &redis.Z{Score: 1, Member: "dead-instance-token"}).Err())

	_, ok := tryAcquireAccountPoolRuntimeLease(id, 1)
	assert.True(t, ok, "expired holder must be purged, freeing the slot")

	// Only the live token should remain.
	card, err := common.RDB.ZCard(ctx, key).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), card)
}

func TestAccountPoolRedisLeaseSharedAcrossClients(t *testing.T) {
	setupAccountPoolRedisForTest(t)
	const id = 14

	// Two acquisitions against the SAME Redis store (two logical instances)
	// must share the cap — the second is denied at cap=1.
	_, ok1 := tryAcquireAccountPoolRuntimeLease(id, 1)
	require.True(t, ok1)
	_, ok2 := tryAcquireAccountPoolRuntimeLease(id, 1)
	assert.False(t, ok2, "cap is enforced across the shared Redis store")
}

// ---- Per-user concurrency (并发) -------------------------------------------

func TestAccountPoolRedisUserSlotCapAndScope(t *testing.T) {
	setupAccountPoolRedisForTest(t)
	const binding, userA, userB = 5, 100, 200

	r1, ok1 := TryAcquireAccountPoolUserSlot(binding, userA, 1)
	require.True(t, ok1)
	_, ok2 := TryAcquireAccountPoolUserSlot(binding, userA, 1)
	assert.False(t, ok2, "same (binding,user) denied at cap=1")

	// A different user has an independent slot.
	rB, okB := TryAcquireAccountPoolUserSlot(binding, userB, 1)
	require.True(t, okB, "different user is scoped to its own key")

	r1()
	rB()
}

func TestAccountPoolRedisUserSlotGuards(t *testing.T) {
	setupAccountPoolRedisForTest(t)

	release, ok := TryAcquireAccountPoolUserSlot(5, 100, 0)
	require.True(t, ok, "maxConcurrency<=0 is unlimited")
	require.NotNil(t, release)

	release, ok = TryAcquireAccountPoolUserSlot(0, 100, 1)
	require.True(t, ok, "bindingID<=0 is unscoped no-op")
	require.NotNil(t, release)

	release, ok = TryAcquireAccountPoolUserSlot(5, 0, 1)
	require.True(t, ok, "userID<=0 is unscoped no-op")
	require.NotNil(t, release)
}

// ---- Fail-safe fallback to in-memory on Redis errors -----------------------
//
// The core safety contract: a Redis op error at request time must NOT fail the
// request; it degrades to the in-memory path for that op so per-instance
// enforcement still applies. miniredis.SetError makes every command error.

func TestAccountPoolRedisBlockFallsBackToInMemoryOnError(t *testing.T) {
	mr := setupAccountPoolRedisForTest(t)
	now := common.GetTimestamp()
	const id = 21

	mr.SetError("CRASHED")
	blockAccountPoolRuntime(id, now+300)                  // Redis SET errors → in-memory block
	assert.True(t, accountPoolRuntimeBlocked(id, now+10)) // Redis GET errors → in-memory view → blocked
	mr.SetError("")
}

func TestAccountPoolRedisLeaseFallsBackToInMemoryOnError(t *testing.T) {
	mr := setupAccountPoolRedisForTest(t)
	const id = 22

	mr.SetError("CRASHED")
	r1, ok1 := tryAcquireAccountPoolRuntimeLease(id, 1) // Redis errors → in-memory acquire
	require.True(t, ok1)
	_, ok2 := tryAcquireAccountPoolRuntimeLease(id, 1) // in-memory limiter still enforces cap=1
	assert.False(t, ok2, "in-memory fallback must enforce the cap during a Redis outage")

	r1()                                                // in-memory release
	r3, ok3 := tryAcquireAccountPoolRuntimeLease(id, 1) // slot freed by the in-memory release
	require.True(t, ok3)
	r3()
	mr.SetError("")
}

func TestAccountPoolRedisAffinityFallsBackToInMemoryOnError(t *testing.T) {
	mr := setupAccountPoolRedisForTest(t)
	now := common.GetTimestamp()
	const key, binding, account = "sig-fallback", 4, 77

	mr.SetError("CRASHED")
	rememberAccountPoolRuntimeAffinity(key, binding, account, now) // Redis errors → in-memory pin
	got, ok := lookupAccountPoolRuntimeAffinity(key, binding, now) // Redis errors → in-memory view → hit
	require.True(t, ok, "in-memory fallback pin must be found during a Redis outage")
	assert.Equal(t, account, got)
	mr.SetError("")
}
