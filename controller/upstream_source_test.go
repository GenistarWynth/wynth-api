package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type upstreamSourceAPIResponse[T any] struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

func setupUpstreamSourceAPITestDB(t *testing.T) {
	t.Helper()

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedisEnabled := common.RedisEnabled
	oldTranslateMessage := common.TranslateMessage
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	common.TranslateMessage = func(c *gin.Context, key string, args ...map[string]any) string {
		return key
	}
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedisEnabled
		common.TranslateMessage = oldTranslateMessage
	})

	require.NoError(t, db.AutoMigrate(&model.UpstreamSource{}, &model.UpstreamSourceChannelMapping{}, &model.Channel{}, &model.Ability{}, &model.User{}, &model.Log{}))
	require.NoError(t, db.Create(&model.User{
		Id:       1,
		Username: "admin",
		Password: "password",
		Role:     common.RoleAdminUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}).Error)
}

func upstreamSourceAPIRouter(authenticated bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("upstream-source-api-test"))))
	router.GET("/login", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("username", "admin")
		session.Set("role", common.RoleAdminUser)
		session.Set("id", 1)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "default")
		if err := session.Save(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false})
			return
		}
		c.Status(http.StatusNoContent)
	})
	group := router.Group("/api/upstream_sources")
	group.Use(middleware.AdminAuth())
	{
		group.GET("", ListUpstreamSources)
		group.POST("", CreateUpstreamSource)
		group.GET("/:id", GetUpstreamSource)
		group.PUT("/:id", UpdateUpstreamSource)
		group.PUT("/:id/credentials", UpdateUpstreamSourceCredentials)
		group.DELETE("/:id", DeleteUpstreamSource)
		group.POST("/:id/discover", DiscoverUpstreamSource)
		group.GET("/:id/mappings", ListUpstreamSourceMappings)
		group.PUT("/:id/mappings", UpdateUpstreamSourceMappings)
		group.POST("/:id/sync", SyncUpstreamSource)
		group.GET("/:id/sync_result", GetUpstreamSourceSyncResult)
	}
	if authenticated {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/login", nil)
		router.ServeHTTP(recorder, request)
	}
	return router
}

func upstreamSourceAPIRequest[T any](t *testing.T, router *gin.Engine, method string, target string, body any, authenticated bool) upstreamSourceAPIResponse[T] {
	t.Helper()

	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		payload, err := common.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(payload)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("Content-Type", "application/json")
	if authenticated {
		request.Header.Set("New-Api-User", "1")
		loginRecorder := httptest.NewRecorder()
		loginRequest := httptest.NewRequest(http.MethodGet, "/login", nil)
		router.ServeHTTP(loginRecorder, loginRequest)
		require.Equal(t, http.StatusNoContent, loginRecorder.Code)
		for _, cookie := range loginRecorder.Result().Cookies() {
			request.AddCookie(cookie)
		}
	}
	router.ServeHTTP(recorder, request)

	var response upstreamSourceAPIResponse[T]
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

func createUpstreamSourceAPITestSource(t *testing.T, authConfig string) model.UpstreamSource {
	t.Helper()

	source := model.UpstreamSource{
		Name:             "source-a",
		Type:             model.UpstreamSourceTypeSub2API,
		Status:           model.UpstreamSourceStatusEnabled,
		BaseURL:          "https://admin.example.com",
		AdminAPIBasePath: "/api/v1",
		RelayBaseURL:     "https://relay.example.com",
		AuthConfig:       authConfig,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	return source
}

func TestUpstreamSourceAPIListRedactsSecrets(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	createUpstreamSourceAPITestSource(t, `{"email":"admin@example.com","password":"secret-password","access_token":"access-token","refresh_token":"refresh-token"}`)

	response := upstreamSourceAPIRequest[[]dto.UpstreamSourceResponse](t, router, http.MethodGet, "/api/upstream_sources", nil, true)

	require.True(t, response.Success, response.Message)
	require.Len(t, response.Data, 1)
	assert.Equal(t, "source-a", response.Data[0].Name)
	assert.True(t, response.Data[0].HasCredentials)
	assert.NotContains(t, response.Data[0].MaskedEmail, "admin@example.com")
	raw := string(mustMarshalUpstreamSourceAPITest(t, response))
	assert.NotContains(t, raw, "secret-password")
	assert.NotContains(t, raw, "access-token")
	assert.NotContains(t, raw, "refresh-token")
	assert.NotContains(t, raw, "AuthConfig")
}

func TestUpstreamSourceAPIListRedactsStoredErrors(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	source := createUpstreamSourceAPITestSource(t, `{}`)
	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Updates(map[string]any{
		"last_discovery_error": `upstream failed: {"password":"secret-password","access_token":"access-token"}`,
		"last_sync_error":      "authorization: bearer sk-test-secret-token-value",
	}).Error)

	response := upstreamSourceAPIRequest[[]dto.UpstreamSourceResponse](t, router, http.MethodGet, "/api/upstream_sources", nil, true)

	require.True(t, response.Success, response.Message)
	raw := string(mustMarshalUpstreamSourceAPITest(t, response))
	assert.NotContains(t, raw, "secret-password")
	assert.NotContains(t, raw, "access-token")
	assert.NotContains(t, raw, "sk-test-secret-token-value")
	assert.Contains(t, raw, "[redacted]")
}

func TestUpstreamSourceAPIListAcceptsNumericPrivateIPFlag(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	source := createUpstreamSourceAPITestSource(t, `{}`)
	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Update("sync_config", `{"allow_private_ip":1}`).Error)

	response := upstreamSourceAPIRequest[[]dto.UpstreamSourceResponse](t, router, http.MethodGet, "/api/upstream_sources", nil, true)

	require.True(t, response.Success, response.Message)
	require.Len(t, response.Data, 1)
	assert.True(t, response.Data[0].AllowPrivateIP)
}

