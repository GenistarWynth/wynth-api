package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClaudeThinkingAdapterLegacyModelNoMaxTokensUsesDefault is a regression test for
// the MaxTokens default-fill ordering bug:
//
// Before the fix: ClaudeHelper ran the thinking-adapter BEFORE the default-fill in
// claudeHelperWithRuntimeSelected. For a legacy "-thinking" model (type:enabled branch,
// NOT opus-4.7/4.8) with no max_tokens, the thinking adapter clamped MaxTokens to 1280
// and set BudgetTokens=1024. The default-fill of 8192 then never applied.
//
// After the fix: default-fill runs ONCE in ClaudeHelper BEFORE the thinking-adapter block.
// The thinking adapter sees MaxTokens=8192 (>= 1280) and does not override it.
// BudgetTokens = 8192 * 0.8 = 6553.
func TestClaudeThinkingAdapterLegacyModelNoMaxTokensUsesDefault(t *testing.T) {
	// A legacy model (not opus-4.7/4.8) with "-thinking" suffix and nil MaxTokens.
	request := &dto.ClaudeRequest{
		Model:     "claude-3-7-sonnet-20250219-thinking",
		MaxTokens: nil,
	}

	applyClaudeDefaultMaxTokens(request)
	applyClaudeThinkingAdapterTransform(request)

	defaultMax := uint(model_setting.GetClaudeSettings().GetDefaultMaxTokens("claude-3-7-sonnet-20250219"))

	require.NotNil(t, request.MaxTokens, "MaxTokens must not be nil after default-fill + thinking adapter")
	assert.Equal(t, defaultMax, *request.MaxTokens, "MaxTokens must equal GetDefaultMaxTokens (8192), not the thinking-clamp floor (1280)")

	require.NotNil(t, request.Thinking, "Thinking must be set by thinking adapter")
	assert.Equal(t, "enabled", request.Thinking.Type)
	require.NotNil(t, request.Thinking.BudgetTokens, "BudgetTokens must be set")

	expectedBudget := int(float64(defaultMax) * model_setting.GetClaudeSettings().ThinkingAdapterBudgetTokensPercentage)
	assert.Equal(t, expectedBudget, *request.Thinking.BudgetTokens,
		"BudgetTokens must be 80%% of default max (%d), not 80%% of the 1280 floor", defaultMax)
}

// TestClaudeThinkingAdapterLegacyModelExplicitMaxTokensRespected verifies that when
// the client explicitly sets max_tokens (non-zero), the explicit value is preserved.
func TestClaudeThinkingAdapterLegacyModelExplicitMaxTokensRespected(t *testing.T) {
	clientMax := uint(4096)
	request := &dto.ClaudeRequest{
		Model:     "claude-3-7-sonnet-20250219-thinking",
		MaxTokens: &clientMax,
	}

	applyClaudeDefaultMaxTokens(request)
	applyClaudeThinkingAdapterTransform(request)

	require.NotNil(t, request.MaxTokens)
	assert.Equal(t, clientMax, *request.MaxTokens, "explicit client max_tokens must not be overwritten by default-fill")
}

// TestClaudeDefaultMaxTokensFillNilOnly verifies that applyClaudeDefaultMaxTokens
// fills MaxTokens only when nil or zero, and that the default value is 8192.
func TestClaudeDefaultMaxTokensFillNilOnly(t *testing.T) {
	t.Run("nil MaxTokens gets filled", func(t *testing.T) {
		req := &dto.ClaudeRequest{Model: "claude-3-5-sonnet-20241022", MaxTokens: nil}
		applyClaudeDefaultMaxTokens(req)
		require.NotNil(t, req.MaxTokens)
		assert.Equal(t, uint(8192), *req.MaxTokens)
	})

	t.Run("zero MaxTokens gets filled", func(t *testing.T) {
		zero := uint(0)
		req := &dto.ClaudeRequest{Model: "claude-3-5-sonnet-20241022", MaxTokens: &zero}
		applyClaudeDefaultMaxTokens(req)
		require.NotNil(t, req.MaxTokens)
		assert.Equal(t, uint(8192), *req.MaxTokens)
	})

	t.Run("non-zero MaxTokens is preserved", func(t *testing.T) {
		val := uint(2048)
		req := &dto.ClaudeRequest{Model: "claude-3-5-sonnet-20241022", MaxTokens: &val}
		applyClaudeDefaultMaxTokens(req)
		require.NotNil(t, req.MaxTokens)
		assert.Equal(t, uint(2048), *req.MaxTokens)
	})
}
