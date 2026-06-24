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
	return accountPoolRuntimeLeases.tryAcquire(accountID, maxConcurrency)
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
