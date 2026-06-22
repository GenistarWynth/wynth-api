package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupChannelTagControllerTestDB(t *testing.T) {
	t.Helper()

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedisEnabled := common.RedisEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedisEnabled
	})

	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.Log{}, &model.User{}))
	require.NoError(t, db.Create(&model.User{
		Id:       1,
		Username: "admin",
		Role:     common.RoleAdminUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}).Error)
}

func channelTagControllerRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/api/channel/tag", EditTagChannels)
	return router
}

func channelTagAPIRequest(t *testing.T, router *gin.Engine, body any) map[string]any {
	t.Helper()

	payload, err := common.Marshal(body)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/channel/tag", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("New-Api-User", "1")
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)

	var response map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

func createChannelTagTestChannel(t *testing.T, tag string, models string, group string) model.Channel {
	t.Helper()

	priority := int64(10)
	weight := uint(20)
	modelMapping := `{"old":"old-upstream"}`
	channel := model.Channel{
		Type:         1,
		Key:          "sk-test",
		Name:         "tag-test",
		Models:       models,
		Group:        group,
		ModelMapping: &modelMapping,
		Priority:     &priority,
		Weight:       &weight,
		Tag:          &tag,
	}
	require.NoError(t, model.DB.Create(&channel).Error)
	return channel
}

func TestEditTagChannelsOnlyUpdatesSelectedFields(t *testing.T) {
	setupChannelTagControllerTestDB(t)
	router := channelTagControllerRouter()
	channel := createChannelTagTestChannel(t, "pool", "gpt-old", "default")

	response := channelTagAPIRequest(t, router, map[string]any{
		"tag":           "pool",
		"fields":        []string{"groups"},
		"models":        "gpt-new",
		"groups":        "pro",
		"model_mapping": `{"new":"new-upstream"}`,
	})

	require.Equal(t, true, response["success"])
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	assert.Equal(t, "gpt-old", reloaded.Models)
	assert.Equal(t, "pro", reloaded.Group)
	require.NotNil(t, reloaded.ModelMapping)
	assert.JSONEq(t, `{"old":"old-upstream"}`, *reloaded.ModelMapping)
}

func TestEditTagChannelsEmptySelectedFieldsDoesNotMutateTag(t *testing.T) {
	setupChannelTagControllerTestDB(t)
	router := channelTagControllerRouter()
	channel := createChannelTagTestChannel(t, "pool", "gpt-old", "default")

	response := channelTagAPIRequest(t, router, map[string]any{
		"tag":     "pool",
		"fields":  []string{},
		"new_tag": "renamed",
		"models":  "gpt-new",
		"groups":  "pro",
	})

	require.Equal(t, true, response["success"])
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	require.NotNil(t, reloaded.Tag)
	assert.Equal(t, "pool", *reloaded.Tag)
	assert.Equal(t, "gpt-old", reloaded.Models)
	assert.Equal(t, "default", reloaded.Group)
}
