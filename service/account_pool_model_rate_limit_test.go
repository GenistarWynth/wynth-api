package service

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Helper / parse-marshal round-trip ───────────────────────────────────────

func TestParseAccountPoolModelRateLimitsEmptyInput(t *testing.T) {
	m, err := parseAccountPoolModelRateLimits("")
	require.NoError(t, err)
	assert.Empty(t, m)
}

func TestParseAccountPoolModelRateLimitsWhitespaceInput(t *testing.T) {
	m, err := parseAccountPoolModelRateLimits("   ")
	require.NoError(t, err)
	assert.Empty(t, m)
}

func TestParseAccountPoolModelRateLimitsRoundTrip(t *testing.T) {
	orig := map[string]int64{
		"gpt-foo":     9999,
		"claude-opus": 12345,
	}
	raw, err := marshalAccountPoolModelRateLimits(orig)
	require.NoError(t, err)
	assert.NotEmpty(t, raw)

	got, err := parseAccountPoolModelRateLimits(raw)
	require.NoError(t, err)
	assert.Equal(t, orig, got)
}

func TestParseAccountPoolModelRateLimitsMalformedJSON(t *testing.T) {
	_, err := parseAccountPoolModelRateLimits("{not json")
	assert.Error(t, err)
}

// ─── accountPoolModelRateLimited ─────────────────────────────────────────────

func TestAccountPoolModelRateLimitedModelAbsent(t *testing.T) {
	raw, err := marshalAccountPoolModelRateLimits(map[string]int64{
		"gpt-foo": 9999,
	})
	require.NoError(t, err)
	// "gpt-bar" not in map → not limited
	assert.False(t, accountPoolModelRateLimited(raw, "gpt-bar", 5000))
}

func TestAccountPoolModelRateLimitedResetAtFuture(t *testing.T) {
	now := int64(5000)
	raw, err := marshalAccountPoolModelRateLimits(map[string]int64{
		"gpt-foo": now + 1, // resetAt > now → limited
	})
	require.NoError(t, err)
	assert.True(t, accountPoolModelRateLimited(raw, "gpt-foo", now))
}

func TestAccountPoolModelRateLimitedResetAtPast(t *testing.T) {
	now := int64(5000)
	raw, err := marshalAccountPoolModelRateLimits(map[string]int64{
		"gpt-foo": now - 1, // resetAt <= now → not limited (expired)
	})
	require.NoError(t, err)
	assert.False(t, accountPoolModelRateLimited(raw, "gpt-foo", now))
}

func TestAccountPoolModelRateLimitedResetAtEqual(t *testing.T) {
	now := int64(5000)
	raw, err := marshalAccountPoolModelRateLimits(map[string]int64{
		"gpt-foo": now, // exactly equal → not limited
	})
	require.NoError(t, err)
	assert.False(t, accountPoolModelRateLimited(raw, "gpt-foo", now))
}

func TestAccountPoolModelRateLimitedEmptyRaw(t *testing.T) {
	assert.False(t, accountPoolModelRateLimited("", "gpt-foo", 5000))
}

// ─── classifyAccountPoolFailure — model-not-found cases ─────────────────────

func TestClassifyAccountPoolFailureModelNotFoundSetsModelRateLimit(t *testing.T) {
	const now = int64(1000)
	cfg := accountPoolFailureConfig()
	expectedResetAt := now + int64(cfg.ModelNotFoundCooldownMinutes*60)

	account := model.AccountPoolAccount{Status: model.AccountPoolAccountStatusEnabled}
	makeErrWithBody := func(msg string, code int, body []byte) *types.NewAPIError {
		e := types.NewErrorWithStatusCode(errors.New(msg), types.ErrorCodeBadResponseStatusCode, code)
		e.SetUpstreamResponse(nil, body, code)
		return e
	}

	tests := []struct {
		name      string
		code      int
		body      []byte
		reqModel  string
		wantInMap bool
	}{
		{
			name:      "404 does not exist sets model rate limit",
			code:      404,
			body:      []byte(`{"error":{"message":"The model 'gpt-foo' does not exist"}}`),
			reqModel:  "gpt-foo",
			wantInMap: true,
		},
		{
			name:      "404 model_not_found sets model rate limit",
			code:      404,
			body:      []byte(`{"error":{"code":"model_not_found","message":"model_not_found: gpt-bar"}}`),
			reqModel:  "gpt-bar",
			wantInMap: true,
		},
		{
			name:      "404 not_found_error (Anthropic) sets model rate limit",
			code:      404,
			body:      []byte(`{"type":"error","error":{"type":"not_found_error","message":"model claude-foo not found"}}`),
			reqModel:  "claude-foo",
			wantInMap: true,
		},
		{
			name:      "400 model is not found sets model rate limit",
			code:      400,
			body:      []byte(`{"error":{"message":"The model 'gemini-x' is not found"}}`),
			reqModel:  "gemini-x",
			wantInMap: true,
		},
		{
			name:      "400 model not found (both words) sets model rate limit",
			code:      400,
			body:      []byte(`{"error":{"message":"model gpt-foo not found in your org"}}`),
			reqModel:  "gpt-foo",
			wantInMap: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := makeErrWithBody("upstream error", tc.code, tc.body)
			got := classifyAccountPoolFailure(account, err, false, now, "openai", tc.reqModel)
			require.NotNil(t, got)

			// Must have model_rate_limits with the model set to ~now+cooldown
			require.Contains(t, got, "model_rate_limits")
			mrl, parseErr := parseAccountPoolModelRateLimits(got["model_rate_limits"].(string))
			require.NoError(t, parseErr)
			assert.Equal(t, expectedResetAt, mrl[tc.reqModel])

			// Must NOT set whole-account status or cooldowns
			assert.NotContains(t, got, "status")
			assert.NotContains(t, got, "rate_limited_until")
			assert.NotContains(t, got, "temp_disabled_until")
			assert.NotContains(t, got, "overload_until")
		})
	}
}

