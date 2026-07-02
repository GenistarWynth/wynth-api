package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// insertOverrideChannel seeds a channel plus its ability rows. Ability rows register the
// group with the channel cache (InitChannelCache builds group2model2channels only for
// groups that appear in abilities), so every group in the CSV must get a row.
func insertOverrideChannel(t *testing.T, id int, group, models string, status int) {
	t.Helper()
	priority := int64(100)
	weight := uint(100)
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:       id,
		Type:     constant.ChannelTypeOpenAI,
		Key:      "sk-test",
		Status:   status,
		Name:     "override-ch",
		Group:    group,
		Models:   models,
		Priority: &priority,
		Weight:   &weight,
	}).Error)
	for _, g := range strings.Split(group, ",") {
		for _, m := range strings.Split(models, ",") {
			require.NoError(t, model.DB.Create(&model.Ability{
				Group:     strings.TrimSpace(g),
				Model:     strings.TrimSpace(m),
				ChannelId: id,
				Enabled:   status == common.ChannelStatusEnabled,
				Priority:  &priority,
				Weight:    weight,
			}).Error)
		}
	}
}

func newOverrideCtx(t *testing.T, tokenId int) *gin.Context {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if tokenId != 0 {
		common.SetContextKey(c, constant.ContextKeyTokenId, tokenId)
	}
	return c
}

func TestResolveTokenChannelOverride_NoOverride(t *testing.T) {
	withDistributorStrictPriorityDB(t)
	insertOverrideChannel(t, 1, "default", "gpt-test", common.ChannelStatusEnabled)
	model.InitChannelCache()

	c := newOverrideCtx(t, 70001)
	ch, grp, ok := resolveTokenChannelOverride(c, "gpt-test", "default")
	assert.False(t, ok)
	assert.Nil(t, ch)
	assert.Empty(t, grp)
}

func TestResolveTokenChannelOverride_NoTokenId(t *testing.T) {
	withDistributorStrictPriorityDB(t)
	insertOverrideChannel(t, 1, "default", "gpt-test", common.ChannelStatusEnabled)
	model.InitChannelCache()

	c := newOverrideCtx(t, 0) // no token id in context
	_, _, ok := resolveTokenChannelOverride(c, "gpt-test", "default")
	assert.False(t, ok)
}

func TestResolveTokenChannelOverride_ValidFixedGroup(t *testing.T) {
	withDistributorStrictPriorityDB(t)
	insertOverrideChannel(t, 5, "default", "gpt-test", common.ChannelStatusEnabled)
	model.InitChannelCache()

	const tok = 70002
	require.NoError(t, service.SetTokenChannelOverride(tok, 5, 9, time.Minute))
	t.Cleanup(func() { _ = service.ClearTokenChannelOverride(tok) })

	c := newOverrideCtx(t, tok)
	ch, grp, ok := resolveTokenChannelOverride(c, "gpt-test", "default")
	require.True(t, ok)
	require.NotNil(t, ch)
	assert.Equal(t, 5, ch.Id)
	assert.Equal(t, "default", grp)
	_, autoSet := common.GetContextKey(c, constant.ContextKeyAutoGroup)
	assert.False(t, autoSet, "auto group must not be set for a fixed-group token")
}

func TestResolveTokenChannelOverride_DisabledChannelIsKept(t *testing.T) {
	withDistributorStrictPriorityDB(t)
	insertOverrideChannel(t, 6, "default", "gpt-test", common.ChannelStatusManuallyDisabled)
	model.InitChannelCache()

	const tok = 70003
	require.NoError(t, service.SetTokenChannelOverride(tok, 6, 9, time.Minute))
	t.Cleanup(func() { _ = service.ClearTokenChannelOverride(tok) })

	c := newOverrideCtx(t, tok)
	_, _, ok := resolveTokenChannelOverride(c, "gpt-test", "default")
	assert.False(t, ok)
	_, found := service.GetTokenChannelOverride(tok)
	assert.True(t, found, "a disabled channel is a transient failure; the override must be kept")
}

func TestResolveTokenChannelOverride_MissingChannelIsAutoCleared(t *testing.T) {
	withDistributorStrictPriorityDB(t)
	insertOverrideChannel(t, 7, "default", "gpt-test", common.ChannelStatusEnabled)
	model.InitChannelCache()

	const tok = 70004
	require.NoError(t, service.SetTokenChannelOverride(tok, 999, 9, time.Minute)) // 999 never inserted
	c := newOverrideCtx(t, tok)
	_, _, ok := resolveTokenChannelOverride(c, "gpt-test", "default")
	assert.False(t, ok)
	_, found := service.GetTokenChannelOverride(tok)
	assert.False(t, found, "a missing channel is a permanent failure; the override must be auto-cleared")
}

