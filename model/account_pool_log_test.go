package model

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAccountPoolLogTestDB(t *testing.T) {
	t.Helper()
	oldDB := DB
	oldLogDB := LOG_DB
	oldLogConsumeEnabled := common.LogConsumeEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	DB = db
	LOG_DB = db
	common.LogConsumeEnabled = true
	require.NoError(t, db.AutoMigrate(&Log{}))
	t.Cleanup(func() {
		DB = oldDB
		LOG_DB = oldLogDB
		common.LogConsumeEnabled = oldLogConsumeEnabled
	})
}

func TestRecordConsumeLogAttachesSelectedAccountPoolIDs(t *testing.T) {
	setupAccountPoolLogTestDB(t)
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("username", "alice")
	common.SetContextKey(ctx, constant.ContextKeyAccountPoolID, 17)
	common.SetContextKey(ctx, constant.ContextKeyAccountPoolAccountID, 29)

	RecordConsumeLog(ctx, 7, RecordConsumeLogParams{
		PromptTokens:     120,
		CompletionTokens: 30,
		ModelName:        "grok-4",
	})

	var log Log
	require.NoError(t, LOG_DB.First(&log).Error)
	assert.Equal(t, 17, log.AccountPoolId)
	assert.Equal(t, 29, log.AccountPoolAccountId)
}

func TestRecordErrorLogSanitizesEveryStoredTextField(t *testing.T) {
	setupAccountPoolLogTestDB(t)
	require.NoError(t, DB.AutoMigrate(&User{}))
	require.NoError(t, DB.Create(&User{Id: 7, Username: "alice", Status: common.UserStatusEnabled}).Error)
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("username", "alice")
	ctx.Set(common.UpstreamRequestIdKey, "safe-request Authorization: Bearer upstream-request-secret")

	RecordErrorLog(
		ctx,
		7,
		11,
		"gpt-test",
		"token",
		`provider capacity exhausted {"Set-Cookie":"session=content-secret"}`,
		13,
		1,
		false,
		"default",
		map[string]any{
			"safe_status": "rate_limited",
			"diagnostic":  "X-Goog-Api-Key: other-field-secret",
		},
	)

	var log Log
	require.NoError(t, LOG_DB.First(&log).Error)
	stored := strings.Join([]string{log.Content, log.UpstreamRequestId, log.Other}, "\n")
	assert.NotContains(t, stored, "content-secret")
	assert.NotContains(t, stored, "upstream-request-secret")
	assert.NotContains(t, stored, "other-field-secret")
	assert.Contains(t, stored, "provider capacity exhausted")
	assert.Contains(t, stored, "rate_limited")
}

func TestGetAccountPoolUsage24hAggregatesOnlySelectedAccountAndWindow(t *testing.T) {
	setupAccountPoolLogTestDB(t)
	now := int64(2_000_000_000)
	require.NoError(t, LOG_DB.Create(&[]Log{
		{CreatedAt: now - 60, Type: LogTypeConsume, AccountPoolId: 17, AccountPoolAccountId: 29, PromptTokens: 100, CompletionTokens: 20},
		{CreatedAt: now - 120, Type: LogTypeConsume, AccountPoolId: 17, AccountPoolAccountId: 29, PromptTokens: 50, CompletionTokens: 10},
		{CreatedAt: now - 25*60*60, Type: LogTypeConsume, AccountPoolId: 17, AccountPoolAccountId: 29, PromptTokens: 999, CompletionTokens: 999},
		{CreatedAt: now - 60, Type: LogTypeConsume, AccountPoolId: 17, AccountPoolAccountId: 30, PromptTokens: 888, CompletionTokens: 888},
		{CreatedAt: now - 60, Type: LogTypeError, AccountPoolId: 17, AccountPoolAccountId: 29, PromptTokens: 777, CompletionTokens: 777},
	}).Error)

	usage, err := GetAccountPoolUsage24h(17, 29, now)
	require.NoError(t, err)
	assert.True(t, usage.HasLogs)
	assert.Equal(t, int64(2), usage.Requests)
	assert.Equal(t, int64(150), usage.PromptTokens)
	assert.Equal(t, int64(30), usage.CompletionTokens)

	empty, err := GetAccountPoolUsage24h(17, 31, now)
	require.NoError(t, err)
	assert.False(t, empty.HasLogs)
}
