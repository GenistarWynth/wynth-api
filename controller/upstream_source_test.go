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
	"github.com/QuantumNous/new-api/service"

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
	model.InitCommonColumnsForTest()
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

	require.NoError(t, db.AutoMigrate(
		&model.UpstreamSource{},
		&model.UpstreamSourceSession{},
		&model.UpstreamSourceChannelMapping{},
		&model.UpstreamSourceScan{},
		&model.UpstreamSourceGroupChange{},
		&model.UpstreamSourceBalanceSnapshot{},
		&model.UpstreamSourceCostSnapshot{},
		&model.UpstreamSourceAnnouncement{},
		&model.UpstreamSourceAnnouncementIdentity{},
		&model.UpstreamSourceAnnouncementState{},
		&model.UpstreamSourceSubscriptionUsageSnapshot{},
		&model.UpstreamSourceCapabilityOutcome{},
		&model.UpstreamSourceNotificationSubscription{},
		&model.UpstreamSourceNotificationCooldown{},
		&model.UpstreamSourceNotificationDelivery{},
		&model.Channel{},
		&model.Ability{},
		&model.User{},
		&model.Log{},
		&model.ChannelMonitorLog{},
	))
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
		group.PUT("/:id/monitor", UpdateUpstreamSourceMonitor)
		group.DELETE("/:id/session", ClearUpstreamSourceSession)
		group.DELETE("/:id", DeleteUpstreamSource)
		group.POST("/:id/discover", DiscoverUpstreamSource)
		group.GET("/:id/mappings", ListUpstreamSourceMappings)
		group.PUT("/:id/mappings", UpdateUpstreamSourceMappings)
		group.GET("/:id/scans", ListUpstreamSourceScans)
		group.GET("/:id/monitor/runs", ListUpstreamSourceMonitorRuns)
		group.GET("/:id/monitor/snapshots", GetUpstreamSourceMonitorSnapshots)
		group.GET("/:id/monitor/outcomes", ListUpstreamSourceCapabilityOutcomes)
		group.GET("/:id/announcements", ListUpstreamSourceAnnouncements)
		group.GET("/:id/changes", ListUpstreamSourceGroupChanges)
		group.GET("/:id/notification_subscriptions", ListUpstreamSourceNotificationSubscriptions)
		group.POST("/:id/notification_subscriptions", CreateUpstreamSourceNotificationSubscription)
		group.PUT("/:id/notification_subscriptions/:subscription_id", UpdateUpstreamSourceNotificationSubscription)
		group.DELETE("/:id/notification_subscriptions/:subscription_id", DeleteUpstreamSourceNotificationSubscription)
		group.GET("/:id/notification_deliveries", ListUpstreamSourceNotificationDeliveries)
		group.POST("/:id/sync", SyncUpstreamSource)
		group.GET("/:id/sync_result", GetUpstreamSourceSyncResult)
		group.POST("/:id/auto_priority/run", RunUpstreamSourceAutoPriority)
	}
	if authenticated {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/login", nil)
		router.ServeHTTP(recorder, request)
	}
	return router
}

func TestUpstreamSourceAPIMonitorControlsUpdateSchedule(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	source := createUpstreamSourceAPITestSource(t, `{"email":"admin@example.com","password":"secret"}`)

	response := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodPut, "/api/upstream_sources/"+strconv.Itoa(source.Id)+"/monitor", map[string]any{
		"enabled":          true,
		"interval_minutes": 15,
	}, true)

	require.True(t, response.Success, response.Message)
	assert.True(t, response.Data.MonitorEnabled)
	assert.Equal(t, 15, response.Data.MonitorIntervalMinutes)
	assert.Greater(t, response.Data.NextMonitorAt, int64(0))
	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.True(t, reloaded.MonitorEnabled)
	assert.Equal(t, 15, reloaded.MonitorIntervalMinutes)
	assert.Greater(t, reloaded.NextMonitorAt, int64(0))
}

