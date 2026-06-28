package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func withDistributorStrictPriorityDB(t *testing.T) {
	t.Helper()
	previousDB := model.DB
	previousMemoryCache := common.MemoryCacheEnabled
	previousMainDBType := common.MainDatabaseType()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	common.MemoryCacheEnabled = true
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))

	t.Cleanup(func() {
		common.MemoryCacheEnabled = previousMemoryCache
		common.SetMainDatabaseType(previousMainDBType)
		model.DB = previousDB
		model.InitChannelCache()
	})
}

func insertDistributorStrictPriorityCandidate(t *testing.T, id int, group string) {
	t.Helper()
	priority := int64(100)
	weight := uint(100)
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:       id,
		Type:     constant.ChannelTypeOpenAI,
		Key:      "sk-test",
		Status:   common.ChannelStatusEnabled,
		Name:     group + "-channel",
		Group:    group,
		Models:   "gpt-test",
		Priority: &priority,
		Weight:   &weight,
	}).Error)
	require.NoError(t, model.DB.Create(&model.Ability{
		Group:     group,
		Model:     "gpt-test",
		ChannelId: id,
		Enabled:   true,
		Priority:  &priority,
		Weight:    weight,
	}).Error)
}

func withDistributorStrictPriorityAutoGroups(t *testing.T) {
	t.Helper()
	previousAutoGroups := setting.AutoGroups2JsonString()
	previousUsableGroups := setting.UserUsableGroups2JSONString()
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["default","vip"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"default group","vip":"vip group"}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(previousAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousUsableGroups))
	})
}

func withDistributorStrictPriorityAffinityRule(t *testing.T, affinityValue string) {
	t.Helper()
	setting := operation_setting.GetChannelAffinitySetting()
	previousEnabled := setting.Enabled
	previousRules := setting.Rules
	setting.Enabled = true
	setting.Rules = []operation_setting.ChannelAffinityRule{
		{
			Name:       "strict-priority-affinity",
			ModelRegex: []string{"^gpt-test$"},
			PathRegex:  []string{"/v1/chat/completions"},
			KeySources: []operation_setting.ChannelAffinityKeySource{
				{Type: "request_header", Key: "X-Affinity-Key"},
			},
			IncludeRuleName:   true,
			IncludeUsingGroup: true,
			TTLSeconds:        60,
		},
	}
	t.Cleanup(func() {
		setting.Enabled = previousEnabled
		setting.Rules = previousRules
	})

	seedRecorder := httptest.NewRecorder()
	seedCtx, _ := gin.CreateTestContext(seedRecorder)
	seedCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	seedCtx.Request.Header.Set("X-Affinity-Key", affinityValue)
	_, found := service.GetPreferredChannelByAffinity(seedCtx, "gpt-test", "auto")
	require.False(t, found)
	service.RecordChannelAffinity(seedCtx, 2)
	t.Cleanup(func() {
		service.ClearCurrentChannelAffinityCache(seedCtx)
	})
}

func TestDistributeSetsAutoGroupIndexForAffinityHit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withDistributorStrictPriorityDB(t)
	withDistributorStrictPriorityAutoGroups(t)
	insertDistributorStrictPriorityCandidate(t, 1, "default")
	insertDistributorStrictPriorityCandidate(t, 2, "vip")
	model.InitChannelCache()

	affinityValue := "tenant-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + time.Now().Format("150405.000000000")
	withDistributorStrictPriorityAffinityRule(t, affinityValue)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Request.Header.Set("X-Affinity-Key", affinityValue)
	common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "auto")
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")

	Distribute()(ctx)

	require.False(t, ctx.IsAborted())
	require.Equal(t, 2, common.GetContextKeyInt(ctx, constant.ContextKeyChannelId))
	autoGroup, exists := common.GetContextKey(ctx, constant.ContextKeyAutoGroup)
	require.True(t, exists)
	require.Equal(t, "vip", autoGroup)
	autoGroupIndex, exists := common.GetContextKey(ctx, constant.ContextKeyAutoGroupIndex)
	require.True(t, exists)
	require.Equal(t, 1, autoGroupIndex)
}