func TestUpstreamSourceAPICreateStoresCredentialsButReturnsMaskedState(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	request := dto.UpstreamSourceCreateRequest{
		Name:                   "created-source",
		Type:                   model.UpstreamSourceTypeSub2API,
		BaseURL:                "https://admin.example.com",
		RelayBaseURL:           "https://relay.example.com",
		Email:                  "wynth@example.com",
		Password:               "plain-password",
		LocalGroup:             "paid",
		ChannelType:            constant.ChannelTypeOpenAI,
		DefaultPriority:        12,
		DefaultWeight:          34,
		EnableMonitor:          true,
		MonitorIntervalMinutes: 7,
		AutoSyncModels:         true,
	}

	response := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodPost, "/api/upstream_sources", request, true)

	require.True(t, response.Success, response.Message)
	assert.Equal(t, "created-source", response.Data.Name)
	assert.True(t, response.Data.HasCredentials)
	assert.NotContains(t, response.Data.MaskedEmail, "wynth@example.com")
	assert.Equal(t, "paid", response.Data.LocalGroup)
	assert.Equal(t, constant.ChannelTypeOpenAI, response.Data.ChannelType)
	assert.Equal(t, int64(12), response.Data.DefaultPriority)
	assert.Equal(t, uint(34), response.Data.DefaultWeight)
	assert.True(t, response.Data.EnableMonitor)
	assert.Equal(t, 7, response.Data.MonitorIntervalMinutes)
	assert.True(t, response.Data.AutoSyncModels)
	raw := string(mustMarshalUpstreamSourceAPITest(t, response))
	assert.NotContains(t, raw, "plain-password")
	var source model.UpstreamSource
	require.NoError(t, model.DB.First(&source, response.Data.Id).Error)
	assert.Contains(t, source.AuthConfig, "wynth@example.com")
	assert.Contains(t, source.AuthConfig, "plain-password")
	assert.Contains(t, source.SyncConfig, "paid")
	assert.Contains(t, source.SyncConfig, "default_priority")
}

