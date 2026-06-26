package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAccountPoolFailureState(t *testing.T) {
	t.Run("empty string returns zero value and nil error", func(t *testing.T) {
		s, err := parseAccountPoolFailureState("")
		require.NoError(t, err)
		assert.Equal(t, accountPoolFailureState{}, s)
	})

	t.Run("whitespace-only string returns zero value and nil error", func(t *testing.T) {
		s, err := parseAccountPoolFailureState("   ")
		require.NoError(t, err)
		assert.Equal(t, accountPoolFailureState{}, s)
	})

	t.Run("round-trip marshal then parse returns original value", func(t *testing.T) {
		original := accountPoolFailureState{
			ConsecutiveFailures: 3,
			LastStatus:          429,
			HTTP403Count:        2,
			HTTP403WindowStart:  1700000000,
			Last401At:           1700000100,
		}
		raw, err := original.marshal()
		require.NoError(t, err)

		parsed, err := parseAccountPoolFailureState(raw)
		require.NoError(t, err)
		assert.Equal(t, original, parsed)
	})
}

func TestParseAccountPoolRuntimeOptions(t *testing.T) {
	t.Run("empty string returns zero value with PoolMode false and nil error", func(t *testing.T) {
		opts, err := parseAccountPoolRuntimeOptions("")
		require.NoError(t, err)
		assert.Equal(t, accountPoolRuntimeOptions{}, opts)
		assert.False(t, opts.PoolMode)
	})

	t.Run("populated JSON is parsed correctly", func(t *testing.T) {
		raw := `{"pool_mode":true,"pool_mode_retry_count":3,"pool_mode_retry_status_codes":[429,500]}`
		opts, err := parseAccountPoolRuntimeOptions(raw)
		require.NoError(t, err)
		assert.True(t, opts.PoolMode)
		assert.Equal(t, 3, opts.PoolModeRetryCount)
		assert.Equal(t, []int{429, 500}, opts.PoolModeRetryStatusCodes)
	})
}