func TestResolveTokenChannelOverride_WrongGroupRejected(t *testing.T) {
	withDistributorStrictPriorityDB(t)
	withDistributorStrictPriorityAutoGroups(t) // registers default + vip as usable groups
	insertOverrideChannel(t, 8, "default", "gpt-test", common.ChannelStatusEnabled)
	insertOverrideChannel(t, 9, "vip", "gpt-test", common.ChannelStatusEnabled)
	model.InitChannelCache()

	const tok = 70005
	require.NoError(t, service.SetTokenChannelOverride(tok, 8, 9, time.Minute)) // ch8 is in "default"
	t.Cleanup(func() { _ = service.ClearTokenChannelOverride(tok) })

	c := newOverrideCtx(t, tok)
	_, _, ok := resolveTokenChannelOverride(c, "gpt-test", "vip") // token effective group is "vip"
	assert.False(t, ok, "override channel not in the token's group must be rejected")
	_, kept := service.GetTokenChannelOverride(tok)
	assert.True(t, kept, "wrong-group is a transient failure; the override must be kept, not cleared")
}

func TestResolveTokenChannelOverride_ModelUnsupportedRejected(t *testing.T) {
	withDistributorStrictPriorityDB(t)
	insertOverrideChannel(t, 10, "default", "gpt-test", common.ChannelStatusEnabled)
	model.InitChannelCache()

	const tok = 70006
	require.NoError(t, service.SetTokenChannelOverride(tok, 10, 9, time.Minute))
	t.Cleanup(func() { _ = service.ClearTokenChannelOverride(tok) })

	c := newOverrideCtx(t, tok)
	_, _, ok := resolveTokenChannelOverride(c, "other-model", "default")
	assert.False(t, ok, "override channel that does not serve the model must be rejected")
	_, kept := service.GetTokenChannelOverride(tok)
	assert.True(t, kept, "model-unsupported is a transient failure; the override must be kept, not cleared")
}

func TestResolveTokenChannelOverride_AutoResolvesSubGroup(t *testing.T) {
	withDistributorStrictPriorityDB(t)
	withDistributorStrictPriorityAutoGroups(t) // auto = [default, vip]
	insertOverrideChannel(t, 11, "vip", "gpt-test", common.ChannelStatusEnabled)
	model.InitChannelCache()

	const tok = 70007
	require.NoError(t, service.SetTokenChannelOverride(tok, 11, 9, time.Minute))
	t.Cleanup(func() { _ = service.ClearTokenChannelOverride(tok) })

	c := newOverrideCtx(t, tok)
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	ch, grp, ok := resolveTokenChannelOverride(c, "gpt-test", "auto")
	require.True(t, ok)
	assert.Equal(t, 11, ch.Id)
	assert.Equal(t, "vip", grp)
	ag, agSet := common.GetContextKey(c, constant.ContextKeyAutoGroup)
	require.True(t, agSet)
	assert.Equal(t, "vip", ag)
	assert.Equal(t, 1, common.GetContextKeyInt(c, constant.ContextKeyAutoGroupIndex))
}

func TestResolveTokenChannelOverride_AutoNoSubGroupRejected(t *testing.T) {
	withDistributorStrictPriorityDB(t)
	withDistributorStrictPriorityAutoGroups(t)
	insertOverrideChannel(t, 12, "default", "gpt-test", common.ChannelStatusEnabled)
	model.InitChannelCache()

	const tok = 70008
	require.NoError(t, service.SetTokenChannelOverride(tok, 12, 9, time.Minute))
	t.Cleanup(func() { _ = service.ClearTokenChannelOverride(tok) })

	c := newOverrideCtx(t, tok)
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	_, _, ok := resolveTokenChannelOverride(c, "other-model", "auto") // model served by no sub-group
	assert.False(t, ok)
	_, autoSet := common.GetContextKey(c, constant.ContextKeyAutoGroup)
	assert.False(t, autoSet, "no auto sub-group must be written when the override does not resolve")
	_, kept := service.GetTokenChannelOverride(tok)
	assert.True(t, kept, "auto-no-subgroup is a transient failure; the override must be kept, not cleared")
}

func TestDistributeHonorsChannelOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withDistributorStrictPriorityDB(t)
	insertOverrideChannel(t, 21, "default", "gpt-test", common.ChannelStatusEnabled)
	insertOverrideChannel(t, 22, "default", "gpt-test", common.ChannelStatusEnabled)
	model.InitChannelCache()

	const tok = 70009
	require.NoError(t, service.SetTokenChannelOverride(tok, 22, 9, time.Minute))
	t.Cleanup(func() { _ = service.ClearTokenChannelOverride(tok) })

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyTokenId, tok)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")

	Distribute()(c)

	require.False(t, c.IsAborted())
	assert.Equal(t, 22, common.GetContextKeyInt(c, constant.ContextKeyChannelId), "override channel must win over random selection")
	_, specific := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId)
	assert.False(t, specific, "override must not set specific_channel_id, so within-request retries stay enabled")
}

