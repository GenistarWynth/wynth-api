package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadTestCandidate builds a minimal candidate with the fields the load-skew filter reads.
func loadTestCandidate(id int, priority int64) accountPoolAccountCandidate {
	return accountPoolAccountCandidate{
		account: model.AccountPoolAccount{
			Id:       id,
			Priority: priority,
		},
	}
}

func candidateIDSet(candidates []accountPoolAccountCandidate) map[int]struct{} {
	set := make(map[int]struct{}, len(candidates))
	for _, c := range candidates {
		set[c.account.Id] = struct{}{}
	}
	return set
}

// seedInMemoryLeases acquires n real leases for accountID against a high cap, registering cleanup.
func seedInMemoryLeases(t *testing.T, accountID, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		release, ok := tryAcquireAccountPoolRuntimeLease(accountID, 1000)
		require.True(t, ok, "seed lease must acquire")
		t.Cleanup(release)
	}
}

// TestLoadSkewSmallScaleNoOp: all-zero in-flight load must return the full input unchanged.
func TestLoadSkewSmallScaleNoOp(t *testing.T) {
	ResetAccountPoolRuntimeForTest()
	restore := setAccountPoolLoadAwareEnabledForTest(true)
	defer restore()

	candidates := []accountPoolAccountCandidate{
		loadTestCandidate(1, 100),
		loadTestCandidate(2, 100),
		loadTestCandidate(3, 100),
	}
	got := loadSkewAccountPoolCandidates(candidates)
	require.Len(t, got, len(candidates))
	assert.Equal(t, candidateIDSet(candidates), candidateIDSet(got), "no-op must keep every candidate")
}

// TestLoadSkewConcurrencyTier: in-flight {A:0,B:2,C:0} → only A,C survive.
func TestLoadSkewConcurrencyTier(t *testing.T) {
	ResetAccountPoolRuntimeForTest()
	restore := setAccountPoolLoadAwareEnabledForTest(true)
	defer restore()

	seedInMemoryLeases(t, 2, 2) // account B has 2 in-flight

	candidates := []accountPoolAccountCandidate{
		loadTestCandidate(1, 100), // A
		loadTestCandidate(2, 100), // B (busy)
		loadTestCandidate(3, 100), // C
	}
	got := loadSkewAccountPoolCandidates(candidates)
	assert.Equal(t, map[int]struct{}{1: {}, 3: {}}, candidateIDSet(got), "busy account B must be excluded")
}

// TestLoadSkewDisabledIsByteIdentical: disabled gate returns the exact input slice regardless of load.
func TestLoadSkewDisabledIsByteIdentical(t *testing.T) {
	ResetAccountPoolRuntimeForTest()
	restore := setAccountPoolLoadAwareEnabledForTest(false)
	defer restore()

	seedInMemoryLeases(t, 2, 5) // even with skewed load, disabled must ignore it

	candidates := []accountPoolAccountCandidate{
		loadTestCandidate(1, 100),
		loadTestCandidate(2, 100),
	}
	got := loadSkewAccountPoolCandidates(candidates)
	require.Len(t, got, len(candidates))
	assert.Equal(t, candidateIDSet(candidates), candidateIDSet(got))
}

// TestLoadSkewSingleCandidateNoOp: len<=1 short-circuits without touching in-flight counts.
func TestLoadSkewSingleCandidateNoOp(t *testing.T) {
	ResetAccountPoolRuntimeForTest()
	restore := setAccountPoolLoadAwareEnabledForTest(true)
	defer restore()

	candidates := []accountPoolAccountCandidate{loadTestCandidate(1, 100)}
	got := loadSkewAccountPoolCandidates(candidates)
	assert.Equal(t, candidateIDSet(candidates), candidateIDSet(got))
}

// TestLoadSkewEqualNonzeroInFlight: when ALL candidates carry equal non-zero in-flight counts,
// loadSkew must return the full set (no-op), preserving weighted/RR distribution unchanged.
func TestLoadSkewEqualNonzeroInFlight(t *testing.T) {
	ResetAccountPoolRuntimeForTest()
	restore := setAccountPoolLoadAwareEnabledForTest(true)
	defer restore()

	// Give every candidate exactly 2 in-flight leases — equal load at non-zero.
	seedInMemoryLeases(t, 1, 2)
	seedInMemoryLeases(t, 2, 2)
	seedInMemoryLeases(t, 3, 2)

	candidates := []accountPoolAccountCandidate{
		loadTestCandidate(1, 100),
		loadTestCandidate(2, 100),
		loadTestCandidate(3, 100),
	}
	got := loadSkewAccountPoolCandidates(candidates)
	require.Len(t, got, len(candidates), "equal non-zero in-flight must keep all candidates")
	assert.Equal(t, candidateIDSet(candidates), candidateIDSet(got))
}

