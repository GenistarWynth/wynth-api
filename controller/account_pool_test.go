package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
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

type accountPoolAPIResponse[T any] struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type accountPoolAPIResult[T any] struct {
	Response accountPoolAPIResponse[T]
	Raw      []byte
	Code     int
}

func setupAccountPoolAPITestDB(t *testing.T) {
	t.Helper()

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedisEnabled := common.RedisEnabled
	oldTranslateMessage := common.TranslateMessage
	oldCryptoSecret := common.CryptoSecret
	oldCryptoSecretStable := common.CryptoSecretStable

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	common.CryptoSecret = "account-pool-api-test-secret"
	common.CryptoSecretStable = true
	common.TranslateMessage = func(c *gin.Context, key string, args ...map[string]any) string {
		return key
	}

	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedisEnabled
		common.TranslateMessage = oldTranslateMessage
		common.CryptoSecret = oldCryptoSecret
		common.CryptoSecretStable = oldCryptoSecretStable
	})

	require.NoError(t, db.AutoMigrate(
		&model.AccountPool{},
		&model.AccountPoolAccount{},
		&model.AccountPoolProxy{},
		&model.AccountPoolChannelBinding{},
		&model.Channel{},
		&model.User{},
		&model.Log{},
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

func accountPoolAPIRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("account-pool-api-test"))))
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
	group := router.Group("/api/account_pools")
	group.Use(middleware.AdminAuth())
	{
		group.GET("", ListAccountPools)
		group.POST("", CreateAccountPool)
		group.GET("/proxies", ListAccountPoolProxies)
		group.POST("/proxies", CreateAccountPoolProxy)
		group.GET("/:id", GetAccountPool)
		group.PUT("/:id", UpdateAccountPool)
		group.DELETE("/:id", DeleteAccountPool)
		group.GET("/:id/accounts", ListAccountPoolAccounts)
		group.POST("/:id/accounts", CreateAccountPoolAccount)
		group.GET("/:id/bindings", ListAccountPoolBindings)
		group.POST("/:id/bindings", CreateAccountPoolBinding)
	}
	return router
}

func accountPoolAPIRequest[T any](t *testing.T, router *gin.Engine, method string, target string, body any) accountPoolAPIResult[T] {
	t.Helper()

	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		payload, err := common.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(payload)
	}

	loginRecorder := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(http.MethodGet, "/login", nil)
	router.ServeHTTP(loginRecorder, loginRequest)
	require.Equal(t, http.StatusNoContent, loginRecorder.Code)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("New-Api-User", "1")
	for _, cookie := range loginRecorder.Result().Cookies() {
		request.AddCookie(cookie)
	}
	router.ServeHTTP(recorder, request)

	raw := recorder.Body.Bytes()
	var response accountPoolAPIResponse[T]
	require.NoError(t, common.Unmarshal(raw, &response))
	return accountPoolAPIResult[T]{
		Response: response,
		Raw:      raw,
		Code:     recorder.Code,
	}
}

func TestAccountPoolAPICreateListAndRedaction(t *testing.T) {
	setupAccountPoolAPITestDB(t)
	router := accountPoolAPIRouter()

	poolResult := accountPoolAPIRequest[dto.AccountPoolResponse](t, router, http.MethodPost, "/api/account_pools", dto.AccountPoolCreateRequest{
		Name:     "pool-a",
		Platform: model.AccountPoolPlatformOpenAI,
	})
	require.True(t, poolResult.Response.Success, poolResult.Response.Message)

	accountResult := accountPoolAPIRequest[dto.AccountPoolAccountResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(poolResult.Response.Data.Id)+"/accounts", dto.AccountPoolAccountCreateRequest{
		Name: "primary-key",
		Credential: dto.AccountPoolCredentialConfigRequest{
			Type:   "api_key",
			APIKey: "sk-account-secret",
		},
	})
	require.True(t, accountResult.Response.Success, accountResult.Response.Message)
	assert.True(t, accountResult.Response.Data.HasCredential)

	listResult := accountPoolAPIRequest[[]dto.AccountPoolAccountResponse](t, router, http.MethodGet, "/api/account_pools/"+strconv.Itoa(poolResult.Response.Data.Id)+"/accounts", nil)

	require.True(t, listResult.Response.Success, listResult.Response.Message)
	require.Len(t, listResult.Response.Data, 1)
	assert.True(t, listResult.Response.Data[0].HasCredential)
	assert.NotEmpty(t, listResult.Response.Data[0].CredentialPreview)
	raw := string(listResult.Raw)
	assert.NotContains(t, raw, "sk-account-secret")
	assert.Contains(t, raw, "credential_preview")
}