func TestUpstreamSourceAPINotificationSubscriptionCRUDAndDeliveryAudit(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	source := createUpstreamSourceAPITestSource(t, `{}`)
	basePath := "/api/upstream_sources/" + strconv.Itoa(source.Id)

	created := upstreamSourceAPIRequest[model.UpstreamSourceNotificationSubscription](t, router, http.MethodPost, basePath+"/notification_subscriptions", map[string]any{
		"event_type":       model.UpstreamSourceNotificationEventRateChanged,
		"group_id":         "premium",
		"enabled":          true,
		"cooldown_seconds": 600,
	}, true)
	require.True(t, created.Success, created.Message)
	assert.Equal(t, 1, created.Data.UserID)
	assert.Equal(t, source.Id, created.Data.SourceID)
	assert.Equal(t, "premium", created.Data.GroupID)

	listed := upstreamSourceAPIRequest[[]model.UpstreamSourceNotificationSubscription](t, router, http.MethodGet, basePath+"/notification_subscriptions", nil, true)
	require.True(t, listed.Success, listed.Message)
	require.Len(t, listed.Data, 1)
	assert.Equal(t, created.Data.Id, listed.Data[0].Id)

	updated := upstreamSourceAPIRequest[model.UpstreamSourceNotificationSubscription](t, router, http.MethodPut, basePath+"/notification_subscriptions/"+strconv.Itoa(created.Data.Id), map[string]any{
		"event_type": model.UpstreamSourceNotificationEventAnnouncementNew,
		"enabled":    false,
	}, true)
	require.True(t, updated.Success, updated.Message)
	assert.False(t, updated.Data.Enabled)
	assert.Equal(t, model.UpstreamSourceNotificationEventAnnouncementNew, updated.Data.EventType)

	require.NoError(t, model.DB.Create(&model.UpstreamSourceNotificationDelivery{
		UserID: 1, SourceID: source.Id, ScanID: 12, EventType: model.UpstreamSourceNotificationEventAnnouncementNew,
		EventKey: "announcement:12", Status: model.UpstreamSourceNotificationDeliveryFailed, Attempts: 3,
		ErrorSummary: "sanitized failure", CreatedAt: 100,
	}).Error)
	deliveries := upstreamSourceAPIRequest[[]model.UpstreamSourceNotificationDelivery](t, router, http.MethodGet, basePath+"/notification_deliveries", nil, true)
	require.True(t, deliveries.Success, deliveries.Message)
	require.Len(t, deliveries.Data, 1)
	assert.Equal(t, "sanitized failure", deliveries.Data[0].ErrorSummary)

	deleted := upstreamSourceAPIRequest[any](t, router, http.MethodDelete, basePath+"/notification_subscriptions/"+strconv.Itoa(created.Data.Id), nil, true)
	require.True(t, deleted.Success, deleted.Message)
	var count int64
	require.NoError(t, model.DB.Model(&model.UpstreamSourceNotificationSubscription{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestUpstreamSourceAPIReadsDurableAuthHealth(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	source := createUpstreamSourceAPITestSource(t, `{"email":"admin@example.com","password":"secret"}`)
	now := common.GetTimestamp()
	require.NoError(t, model.UpsertUpstreamSourceSessionTx(model.DB, &model.UpstreamSourceSession{
		SourceID:        source.Id,
		SessionConfig:   `{"access_token":"session-secret","session_source":"manual"}`,
		SessionSource:   "manual",
		AuthStatus:      model.UpstreamSourceAuthStatusHealthy,
		LastValidatedAt: now - 10,
		LastRefreshedAt: now - 20,
		ExpiresAt:       now + 60,
		CreatedTime:     now - 20,
		UpdatedTime:     now - 10,
	}))

	response := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodGet, "/api/upstream_sources/"+strconv.Itoa(source.Id), nil, true)

	require.True(t, response.Success, response.Message)
	assert.Equal(t, model.UpstreamSourceAuthStatusExpiring, response.Data.AuthStatus)
	assert.Equal(t, "manual", response.Data.SessionSource)
	assert.Equal(t, now-10, response.Data.AuthLastValidatedAt)
	assert.Equal(t, now-20, response.Data.AuthLastRefreshedAt)
	assert.Equal(t, now+60, response.Data.AuthExpiresAt)
	var session model.UpstreamSourceSession
	require.NoError(t, model.DB.Where("source_id = ?", source.Id).First(&session).Error)
	assert.Equal(t, model.UpstreamSourceAuthStatusExpiring, session.AuthStatus, "expiry reconciliation must be durable")
}

func TestUpstreamSourceAPIClearSessionPreservesCredentials(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	source := createUpstreamSourceAPITestSource(t, `{"email":"admin@example.com","password":"long-lived","access_token":"legacy-session"}`)
	require.NoError(t, model.UpsertUpstreamSourceSessionTx(model.DB, &model.UpstreamSourceSession{
		SourceID:      source.Id,
		SessionConfig: `{"access_token":"current-session","refresh_token":"current-refresh"}`,
		AuthStatus:    model.UpstreamSourceAuthStatusHealthy,
	}))

	response := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodDelete, "/api/upstream_sources/"+strconv.Itoa(source.Id)+"/session", nil, true)

	require.True(t, response.Success, response.Message)
	assert.True(t, response.Data.HasCredentials)
	assert.Equal(t, model.UpstreamSourceAuthStatusUnknown, response.Data.AuthStatus)
	assert.Empty(t, response.Data.SessionSource)
	session, err := model.GetUpstreamSourceSession(source.Id)
	require.NoError(t, err)
	assert.Nil(t, session)
	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	plaintext, err := service.ReadUpstreamSourceAuthConfig(reloaded.AuthConfig)
	require.NoError(t, err)
	assert.Contains(t, plaintext, "admin@example.com")
	assert.Contains(t, plaintext, "long-lived")
	assert.NotContains(t, plaintext, "legacy-session")
	assert.NotContains(t, plaintext, "current-session")
}

func TestUpstreamSourceAPIMonitorRunsFiltersOtherScanTypes(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	source := createUpstreamSourceAPITestSource(t, `{}`)
	scans := []model.UpstreamSourceScan{
		{SourceID: source.Id, ScanType: model.UpstreamSourceScanTypeDiscover, Status: model.UpstreamSourceScanStatusSuccess, StartedAt: 100, FinishedAt: 101},
		{SourceID: source.Id, ScanType: model.UpstreamSourceScanTypeMonitor, Status: model.UpstreamSourceScanStatusFailed, StartedAt: 200, FinishedAt: 201, ErrorSummary: "collector failed"},
		{SourceID: source.Id, ScanType: model.UpstreamSourceScanTypeMonitor, Status: model.UpstreamSourceScanStatusSuccess, StartedAt: 300, FinishedAt: 301},
	}
	require.NoError(t, model.DB.Create(&scans).Error)

	response := upstreamSourceAPIRequest[[]model.UpstreamSourceScan](t, router, http.MethodGet, "/api/upstream_sources/"+strconv.Itoa(source.Id)+"/monitor/runs", nil, true)

	require.True(t, response.Success, response.Message)
	require.Len(t, response.Data, 2)
	assert.Equal(t, scans[2].Id, response.Data[0].Id)
	assert.Equal(t, scans[1].Id, response.Data[1].Id)
	for _, scan := range response.Data {
		assert.Equal(t, model.UpstreamSourceScanTypeMonitor, scan.ScanType)
	}
}

func TestUpstreamSourceAPIReadsCollectedMonitorData(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	source := createUpstreamSourceAPITestSource(t, `{}`)
	other := createUpstreamSourceAPITestSource(t, `{}`)
	scans := []model.UpstreamSourceScan{
		{SourceID: source.Id, ScanType: model.UpstreamSourceScanTypeMonitor, Status: model.UpstreamSourceScanStatusSuccess, StartedAt: 100, FinishedAt: 101},
		{SourceID: source.Id, ScanType: model.UpstreamSourceScanTypeMonitor, Status: model.UpstreamSourceScanStatusSuccess, StartedAt: 200, FinishedAt: 201},
	}
	require.NoError(t, model.DB.Create(&scans).Error)
	require.NoError(t, model.DB.Create(&model.UpstreamSourceBalanceSnapshot{
		SourceID: source.Id, ScanID: scans[1].Id, Available: 25.5, Currency: "USD", CollectedAt: 201,
	}).Error)
	require.NoError(t, model.DB.Create(&[]model.UpstreamSourceCostSnapshot{
		{SourceID: source.Id, ScanID: scans[0].Id, Amount: 1.5, Currency: "USD", PeriodStart: 90, PeriodEnd: 100, CollectedAt: 101},
		{SourceID: source.Id, ScanID: scans[1].Id, Amount: 2.5, Currency: "USD", PeriodStart: 190, PeriodEnd: 200, CollectedAt: 201},
		{SourceID: other.Id, ScanID: scans[1].Id, Amount: 99, Currency: "USD", PeriodStart: 190, PeriodEnd: 200, CollectedAt: 201},
	}).Error)
	limit, remaining, percent := 10.0, 8.0, 80.0
	require.NoError(t, model.DB.Create(&[]model.UpstreamSourceSubscriptionUsageSnapshot{
		{SourceID: source.Id, ScanID: scans[1].Id, SubscriptionKey: "sub-1", Window: "daily", Unit: "USD", Used: 2, Limit: &limit, Remaining: &remaining, RemainingPercent: &percent, CollectedAt: 201},
		{SourceID: source.Id, ScanID: scans[1].Id, SubscriptionKey: "sub-1", Window: "weekly", Unit: "USD", Used: 4, Limit: &limit, Remaining: &remaining, RemainingPercent: &percent, CollectedAt: 201},
	}).Error)
	require.NoError(t, model.DB.Create(&[]model.UpstreamSourceAnnouncement{
		{SourceID: source.Id, ScanID: scans[0].Id, SourceKey: "old", Title: "Old", FirstSeenAt: 101, LastSeenAt: 101},
		{SourceID: source.Id, ScanID: scans[1].Id, SourceKey: "new", Title: "New", FirstSeenAt: 201, LastSeenAt: 201, IsNew: true},
		{SourceID: other.Id, ScanID: scans[1].Id, SourceKey: "other", Title: "Other", FirstSeenAt: 201, LastSeenAt: 201},
	}).Error)
	require.NoError(t, model.DB.Create(&[]model.UpstreamSourceCapabilityOutcome{
		{SourceID: source.Id, ScanID: scans[1].Id, Capability: model.UpstreamSourceCapabilityBalance, Status: model.UpstreamSourceCapabilityStatusSuccess, StartedAt: 200, FinishedAt: 201},
		{SourceID: source.Id, ScanID: scans[1].Id, Capability: model.UpstreamSourceCapabilitySubscriptionUsage, Status: model.UpstreamSourceCapabilityStatusSuccess, ItemCount: 1, StartedAt: 200, FinishedAt: 201},
	}).Error)

	snapshotResponse := upstreamSourceAPIRequest[service.UpstreamSourceLatestMonitorSnapshots](t, router, http.MethodGet, "/api/upstream_sources/"+strconv.Itoa(source.Id)+"/monitor/snapshots", nil, true)
	require.True(t, snapshotResponse.Success, snapshotResponse.Message)
	require.NotNil(t, snapshotResponse.Data.Balance)
	assert.Equal(t, 25.5, snapshotResponse.Data.Balance.Available)
	require.NotNil(t, snapshotResponse.Data.Cost)
	assert.Equal(t, 2.5, snapshotResponse.Data.Cost.Amount)
	require.Len(t, snapshotResponse.Data.SubscriptionUsage, 2)
	for _, window := range snapshotResponse.Data.SubscriptionUsage {
		assert.Equal(t, scans[1].Id, window.ScanID)
	}

	announcementResponse := upstreamSourceAPIRequest[[]model.UpstreamSourceAnnouncement](t, router, http.MethodGet, "/api/upstream_sources/"+strconv.Itoa(source.Id)+"/announcements?limit=1", nil, true)
	require.True(t, announcementResponse.Success, announcementResponse.Message)
	require.Len(t, announcementResponse.Data, 1)
	assert.Equal(t, "new", announcementResponse.Data[0].SourceKey)

	outcomeResponse := upstreamSourceAPIRequest[[]model.UpstreamSourceCapabilityOutcome](t, router, http.MethodGet, "/api/upstream_sources/"+strconv.Itoa(source.Id)+"/monitor/outcomes?limit=10", nil, true)
	require.True(t, outcomeResponse.Success, outcomeResponse.Message)
	require.Len(t, outcomeResponse.Data, 2)
	for _, outcome := range outcomeResponse.Data {
		assert.Equal(t, source.Id, outcome.SourceID)
	}
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
		Name:         "created-source",
		Type:         model.UpstreamSourceTypeSub2API,
		BaseURL:      "https://admin.example.com",
		RelayBaseURL: "https://relay.example.com",
		Email:        "wynth@example.com",
		Password:     "plain-password",
		LocalGroup:   "paid",
		LocalGroupRules: []dto.UpstreamSourceLocalGroupRule{
			{
				Name:        "OpenAI pro",
				LocalGroup:  "paid",
				ChannelType: constant.ChannelTypeOpenAI,
				Priority:    common.GetPointer(int64(12)),
				Weight:      common.GetPointer(uint(34)),
				Platforms:   []string{"openai"},
				Monitor: &dto.UpstreamSourceRuleMonitor{
					Enabled: common.GetPointer(true),
					Model:   "gpt-4o-mini",
				},
			},
		},
	}

	response := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodPost, "/api/upstream_sources", request, true)

	require.True(t, response.Success, response.Message)
	assert.Equal(t, "created-source", response.Data.Name)
	assert.True(t, response.Data.HasCredentials)
	assert.NotContains(t, response.Data.MaskedEmail, "wynth@example.com")
	assert.Equal(t, "paid", response.Data.LocalGroup)
	require.Len(t, response.Data.LocalGroupRules, 1)
	rule := response.Data.LocalGroupRules[0]
	assert.Equal(t, constant.ChannelTypeOpenAI, rule.ChannelType)
	require.NotNil(t, rule.Priority)
	assert.Equal(t, int64(12), *rule.Priority)
	require.NotNil(t, rule.Weight)
	assert.Equal(t, uint(34), *rule.Weight)
	require.NotNil(t, rule.Monitor)
	assert.Equal(t, "gpt-4o-mini", rule.Monitor.Model)
	raw := string(mustMarshalUpstreamSourceAPITest(t, response))
	assert.NotContains(t, raw, "plain-password")
	var source model.UpstreamSource
	require.NoError(t, model.DB.First(&source, response.Data.Id).Error)
	assert.Contains(t, source.AuthConfig, "wynth@example.com")
	assert.Contains(t, source.AuthConfig, "plain-password")
	assert.Contains(t, source.SyncConfig, "paid")
	var syncConfig map[string]any
	require.NoError(t, common.UnmarshalJsonStr(source.SyncConfig, &syncConfig))
	assert.Equal(t, float64(1), syncConfig["sync_config_version"])
}