// TestLoadSkewStarvationResolvesUnderLoad: an account that starts carrying more load than another
// must exit the min-load tier, making the lighter-loaded account the sole selectable candidate.
// This proves a "slower" or less-preferred account becomes selectable once peers carry load,
// preventing the starvation loop the latency hard-exclusion would have caused.
func TestLoadSkewStarvationResolvesUnderLoad(t *testing.T) {
	ResetAccountPoolRuntimeForTest()
	restore := setAccountPoolLoadAwareEnabledForTest(true)
	defer restore()

	// Phase 1: A and B both at 0 in-flight → both selectable.
	candidatesPhase1 := []accountPoolAccountCandidate{
		loadTestCandidate(10, 100), // A
		loadTestCandidate(20, 100), // B
	}
	got1 := loadSkewAccountPoolCandidates(candidatesPhase1)
	assert.Equal(t, candidateIDSet(candidatesPhase1), candidateIDSet(got1), "phase 1: both at zero in-flight, both must be selectable")

	// Phase 2: raise A's in-flight above B's → B becomes the sole min-load candidate.
	seedInMemoryLeases(t, 10, 3) // A now has 3 in-flight, B still 0
	candidatesPhase2 := []accountPoolAccountCandidate{
		loadTestCandidate(10, 100), // A (busy)
		loadTestCandidate(20, 100), // B (idle)
	}
	got2 := loadSkewAccountPoolCandidates(candidatesPhase2)
	assert.Equal(t, map[int]struct{}{20: {}}, candidateIDSet(got2), "phase 2: B must be the sole min-load candidate when A carries load")
}

// TestSelectAccountPoolCandidatePriorityDominatesLoad: a less-loaded LOWER-priority account must
// NOT be chosen over a higher-priority (busier) one. Priority filter runs before the load skew.
func TestSelectAccountPoolCandidatePriorityDominatesLoad(t *testing.T) {
	ResetAccountPoolRuntimeForTest()
	restore := setAccountPoolLoadAwareEnabledForTest(true)
	defer restore()

	seedInMemoryLeases(t, 1, 3) // the high-priority account is busy

	candidates := []accountPoolAccountCandidate{
		loadTestCandidate(1, 100), // high priority, busy
		loadTestCandidate(2, 50),  // lower priority, idle
	}
	// round_robin policy is deterministic for a single survivor.
	selected := selectAccountPoolCandidate(candidates, "round_robin")
	assert.Equal(t, 1, selected.account.Id, "must pick the higher-priority account despite its higher load")
}

// TestInFlightCountsInMemory verifies the heuristic read reflects acquired in-memory leases.
func TestInFlightCountsInMemory(t *testing.T) {
	ResetAccountPoolRuntimeForTest()
	seedInMemoryLeases(t, 5, 3)
	seedInMemoryLeases(t, 6, 1)

	counts := accountPoolRuntimeInFlightCounts([]int{5, 6, 7})
	assert.Equal(t, 3, counts[5])
	assert.Equal(t, 1, counts[6])
	assert.Equal(t, 0, counts[7], "account with no leases reports 0")
}

// TestInFlightCountsRedis verifies the Redis batch reflects per-account ZCARD via real leases.
func TestInFlightCountsRedis(t *testing.T) {
	setupAccountPoolRedisForTest(t)

	// Acquire real Redis leases so the live-member ZCOUNT reflects them.
	for i := 0; i < 3; i++ {
		release, ok := tryAcquireAccountPoolRuntimeLease(5, 1000)
		require.True(t, ok)
		t.Cleanup(release)
	}
	release, ok := tryAcquireAccountPoolRuntimeLease(6, 1000)
	require.True(t, ok)
	t.Cleanup(release)

	counts := accountPoolRuntimeInFlightCounts([]int{5, 6, 7})
	assert.Equal(t, 3, counts[5])
	assert.Equal(t, 1, counts[6])
	assert.Equal(t, 0, counts[7])
}

// TestLoadSkewUsesRedisCounts confirms the load tier uses Redis-sourced in-flight counts.
func TestLoadSkewUsesRedisCounts(t *testing.T) {
	setupAccountPoolRedisForTest(t)
	restore := setAccountPoolLoadAwareEnabledForTest(true)
	defer restore()

	for i := 0; i < 2; i++ {
		release, ok := tryAcquireAccountPoolRuntimeLease(2, 1000) // account B busy in Redis
		require.True(t, ok)
		t.Cleanup(release)
	}

	candidates := []accountPoolAccountCandidate{
		loadTestCandidate(1, 100),
		loadTestCandidate(2, 100),
		loadTestCandidate(3, 100),
	}
	got := loadSkewAccountPoolCandidates(candidates)
	assert.Equal(t, map[int]struct{}{1: {}, 3: {}}, candidateIDSet(got), "load tier must use Redis ZCOUNT")
}

// TestAffinityFallbackLandsOnLoadBest: when the affinity-pinned account is full (in attempted),
// the next pick via selectAccountPoolAccountWithLease must land on the least-loaded remaining
// account, not just any remaining account.
func TestAffinityFallbackLandsOnLoadBest(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	ResetAccountPoolRuntimeForTest()
	restore := setAccountPoolLoadAwareEnabledForTest(true)
	defer restore()

	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	pinned := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "pinned", Priority: 100, Weight: 0, MaxConcurrency: 1, MaxConcurrencySet: true,
	})
	busy := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "busy", Priority: 100, Weight: 0,
	})
	idle := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "idle", Priority: 100, Weight: 0,
	})

	// Fill the pinned account so its lease acquire fails → it joins the per-request attempted set.
	pinnedRelease, ok := tryAcquireAccountPoolRuntimeLease(pinned.Id, 1)
	require.True(t, ok)
	t.Cleanup(pinnedRelease)

	// Make "busy" carry load so the load skew prefers "idle" among the non-pinned candidates.
	seedInMemoryLeases(t, busy.Id, 2)

	result, release, err := SelectAccountPoolAccountWithLease(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		AttemptedAccountIDs:  map[int]struct{}{pinned.Id: {}},
		Now:                  100,
	})
	require.NoError(t, err)
	t.Cleanup(release)
	assert.Equal(t, idle.Id, result.AccountID, "fallback must land on the least-loaded remaining account")
}