func TestClassifyAccountPoolFailureModelNotFoundPreservesOtherModels(t *testing.T) {
	const now = int64(1000)
	cfg := accountPoolFailureConfig()
	existingResetAt := now + 5000

	// Pre-seed another model in model_rate_limits
	existingRaw, err := marshalAccountPoolModelRateLimits(map[string]int64{
		"claude-opus": existingResetAt,
	})
	require.NoError(t, err)

	account := model.AccountPoolAccount{
		Status:          model.AccountPoolAccountStatusEnabled,
		ModelRateLimits: existingRaw,
	}
	body := []byte(`{"error":{"message":"The model 'gpt-foo' does not exist"}}`)
	e := types.NewErrorWithStatusCode(errors.New("not found"), types.ErrorCodeBadResponseStatusCode, 404)
	e.SetUpstreamResponse(nil, body, 404)

	got := classifyAccountPoolFailure(account, e, false, now, "openai", "gpt-foo")
	require.Contains(t, got, "model_rate_limits")
	mrl, parseErr := parseAccountPoolModelRateLimits(got["model_rate_limits"].(string))
	require.NoError(t, parseErr)

	// gpt-foo should be newly blocked
	assert.Equal(t, now+int64(cfg.ModelNotFoundCooldownMinutes*60), mrl["gpt-foo"])
	// claude-opus should be preserved
	assert.Equal(t, existingResetAt, mrl["claude-opus"])
}

func TestClassifyAccountPoolFailureGeneric404NoModelWordingUnchanged(t *testing.T) {
	const now = int64(1000)
	account := model.AccountPoolAccount{Status: model.AccountPoolAccountStatusEnabled}
	// Generic 404 with no model-not-found wording — base only, no model_rate_limits
	body := []byte(`{"error":{"message":"resource not found"}}`)
	e := types.NewErrorWithStatusCode(errors.New("not found"), types.ErrorCodeBadResponseStatusCode, 404)
	e.SetUpstreamResponse(nil, body, 404)

	got := classifyAccountPoolFailure(account, e, false, now, "openai", "gpt-foo")

	assert.NotContains(t, got, "model_rate_limits")
	assert.NotContains(t, got, "status")
	assert.NotContains(t, got, "rate_limited_until")
	assert.NotContains(t, got, "temp_disabled_until")
}

func TestClassifyAccountPoolFailureModelNotFoundWithEmptyRequestedModelFallsThrough(t *testing.T) {
	const now = int64(1000)
	account := model.AccountPoolAccount{Status: model.AccountPoolAccountStatusEnabled}
	body := []byte(`{"error":{"message":"The model 'gpt-foo' does not exist"}}`)
	e := types.NewErrorWithStatusCode(errors.New("not found"), types.ErrorCodeBadResponseStatusCode, 404)
	e.SetUpstreamResponse(nil, body, 404)

	// requestedModel="" → model-not-found detection fires but can't block anything specific;
	// should NOT set whole-account cooldowns and should NOT panic
	got := classifyAccountPoolFailure(account, e, false, now, "openai", "")

	assert.NotContains(t, got, "model_rate_limits")
	assert.NotContains(t, got, "status")
}

// ─── RecordAccountPoolRuntimeAttemptSuccess — model rate limit removal ───────