// TestUpstreamSourceAPICreatePersistsBaseLocalGroupAsDefaultLocalGroupFallback
// guards against a regression where a source's base Local Group ("paid")
// stopped propagating into the persisted default_local_group fallback used
// by blank-local_group rules (service.upstreamSourceDefaultLocalGroup). The
// request DTO no longer carries default_local_group directly, so the
// controller must derive it from LocalGroup instead of leaving a stale
// "default" placeholder in the persisted sync_config.
func TestUpstreamSourceAPICreatePersistsBaseLocalGroupAsDefaultLocalGroupFallback(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	request := dto.UpstreamSourceCreateRequest{
		Name:       "paid-group-source",
		Type:       model.UpstreamSourceTypeSub2API,
		BaseURL:    "https://admin.example.com",
		LocalGroup: "paid",
		LocalGroupRules: []dto.UpstreamSourceLocalGroupRule{
			{
				Name:       "Catch-all",
				LocalGroup: "",
				Platforms:  []string{"openai"},
			},
		},
	}

	response := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodPost, "/api/upstream_sources", request, true)

	require.True(t, response.Success, response.Message)
	assert.Equal(t, "paid", response.Data.LocalGroup)
	require.Len(t, response.Data.LocalGroupRules, 1)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, response.Data.Id).Error)
	var syncConfig map[string]any
	require.NoError(t, common.UnmarshalJsonStr(reloaded.SyncConfig, &syncConfig))
	assert.Equal(t, "paid", syncConfig["local_group"])
	assert.Equal(t, "paid", syncConfig["default_local_group"])
}

