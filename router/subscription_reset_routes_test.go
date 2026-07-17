package router

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
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

var subscriptionResetRouteDBCounter atomic.Uint32

type subscriptionResetRouteResponse struct {
	Success bool                          `json:"success"`
	Message string                        `json:"message"`
	Code    string                        `json:"code"`
	Data    model.SubscriptionResetResult `json:"data"`
}

type subscriptionResetAuditOther struct {
	Op struct {
		Action string         `json:"action"`
		Params map[string]any `json:"params"`
	} `json:"op"`
	AdminInfo struct {
		AdminID int `json:"admin_id"`
	} `json:"admin_info"`
}

func setupSubscriptionResetRouteTest(t *testing.T) (*gorm.DB, model.SubscriptionPlan) {
	t.Helper()
	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedisEnabled := common.RedisEnabled
	oldCriticalRateLimitEnable := common.CriticalRateLimitEnable
	oldGlobalAPIRateLimitEnable := common.GlobalApiRateLimitEnable
	oldTranslateMessage := common.TranslateMessage
	oldMainDBType := common.MainDatabaseType()

	dsn := fmt.Sprintf("file:subscription-reset-routes-%d?mode=memory&cache=shared", subscriptionResetRouteDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	common.CriticalRateLimitEnable = false
	common.GlobalApiRateLimitEnable = false
	common.TranslateMessage = func(_ *gin.Context, key string, _ ...map[string]any) string { return key }
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedisEnabled
		common.CriticalRateLimitEnable = oldCriticalRateLimitEnable
		common.GlobalApiRateLimitEnable = oldGlobalAPIRateLimitEnable
		common.TranslateMessage = oldTranslateMessage
		common.SetMainDatabaseType(oldMainDBType)
		require.NoError(t, sqlDB.Close())
	})

	require.NoError(t, db.AutoMigrate(&model.User{}, &model.SubscriptionPlan{}, &model.UserSubscription{}, &model.Log{}))
	users := []model.User{
		{Id: 1, Username: "root", Password: "password", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "reset-route-root"},
		{Id: 2, Username: "admin", Password: "password", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "reset-route-admin"},
		{Id: 3, Username: "peer-admin", Password: "password", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "reset-route-peer"},
		{Id: 4, Username: "member", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "reset-route-member"},
	}
	require.NoError(t, db.Create(&users).Error)
	plan := model.SubscriptionPlan{Id: 501, Title: "Reset Plan", PriceAmount: 1, DurationUnit: model.SubscriptionDurationMonth, DurationValue: 1, TotalAmount: 1000, QuotaResetPeriod: model.SubscriptionResetDaily}
	require.NoError(t, db.Create(&plan).Error)
	now := model.GetDBTimestamp()
	subs := []model.UserSubscription{
		{Id: 601, UserId: 3, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 321, StartTime: now - 3600, EndTime: now + 86400, Status: "active", LastResetTime: now - 1800, NextResetTime: now + 1800},
		{Id: 602, UserId: 4, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 654, StartTime: now - 3600, EndTime: now + 86400, Status: "active", LastResetTime: now - 1800, NextResetTime: now + 1800},
	}
	require.NoError(t, db.Create(&subs).Error)
	return db, plan
}

func subscriptionResetRouteEngine(actor model.User, verified bool) (*gin.Engine, []*http.Cookie) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("subscription-reset-route-test"))))
	engine.GET("/test-login", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("username", actor.Username)
		session.Set("role", actor.Role)
		session.Set("id", actor.Id)
		session.Set("status", actor.Status)
		session.Set("group", actor.Group)
		if verified {
			session.Set(middleware.SecureVerificationSessionKey, time.Now().Unix())
		}
		if err := session.Save(); err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusNoContent)
	})
	SetApiRouter(engine)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/test-login", nil))
	return engine, recorder.Result().Cookies()
}

func subscriptionResetRouteRequest(t *testing.T, engine *gin.Engine, cookies []*http.Cookie, actorID int, target string, body string) (*httptest.ResponseRecorder, subscriptionResetRouteResponse) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, target, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("New-Api-User", strconv.Itoa(actorID))
	for _, value := range cookies {
		request.AddCookie(value)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	var response subscriptionResetRouteResponse
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		response.Message = recorder.Body.String()
	}
	return recorder, response
}

func subscriptionResetAmountUsed(t *testing.T, db *gorm.DB, id int) int64 {
	t.Helper()
	var sub model.UserSubscription
	require.NoError(t, db.First(&sub, id).Error)
	return sub.AmountUsed
}

func TestSubscriptionPlanResetRouteRequiresStepUpBeforeMutation(t *testing.T) {
	db, plan := setupSubscriptionResetRouteTest(t)
	root, err := model.GetUserById(1, false)
	require.NoError(t, err)
	engine, cookies := subscriptionResetRouteEngine(*root, false)

	recorder, response := subscriptionResetRouteRequest(t, engine, cookies, root.Id, fmt.Sprintf("/api/subscription/admin/plans/%d/subscriptions/reset", plan.Id), `{}`)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.False(t, response.Success)
	assert.Equal(t, "VERIFICATION_REQUIRED", response.Code)
	assert.EqualValues(t, 321, subscriptionResetAmountUsed(t, db, 601))
}

