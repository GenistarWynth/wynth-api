package service

import "sync"

const (
	// accountPoolRuntimeBlockFloorSeconds is the minimum in-process block duration applied
	// to a just-failed account when no specific cooldown timestamp is available (e.g., the
	// account was expired immediately without a cooldown, or the cooldown is zero). It bridges
	// the DB-read propagation window so a re-selection cannot race with the failure write.
	accountPoolRuntimeBlockFloorSeconds = int64(120)

	// accountPoolRuntimeBlockCapSeconds caps the in-process block to bound stale state.
	// The DB remains the source of truth for real cooldown duration; the in-process block
	// only bridges the read-propagation window, hence the cap.
	accountPoolRuntimeBlockCapSeconds = int64(600)
)

type accountPoolRuntimeBlockManager struct {
	mu     sync.Mutex
	blocks map[int]int64 // accountID → blockedUntil unix timestamp
}

func newAccountPoolRuntimeBlockManager() *accountPoolRuntimeBlockManager {
	return &accountPoolRuntimeBlockManager{blocks: map[int]int64{}}
}

var accountPoolRuntimeBlocks = newAccountPoolRuntimeBlockManager()

// blockAccountPoolRuntime sets an in-process block for accountID until the given unix timestamp.
// The blockedUntil value is monotonic: it is set to max(existing, until).
// accountID <= 0 or until <= 0 are no-ops.
func blockAccountPoolRuntime(accountID int, until int64) {
	if accountID <= 0 || until <= 0 {
		return
	}
	if accountPoolRedisOn() {
		if err := accountPoolRedisBlockSet(accountID, until); err == nil {
			return
		}
		// Redis error: fall back to the in-memory block for this instance.
	}
	accountPoolRuntimeBlocks.mu.Lock()
	defer accountPoolRuntimeBlocks.mu.Unlock()
	if existing, ok := accountPoolRuntimeBlocks.blocks[accountID]; !ok || until > existing {
		accountPoolRuntimeBlocks.blocks[accountID] = until
	}
}

// accountPoolRuntimeBlocked reports whether accountID has a live in-process block at the
// given now timestamp. It lazily deletes expired entries.
func accountPoolRuntimeBlocked(accountID int, now int64) bool {
	if accountID <= 0 {
		return false
	}
	if accountPoolRedisOn() {
		blocked, err := accountPoolRedisBlocked(accountID, now)
		if err == nil {
			return blocked
		}
		// Redis error: fall back to the in-memory view for this instance.
	}
	accountPoolRuntimeBlocks.mu.Lock()
	defer accountPoolRuntimeBlocks.mu.Unlock()
	until, ok := accountPoolRuntimeBlocks.blocks[accountID]
	if !ok {
		return false
	}
	if now < until {
		return true
	}
	// Lazily remove expired entry.
	delete(accountPoolRuntimeBlocks.blocks, accountID)
	return false
}

// clearAccountPoolRuntimeBlock removes any in-process block for accountID.
func clearAccountPoolRuntimeBlock(accountID int) {
	if accountID <= 0 {
		return
	}
	if accountPoolRedisOn() {
		accountPoolRedisBlockClear(accountID)
	}
	// Always clear the in-memory entry too; harmless when Redis is authoritative.
	accountPoolRuntimeBlocks.mu.Lock()
	defer accountPoolRuntimeBlocks.mu.Unlock()
	delete(accountPoolRuntimeBlocks.blocks, accountID)
}

// resetAccountPoolRuntimeBlocksForTest replaces the block manager with a fresh one.
// This must only be called from tests.
func resetAccountPoolRuntimeBlocksForTest() {
	accountPoolRuntimeBlocks = newAccountPoolRuntimeBlockManager()
}

// ResetAccountPoolRuntimeBlocksForTest is the exported form of resetAccountPoolRuntimeBlocksForTest,
// used by tests in other packages (e.g., relay) that call RecordAccountPoolRuntimeAttemptFailure
// and need a clean block state between tests.
func ResetAccountPoolRuntimeBlocksForTest() {
	resetAccountPoolRuntimeBlocksForTest()
}