func TestUpstreamSourceAPICreateRoundTripsAutoPriorityConfig(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	request := dto.UpstreamSourceCreateRequest{
		Name:       "auto-priority-source",
		Type:       model.UpstreamSourceTypeSub2API,
		BaseURL:    "https://admin.example.com",
		LocalGroup: "paid",
		LocalGroupRules: []dto.UpstreamSourceLocalGroupRule{
			{
				Name:       "OpenAI pro",
				LocalGroup: "paid",
				Platforms:  []string{"openai"},
				AutoPriority: &dto.UpstreamSourceRuleAutoPriority{
					Enabled:     common.GetPointer(false),
					WindowHours: common.GetPointer(48),
				},
			},
		},
	}

	response := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodPost, "/api/upstream_sources", request, true)

	require.True(t, response.Success, response.Message)
	require.Len(t, response.Data.LocalGroupRules, 1)
	require.NotNil(t, response.Data.LocalGroupRules[0].AutoPriority)
	require.NotNil(t, response.Data.LocalGroupRules[0].AutoPriority.Enabled)
	assert.False(t, *response.Data.LocalGroupRules[0].AutoPriority.Enabled)
	require.NotNil(t, response.Data.LocalGroupRules[0].AutoPriority.WindowHours)
	assert.Equal(t, 48, *response.Data.LocalGroupRules[0].AutoPriority.WindowHours)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, response.Data.Id).Error)
	var syncConfig map[string]any
	require.NoError(t, common.UnmarshalJsonStr(reloaded.SyncConfig, &syncConfig))
	require.Contains(t, syncConfig, "local_group_rules")
	rules, ok := syncConfig["local_group_rules"].([]any)
	require.True(t, ok)
	require.Len(t, rules, 1)
	rule, ok := rules[0].(map[string]any)
	require.True(t, ok)
	autoPriority, ok := rule["auto_priority"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, autoPriority["enabled"])
	assert.NotContains(t, autoPriority, "interval_minutes")
	assert.Equal(t, float64(48), autoPriority["window_hours"])
}

