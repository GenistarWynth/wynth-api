package service

import (
	"sync"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
)

const accountPoolRuntimeLeaseReleaseContextKey = "account_pool_runtime_lease_release"

type accountPoolRuntimeReleaseFunc func()

type accountPoolRuntimeLeaseManager struct {
	mu     sync.Mutex
	active map[int]int
}

var accountPoolRuntimeLeases = newAccountPoolRuntimeLeaseManager()
var accountPoolRuntimeSelections = newAccountPoolRuntimeSelectionRecencyManager()

// accountPoolLoadAwareEnabledVar caches the ACCOUNT_POOL_LOAD_AWARE_SCHEDULING gate read once at
// startup. Load-aware scheduling is ON by default but is a no-op at small scale by construction
// (when all candidates have equal/zero in-flight load and no latency samples). Tests may toggle it
// via setAccountPoolLoadAwareEnabledForTest.
var accountPoolLoadAwareEnabledVar = common.GetEnvOrDefaultBool("ACCOUNT_POOL_LOAD_AWARE_SCHEDULING", true)

// accountPoolLoadAwareEnabled reports whether the least-loaded tier filter is active.
func accountPoolLoadAwareEnabled() bool {
	return accountPoolLoadAwareEnabledVar
}

func setAccountPoolLoadAwareEnabledForTest(enabled bool) (restore func()) {
	prev := accountPoolLoadAwareEnabledVar
	accountPoolLoadAwareEnabledVar = enabled
	return func() { accountPoolLoadAwareEnabledVar = prev }
}

func newAccountPoolRuntimeLeaseManager() *accountPoolRuntimeLeaseManager {
	return &accountPoolRuntimeLeaseManager{active: map[int]int{}}
}

type accountPoolRuntimeSelectionRecencyManager struct {
	mu       sync.Mutex
	nextRank int64
	ranks    map[int]int64
}

func newAccountPoolRuntimeSelectionRecencyManager() *accountPoolRuntimeSelectionRecencyManager {
	return &accountPoolRuntimeSelectionRecencyManager{ranks: map[int]int64{}}
}

func tryAcquireAccountPoolRuntimeLease(accountID int, maxConcurrency int) (accountPoolRuntimeReleaseFunc, bool) {
	if accountID <= 0 {
		return nil, false
	}
	if maxConcurrency <= 0 {
		return func() {}, true
	}
	if accountPoolRedisOn() {
		release, ok, err := accountPoolRedisAcquireLease(accountPoolLeaseKey(accountID), maxConcurrency)
		if err == nil {
			if !ok {
				return nil, false
			}
			return release, true
		}
		// Redis error: fall back to per-instance in-memory leasing.
	}
	return accountPoolRuntimeLeases.tryAcquire(accountID, maxConcurrency)
}

// accountPoolRuntimeInFlightCounts returns the current in-flight lease count per account for the
// given accountIDs. This is a heuristic read used only to bias selection toward less-loaded
// accounts; a race against a concurrent acquire/release is acceptable because the lease acquire in
// selectAccountPoolAccountWithLease remains the real concurrency gate.
//
//   - Redis on: one pipelined batch of ZCARD over accountPoolLeaseKey(id) for all ids (single
//     round-trip). On ANY error it falls back to the in-memory path.
//   - Redis off: read the in-memory lease manager's active counts under its mutex.
//
// Accounts with no in-flight leases are reported as 0. The returned map always contains an entry
// for every requested id.
func accountPoolRuntimeInFlightCounts(accountIDs []int) map[int]int {
	counts := make(map[int]int, len(accountIDs))
	if len(accountIDs) == 0 {
		return counts
	}
	if accountPoolRedisOn() {
		if redisCounts, err := accountPoolRedisInFlightCounts(accountIDs); err == nil {
			return redisCounts
		}
		// Redis error: fall back to the in-memory view for this instance.
	}
	accountPoolRuntimeLeases.mu.Lock()
	defer accountPoolRuntimeLeases.mu.Unlock()
	for _, id := range accountIDs {
		counts[id] = accountPoolRuntimeLeases.active[id]
	}
	return counts
}

func rememberAccountPoolRuntimeSelection(accountID int, now int64) {
	accountPoolRuntimeSelections.remember(accountID, now)
}

func accountPoolRuntimeSelectionRank(accountID int, lastUsedAt int64) int64 {
	return accountPoolRuntimeSelections.rank(accountID, lastUsedAt)
}

func (m *accountPoolRuntimeSelectionRecencyManager) remember(accountID int, now int64) {
	if accountID <= 0 {
		return
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.nextRank < now {
		m.nextRank = now
	}
	m.nextRank++
	m.ranks[accountID] = m.nextRank
}

func (m *accountPoolRuntimeSelectionRecencyManager) rank(accountID int, lastUsedAt int64) int64 {
	if accountID <= 0 {
		return lastUsedAt
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if rank, ok := m.ranks[accountID]; ok && rank > lastUsedAt {
		return rank
	}
	return lastUsedAt
}

func (m *accountPoolRuntimeLeaseManager) tryAcquire(accountID int, maxConcurrency int) (accountPoolRuntimeReleaseFunc, bool) {
	if accountID <= 0 {
		return nil, false
	}
	if maxConcurrency <= 0 {
		return func() {}, true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active[accountID] >= maxConcurrency {
		return nil, false
	}
	m.active[accountID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.active[accountID] <= 1 {
				delete(m.active, accountID)
				return
			}
			m.active[accountID]--
		})
	}, true
}

func ReleaseAccountPoolRuntimeSelection(c *gin.Context) {
	if c == nil {
		return
	}
	value, exists := c.Get(accountPoolRuntimeLeaseReleaseContextKey)
	if !exists || value == nil {
		return
	}
	release, ok := value.(accountPoolRuntimeReleaseFunc)
	if !ok || release == nil {
		return
	}
	release()
	c.Set(accountPoolRuntimeLeaseReleaseContextKey, nil)
}

func setAccountPoolRuntimeLeaseRelease(c *gin.Context, release accountPoolRuntimeReleaseFunc) {
	if c == nil || release == nil {
		return
	}
	c.Set(accountPoolRuntimeLeaseReleaseContextKey, release)
}

func resetAccountPoolRuntimeLeasesForTest() {
	accountPoolRuntimeLeases = newAccountPoolRuntimeLeaseManager()
}

func resetAccountPoolRuntimeSelectionRecencyForTest() {
	accountPoolRuntimeSelections = newAccountPoolRuntimeSelectionRecencyManager()
}
