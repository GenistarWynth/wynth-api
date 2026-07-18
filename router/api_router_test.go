package router

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var criticalRateLimitTestClientCounter atomic.Uint32

func TestUpdateSelfUsesCriticalRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	oldDB := model.DB
	oldCriticalRateLimitEnable := common.CriticalRateLimitEnable
	oldCriticalRateLimitNum := common.CriticalRateLimitNum
	oldRedisEnabled := common.RedisEnabled
	oldGlobalAPIRateLimitEnable := common.GlobalApiRateLimitEnable
	t.Cleanup(func() {
		model.DB = oldDB
		common.CriticalRateLimitEnable = oldCriticalRateLimitEnable
		common.CriticalRateLimitNum = oldCriticalRateLimitNum
		common.RedisEnabled = oldRedisEnabled
		common.GlobalApiRateLimitEnable = oldGlobalAPIRateLimitEnable
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.User{}))
	model.DB = db

	accessToken := "0123456789abcdef0123456789abcdef"
	user := model.User{
		Username:    "rate-limit-user",
		Password:    "password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AccessToken: &accessToken,
	}
	require.NoError(t, db.Create(&user).Error)

	common.CriticalRateLimitEnable = true
	common.CriticalRateLimitNum = 1
	common.RedisEnabled = false
	common.GlobalApiRateLimitEnable = false
	clientRemoteAddr := fmt.Sprintf("192.0.2.%d:1234", criticalRateLimitTestClientCounter.Add(1))

	engine := gin.New()
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("api-router-test"))))
	SetApiRouter(engine)

	warmupRequest := httptest.NewRequest(http.MethodPut, "/api/user/self", bytes.NewBufferString(`{"username":"rate-user-updated"}`))
	warmupRequest.Header.Set("Content-Type", "application/json")
	warmupRequest.Header.Set("Authorization", accessToken)
	warmupRequest.Header.Set("New-Api-User", strconv.Itoa(user.Id))
	warmupRequest.RemoteAddr = clientRemoteAddr
	warmupResponse := httptest.NewRecorder()
	engine.ServeHTTP(warmupResponse, warmupRequest)
	require.Equal(t, http.StatusOK, warmupResponse.Code)

	request := httptest.NewRequest(http.MethodPut, "/api/user/self", bytes.NewBufferString(`{"username":"rate-user-updated"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", accessToken)
	request.Header.Set("New-Api-User", strconv.Itoa(user.Id))
	request.RemoteAddr = clientRemoteAddr
	response := httptest.NewRecorder()

	engine.ServeHTTP(response, request)

	assert.Equal(t, http.StatusTooManyRequests, response.Code)
}