func TestUpstreamSourceAPICreateDropsLegacyAutoPriorityInterval(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	rule := map[string]any{
		"name":        "OpenAI pro",
		"local_group": "paid",
		"auto_priority": map[string]any{
			"enabled":          true,
			"interval_minutes": 0,
			"window_hours":     999,
		},
	}
	request := map[string]any{
		"name":              "auto-priority-zero-source",
		"type":              model.UpstreamSourceTypeSub2API,
		"base_url":          "https://admin.example.com",
		"local_group":       "paid",
		"local_group_rules": []map[string]any{rule},
	}

	response := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodPost, "/api/upstream_sources", request, true)

	require.True(t, response.Success, response.Message)
	require.Len(t, response.Data.LocalGroupRules, 1)
	require.NotNil(t, response.Data.LocalGroupRules[0].AutoPriority.WindowHours)
	assert.Equal(t, 168, *response.Data.LocalGroupRules[0].AutoPriority.WindowHours)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, response.Data.Id).Error)
	var syncConfig map[string]any
	require.NoError(t, common.UnmarshalJsonStr(reloaded.SyncConfig, &syncConfig))
	rules, ok := syncConfig["local_group_rules"].([]any)
	require.True(t, ok)
	require.Len(t, rules, 1)
	rawRule, ok := rules[0].(map[string]any)
	require.True(t, ok)
	autoPriority, ok := rawRule["auto_priority"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, autoPriority, "interval_minutes")
	assert.Equal(t, float64(168), autoPriority["window_hours"])

	updateRequest := map[string]any{
		"status":            model.UpstreamSourceStatusEnabled,
		"local_group":       "paid",
		"local_group_rules": []map[string]any{rule},
	}
	updateResponse := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodPut, "/api/upstream_sources/"+strconv.Itoa(response.Data.Id), updateRequest, true)
	require.True(t, updateResponse.Success, updateResponse.Message)
	require.Len(t, updateResponse.Data.LocalGroupRules, 1)
	require.NotNil(t, updateResponse.Data.LocalGroupRules[0].AutoPriority.WindowHours)
	assert.Equal(t, 168, *updateResponse.Data.LocalGroupRules[0].AutoPriority.WindowHours)
}

func TestUpstreamSourceAPIRoundTripsCodexImageGenerationBridgePolicy(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	request := dto.UpstreamSourceCreateRequest{
		Name:    "codex-bridge-source",
		Type:    model.UpstreamSourceTypeNewAPI,
		BaseURL: "https://admin.example.com",
		LocalGroupRules: []dto.UpstreamSourceLocalGroupRule{
			{
				Name:                             "OpenAI pro",
				LocalGroup:                       "paid",
				ChannelType:                      constant.ChannelTypeCodex,
				Platforms:                        []string{"openai"},
				CodexImageGenerationBridgePolicy: dto.CodexImageGenerationBridgePolicyDisabled,
			},
		},
	}

	response := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodPost, "/api/upstream_sources", request, true)

	require.True(t, response.Success, response.Message)
	require.Len(t, response.Data.LocalGroupRules, 1)
	assert.Equal(t, dto.CodexImageGenerationBridgePolicyDisabled, response.Data.LocalGroupRules[0].CodexImageGenerationBridgePolicy)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, response.Data.Id).Error)
	var syncConfig map[string]any
	require.NoError(t, common.UnmarshalJsonStr(reloaded.SyncConfig, &syncConfig))
	rules, ok := syncConfig["local_group_rules"].([]any)
	require.True(t, ok)
	require.Len(t, rules, 1)
	rule, ok := rules[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, dto.CodexImageGenerationBridgePolicyDisabled, rule["codex_image_generation_bridge_policy"])
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
		Name:           "false-sync-source",
		Type:           model.UpstreamSourceTypeSub2API,
		BaseURL:        "https://admin.example.com",
		LocalGroup:     "paid",
		AllowPrivateIP: true,
		LocalGroupRules: []dto.UpstreamSourceLocalGroupRule{
			{
				Name:        "OpenAI pro",
				LocalGroup:  "paid",
				ChannelType: constant.ChannelTypeOpenAI,
				Priority:    common.GetPointer(int64(10)),
				Weight:      common.GetPointer(uint(20)),
				Monitor:     &dto.UpstreamSourceRuleMonitor{Enabled: common.GetPointer(true)},
				AutoSync:    &dto.UpstreamSourceRuleAutoSync{Enabled: common.GetPointer(true)},
			},
		},
	}
	createResponse := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodPost, "/api/upstream_sources", createRequest, true)
	require.True(t, createResponse.Success, createResponse.Message)

	updateRequest := dto.UpstreamSourceUpdateRequest{
		Status:         model.UpstreamSourceStatusEnabled,
		LocalGroup:     "default",
		AllowPrivateIP: false,
		LocalGroupRules: []dto.UpstreamSourceLocalGroupRule{
			{
				Name:        "OpenAI pro",
				LocalGroup:  "default",
				ChannelType: constant.ChannelTypeOpenAI,
				Priority:    common.GetPointer(int64(0)),
				Weight:      common.GetPointer(uint(0)),
				Monitor:     &dto.UpstreamSourceRuleMonitor{Enabled: common.GetPointer(false)},
				AutoSync:    &dto.UpstreamSourceRuleAutoSync{Enabled: common.GetPointer(false)},
			},
		},
	}
	updateResponse := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodPut, "/api/upstream_sources/"+strconv.Itoa(createResponse.Data.Id), updateRequest, true)
	require.True(t, updateResponse.Success, updateResponse.Message)
	assert.False(t, updateResponse.Data.AllowPrivateIP)
	require.Len(t, updateResponse.Data.LocalGroupRules, 1)
	updatedRule := updateResponse.Data.LocalGroupRules[0]
	require.NotNil(t, updatedRule.Priority)
	assert.Equal(t, int64(0), *updatedRule.Priority)
	require.NotNil(t, updatedRule.Weight)
	assert.Equal(t, uint(0), *updatedRule.Weight)
	require.NotNil(t, updatedRule.Monitor)
	require.NotNil(t, updatedRule.Monitor.Enabled)
	assert.False(t, *updatedRule.Monitor.Enabled)
	require.NotNil(t, updatedRule.AutoSync)
	require.NotNil(t, updatedRule.AutoSync.Enabled)
	assert.False(t, *updatedRule.AutoSync.Enabled)

	getResponse := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodGet, "/api/upstream_sources/"+strconv.Itoa(createResponse.Data.Id), nil, true)
	require.True(t, getResponse.Success, getResponse.Message)
	assert.False(t, getResponse.Data.AllowPrivateIP)
	require.Len(t, getResponse.Data.LocalGroupRules, 1)
	getRule := getResponse.Data.LocalGroupRules[0]
	require.NotNil(t, getRule.Priority)
	assert.Equal(t, int64(0), *getRule.Priority)
	require.NotNil(t, getRule.Weight)
	assert.Equal(t, uint(0), *getRule.Weight)
	require.NotNil(t, getRule.Monitor)
	require.NotNil(t, getRule.Monitor.Enabled)
	assert.False(t, *getRule.Monitor.Enabled)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, createResponse.Data.Id).Error)
	var syncConfig map[string]any
	require.NoError(t, common.UnmarshalJsonStr(reloaded.SyncConfig, &syncConfig))
	assert.Equal(t, false, syncConfig["allow_private_ip"])
	assert.Equal(t, float64(1), syncConfig["sync_config_version"])
	rules, ok := syncConfig["local_group_rules"].([]any)
	require.True(t, ok)
	require.Len(t, rules, 1)
	rawRule, ok := rules[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(0), rawRule["priority"])
	assert.Equal(t, float64(0), rawRule["weight"])
	monitor, ok := rawRule["monitor"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, monitor["enabled"])
	autoSync, ok := rawRule["auto_sync"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, autoSync["enabled"])
}