func TestUpstreamSourceAPICreateRoundTripsAutoPriorityConfig(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	request := dto.UpstreamSourceCreateRequest{
		Name:                        "auto-priority-source",
		Type:                        model.UpstreamSourceTypeSub2API,
		BaseURL:                     "https://admin.example.com",
		LocalGroup:                  "paid",
		ChannelType:                 constant.ChannelTypeOpenAI,
		AutoPriorityEnabled:         true,
		AutoPriorityIntervalMinutes: common.GetPointer(3),
		AutoPriorityWindowHours:     common.GetPointer(999),
		LocalGroupRules: []dto.UpstreamSourceLocalGroupRule{
			{
				Name:       "OpenAI pro",
				LocalGroup: "paid",
				Platforms:  []string{"openai"},
				AutoPriority: &dto.UpstreamSourceRuleAutoPriority{
					Enabled:         common.GetPointer(false),
					IntervalMinutes: common.GetPointer(0),
					WindowHours:     common.GetPointer(48),
				},
			},
		},
	}

	response := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodPost, "/api/upstream_sources", request, true)

	require.True(t, response.Success, response.Message)
	assert.True(t, response.Data.AutoPriorityEnabled)
	assert.Equal(t, 3, response.Data.AutoPriorityIntervalMinutes)
	assert.Equal(t, 168, response.Data.AutoPriorityWindowHours)
	require.Len(t, response.Data.LocalGroupRules, 1)
	require.NotNil(t, response.Data.LocalGroupRules[0].AutoPriority)
	require.NotNil(t, response.Data.LocalGroupRules[0].AutoPriority.Enabled)
	assert.False(t, *response.Data.LocalGroupRules[0].AutoPriority.Enabled)
	require.NotNil(t, response.Data.LocalGroupRules[0].AutoPriority.IntervalMinutes)
	require.NotNil(t, response.Data.LocalGroupRules[0].AutoPriority.WindowHours)
	assert.Equal(t, 0, *response.Data.LocalGroupRules[0].AutoPriority.IntervalMinutes)
	assert.Equal(t, 48, *response.Data.LocalGroupRules[0].AutoPriority.WindowHours)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, response.Data.Id).Error)
	var syncConfig map[string]any
	require.NoError(t, common.UnmarshalJsonStr(reloaded.SyncConfig, &syncConfig))
	assert.Equal(t, true, syncConfig["auto_priority_enabled"])
	assert.Equal(t, float64(3), syncConfig["auto_priority_interval_minutes"])
	assert.Equal(t, float64(168), syncConfig["auto_priority_window_hours"])
	require.Contains(t, syncConfig, "local_group_rules")
	rules, ok := syncConfig["local_group_rules"].([]any)
	require.True(t, ok)
	require.Len(t, rules, 1)
	rule, ok := rules[0].(map[string]any)
	require.True(t, ok)
	autoPriority, ok := rule["auto_priority"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, autoPriority["enabled"])
	assert.Equal(t, float64(0), autoPriority["interval_minutes"])
	assert.Equal(t, float64(48), autoPriority["window_hours"])
}

func TestUpstreamSourceAPICreatePersistsExplicitZeroAutoPriorityInterval(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	request := dto.UpstreamSourceCreateRequest{
		Name:                        "auto-priority-zero-source",
		Type:                        model.UpstreamSourceTypeSub2API,
		BaseURL:                     "https://admin.example.com",
		LocalGroup:                  "paid",
		ChannelType:                 constant.ChannelTypeOpenAI,
		AutoPriorityEnabled:         true,
		AutoPriorityIntervalMinutes: common.GetPointer(0),
		AutoPriorityWindowHours:     common.GetPointer(999),
	}

	response := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodPost, "/api/upstream_sources", request, true)

	require.True(t, response.Success, response.Message)
	assert.True(t, response.Data.AutoPriorityEnabled)
	assert.Equal(t, 0, response.Data.AutoPriorityIntervalMinutes)
	assert.Equal(t, 168, response.Data.AutoPriorityWindowHours)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, response.Data.Id).Error)
	var syncConfig map[string]any
	require.NoError(t, common.UnmarshalJsonStr(reloaded.SyncConfig, &syncConfig))
	assert.Equal(t, float64(0), syncConfig["auto_priority_interval_minutes"])
	assert.Equal(t, float64(168), syncConfig["auto_priority_window_hours"])

	updateRequest := dto.UpstreamSourceUpdateRequest{
		Status:                      model.UpstreamSourceStatusEnabled,
		AutoPriorityEnabled:         true,
		AutoPriorityIntervalMinutes: common.GetPointer(0),
		AutoPriorityWindowHours:     common.GetPointer(999),
	}
	updateResponse := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodPut, "/api/upstream_sources/"+strconv.Itoa(response.Data.Id), updateRequest, true)
	require.True(t, updateResponse.Success, updateResponse.Message)
	assert.Equal(t, 0, updateResponse.Data.AutoPriorityIntervalMinutes)
	assert.Equal(t, 168, updateResponse.Data.AutoPriorityWindowHours)
}