func TestDistributeFallsBackWhenOverrideInvalid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withDistributorStrictPriorityDB(t)
	insertOverrideChannel(t, 31, "default", "gpt-test", common.ChannelStatusEnabled)
	model.InitChannelCache()

	const tok = 70010
	require.NoError(t, service.SetTokenChannelOverride(tok, 999, 9, time.Minute)) // nonexistent channel

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyTokenId, tok)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")

	Distribute()(c)

	require.False(t, c.IsAborted(), "an invalid override must never abort live traffic")
	assert.Equal(t, 31, common.GetContextKeyInt(c, constant.ContextKeyChannelId), "must fall back to normal selection")
	_, found := service.GetTokenChannelOverride(tok)
	assert.False(t, found, "the nonexistent-channel override should have been auto-cleared")
}

func withOverrideAffinityRule(t *testing.T) {
	t.Helper()
	setting := operation_setting.GetChannelAffinitySetting()
	prevEnabled := setting.Enabled
	prevRules := setting.Rules
	setting.Enabled = true
	setting.Rules = []operation_setting.ChannelAffinityRule{{
		Name:               "override-test-affinity",
		ModelRegex:         []string{"^gpt-test$"},
		PathRegex:          []string{"/v1/chat/completions"},
		KeySources:         []operation_setting.ChannelAffinityKeySource{{Type: "request_header", Key: "X-Affinity-Key"}},
		IncludeRuleName:    true,
		IncludeUsingGroup:  true,
		TTLSeconds:         60,
		SkipRetryOnFailure: true, // the poison the override must NOT inherit
	}}
	t.Cleanup(func() {
		setting.Enabled = prevEnabled
		setting.Rules = prevRules
	})
}

// Regression guard for the review's confirmed bug: when an override wins, the affinity
// lookup must not run at all, so the override request neither inherits the affinity rule's
// skip-retry nor seeds the affinity cache with the forced channel.
func TestDistributeOverrideDoesNotContaminateAffinity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withDistributorStrictPriorityDB(t)
	withOverrideAffinityRule(t)
	insertOverrideChannel(t, 51, "default", "gpt-test", common.ChannelStatusEnabled)
	insertOverrideChannel(t, 52, "default", "gpt-test", common.ChannelStatusEnabled)
	model.InitChannelCache()

	const tok = 70012
	require.NoError(t, service.SetTokenChannelOverride(tok, 51, 9, time.Minute))
	t.Cleanup(func() { _ = service.ClearTokenChannelOverride(tok) })

	const affinityKey = "affinity-override-regression"
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-Affinity-Key", affinityKey)
	common.SetContextKey(c, constant.ContextKeyTokenId, tok)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")

	Distribute()(c)

	require.False(t, c.IsAborted())
	assert.Equal(t, 51, common.GetContextKeyInt(c, constant.ContextKeyChannelId), "override channel must win over affinity")
	// Finding 1: an override request must stay retryable (it must not inherit the rule's skip-retry).
	assert.False(t, service.ShouldSkipRetryAfterChannelAffinityFailure(c),
		"override request must not inherit the affinity rule's SkipRetryOnFailure")

	// Findings 2/3: the override must not seed the affinity cache with the forced channel.
	// A fresh request with the same affinity key must find nothing recorded.
	probeRec := httptest.NewRecorder()
	probe, _ := gin.CreateTestContext(probeRec)
	probe.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	probe.Request.Header.Set("X-Affinity-Key", affinityKey)
	_, seeded := service.GetPreferredChannelByAffinity(probe, "gpt-test", "default")
	assert.False(t, seeded, "an override-selected request must not write the forced channel into the affinity cache")
}

func TestDistributeModelLimitBlocksBeforeOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// The model-limit rejection path renders an i18n message; load the bundle so
	// abortWithOpenAiMessage -> i18n.T does not dereference a nil localizer.
	require.NoError(t, i18n.Init())
	withDistributorStrictPriorityDB(t)
	insertOverrideChannel(t, 41, "default", "gpt-test", common.ChannelStatusEnabled)
	model.InitChannelCache()

	const tok = 70011
	require.NoError(t, service.SetTokenChannelOverride(tok, 41, 9, time.Minute))
	t.Cleanup(func() { _ = service.ClearTokenChannelOverride(tok) })

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyTokenId, tok)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{"allowed-only": true})

	Distribute()(c)

	assert.True(t, c.IsAborted(), "the token model-limit gate must block before the override is consulted")
}