func TestUpstreamSourceAPICredentialsUpdateClearsCachedTokens(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	source := createUpstreamSourceAPITestSource(t, `{"email":"old@example.com","password":"old-password","access_token":"old-access-token","refresh_token":"old-refresh-token","expires_at":9999999999}`)
	require.NoError(t, model.UpsertUpstreamSourceSessionTx(model.DB, &model.UpstreamSourceSession{
		SourceID:      source.Id,
		SessionConfig: `{"access_token":"dedicated-access-token","refresh_token":"dedicated-refresh-token"}`,
		AuthStatus:    model.UpstreamSourceAuthStatusHealthy,
	}))
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
	session, err := model.GetUpstreamSourceSession(source.Id)
	require.NoError(t, err)
	assert.Nil(t, session, "credential rotation must invalidate independently stored login sessions")
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

func TestUpstreamSourceAPIUpdateRefreshesGeneratedChannelConnection(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	source := createUpstreamSourceAPITestSource(t, `{}`)
	originalSyncConfig := `{"sync_config_version":1,"local_group":"paid","local_group_rules":[{"name":"keep-rule","local_group":"paid","platforms":["openai"],"model_strategy":"fixed","fixed_models":["gpt-4o"]}]}`
	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Update("sync_config", originalSyncConfig).Error)

	rate := 1.0
	mapping := model.UpstreamSourceChannelMapping{
		SourceID: source.Id, SyncEnabled: true, UpstreamGroupID: "group-1",
		UpstreamGroupName: "primary", DiscoveryStatus: model.UpstreamMappingDiscoveryStatusActive,
		EffectiveRateMultiplier: &rate,
	}
	require.NoError(t, model.DB.Create(&mapping).Error)
	oldBaseURL := "https://relay.example.com"
	channel := model.Channel{
		Name: "source-a / 1.000x", Type: constant.ChannelTypeOpenAI, Key: "sk-existing",
		BaseURL: &oldBaseURL, Models: "gpt-4o", Group: "paid", Status: common.ChannelStatusEnabled,
		Priority: common.GetPointer(int64(10)), Weight: common.GetPointer(uint(1)), Tag: common.GetPointer("source-a"),
	}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID: source.Id, GeneratedByUpstreamMappingID: mapping.Id,
	})
	require.NoError(t, model.DB.Create(&channel).Error)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Update("local_channel_id", channel.Id).Error)
	require.NoError(t, model.DB.Create(&model.Ability{
		Group: "paid", Model: "gpt-4o", ChannelId: channel.Id, Enabled: true,
		Priority: common.GetPointer(int64(10)), Weight: 1,
	}).Error)
	model.InitChannelCache()

	request := map[string]any{
		"name": "source-renamed", "type": model.UpstreamSourceTypeNewAPI,
		"status":   model.UpstreamSourceStatusEnabled,
		"base_url": "https://new-admin.example.com", "admin_api_base_path": "/api",
		"relay_base_url": "https://new-relay.example.com", "local_group": "paid",
		"local_group_rules": []map[string]any{{
			"name": "keep-rule", "local_group": "paid", "platforms": []string{"openai"},
			"model_strategy": "fixed", "fixed_models": []string{"gpt-4o"},
		}},
	}
	response := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodPut, "/api/upstream_sources/"+strconv.Itoa(source.Id), request, true)

	require.True(t, response.Success, response.Message)
	assert.Equal(t, model.UpstreamSourceTypeNewAPI, response.Data.Type)
	assert.Equal(t, "https://new-admin.example.com", response.Data.BaseURL)
	assert.Equal(t, "https://new-relay.example.com", response.Data.RelayBaseURL)
	require.Len(t, response.Data.LocalGroupRules, 1)
	assert.Equal(t, "keep-rule", response.Data.LocalGroupRules[0].Name)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Equal(t, model.UpstreamSourceTypeNewAPI, reloaded.Type)
	assert.Contains(t, reloaded.SyncConfig, "keep-rule")
	var reloadedChannel model.Channel
	require.NoError(t, model.DB.First(&reloadedChannel, channel.Id).Error)
	require.NotNil(t, reloadedChannel.BaseURL)
	assert.Equal(t, "https://new-relay.example.com", *reloadedChannel.BaseURL)
	assert.Equal(t, "sk-existing", reloadedChannel.Key)
	assert.Equal(t, constant.ChannelTypeOpenAI, reloadedChannel.Type)
	assert.Equal(t, "paid", reloadedChannel.Group)
	assert.Equal(t, int64(10), reloadedChannel.GetPriority())
	cachedChannel, err := model.CacheGetChannel(channel.Id)
	require.NoError(t, err)
	require.NotNil(t, cachedChannel.BaseURL)
	assert.Equal(t, "https://new-relay.example.com", *cachedChannel.BaseURL)

	adapter, err := service.DefaultUpstreamSourceAdapterFactory(reloaded.Type)
	require.NoError(t, err)
	assert.IsType(t, service.NewAPIAdapter{}, adapter)
}

