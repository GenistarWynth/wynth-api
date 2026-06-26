package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeAffinityTestCandidate builds a minimal accountPoolAccountCandidate for unit tests
// that only care about account ID matching (no DB, no pool filters, no encryption).
func makeAffinityTestCandidate(id int) accountPoolAccountCandidate {
	return accountPoolAccountCandidate{
		account: model.AccountPoolAccount{Id: id},
	}
}

// TestAccountPoolAffinityRememberLookupForget covers the basic remember/lookup/forget contract.
func TestAccountPoolAffinityRememberLookupForget(t *testing.T) {
	resetAccountPoolRuntimeAffinitiesForTest()

	const key = "k1"
	const bindingID = 1
	const accountID = 42
	const t0 = int64(1000)

	rememberAccountPoolRuntimeAffinity(key, bindingID, accountID, t0)

	t.Run("lookup returns the remembered account before expiry", func(t *testing.T) {
		got, ok := lookupAccountPoolRuntimeAffinity(key, bindingID, t0+1)
		require.True(t, ok)
		assert.Equal(t, accountID, got)
	})

	t.Run("lookup rejects a wrong bindingID", func(t *testing.T) {
		_, ok := lookupAccountPoolRuntimeAffinity(key, bindingID+1, t0+1)
		assert.False(t, ok)
	})

	t.Run("forget removes the entry", func(t *testing.T) {
		forgetAccountPoolRuntimeAffinity(key)
		_, ok := lookupAccountPoolRuntimeAffinity(key, bindingID, t0+1)
		assert.False(t, ok)
	})
}

// TestAccountPoolAffinityIdleTTLExpiry verifies that the sliding idle TTL causes expiry
// when no refresh happens.
func TestAccountPoolAffinityIdleTTLExpiry(t *testing.T) {
	resetAccountPoolRuntimeAffinitiesForTest()

	const key = "k2"
	const bindingID = 2
	const accountID = 7
	// Use a large non-zero t0 so the "now <= 0" real-clock branch in remember/lookup
	// is never triggered. Values must stay well below t0+hardCap to avoid the hard-cap path.
	const t0 = int64(1_000_000)

	rememberAccountPoolRuntimeAffinity(key, bindingID, accountID, t0)

	t.Run("lookup succeeds one second before TTL boundary", func(t *testing.T) {
		_, ok := lookupAccountPoolRuntimeAffinity(key, bindingID, t0+accountPoolRuntimeAffinityTTLSeconds-1)
		assert.True(t, ok)
	})

	t.Run("lookup fails at TTL boundary (expiresAt == now is expired)", func(t *testing.T) {
		// expiresAt = t0 + TTL; condition is expiresAt <= now, so now == expiresAt → expired.
		_, ok := lookupAccountPoolRuntimeAffinity(key, bindingID, t0+accountPoolRuntimeAffinityTTLSeconds)
		assert.False(t, ok)
	})
}

// TestAccountPoolAffinityDigestKey verifies that accountPoolRuntimeAffinityDigest produces a
// deterministic hex string.
func TestAccountPoolAffinityDigestKey(t *testing.T) {
	d1 := accountPoolRuntimeAffinityDigest("hello")
	d2 := accountPoolRuntimeAffinityDigest("hello")
	d3 := accountPoolRuntimeAffinityDigest("world")

	assert.Equal(t, d1, d2)
	assert.NotEqual(t, d1, d3)
	assert.Len(t, d1, 64, "SHA-256 hex digest must be 64 chars")
}