func TestChannelAutoPriorityScoreSerializesZeroValues(t *testing.T) {
	snapshot := dto.ChannelOtherSettings{
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{
			OldPriority: 0,
			NewPriority: 0,
			Applied:     false,
		},
	}

	raw := string(mustMarshalUpstreamSourceAPITest(t, snapshot))

	assert.Contains(t, raw, "\"old_priority\":0")
	assert.Contains(t, raw, "\"new_priority\":0")
	assert.Contains(t, raw, "\"applied\":false")
}

func TestUpstreamSourceAPISyncConfigRoundTripsExplicitFalseValues(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	createRequest := dto.UpstreamSourceCreateRequest{
		Name:                   "false-sync-source",
		Type:                   model.UpstreamSourceTypeSub2API,
		BaseURL:                "https://admin.example.com",
		LocalGroup:             "paid",
		ChannelType:            constant.ChannelTypeOpenAI,
		DefaultPriority:        10,
		DefaultWeight:          20,
		EnableMonitor:          true,
		MonitorIntervalMinutes: 5,
		AutoSyncModels:         true,
		AllowPrivateIP:         true,
	}
	createResponse := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodPost, "/api/upstream_sources", createRequest, true)
	require.True(t, createResponse.Success, createResponse.Message)

	updateRequest := dto.UpstreamSourceUpdateRequest{
		Status:                 model.UpstreamSourceStatusEnabled,
		LocalGroup:             "default",
		ChannelType:            constant.ChannelTypeOpenAI,
		DefaultPriority:        0,
		DefaultWeight:          0,
		EnableMonitor:          false,
		MonitorIntervalMinutes: 0,
		AutoSyncModels:         false,
		AllowPrivateIP:         false,
	}
	updateResponse := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodPut, "/api/upstream_sources/"+strconv.Itoa(createResponse.Data.Id), updateRequest, true)
	require.True(t, updateResponse.Success, updateResponse.Message)
	assert.False(t, updateResponse.Data.EnableMonitor)
	assert.Equal(t, 0, updateResponse.Data.MonitorIntervalMinutes)
	assert.False(t, updateResponse.Data.AutoSyncModels)
	assert.False(t, updateResponse.Data.AllowPrivateIP)

	getResponse := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodGet, "/api/upstream_sources/"+strconv.Itoa(createResponse.Data.Id), nil, true)
	require.True(t, getResponse.Success, getResponse.Message)
	assert.False(t, getResponse.Data.EnableMonitor)
	assert.Equal(t, int64(0), getResponse.Data.DefaultPriority)
	assert.Equal(t, uint(0), getResponse.Data.DefaultWeight)
	assert.False(t, getResponse.Data.AutoSyncModels)
	assert.False(t, getResponse.Data.AllowPrivateIP)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, createResponse.Data.Id).Error)
	var syncConfig map[string]any
	require.NoError(t, common.UnmarshalJsonStr(reloaded.SyncConfig, &syncConfig))
	assert.Equal(t, false, syncConfig["enable_monitor"])
	assert.Equal(t, false, syncConfig["auto_sync_models"])
	assert.Equal(t, false, syncConfig["allow_private_ip"])
}

func TestUpstreamSourceAPICredentialsUpdateClearsCachedTokens(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	source := createUpstreamSourceAPITestSource(t, `{"email":"old@example.com","password":"old-password","access_token":"old-access-token","refresh_token":"old-refresh-token","expires_at":9999999999}`)
	request := dto.UpstreamSourceCredentialsUpdateRequest{
		Email:    "new@example.com",
		Password: "new-password",
	}

	response := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodPut, "/api/upstream_sources/"+strconv.Itoa(source.Id)+"/credentials", request, true)

	require.True(t, response.Success, response.Message)
	assert.True(t, response.Data.HasCredentials)
	raw := string(mustMarshalUpstreamSourceAPITest(t, response))
	assert.NotContains(t, raw, "new-password")
	assert.NotContains(t, raw, "old-access-token")
	assert.NotContains(t, raw, "old-refresh-token")

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Contains(t, reloaded.AuthConfig, "new@example.com")
	assert.Contains(t, reloaded.AuthConfig, "new-password")
	var persisted map[string]any
	require.NoError(t, common.UnmarshalJsonStr(reloaded.AuthConfig, &persisted))
	assert.NotContains(t, persisted, "access_token")
	assert.NotContains(t, persisted, "refresh_token")
	assert.NotContains(t, persisted, "expires_at")
}

