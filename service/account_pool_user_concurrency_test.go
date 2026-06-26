package service

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountPoolUserConcurrencyUnlimitedWhenMaxIsZero(t *testing.T) {
	resetAccountPoolUserConcurrencyForTest()

	release, acquired := tryAcquireAccountPoolUserSlot(1, 1, 0)
	require.True(t, acquired)
	require.NotNil(t, release)
	release() // must not panic
}

func TestAccountPoolUserConcurrencyUnlimitedWhenMaxIsNegative(t *testing.T) {
	resetAccountPoolUserConcurrencyForTest()

	release, acquired := tryAcquireAccountPoolUserSlot(1, 1, -5)
	require.True(t, acquired)
	require.NotNil(t, release)
	release()
}

func TestAccountPoolUserConcurrencyNoopForZeroUserID(t *testing.T) {
	resetAccountPoolUserConcurrencyForTest()

	release, acquired := tryAcquireAccountPoolUserSlot(1, 0, 2)
	require.True(t, acquired)
	require.NotNil(t, release)
	release()
}

func TestAccountPoolUserConcurrencyNoopForZeroBindingID(t *testing.T) {
	resetAccountPoolUserConcurrencyForTest()

	release, acquired := tryAcquireAccountPoolUserSlot(0, 1, 2)
	require.True(t, acquired)
	require.NotNil(t, release)
	release()
}

func TestAccountPoolUserConcurrencyAcquireUpToMax(t *testing.T) {
	resetAccountPoolUserConcurrencyForTest()

	release1, acquired1 := tryAcquireAccountPoolUserSlot(10, 42, 2)
	require.True(t, acquired1)
	require.NotNil(t, release1)

	release2, acquired2 := tryAcquireAccountPoolUserSlot(10, 42, 2)
	require.True(t, acquired2)
	require.NotNil(t, release2)

	// At limit — must refuse
	release3, acquired3 := tryAcquireAccountPoolUserSlot(10, 42, 2)
	assert.False(t, acquired3)
	assert.Nil(t, release3)

	release1()
	release2()
}

func TestAccountPoolUserConcurrencyRefuseWhenAtMax(t *testing.T) {
	resetAccountPoolUserConcurrencyForTest()

	release, acquired := tryAcquireAccountPoolUserSlot(5, 7, 1)
	require.True(t, acquired)
	require.NotNil(t, release)
	defer release()

	release2, acquired2 := tryAcquireAccountPoolUserSlot(5, 7, 1)
	assert.False(t, acquired2)
	assert.Nil(t, release2)
}

func TestAccountPoolUserConcurrencyReleaseFreesSlot(t *testing.T) {
	resetAccountPoolUserConcurrencyForTest()

	release, acquired := tryAcquireAccountPoolUserSlot(3, 9, 1)
	require.True(t, acquired)

	// Release frees the slot
	release()

	release2, acquired2 := tryAcquireAccountPoolUserSlot(3, 9, 1)
	require.True(t, acquired2)
	require.NotNil(t, release2)
	release2()
}

func TestAccountPoolUserConcurrencyReleaseIsIdempotent(t *testing.T) {
	resetAccountPoolUserConcurrencyForTest()

	release, acquired := tryAcquireAccountPoolUserSlot(4, 11, 1)
	require.True(t, acquired)

	// Double-release must not panic or double-decrement
	release()
	release()

	// Slot should be available again (only decremented once)
	release2, acquired2 := tryAcquireAccountPoolUserSlot(4, 11, 1)
	require.True(t, acquired2)
	release2()
}

func TestAccountPoolUserConcurrencyDifferentUsersAreIsolated(t *testing.T) {
	resetAccountPoolUserConcurrencyForTest()

	// User 1 holds a slot on binding 10
	release1, acquired1 := tryAcquireAccountPoolUserSlot(10, 1, 1)
	require.True(t, acquired1)
	defer release1()

	// User 2 can still get a slot on the same binding
	release2, acquired2 := tryAcquireAccountPoolUserSlot(10, 2, 1)
	require.True(t, acquired2)
	defer release2()
}

func TestAccountPoolUserConcurrencyDifferentBindingsAreIsolated(t *testing.T) {
	resetAccountPoolUserConcurrencyForTest()

	// User 1 holds a slot on binding 10
	release1, acquired1 := tryAcquireAccountPoolUserSlot(10, 1, 1)
	require.True(t, acquired1)
	defer release1()

	// User 1 can still get a slot on a different binding (20)
	release2, acquired2 := tryAcquireAccountPoolUserSlot(20, 1, 1)
	require.True(t, acquired2)
	defer release2()
}

func TestAccountPoolUserConcurrencyResetHelperClearsState(t *testing.T) {
	resetAccountPoolUserConcurrencyForTest()

	release, acquired := tryAcquireAccountPoolUserSlot(7, 13, 1)
	require.True(t, acquired)
	// Don't release; reset should clear it
	_ = release

	resetAccountPoolUserConcurrencyForTest()

	release2, acquired2 := tryAcquireAccountPoolUserSlot(7, 13, 1)
	require.True(t, acquired2)
	release2()
}

func TestAccountPoolUserConcurrencyThreadSafe(t *testing.T) {
	resetAccountPoolUserConcurrencyForTest()

	const (
		bindingID  = 99
		userID     = 55
		maxConc    = 3
		goroutines = 20
	)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		maxSeen int
		current int
	)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, acquired := tryAcquireAccountPoolUserSlot(bindingID, userID, maxConc)
			if !acquired {
				return
			}
			mu.Lock()
			current++
			if current > maxSeen {
				maxSeen = current
			}
			mu.Unlock()

			// Decrement the observed-concurrency counter while the manager slot is STILL
			// held, then release. This keeps `current` <= the manager's held-slot count at
			// all times, so maxSeen can never exceed maxConc (the manager guarantees at most
			// maxConc held slots). Releasing before decrementing would let the freed slot be
			// re-acquired by another goroutine while this one is still counted in `current`,
			// inflating maxSeen above maxConc (a measurement artifact, not a real breach).
			mu.Lock()
			current--
			mu.Unlock()
			release()
		}()
	}
	wg.Wait()

	// Concurrency must never have exceeded maxConc
	assert.LessOrEqual(t, maxSeen, maxConc)
}
