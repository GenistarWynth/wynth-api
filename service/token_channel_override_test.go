package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests exercise the in-memory fallback path (no Redis configured in tests, so
// common.RDB is nil and the hybrid cache uses its hot LRU). Token ids are unique per
// test to avoid collisions across the process-wide singleton cache.

func TestTokenChannelOverrideSetGetRoundtrip(t *testing.T) {
	const tokenId, channelId, setBy = 91001, 42, 7
	require.NoError(t, SetTokenChannelOverride(tokenId, channelId, setBy, time.Minute))
	t.Cleanup(func() { _ = ClearTokenChannelOverride(tokenId) })

	ov, found := GetTokenChannelOverride(tokenId)
	require.True(t, found)
	assert.Equal(t, channelId, ov.ChannelId)
	assert.Equal(t, setBy, ov.SetByUserId)
	assert.NotZero(t, ov.CreatedAt)
}

func TestTokenChannelOverrideGetUnset(t *testing.T) {
	_, found := GetTokenChannelOverride(91002)
	assert.False(t, found)
}

func TestTokenChannelOverrideClearIsIdempotent(t *testing.T) {
	const tokenId = 91003
	require.NoError(t, SetTokenChannelOverride(tokenId, 1, 1, time.Minute))
	_, found := GetTokenChannelOverride(tokenId)
	require.True(t, found)

	require.NoError(t, ClearTokenChannelOverride(tokenId))
	_, found = GetTokenChannelOverride(tokenId)
	assert.False(t, found)

	// clearing a missing key must not error
	require.NoError(t, ClearTokenChannelOverride(tokenId))
}

func TestTokenChannelOverrideOverwriteLastWriterWins(t *testing.T) {
	const tokenId = 91004
	require.NoError(t, SetTokenChannelOverride(tokenId, 10, 1, time.Minute))
	require.NoError(t, SetTokenChannelOverride(tokenId, 20, 1, time.Minute))
	t.Cleanup(func() { _ = ClearTokenChannelOverride(tokenId) })

	ov, found := GetTokenChannelOverride(tokenId)
	require.True(t, found)
	assert.Equal(t, 20, ov.ChannelId)
}

func TestTokenChannelOverrideKeyIsolation(t *testing.T) {
	const a, b = 91005, 91006
	require.NoError(t, SetTokenChannelOverride(a, 100, 1, time.Minute))
	require.NoError(t, SetTokenChannelOverride(b, 200, 1, time.Minute))
	t.Cleanup(func() {
		_ = ClearTokenChannelOverride(a)
		_ = ClearTokenChannelOverride(b)
	})

	ova, foundA := GetTokenChannelOverride(a)
	ovb, foundB := GetTokenChannelOverride(b)
	require.True(t, foundA)
	require.True(t, foundB)
	assert.Equal(t, 100, ova.ChannelId)
	assert.Equal(t, 200, ovb.ChannelId)
}

func TestClampTokenChannelOverrideTTL(t *testing.T) {
	// Expected values are hard-coded literals (not derived from the constants under test),
	// so a typo in the default/max constants is caught rather than tautologically passing.
	cases := []struct {
		name    string
		seconds int
		want    time.Duration
	}{
		{"zero -> default 30m", 0, 1800 * time.Second},
		{"negative -> default 30m", -5, 1800 * time.Second},
		{"in range", 500, 500 * time.Second},
		{"over max -> max 24h", 86401, 86400 * time.Second},
		{"exactly max 24h", 86400, 86400 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ClampTokenChannelOverrideTTL(tc.seconds))
		})
	}
}