func TestUpstreamSourceAPIUpdateRejectsInvalidStatus(t *testing.T) {
	for _, status := range []string{"garbage", model.UpstreamSourceStatusDeleted} {
		t.Run(status, func(t *testing.T) {
			setupUpstreamSourceAPITestDB(t)
			router := upstreamSourceAPIRouter(true)
			source := createUpstreamSourceAPITestSource(t, `{}`)
			request := dto.UpstreamSourceUpdateRequest{
				Name:   "source-renamed",
				Status: status,
			}

			response := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodPut, "/api/upstream_sources/"+strconv.Itoa(source.Id), request, true)

			require.False(t, response.Success)
			assert.Contains(t, response.Message, "status")
			var reloaded model.UpstreamSource
			require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
			assert.Equal(t, model.UpstreamSourceStatusEnabled, reloaded.Status)
		})
	}
}

func TestUpstreamSourceAPIDiscoverRequiresAdmin(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(false)
	source := createUpstreamSourceAPITestSource(t, `{}`)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/upstream_sources/"+strconv.Itoa(source.Id)+"/discover", nil)
	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestUpstreamSourceAPIDeleteRecordsAudit(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	source := createUpstreamSourceAPITestSource(t, `{}`)

	response := upstreamSourceAPIRequest[any](t, router, http.MethodDelete, "/api/upstream_sources/"+strconv.Itoa(source.Id), nil, true)

	require.True(t, response.Success, response.Message)
	var log model.Log
	require.NoError(t, model.LOG_DB.Where("type = ?", model.LogTypeManage).First(&log).Error)
	assert.Contains(t, log.Content, "Deleted upstream source")
	assert.Contains(t, log.Content, source.Name)
	assert.Contains(t, log.Other, "upstream_source.delete")
}

func TestUpstreamSourceAPIListMappingsRedactsStoredErrors(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	source := createUpstreamSourceAPITestSource(t, `{}`)
	require.NoError(t, model.DB.Create(&model.UpstreamSourceChannelMapping{
		SourceID:        source.Id,
		UpstreamGroupID: "10",
		LastError:       "authorization: bearer sk-mapping-secret-token-value",
	}).Error)

	response := upstreamSourceAPIRequest[[]dto.UpstreamSourceMappingResponse](t, router, http.MethodGet, "/api/upstream_sources/"+strconv.Itoa(source.Id)+"/mappings", nil, true)

	require.True(t, response.Success, response.Message)
	raw := string(mustMarshalUpstreamSourceAPITest(t, response))
	assert.NotContains(t, raw, "sk-mapping-secret-token-value")
	assert.Contains(t, raw, "[redacted]")
}

func TestUpstreamSourceAPISyncReturnsMappingResults(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	source := createUpstreamSourceAPITestSource(t, `{}`)
	rate := 1.0
	require.NoError(t, model.DB.Create(&model.UpstreamSourceChannelMapping{
		SourceID:                source.Id,
		SyncEnabled:             true,
		UpstreamGroupID:         "10",
		UpstreamGroupName:       "primary",
		DiscoveryStatus:         model.UpstreamMappingDiscoveryStatusActive,
		EffectiveRateMultiplier: &rate,
	}).Error)

	response := upstreamSourceAPIRequest[dto.UpstreamSourceSyncResult](t, router, http.MethodPost, "/api/upstream_sources/"+strconv.Itoa(source.Id)+"/sync", nil, true)

	require.True(t, response.Success, response.Message)
	assert.Equal(t, source.Id, response.Data.SourceID)
	require.Len(t, response.Data.Results, 1)
	assert.Equal(t, "10", response.Data.Results[0].UpstreamGroupID)
	raw := string(mustMarshalUpstreamSourceAPITest(t, response))
	assert.NotContains(t, raw, "sk-")
}

func TestUpstreamSourceAPISyncResultReturnsLastMappingStatuses(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	source := createUpstreamSourceAPITestSource(t, `{}`)
	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Updates(map[string]any{
		"last_sync_status": model.UpstreamSyncStatusFailed,
		"last_sync_error":  "one mapping failed",
		"last_sync_time":   int64(1234),
	}).Error)
	require.NoError(t, model.DB.Create(&model.UpstreamSourceChannelMapping{
		SourceID:        source.Id,
		SyncEnabled:     true,
		UpstreamGroupID: "10",
		UpstreamKeyID:   "upstream-key-id",
		LocalChannelID:  55,
		SyncStatus:      model.UpstreamMappingSyncStatusFailed,
		LastError:       `models failed: {"password":"mapping-secret-password"}`,
	}).Error)

	response := upstreamSourceAPIRequest[dto.UpstreamSourceSyncResult](t, router, http.MethodGet, "/api/upstream_sources/"+strconv.Itoa(source.Id)+"/sync_result", nil, true)

	require.True(t, response.Success, response.Message)
	assert.Equal(t, model.UpstreamSyncStatusFailed, response.Data.Status)
	assert.Equal(t, "one mapping failed", response.Data.Error)
	require.Len(t, response.Data.Results, 1)
	assert.Equal(t, model.UpstreamMappingSyncStatusFailed, response.Data.Results[0].Status)
	assert.Equal(t, 55, response.Data.Results[0].LocalChannelID)
	raw := string(mustMarshalUpstreamSourceAPITest(t, response))
	assert.NotContains(t, raw, "upstream-key-id")
	assert.NotContains(t, raw, "mapping-secret-password")
	assert.Contains(t, raw, "[redacted]")
}