func TestRecordAccountPoolRuntimeAttemptSuccessRemovesModelFromRateLimit(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)

	now := int64(9000)
	existingRaw, err := marshalAccountPoolModelRateLimits(map[string]int64{
		"gpt-foo":     now + 3600, // should be removed by success
		"claude-opus": now + 7200, // should be preserved
	})
	require.NoError(t, err)

	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "success-model-rl",
	})
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", account.Id).
		Updates(map[string]any{"model_rate_limits": existingRaw}).Error)

	require.NoError(t, RecordAccountPoolRuntimeAttemptSuccess(account.Id, now, "gpt-foo"))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)

	mrl, parseErr := parseAccountPoolModelRateLimits(reloaded.ModelRateLimits)
	require.NoError(t, parseErr)
	assert.NotContains(t, mrl, "gpt-foo", "successful model should be removed from rate limits")
	assert.Contains(t, mrl, "claude-opus", "other model should be preserved")
	assert.Equal(t, now+int64(7200), mrl["claude-opus"])
}

func TestRecordAccountPoolRuntimeAttemptSuccessWithEmptyRequestedModelKeepsMap(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)

	now := int64(9000)
	existingRaw, err := marshalAccountPoolModelRateLimits(map[string]int64{
		"gpt-foo": now + 3600,
	})
	require.NoError(t, err)

	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "success-no-model",
	})
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", account.Id).
		Updates(map[string]any{"model_rate_limits": existingRaw}).Error)

	// Empty requestedModel: should not touch model_rate_limits
	require.NoError(t, RecordAccountPoolRuntimeAttemptSuccess(account.Id, now, ""))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	mrl, parseErr := parseAccountPoolModelRateLimits(reloaded.ModelRateLimits)
	require.NoError(t, parseErr)
	// gpt-foo should still be present since we didn't pass a model
	assert.Contains(t, mrl, "gpt-foo")
}

func TestRecordAccountPoolRuntimeAttemptSuccessMapBecomesEmptyWritesBlank(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)

	now := int64(9000)
	existingRaw, err := marshalAccountPoolModelRateLimits(map[string]int64{
		"gpt-foo": now + 3600,
	})
	require.NoError(t, err)

	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "success-last-model",
	})
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", account.Id).
		Updates(map[string]any{"model_rate_limits": existingRaw}).Error)

	require.NoError(t, RecordAccountPoolRuntimeAttemptSuccess(account.Id, now, "gpt-foo"))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	// Map is now empty → stored as ""
	assert.Empty(t, reloaded.ModelRateLimits)
}

// ─── Selection filter: model-rate-limited account excluded/included ───────────

func TestLoadAccountPoolSelectionContextExcludesModelRateLimitedAccount(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)

	now := int64(10000)
	// Build model_rate_limits with "gpt-foo" blocked until far future
	rlRaw, err := marshalAccountPoolModelRateLimits(map[string]int64{
		"gpt-foo": now + 9999,
	})
	require.NoError(t, err)

	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "rate-limited-for-gpt-foo",
	})
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", account.Id).
		Updates(map[string]any{"model_rate_limits": rlRaw}).Error)

	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	// Request model X=gpt-foo — account is rate-limited for it → no candidate
	req := AccountPoolSelectionRequest{
		ChannelID:    channel.Id,
		RequestModel: "gpt-foo",
		Now:          now,
	}
	ctx, ctxErr := loadAccountPoolSelectionContext(req)
	require.NoError(t, ctxErr)
	assert.Empty(t, ctx.candidates, "account rate-limited for gpt-foo should be excluded when requesting gpt-foo")
}

func TestLoadAccountPoolSelectionContextIncludesAccountForDifferentModel(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)

	now := int64(10000)
	// Block account for "gpt-foo" only
	rlRaw, err := marshalAccountPoolModelRateLimits(map[string]int64{
		"gpt-foo": now + 9999,
	})
	require.NoError(t, err)

	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "limited-for-foo-only",
	})
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", account.Id).
		Updates(map[string]any{"model_rate_limits": rlRaw}).Error)

	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	// Request model Y=gpt-bar — account is NOT rate-limited for it → should be a candidate
	req := AccountPoolSelectionRequest{
		ChannelID:    channel.Id,
		RequestModel: "gpt-bar",
		Now:          now,
	}
	ctx, ctxErr := loadAccountPoolSelectionContext(req)
	require.NoError(t, ctxErr)
	assert.Len(t, ctx.candidates, 1, "account should be a candidate for gpt-bar even though gpt-foo is limited")
	assert.Equal(t, account.Id, ctx.candidates[0].account.Id)
}