func TestUpstreamSourceAPIUpdateRollsBackWhenGeneratedChannelRefreshFails(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	source := createUpstreamSourceAPITestSource(t, `{}`)
	rate := 1.0
	mapping := model.UpstreamSourceChannelMapping{
		SourceID: source.Id, UpstreamGroupID: "group-1", DiscoveryStatus: model.UpstreamMappingDiscoveryStatusActive,
		EffectiveRateMultiplier: &rate,
	}
	require.NoError(t, model.DB.Create(&mapping).Error)
	oldBaseURL := "https://relay.example.com"
	channel := model.Channel{Name: "source-a / 1.000x", Type: constant.ChannelTypeOpenAI, Key: "sk-existing", BaseURL: &oldBaseURL}
	channel.SetOtherSettings(dto.ChannelOtherSettings{GeneratedByUpstreamSourceID: source.Id, GeneratedByUpstreamMappingID: mapping.Id})
	require.NoError(t, model.DB.Create(&channel).Error)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Update("local_channel_id", channel.Id).Error)
	require.NoError(t, model.DB.Exec(`CREATE TRIGGER fail_generated_channel_refresh BEFORE UPDATE OF base_url ON channels BEGIN SELECT RAISE(FAIL, 'refresh failed'); END`).Error)

	response := upstreamSourceAPIRequest[dto.UpstreamSourceResponse](t, router, http.MethodPut, "/api/upstream_sources/"+strconv.Itoa(source.Id), map[string]any{
		"name": source.Name, "type": model.UpstreamSourceTypeNewAPI, "status": source.Status,
		"base_url": "https://new-admin.example.com", "relay_base_url": "https://new-relay.example.com",
	}, true)

	require.False(t, response.Success)
	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Equal(t, model.UpstreamSourceTypeSub2API, reloaded.Type)
	assert.Equal(t, "https://admin.example.com", reloaded.BaseURL)
	assert.Equal(t, "https://relay.example.com", reloaded.RelayBaseURL)
	var reloadedChannel model.Channel
	require.NoError(t, model.DB.First(&reloadedChannel, channel.Id).Error)
	require.NotNil(t, reloadedChannel.BaseURL)
	assert.Equal(t, oldBaseURL, *reloadedChannel.BaseURL)
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

