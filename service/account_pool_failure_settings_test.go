package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAccountPoolFailureConfigDefaults(t *testing.T) {
	cfg := accountPoolFailureConfig()

	assert.Equal(t, 5, cfg.RateLimit429FallbackSeconds)
	assert.Equal(t, 7200, cfg.RateLimit429CapSeconds)
	assert.Equal(t, true, cfg.RateLimit429FallbackEnabled)
	assert.Equal(t, 3, cfg.HTTP403Threshold)
	assert.Equal(t, 180, cfg.HTTP403WindowMinutes)
	assert.Equal(t, 10, cfg.HTTP403CooldownMinutes)
	assert.Equal(t, 10, cfg.OAuth401CooldownMinutes)
	assert.Equal(t, 30, cfg.OAuth401RestrikeWindowMinutes)
	assert.Equal(t, 10, cfg.OverloadCooldownMinutes)
	assert.Equal(t, 10, cfg.TransportPersistentMinutes)
	assert.Equal(t, 60, cfg.TransportTransientSeconds)
	assert.Equal(t, []int{60, 300, 1800}, cfg.Escalation5xxTiersSeconds)
	assert.Equal(t, 6, cfg.Escalation5xxHardCapCount)
}

func TestClampRateLimit429CooldownSeconds(t *testing.T) {
	cases := []struct {
		input    int
		expected int
	}{
		{0, 1},
		{1, 1},
		{5, 5},
		{7200, 7200},
		{99999, 7200},
		{-3, 1},
	}

	for _, tc := range cases {
		got := clampRateLimit429CooldownSeconds(tc.input)
		assert.Equal(t, tc.expected, got, "input=%d", tc.input)
	}
}
