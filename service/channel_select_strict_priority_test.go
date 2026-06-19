package service

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func clearServiceStrictPriorityTables(t *testing.T) {
	t.Helper()
	require.NoError(t, model.DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM channels").Error)
	model.InitChannelCache()
	t.Cleanup(func() {
		require.NoError(t, model.DB.Exec("DELETE FROM abilities").Error)
		require.NoError(t, model.DB.Exec("DELETE FROM channels").Error)
		model.InitChannelCache()
	})
}

func withServiceStrictPriorityMemoryCache(t *testing.T) {
	t.Helper()
	previous := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	model.InitChannelCache()
	t.Cleanup(func() {
		common.MemoryCacheEnabled = previous
		model.InitChannelCache()
	})
}

func withServiceStrictPriorityAutoGroups(t *testing.T, groups string) {
	t.Helper()
	previous := setting.AutoGroups2JsonString()
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(groups))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(previous))
	})
}

func newServiceStrictPriorityContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	return ctx
}

func insertServiceStrictPriorityCandidate(t *testing.T, id int, group, modelName string, priority int64) {
	t.Helper()
	weight := uint(100)
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:       id,
		Type:     constant.ChannelTypeOpenAI,
		Key:      fmt.Sprintf("key-%d", id),
		Status:   common.ChannelStatusEnabled,
		Name:     fmt.Sprintf("channel-%d", id),
		Group:    group,
		Models:   modelName,
		Priority: &priority,
		Weight:   &weight,
	}).Error)
	require.NoError(t, model.DB.Create(&model.Ability{
		Group:     group,
		Model:     modelName,
		ChannelId: id,
		Enabled:   true,
		Priority:  &priority,
		Weight:    weight,
	}).Error)
}

func TestAttemptedChannelIDSetFromContextParsesUseChannel(t *testing.T) {
	ctx := newServiceStrictPriorityContext()
	ctx.Set("use_channel", []string{"1", " 2 ", "bad", "0", "2"})

	attempted := attemptedChannelIDSetFromContext(ctx)

	assert.Equal(t, map[int]struct{}{1: {}, 2: {}}, attempted)
}

func TestAttemptedChannelIDSetFromContextReturnsNilWithoutValidIDs(t *testing.T) {
	ctx := newServiceStrictPriorityContext()
	ctx.Set("use_channel", []string{"bad", "0", "-1", " "})

	assert.Nil(t, attemptedChannelIDSetFromContext(nil))
	assert.Nil(t, attemptedChannelIDSetFromContext(ctx))
}

func TestCacheGetRandomSatisfiedChannelUsesAttemptedChannels(t *testing.T) {
	clearServiceStrictPriorityTables(t)
	withServiceStrictPriorityMemoryCache(t)
	insertServiceStrictPriorityCandidate(t, 1, "default", "gpt-test", 100)
	insertServiceStrictPriorityCandidate(t, 2, "default", "gpt-test", 100)
	insertServiceStrictPriorityCandidate(t, 3, "default", "gpt-test", 50)
	model.InitChannelCache()

	ctx := newServiceStrictPriorityContext()
	ctx.Set("use_channel", []string{"1"})

	channel, selectedGroup, err := CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx:         ctx,
		TokenGroup:  "default",
		ModelName:   "gpt-test",
		RequestPath: "",
	})

	require.NoError(t, err)
	require.NotNil(t, channel)
	assert.Equal(t, 2, channel.Id)
	assert.Equal(t, "default", selectedGroup)
}

func TestAutoGroupTriesLowerPriorityRemainingChannelBeforeNextGroup(t *testing.T) {
	clearServiceStrictPriorityTables(t)
	withServiceStrictPriorityMemoryCache(t)
	withServiceStrictPriorityAutoGroups(t, `["default","vip"]`)
	insertServiceStrictPriorityCandidate(t, 1, "default", "gpt-test", 100)
	insertServiceStrictPriorityCandidate(t, 2, "default", "gpt-test", 50)
	insertServiceStrictPriorityCandidate(t, 3, "vip", "gpt-test", 100)
	model.InitChannelCache()

	ctx := newServiceStrictPriorityContext()
	ctx.Set("use_channel", []string{"1"})

	channel, selectedGroup, err := CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx:         ctx,
		TokenGroup:  "auto",
		ModelName:   "gpt-test",
		RequestPath: "",
	})

	require.NoError(t, err)
	require.NotNil(t, channel)
	assert.Equal(t, 2, channel.Id)
	assert.Equal(t, "default", selectedGroup)
	autoGroup, exists := common.GetContextKey(ctx, constant.ContextKeyAutoGroup)
	require.True(t, exists)
	assert.Equal(t, "default", autoGroup)
}

func TestAutoGroupAdvancesWhenCurrentGroupExhausted(t *testing.T) {
	clearServiceStrictPriorityTables(t)
	withServiceStrictPriorityMemoryCache(t)
	withServiceStrictPriorityAutoGroups(t, `["default","vip"]`)
	insertServiceStrictPriorityCandidate(t, 1, "default", "gpt-test", 100)
	insertServiceStrictPriorityCandidate(t, 2, "vip", "gpt-test", 100)
	model.InitChannelCache()

	ctx := newServiceStrictPriorityContext()
	ctx.Set("use_channel", []string{"1"})

	channel, selectedGroup, err := CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx:         ctx,
		TokenGroup:  "auto",
		ModelName:   "gpt-test",
		RequestPath: "",
	})

	require.NoError(t, err)
	require.NotNil(t, channel)
	assert.Equal(t, 2, channel.Id)
	assert.Equal(t, "vip", selectedGroup)
	autoGroup, exists := common.GetContextKey(ctx, constant.ContextKeyAutoGroup)
	require.True(t, exists)
	assert.Equal(t, "vip", autoGroup)
	autoGroupIndex, exists := common.GetContextKey(ctx, constant.ContextKeyAutoGroupIndex)
	require.True(t, exists)
	assert.Equal(t, 1, autoGroupIndex)
}