func TestUpstreamSourceAPIHistoryIsScopedAndBounded(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	sourceA := createUpstreamSourceAPITestSource(t, `{}`)
	sourceB := createUpstreamSourceAPITestSource(t, `{}`)
	scans := []model.UpstreamSourceScan{
		{SourceID: sourceA.Id, ScanType: model.UpstreamSourceScanTypeDiscover, Status: model.UpstreamSourceScanStatusSuccess, StartedAt: 100, FinishedAt: 101},
		{SourceID: sourceA.Id, ScanType: model.UpstreamSourceScanTypeDiscover, Status: model.UpstreamSourceScanStatusFailed, StartedAt: 200, FinishedAt: 201},
		{SourceID: sourceB.Id, ScanType: model.UpstreamSourceScanTypeDiscover, Status: model.UpstreamSourceScanStatusSuccess, StartedAt: 300, FinishedAt: 301},
	}
	require.NoError(t, model.DB.Create(&scans).Error)
	changes := []model.UpstreamSourceGroupChange{
		{SourceID: sourceA.Id, ScanID: scans[0].Id, ChangeType: model.UpstreamSourceGroupChangeAdded, UpstreamGroupID: "10", CreatedAt: 100},
		{SourceID: sourceA.Id, ScanID: scans[1].Id, ChangeType: model.UpstreamSourceGroupChangeRemoved, UpstreamGroupID: "20", CreatedAt: 200},
		{SourceID: sourceB.Id, ScanID: scans[2].Id, ChangeType: model.UpstreamSourceGroupChangeAdded, UpstreamGroupID: "other", CreatedAt: 300},
	}
	require.NoError(t, model.DB.Create(&changes).Error)

	scanResponse := upstreamSourceAPIRequest[[]model.UpstreamSourceScan](t, router, http.MethodGet, "/api/upstream_sources/"+strconv.Itoa(sourceA.Id)+"/scans?limit=1", nil, true)
	require.True(t, scanResponse.Success, scanResponse.Message)
	require.Len(t, scanResponse.Data, 1)
	assert.Equal(t, scans[1].Id, scanResponse.Data[0].Id)
	assert.Equal(t, sourceA.Id, scanResponse.Data[0].SourceID)

	changeResponse := upstreamSourceAPIRequest[[]model.UpstreamSourceGroupChange](t, router, http.MethodGet, "/api/upstream_sources/"+strconv.Itoa(sourceA.Id)+"/changes?limit=1", nil, true)
	require.True(t, changeResponse.Success, changeResponse.Message)
	require.Len(t, changeResponse.Data, 1)
	assert.Equal(t, changes[1].Id, changeResponse.Data[0].Id)
	assert.Equal(t, sourceA.Id, changeResponse.Data[0].SourceID)
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

func TestUpstreamSourceAPIRunAutoPriorityReturnsResults(t *testing.T) {
	setupUpstreamSourceAPITestDB(t)
	router := upstreamSourceAPIRouter(true)
	source := createUpstreamSourceAPITestSource(t, `{}`)
	syncConfig, err := common.Marshal(map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 0,
		"auto_priority_window_hours":     24,
	})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Update("sync_config", string(syncConfig)).Error)
	rate := 0.5
	mapping := model.UpstreamSourceChannelMapping{
		SourceID:                source.Id,
		SyncEnabled:             true,
		UpstreamGroupID:         "10",
		UpstreamGroupName:       "primary",
		UpstreamPlatform:        "openai",
		DiscoveryStatus:         model.UpstreamMappingDiscoveryStatusActive,
		UpstreamStatus:          model.UpstreamSourceStatusEnabled,
		EffectiveRateMultiplier: &rate,
	}
	require.NoError(t, model.DB.Create(&mapping).Error)
	channel := model.Channel{
		Type:          constant.ChannelTypeOpenAI,
		Key:           "sk-test",
		Status:        common.ChannelStatusEnabled,
		Name:          "source-a / primary",
		Weight:        common.GetPointer(uint(0)),
		Models:        "gpt-4o",
		Group:         "default",
		Priority:      common.GetPointer(int64(100)),
		Other:         "{}",
		OtherInfo:     "{}",
		OtherSettings: "{}",
	}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:   true,
		GeneratedByUpstreamSourceID:  source.Id,
		GeneratedByUpstreamMappingID: mapping.Id,
	})
	require.NoError(t, model.DB.Create(&channel).Error)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Update("local_channel_id", channel.Id).Error)
	require.NoError(t, model.DB.Create(&model.Ability{
		Group:     channel.Group,
		Model:     "gpt-4o",
		ChannelId: channel.Id,
		Enabled:   true,
		Priority:  common.GetPointer(int64(100)),
		Weight:    0,
	}).Error)
	manualChannel := model.Channel{
		Type:          constant.ChannelTypeOpenAI,
		Key:           "sk-manual",
		Status:        common.ChannelStatusEnabled,
		Name:          "manual peer",
		Weight:        common.GetPointer(uint(0)),
		Models:        "gpt-4o",
		Group:         channel.Group,
		Priority:      common.GetPointer(int64(100)),
		Other:         "{}",
		OtherInfo:     "{}",
		OtherSettings: "{}",
	}
	manualChannel.SetOtherSettings(dto.ChannelOtherSettings{ChannelAutoPriorityEnabled: true})
	require.NoError(t, model.DB.Create(&manualChannel).Error)
	require.NoError(t, model.DB.Create(&model.Ability{
		Group:     manualChannel.Group,
		Model:     "gpt-4o",
		ChannelId: manualChannel.Id,
		Enabled:   true,
		Priority:  common.GetPointer(int64(100)),
		Weight:    0,
	}).Error)

	response := upstreamSourceAPIRequest[dto.UpstreamSourceAutoPriorityResult](t, router, http.MethodPost, "/api/upstream_sources/"+strconv.Itoa(source.Id)+"/auto_priority/run", nil, true)

	require.True(t, response.Success, response.Message)
	assert.Equal(t, source.Id, response.Data.SourceID)
	require.Len(t, response.Data.Results, 2)
	assert.ElementsMatch(t, []int{channel.Id, manualChannel.Id}, []int{
		response.Data.Results[0].LocalChannelID,
		response.Data.Results[1].LocalChannelID,
	})
	assert.Equal(t, mapping.Id, response.Data.Results[0].MappingID)
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

func TestUpstreamSourceResponseReportsTurnstileBlocked(t *testing.T) {
	source := model.UpstreamSource{
		Id:            1,
		Type:          model.UpstreamSourceTypeNewAPI,
		LastSyncError: service.ErrUpstreamSourceTurnstileRequired.Error(),
	}
	resp := upstreamSourceResponse(source)
	assert.True(t, resp.TurnstileBlocked)
}

func mustMarshalUpstreamSourceAPITest(t *testing.T, value any) []byte {
	t.Helper()

	data, err := common.Marshal(value)
	require.NoError(t, err)
	return data
}

func TestUpstreamSourceURLNormalizationStripsTrailingSlashes(t *testing.T) {
	t.Run("create trims trailing slashes and keeps leading path slash", func(t *testing.T) {
		source, err := upstreamSourceFromCreateRequest(dto.UpstreamSourceCreateRequest{
			Name:             "s",
			Type:             model.UpstreamSourceTypeSub2API,
			BaseURL:          "https://api.example.com/",
			RelayBaseURL:     "https://relay.example.com//",
			AdminAPIBasePath: "/api/v1/",
		})
		require.NoError(t, err)
		assert.Equal(t, "https://api.example.com", source.BaseURL)
		assert.Equal(t, "https://relay.example.com", source.RelayBaseURL)
		assert.Equal(t, "/api/v1", source.AdminAPIBasePath)
	})

	t.Run("create falls back relay URL to the trimmed base URL", func(t *testing.T) {
		source, err := upstreamSourceFromCreateRequest(dto.UpstreamSourceCreateRequest{
			Name:    "s",
			Type:    model.UpstreamSourceTypeSub2API,
			BaseURL: "https://api.example.com/",
		})
		require.NoError(t, err)
		assert.Equal(t, "https://api.example.com", source.RelayBaseURL)
	})

	t.Run("update trims trailing slashes", func(t *testing.T) {
		updates, err := upstreamSourceUpdateMap(dto.UpstreamSourceUpdateRequest{
			BaseURL:          "https://api.example.com/",
			RelayBaseURL:     "https://relay.example.com/",
			AdminAPIBasePath: "/api/v1/",
		})
		require.NoError(t, err)
		assert.Equal(t, "https://api.example.com", updates["base_url"])
		assert.Equal(t, "https://relay.example.com", updates["relay_base_url"])
		assert.Equal(t, "/api/v1", updates["admin_api_base_path"])
	})
}
