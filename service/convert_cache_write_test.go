package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeCacheCreationSplitClampsNegativeKnownValues(t *testing.T) {
	got5m, got1h := NormalizeCacheCreationSplit(10, -3, -7)
	require.Equal(t, 10, got5m)
	assert.Zero(t, got1h)
}
