package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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

var systemInstanceRouteDBCounter atomic.Uint32

type systemInstanceRouteResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Code    string `json:"code"`
	Data    struct {
		DeletedCount int64 `json:"deleted_count"`
	} `json:"data"`
}

func setupSystemInstanceRouteTest(t *testing.T) (*gorm.DB, int64) {
	t.Helper()
	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedisEnabled := common.RedisEnabled
	oldCriticalRateLimitEnable := common.CriticalRateLimitEnable
	oldGlobalAPIRateLimitEnable := common.GlobalApiRateLimitEnable
	oldTranslateMessage := common.TranslateMessage
	oldMainDBType := common.MainDatabaseType()

	dsn := fmt.Sprintf("file:system-instance-routes-%d?mode=memory&cache=shared", systemInstanceRouteDBCounter.Add(1))
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

	require.NoError(t, db.AutoMigrate(&model.User{}, &model.SystemInstance{}, &model.Log{}))
	users := []model.User{
		{Id: 1, Username: "root", Password: "password", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "instance-route-root"},
		{Id: 2, Username: "admin", Password: "password", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "instance-route-admin"},
	}
	require.NoError(t, db.Create(&users).Error)
	now := common.GetTimestamp()
	instances := []model.SystemInstance{
		{NodeName: "stale-a", LastSeenAt: now - model.SystemInstanceStaleAfterSeconds - 1},
		{NodeName: "stale-b", LastSeenAt: now - model.SystemInstanceStaleAfterSeconds - 20},
		{NodeName: "online", LastSeenAt: now},
	}
	require.NoError(t, db.Create(&instances).Error)
	return db, now
}

func systemInstanceRouteEngine(actor model.User, verified bool) (*gin.Engine, []*http.Cookie) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("system-instance-route-test"))))
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

func systemInstanceRouteRequest(t *testing.T, engine *gin.Engine, cookies []*http.Cookie, actorID int, target string) (*httptest.ResponseRecorder, systemInstanceRouteResponse) {
	t.Helper()
	request := httptest.NewRequest(http.MethodDelete, target, nil)
	request.Header.Set("New-Api-User", strconv.Itoa(actorID))
	for _, value := range cookies {
		request.AddCookie(value)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	var response systemInstanceRouteResponse
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		response.Message = recorder.Body.String()
	}
	return recorder, response
}

func countSystemInstances(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&model.SystemInstance{}).Count(&count).Error)
	return count
}

func TestSystemInstanceDeleteRouteRejectsAdmin(t *testing.T) {
	db, _ := setupSystemInstanceRouteTest(t)
	admin, err := model.GetUserById(2, false)
	require.NoError(t, err)
	engine, cookies := systemInstanceRouteEngine(*admin, true)

	_, response := systemInstanceRouteRequest(t, engine, cookies, admin.Id, "/api/system-info/stale-instances")

	assert.False(t, response.Success)
	assert.EqualValues(t, 3, countSystemInstances(t, db))
}

func TestSystemInstanceDeleteRouteRequiresStepUpBeforeMutation(t *testing.T) {
	db, _ := setupSystemInstanceRouteTest(t)
	root, err := model.GetUserById(1, false)
	require.NoError(t, err)
	engine, cookies := systemInstanceRouteEngine(*root, false)

	recorder, response := systemInstanceRouteRequest(t, engine, cookies, root.Id, "/api/system-info/instances/stale-a")

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Equal(t, "VERIFICATION_REQUIRED", response.Code)
	assert.EqualValues(t, 3, countSystemInstances(t, db))
}

func TestSystemInstanceDeleteRouteDeletesOneStaleRowAndAuditsOperator(t *testing.T) {
	db, _ := setupSystemInstanceRouteTest(t)
	root, err := model.GetUserById(1, false)
	require.NoError(t, err)
	engine, cookies := systemInstanceRouteEngine(*root, true)

	_, response := systemInstanceRouteRequest(t, engine, cookies, root.Id, "/api/system-info/instances/stale-a")

	require.True(t, response.Success, response.Message)
	assert.EqualValues(t, 1, response.Data.DeletedCount)
	assert.EqualValues(t, 2, countSystemInstances(t, db))
	assertSystemInstanceAudit(t, db, root.Id, "system_instance.delete_stale", "node_name", "stale-a")
}

func TestSystemInstanceDeleteRouteSupportsEncodedNodeNames(t *testing.T) {
	db, now := setupSystemInstanceRouteTest(t)
	nodeName := "北京/master 1"
	require.NoError(t, db.Create(&model.SystemInstance{
		NodeName:   nodeName,
		LastSeenAt: now - model.SystemInstanceStaleAfterSeconds - 1,
	}).Error)
	root, err := model.GetUserById(1, false)
	require.NoError(t, err)
	engine, cookies := systemInstanceRouteEngine(*root, true)

	target := "/api/system-info/instances/" + url.PathEscape(nodeName)
	recorder, response := systemInstanceRouteRequest(t, engine, cookies, root.Id, target)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.True(t, response.Success, response.Message)
	var remaining int64
	require.NoError(t, db.Model(&model.SystemInstance{}).Where("node_name = ?", nodeName).Count(&remaining).Error)
	assert.Zero(t, remaining)
	assertSystemInstanceAudit(t, db, root.Id, "system_instance.delete_stale", "node_name", nodeName)
}

func TestSystemInstanceDeleteAllStaleRouteReturnsExactCountAndAuditsOperator(t *testing.T) {
	db, _ := setupSystemInstanceRouteTest(t)
	root, err := model.GetUserById(1, false)
	require.NoError(t, err)
	engine, cookies := systemInstanceRouteEngine(*root, true)

	_, response := systemInstanceRouteRequest(t, engine, cookies, root.Id, "/api/system-info/stale-instances")

	require.True(t, response.Success, response.Message)
	assert.EqualValues(t, 2, response.Data.DeletedCount)
	assert.EqualValues(t, 1, countSystemInstances(t, db))
	assertSystemInstanceAudit(t, db, root.Id, "system_instance.delete_stale_all", "deleted_count", float64(2))
}

func assertSystemInstanceAudit(t *testing.T, db *gorm.DB, operatorID int, action string, param string, value any) {
	t.Helper()
	var logs []model.Log
	require.NoError(t, db.Where("user_id = ? AND type = ?", operatorID, model.LogTypeManage).Find(&logs).Error)
	require.Len(t, logs, 1, "canonical audit must suppress fallback audit")
	var other subscriptionResetAuditOther
	require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
	assert.Equal(t, action, other.Op.Action)
	assert.Equal(t, value, other.Op.Params[param])
	assert.Equal(t, operatorID, other.AdminInfo.AdminID)
}