func TestUpstreamSourceAPIUpdateMappingsScopesSelectionToSource(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	sourceA := createUpstreamSourceAPITestSource(t, `{}`)
	sourceB := createUpstreamSourceAPITestSource(t, `{}`)
	sourceAMappingA := model.UpstreamSourceChannelMapping{
		SourceID:        sourceA.Id,
		SyncEnabled:     false,
		UpstreamGroupID: "source-a-1",
	}
	sourceAMappingB := model.UpstreamSourceChannelMapping{
		SourceID:        sourceA.Id,
		SyncEnabled:     true,
		UpstreamGroupID: "source-a-2",
	}
	sourceBMapping := model.UpstreamSourceChannelMapping{
		SourceID:        sourceB.Id,
		SyncEnabled:     true,
		UpstreamGroupID: "source-b-1",
	}
	require.NoError(t, model.DB.Create(&sourceAMappingA).Error)
	require.NoError(t, model.DB.Create(&sourceAMappingB).Error)
	require.NoError(t, model.DB.Create(&sourceBMapping).Error)
	request := dto.UpstreamSourceMappingUpdateRequest{
		MappingIDs: []int{sourceAMappingA.Id, sourceBMapping.Id},
	}

	response := upstreamSourceAPIRequest[[]dto.UpstreamSourceMappingResponse](t, router, http.MethodPut, "/api/upstream_sources/"+strconv.Itoa(sourceA.Id)+"/mappings", request, true)

	require.True(t, response.Success, response.Message)
	require.Len(t, response.Data, 2)
	assert.Equal(t, sourceA.Id, response.Data[0].SourceID)
	assert.Equal(t, sourceA.Id, response.Data[1].SourceID)
	var reloadedA1, reloadedA2, reloadedB model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.First(&reloadedA1, sourceAMappingA.Id).Error)
	require.NoError(t, model.DB.First(&reloadedA2, sourceAMappingB.Id).Error)
	require.NoError(t, model.DB.First(&reloadedB, sourceBMapping.Id).Error)
	assert.True(t, reloadedA1.SyncEnabled)
	assert.False(t, reloadedA2.SyncEnabled)
	assert.True(t, reloadedB.SyncEnabled)
}

func mustMarshalUpstreamSourceAPITest(t *testing.T, value any) []byte {
	t.Helper()

	data, err := common.Marshal(value)
	require.NoError(t, err)
	return data
}
