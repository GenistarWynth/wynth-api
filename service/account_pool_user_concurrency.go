package service

import "sync"

// accountPoolUserConcurrencyKey is the composite key for per-user concurrency tracking.
type accountPoolUserConcurrencyKey struct {
	bindingID int
	userID    int
}

// accountPoolUserConcurrencyManager tracks in-flight request counts per (bindingID, userID) pair.
type accountPoolUserConcurrencyManager struct {
	mu     sync.Mutex
	active map[accountPoolUserConcurrencyKey]int
}

var accountPoolUserConcurrency = newAccountPoolUserConcurrencyManager()

func newAccountPoolUserConcurrencyManager() *accountPoolUserConcurrencyManager {
	return &accountPoolUserConcurrencyManager{
		active: make(map[accountPoolUserConcurrencyKey]int),
	}
}

// TryAcquireAccountPoolUserSlot is the exported version of tryAcquireAccountPoolUserSlot
// for use by packages outside service (e.g. relay).
func TryAcquireAccountPoolUserSlot(bindingID int, userID int, maxConcurrency int) (func(), bool) {
	return tryAcquireAccountPoolUserSlot(bindingID, userID, maxConcurrency)
}

// tryAcquireAccountPoolUserSlot attempts to acquire a per-user concurrency slot for the
// given (bindingID, userID) pair.
//
// Rules:
//   - maxConcurrency <= 0  → unlimited; returns no-op release + true.
//   - userID <= 0 || bindingID <= 0 → can't scope; returns no-op release + true.
//   - current count >= maxConcurrency → returns nil, false (slot denied).
//   - otherwise increments the counter and returns a sync.Once release func + true.
func tryAcquireAccountPoolUserSlot(bindingID int, userID int, maxConcurrency int) (func(), bool) {
	if maxConcurrency <= 0 {
		return func() {}, true
	}
	if userID <= 0 || bindingID <= 0 {
		return func() {}, true
	}
	return accountPoolUserConcurrency.tryAcquire(bindingID, userID, maxConcurrency)
}

func (m *accountPoolUserConcurrencyManager) tryAcquire(bindingID int, userID int, maxConcurrency int) (func(), bool) {
	key := accountPoolUserConcurrencyKey{bindingID: bindingID, userID: userID}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active[key] >= maxConcurrency {
		return nil, false
	}
	m.active[key]++
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.active[key] <= 1 {
				delete(m.active, key)
				return
			}
			m.active[key]--
		})
	}, true
}

// resetAccountPoolUserConcurrencyForTest resets the global concurrency manager.
// Must only be called from tests within the service package.
func resetAccountPoolUserConcurrencyForTest() {
	accountPoolUserConcurrency = newAccountPoolUserConcurrencyManager()
}

// ResetAccountPoolUserConcurrencyForTest is an exported test helper for use by
// tests in other packages (e.g. relay). Must only be called from tests.
func ResetAccountPoolUserConcurrencyForTest() {
	resetAccountPoolUserConcurrencyForTest()
}

// TryAcquireAccountPoolUserSlotForTest exposes tryAcquireAccountPoolUserSlot for
// use by tests in other packages. Must only be called from tests.
func TryAcquireAccountPoolUserSlotForTest(bindingID int, userID int, maxConcurrency int) (func(), bool) {
	return tryAcquireAccountPoolUserSlot(bindingID, userID, maxConcurrency)
}
