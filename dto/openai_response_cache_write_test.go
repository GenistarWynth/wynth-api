package dto

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCacheCreationTokensTotal(t *testing.T) {
	tests := []struct {
		name    string
		details InputTokenDetails
		want    int
	}{
		{"native only", InputTokenDetails{CacheWriteTokens: 7}, 7},
		{"legacy only", InputTokenDetails{CachedCreationTokens: 6}, 6},
		{"maximum wins", InputTokenDetails{CachedCreationTokens: 6, CacheWriteTokens: 9}, 9},
		{"negatives clamp", InputTokenDetails{CachedCreationTokens: -6, CacheWriteTokens: -9}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assert.Equal(t, tt.want, tt.details.CacheCreationTokensTotal()) })
	}
}
