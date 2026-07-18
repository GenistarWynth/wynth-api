package service

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildClaudeUsageCacheCreationSplitPolicy(t *testing.T) {
	tests := []struct {
		name      string
		usage     dto.Usage
		wantTotal int
		want5m    int
		want1h    int
	}{
		{"native only", dto.Usage{PromptTokensDetails: dto.InputTokenDetails{CacheWriteTokens: 15}}, 15, 15, 0},
		{"legacy split", dto.Usage{PromptTokensDetails: dto.InputTokenDetails{CachedCreationTokens: 10}, ClaudeCacheCreation5mTokens: 3, ClaudeCacheCreation1hTokens: 7}, 10, 3, 7},
		{"native max preserves known split", dto.Usage{PromptTokensDetails: dto.InputTokenDetails{CachedCreationTokens: 10, CacheWriteTokens: 15}, ClaudeCacheCreation5mTokens: 3, ClaudeCacheCreation1hTokens: 7}, 15, 8, 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildClaudeUsageFromOpenAIUsage(&tt.usage)
			require.NotNil(t, got)
			assert.Equal(t, tt.wantTotal, got.CacheCreationInputTokens)
			require.NotNil(t, got.CacheCreation)
			assert.Equal(t, tt.want5m, got.CacheCreation.Ephemeral5mInputTokens)
			assert.Equal(t, tt.want1h, got.CacheCreation.Ephemeral1hInputTokens)
		})
	}
}

func TestNormalizeCacheCreationSplitClampsNegativeKnownValues(t *testing.T) {
	got5m, got1h := NormalizeCacheCreationSplit(10, -3, -7)
	require.Equal(t, 10, got5m)
	assert.Zero(t, got1h)
}