// TestAccountPoolAffinityCreatedAtPreservedAcrossRefreshes asserts that the birth time
// (createdAt) is fixed on the first remember and NOT overwritten by subsequent remember calls,
// while expiresAt slides forward on each refresh.
func TestAccountPoolAffinityCreatedAtPreservedAcrossRefreshes(t *testing.T) {
	resetAccountPoolRuntimeAffinitiesForTest()

	const key = "k-birth"
	const bindingID = 5
	const accountID = 99
	const t0 = int64(0)

	// First remember – establishes createdAt = t0.
	rememberAccountPoolRuntimeAffinity(key, bindingID, accountID, t0)

	// Refresh at t1 = one second before idle TTL would expire. expiresAt slides forward.
	t1 := t0 + accountPoolRuntimeAffinityTTLSeconds - 1
	rememberAccountPoolRuntimeAffinity(key, bindingID, accountID, t1)

	t.Run("refresh extends the idle window", func(t *testing.T) {
		// t1+1 is past the original expiresAt (t0+TTL) but within the refreshed window (t1+TTL).
		got, ok := lookupAccountPoolRuntimeAffinity(key, bindingID, t1+1)
		require.True(t, ok)
		assert.Equal(t, accountID, got)
	})

	t.Run("hard cap cuts off even a freshly refreshed entry", func(t *testing.T) {
		// Hard cap is relative to createdAt=t0; the refresh at t1 does NOT reset createdAt.
		hardCapBoundary := t0 + accountPoolRuntimeAffinityHardCapSeconds
		_, ok := lookupAccountPoolRuntimeAffinity(key, bindingID, hardCapBoundary)
		assert.False(t, ok, "hard cap must reject the pin even when expiresAt was freshly refreshed")
	})
}

// TestAccountPoolAffinityHardCapExpiry covers the hard TTL cap contract end-to-end:
// a session that keeps refreshing its pin must still be evicted after 4h.
func TestAccountPoolAffinityHardCapExpiry(t *testing.T) {
	resetAccountPoolRuntimeAffinitiesForTest()

	const key = "k-hardcap"
	const bindingID = 3
	const accountID = 55
	const t0 = int64(1_000_000)

	// Establish pin at t0.
	rememberAccountPoolRuntimeAffinity(key, bindingID, accountID, t0)

	// Refresh just before hard cap boundary – simulating a long-lived active session.
	refreshTime := t0 + accountPoolRuntimeAffinityHardCapSeconds - 1
	rememberAccountPoolRuntimeAffinity(key, bindingID, accountID, refreshTime)

	hardCapBoundary := t0 + accountPoolRuntimeAffinityHardCapSeconds

	t.Run("pin valid one second before hard cap", func(t *testing.T) {
		_, ok := lookupAccountPoolRuntimeAffinity(key, bindingID, hardCapBoundary-1)
		assert.True(t, ok)
	})

	t.Run("pin evicted at hard cap boundary", func(t *testing.T) {
		_, ok := lookupAccountPoolRuntimeAffinity(key, bindingID, hardCapBoundary)
		assert.False(t, ok)
	})
}

// TestAccountPoolAffinityTransientRetentionViaScheduler is a scheduler-level test that drives
// selectAccountPoolAffinityCandidate directly. It verifies that when the pinned account is
// absent from the current candidates slice (e.g., it is in AttemptedAccountIDs for this
// request), the pin is NOT forgotten — only the current selection falls through to a fallback,
// while the next request with the account back in candidates honors the original pin.
//
// Eviction is owned by: the relay failure path (ForgetSelectedAccountPoolRuntimeAffinity) +
// the idle TTL + the hard cap. A transient absence must NOT drop the pin.
func TestAccountPoolAffinityTransientRetentionViaScheduler(t *testing.T) {
	resetAccountPoolRuntimeAffinitiesForTest()

	const key = "k-transient"
	const bindingID = 10
	const now = int64(500)

	accountA := makeAffinityTestCandidate(101)
	accountB := makeAffinityTestCandidate(202)

	// Pin to account A.
	rememberAccountPoolRuntimeAffinity(key, bindingID, accountA.account.Id, now)

	t.Run("returns ok=false when pinned account absent from candidates", func(t *testing.T) {
		// Pass only B in candidates (A is absent, simulating it was attempted this request).
		_, ok := selectAccountPoolAffinityCandidate(key, bindingID, []accountPoolAccountCandidate{accountB}, now)
		assert.False(t, ok)
	})

	t.Run("pin is NOT forgotten after transient absence", func(t *testing.T) {
		got, ok := lookupAccountPoolRuntimeAffinity(key, bindingID, now)
		require.True(t, ok, "pin must still be present after transient candidate absence")
		assert.Equal(t, accountA.account.Id, got)
	})

	t.Run("returns A when A is back in candidates", func(t *testing.T) {
		got, ok := selectAccountPoolAffinityCandidate(key, bindingID, []accountPoolAccountCandidate{accountB, accountA}, now)
		require.True(t, ok)
		assert.Equal(t, accountA.account.Id, got.account.Id)
	})
}