func TestSubscriptionPlanResetRouteIsRootOnly(t *testing.T) {
	db, plan := setupSubscriptionResetRouteTest(t)
	admin, err := model.GetUserById(2, false)
	require.NoError(t, err)
	engine, cookies := subscriptionResetRouteEngine(*admin, true)

	_, response := subscriptionResetRouteRequest(t, engine, cookies, admin.Id, fmt.Sprintf("/api/subscription/admin/plans/%d/subscriptions/reset", plan.Id), `{}`)

	assert.False(t, response.Success)
	assert.EqualValues(t, 321, subscriptionResetAmountUsed(t, db, 601))
}

func TestSubscriptionUserResetRouteEnforcesRoleHierarchy(t *testing.T) {
	db, plan := setupSubscriptionResetRouteTest(t)
	admin, err := model.GetUserById(2, false)
	require.NoError(t, err)
	engine, cookies := subscriptionResetRouteEngine(*admin, true)

	_, response := subscriptionResetRouteRequest(t, engine, cookies, admin.Id, "/api/subscription/admin/users/3/subscriptions/reset", fmt.Sprintf(`{"plan_id":%d}`, plan.Id))

	assert.False(t, response.Success)
	assert.EqualValues(t, 321, subscriptionResetAmountUsed(t, db, 601))
}

func TestSubscriptionUserResetRouteDefaultsAdvanceAndRecordsCanonicalOperatorAudit(t *testing.T) {
	db, plan := setupSubscriptionResetRouteTest(t)
	root, err := model.GetUserById(1, false)
	require.NoError(t, err)
	engine, cookies := subscriptionResetRouteEngine(*root, true)

	_, response := subscriptionResetRouteRequest(t, engine, cookies, root.Id, "/api/subscription/admin/users/3/subscriptions/reset", fmt.Sprintf(`{"plan_id":%d}`, plan.Id))

	require.True(t, response.Success, response.Message)
	assert.True(t, response.Data.AdvanceResetTime)
	assert.Equal(t, 1, response.Data.ResetCount)
	assert.Equal(t, []int{3}, response.Data.AffectedUserIds)
	assert.Zero(t, subscriptionResetAmountUsed(t, db, 601))

	var operatorLogs []model.Log
	require.NoError(t, db.Where("user_id = ? AND type = ?", root.Id, model.LogTypeManage).Find(&operatorLogs).Error)
	require.Len(t, operatorLogs, 1, "canonical audit must suppress fallback audit")
	var other subscriptionResetAuditOther
	require.NoError(t, common.UnmarshalJsonStr(operatorLogs[0].Other, &other))
	assert.Equal(t, "subscription.user_plan_reset", other.Op.Action)
	assert.EqualValues(t, 3, other.Op.Params["target_user_id"])
	assert.EqualValues(t, plan.Id, other.Op.Params["plan_id"])
	assert.Equal(t, root.Id, other.AdminInfo.AdminID)
}

func TestSubscriptionPlanResetRoutePreservesTimesWhenExplicitFalseAndAuditsOperator(t *testing.T) {
	db, plan := setupSubscriptionResetRouteTest(t)
	root, err := model.GetUserById(1, false)
	require.NoError(t, err)
	engine, cookies := subscriptionResetRouteEngine(*root, true)
	var before model.UserSubscription
	require.NoError(t, db.First(&before, 601).Error)

	_, response := subscriptionResetRouteRequest(t, engine, cookies, root.Id, fmt.Sprintf("/api/subscription/admin/plans/%d/subscriptions/reset", plan.Id), `{"advance_reset_time":false}`)

	require.True(t, response.Success, response.Message)
	assert.False(t, response.Data.AdvanceResetTime)
	assert.Equal(t, 2, response.Data.ResetCount)
	assert.Equal(t, 2, response.Data.UserCount)
	assert.Equal(t, []int{3, 4}, response.Data.AffectedUserIds)
	var after model.UserSubscription
	require.NoError(t, db.First(&after, 601).Error)
	assert.Equal(t, before.LastResetTime, after.LastResetTime)
	assert.Equal(t, before.NextResetTime, after.NextResetTime)

	var operatorLogs []model.Log
	require.NoError(t, db.Where("user_id = ? AND type = ?", root.Id, model.LogTypeManage).Find(&operatorLogs).Error)
	require.Len(t, operatorLogs, 1, "canonical audit must suppress fallback audit")
	var other subscriptionResetAuditOther
	require.NoError(t, common.UnmarshalJsonStr(operatorLogs[0].Other, &other))
	assert.Equal(t, "subscription.plan_reset", other.Op.Action)
	assert.EqualValues(t, plan.Id, other.Op.Params["plan_id"])
	assert.Equal(t, root.Id, other.AdminInfo.AdminID)
}