func TestAccountPoolAPIBindingRejectsEnabledChannel(t *testing.T) {
	setupAccountPoolAPITestDB(t)
	router := accountPoolAPIRouter()
	pool := createAccountPoolAPITestPool(t, router)
	channel := createAccountPoolAPITestChannel(t, common.ChannelStatusEnabled)

	result := accountPoolAPIRequest[dto.AccountPoolBindingResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/bindings", dto.AccountPoolBindingCreateRequest{
		ChannelID: channel.Id,
	})

	require.False(t, result.Response.Success)
	assert.Contains(t, result.Response.Message, "disabled channel")
}

func TestAccountPoolAPIBindingCreatesDraftForDisabledChannel(t *testing.T) {
	setupAccountPoolAPITestDB(t)
	router := accountPoolAPIRouter()
	pool := createAccountPoolAPITestPool(t, router)
	channel := createAccountPoolAPITestChannel(t, common.ChannelStatusManuallyDisabled)

	result := accountPoolAPIRequest[dto.AccountPoolBindingResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/bindings", dto.AccountPoolBindingCreateRequest{
		ChannelID: channel.Id,
	})

	require.True(t, result.Response.Success, result.Response.Message)
	assert.Equal(t, model.AccountPoolBindingStatusDraft, result.Response.Data.Status)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, result.Response.Data.ChannelStatus)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, reloaded.Status)
}

func TestAccountPoolAPIProxyRedaction(t *testing.T) {
	setupAccountPoolAPITestDB(t)
	router := accountPoolAPIRouter()

	createResult := accountPoolAPIRequest[dto.AccountPoolProxyResponse](t, router, http.MethodPost, "/api/account_pools/proxies", dto.AccountPoolProxyCreateRequest{
		Name:     "proxy-a",
		Protocol: "http",
		Host:     "127.0.0.1",
		Port:     8080,
		Username: "proxy-user",
		Password: "proxy-password-secret",
	})

	require.True(t, createResult.Response.Success, createResult.Response.Message)
	assert.True(t, createResult.Response.Data.HasPassword)
	assert.NotContains(t, string(createResult.Raw), "proxy-password-secret")
	var stored model.AccountPoolProxy
	require.NoError(t, model.DB.First(&stored, createResult.Response.Data.Id).Error)
	assert.NotContains(t, stored.Password, "proxy-password-secret")

	listResult := accountPoolAPIRequest[[]dto.AccountPoolProxyResponse](t, router, http.MethodGet, "/api/account_pools/proxies", nil)

	require.True(t, listResult.Response.Success, listResult.Response.Message)
	require.Len(t, listResult.Response.Data, 1)
	assert.True(t, listResult.Response.Data[0].HasPassword)
	assert.NotContains(t, string(listResult.Raw), "proxy-password-secret")
}

func createAccountPoolAPITestPool(t *testing.T, router *gin.Engine) dto.AccountPoolResponse {
	t.Helper()

	result := accountPoolAPIRequest[dto.AccountPoolResponse](t, router, http.MethodPost, "/api/account_pools", dto.AccountPoolCreateRequest{
		Name:     "pool-a",
		Platform: model.AccountPoolPlatformOpenAI,
	})
	require.True(t, result.Response.Success, result.Response.Message)
	return result.Response.Data
}

func createAccountPoolAPITestChannel(t *testing.T, status int) model.Channel {
	t.Helper()

	channel := model.Channel{
		Type:   1,
		Key:    "sk-channel",
		Name:   "channel-a",
		Status: status,
	}
	require.NoError(t, model.DB.Create(&channel).Error)
	return channel
}
