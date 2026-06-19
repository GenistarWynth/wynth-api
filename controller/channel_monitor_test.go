package controller

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupControllerChannelMonitorTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedisEnabled := common.RedisEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedisEnabled
	})

	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelMonitorLog{}, &model.User{}, &model.Log{}))
	require.NoError(t, db.Create(&model.User{
		Id:       1,
		Username: "root",
		Password: "password",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}).Error)
	return db
}

func monitorSettingsJSON(t *testing.T, settings dto.ChannelOtherSettings) string {
	t.Helper()

	data, err := common.Marshal(settings)
	require.NoError(t, err)
	return string(data)
}

func monitorChannel(t *testing.T, id int, status int, settings dto.ChannelOtherSettings) *model.Channel {
	t.Helper()

	return &model.Channel{
		Id:            id,
		Type:          constant.ChannelTypeOpenAI,
		Name:          "channel",
		Status:        status,
		OtherSettings: monitorSettingsJSON(t, settings),
	}
}

func TestGetChannelIncludesMonitorInfo(t *testing.T) {
	db := setupControllerChannelMonitorTestDB(t)
	gin.SetMode(gin.TestMode)

	channel := &model.Channel{
		Id:     31,
		Type:   constant.ChannelTypeOpenAI,
		Name:   "monitored-channel",
		Key:    "secret-key",
		Status: common.ChannelStatusEnabled,
		Models: "gpt-4o-mini",
		Group:  "default",
		OtherSettings: monitorSettingsJSON(t, dto.ChannelOtherSettings{
			ChannelMonitorEnabled:         true,
			ChannelMonitorIntervalMinutes: 3,
		}),
	}
	require.NoError(t, db.Create(channel).Error)

	checkedAt := common.GetTimestamp() - 60
	require.NoError(t, model.RecordChannelMonitorLog(model.ChannelMonitorLog{
		ChannelID: channel.Id,
		Status:    model.ChannelMonitorStatusSuccess,
		LatencyMS: 120,
		Message:   "ok",
		CheckedAt: checkedAt - 60,
	}))
	require.NoError(t, model.RecordChannelMonitorLog(model.ChannelMonitorLog{
		ChannelID: channel.Id,
		Status:    model.ChannelMonitorStatusFailed,
		LatencyMS: 450,
		Message:   "upstream rejected test request",
		CheckedAt: checkedAt,
	}))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "31"}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/channel/31", nil)

	GetChannel(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool          `json:"success"`
		Data    model.Channel `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	assert.Empty(t, response.Data.Key)
	require.NotNil(t, response.Data.MonitorInfo)
	assert.True(t, response.Data.MonitorInfo.Enabled)
	assert.Equal(t, 3, response.Data.MonitorInfo.IntervalMinutes)
	assert.Equal(t, model.ChannelMonitorStatusFailed, response.Data.MonitorInfo.LatestStatus)
	assert.Equal(t, checkedAt, response.Data.MonitorInfo.LatestCheckedAt)
	assert.Equal(t, int64(450), response.Data.MonitorInfo.LatestLatencyMS)
	assert.Equal(t, "upstream rejected test request", response.Data.MonitorInfo.LatestMessage)
	assert.Equal(t, int64(2), response.Data.MonitorInfo.SevenDayChecks)
	assert.Equal(t, int64(1), response.Data.MonitorInfo.SevenDaySuccesses)
	require.NotNil(t, response.Data.MonitorInfo.SevenDayAvailability)
	assert.InDelta(t, 0.5, *response.Data.MonitorInfo.SevenDayAvailability, 0.0001)
	assert.Equal(t, int64(285), response.Data.MonitorInfo.AverageLatencyMS)
}

func TestGetAllChannelsIncludesMonitorInfo(t *testing.T) {
	db := setupControllerChannelMonitorTestDB(t)
	gin.SetMode(gin.TestMode)

	channel := &model.Channel{
		Id:     41,
		Type:   constant.ChannelTypeOpenAI,
		Name:   "listed-monitored-channel",
		Key:    "secret-key",
		Status: common.ChannelStatusEnabled,
		Models: "gpt-4o-mini",
		Group:  "default",
		OtherSettings: monitorSettingsJSON(t, dto.ChannelOtherSettings{
			ChannelMonitorEnabled:         true,
			ChannelMonitorIntervalMinutes: 5,
		}),
	}
	require.NoError(t, db.Create(channel).Error)

	checkedAt := common.GetTimestamp() - 30
	require.NoError(t, model.RecordChannelMonitorLog(model.ChannelMonitorLog{
		ChannelID: channel.Id,
		Status:    model.ChannelMonitorStatusSuccess,
		LatencyMS: 123,
		Message:   "ok",
		CheckedAt: checkedAt,
	}))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/channel/?p=1&page_size=10&sort_by=id&sort_order=asc", nil)

	GetAllChannels(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Items []model.Channel `json:"items"`
			Total int64           `json:"total"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Len(t, response.Data.Items, 1)
	assert.Equal(t, int64(1), response.Data.Total)
	assert.Empty(t, response.Data.Items[0].Key)
	require.NotNil(t, response.Data.Items[0].MonitorInfo)
	assert.True(t, response.Data.Items[0].MonitorInfo.Enabled)
	assert.Equal(t, 5, response.Data.Items[0].MonitorInfo.IntervalMinutes)
	assert.Equal(t, model.ChannelMonitorStatusSuccess, response.Data.Items[0].MonitorInfo.LatestStatus)
	assert.Equal(t, checkedAt, response.Data.Items[0].MonitorInfo.LatestCheckedAt)
	assert.Equal(t, int64(123), response.Data.Items[0].MonitorInfo.LatestLatencyMS)
	assert.Equal(t, int64(1), response.Data.Items[0].MonitorInfo.SevenDayChecks)
	assert.Equal(t, int64(1), response.Data.Items[0].MonitorInfo.SevenDaySuccesses)
}

func TestFilterDueChannelMonitorCandidates(t *testing.T) {
	now := int64(10_000)
	channels := []*model.Channel{
		nil,
		monitorChannel(t, 1, common.ChannelStatusEnabled, dto.ChannelOtherSettings{
			ChannelMonitorEnabled:         true,
			ChannelMonitorIntervalMinutes: 5,
		}),
		monitorChannel(t, 2, common.ChannelStatusEnabled, dto.ChannelOtherSettings{
			ChannelMonitorEnabled:         true,
			ChannelMonitorIntervalMinutes: 5,
		}),
		monitorChannel(t, 3, common.ChannelStatusManuallyDisabled, dto.ChannelOtherSettings{
			ChannelMonitorEnabled: true,
		}),
		monitorChannel(t, 4, common.ChannelStatusEnabled, dto.ChannelOtherSettings{}),
		monitorChannel(t, 5, common.ChannelStatusAutoDisabled, dto.ChannelOtherSettings{
			ChannelMonitorEnabled:         true,
			ChannelMonitorIntervalMinutes: 5,
		}),
	}
	latest := map[int]model.ChannelMonitorLog{
		2: {ChannelID: 2, CheckedAt: now - 299},
		3: {ChannelID: 3, CheckedAt: now - 1000},
		5: {ChannelID: 5, CheckedAt: now - 300},
	}

	candidates := filterDueChannelMonitorCandidates(channels, latest, now)

	require.Len(t, candidates, 2)
	assert.Equal(t, 1, candidates[0].Id)
	assert.Equal(t, 5, candidates[1].Id)
}

func TestFilterDueChannelMonitorCandidatesKeepsInvalidSettingsReadOnly(t *testing.T) {
	db := setupControllerChannelMonitorTestDB(t)
	channel := &model.Channel{
		Id:            6,
		Type:          constant.ChannelTypeOpenAI,
		Name:          "invalid-settings",
		Status:        common.ChannelStatusEnabled,
		Key:           "test-key",
		OtherSettings: "{bad-json",
	}
	require.NoError(t, db.Create(channel).Error)

	candidates := filterDueChannelMonitorCandidates([]*model.Channel{channel}, nil, 10_000)

	assert.Empty(t, candidates)
	assert.Equal(t, "{bad-json", channel.OtherSettings)
	var reloaded model.Channel
	require.NoError(t, db.First(&reloaded, channel.Id).Error)
	assert.Equal(t, "{bad-json", reloaded.OtherSettings)
}

func TestChannelMonitorStatusFromResult(t *testing.T) {
	upstreamErr := types.NewError(errors.New("upstream failed"), types.ErrorCodeChannelInvalidKey)

	assert.Equal(t, model.ChannelMonitorStatusError, channelMonitorStatusFromResult(testResult{
		localErr:    errors.New("local setup failed"),
		newAPIError: types.NewError(errors.New("gen relay info failed"), types.ErrorCodeGenRelayInfoFailed),
	}))
	assert.Equal(t, model.ChannelMonitorStatusFailed, channelMonitorStatusFromResult(testResult{
		localErr:          errors.New("wrapped upstream failed"),
		newAPIError:       upstreamErr,
		upstreamAttempted: true,
	}))
	assert.Equal(t, model.ChannelMonitorStatusError, channelMonitorStatusFromResult(testResult{
		localErr: errors.New("probe setup failed"),
	}))
	assert.Equal(t, model.ChannelMonitorStatusSuccess, channelMonitorStatusFromResult(testResult{}))
}

func TestRecordChannelTestConsumeLogSkipsMonitorProbes(t *testing.T) {
	db := setupControllerChannelMonitorTestDB(t)
	gin.SetMode(gin.TestMode)
	oldLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	t.Cleanup(func() {
		common.LogConsumeEnabled = oldLogConsumeEnabled
	})

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("username", "root")
	params := model.RecordConsumeLogParams{
		ChannelId:        1,
		PromptTokens:     2,
		CompletionTokens: 3,
		ModelName:        "gpt-4o-mini",
		TokenName:        "模型测试",
		Quota:            5,
		Content:          "模型测试",
		Group:            "default",
	}

	recordChannelTestConsumeLog(channelTestOptions{recordConsumeLog: false}, ctx, 1, params)
	var count int64
	require.NoError(t, db.Model(&model.Log{}).Count(&count).Error)
	assert.Zero(t, count)

	recordChannelTestConsumeLog(channelTestOptions{recordConsumeLog: true}, ctx, 1, params)
	require.NoError(t, db.Model(&model.Log{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestApplyChannelMonitorStatusMutationHonorsEnableGate(t *testing.T) {
	db := setupControllerChannelMonitorTestDB(t)
	channel := &model.Channel{
		Id:     10,
		Type:   constant.ChannelTypeOpenAI,
		Name:   "auto-disabled",
		Key:    "test-key",
		Status: common.ChannelStatusAutoDisabled,
	}
	require.NoError(t, db.Create(channel).Error)

	oldAutomaticEnable := common.AutomaticEnableChannelEnabled
	t.Cleanup(func() {
		common.AutomaticEnableChannelEnabled = oldAutomaticEnable
	})

	common.AutomaticEnableChannelEnabled = false
	applyChannelMonitorStatusMutation(channel, testResult{}, 100)
	var reloaded model.Channel
	require.NoError(t, db.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusAutoDisabled, reloaded.Status)

	common.AutomaticEnableChannelEnabled = true
	applyChannelMonitorStatusMutation(channel, testResult{}, 100)
	require.NoError(t, db.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusEnabled, reloaded.Status)
}

func TestApplyChannelMonitorStatusMutationDoesNotRecoverAfterLocalError(t *testing.T) {
	db := setupControllerChannelMonitorTestDB(t)
	channel := &model.Channel{
		Id:     12,
		Type:   constant.ChannelTypeOpenAI,
		Name:   "auto-disabled-local-error",
		Key:    "test-key",
		Status: common.ChannelStatusAutoDisabled,
	}
	require.NoError(t, db.Create(channel).Error)

	oldAutomaticEnable := common.AutomaticEnableChannelEnabled
	common.AutomaticEnableChannelEnabled = true
	t.Cleanup(func() {
		common.AutomaticEnableChannelEnabled = oldAutomaticEnable
	})

	applyChannelMonitorStatusMutation(channel, testResult{localErr: errors.New("local setup failed")}, 100)

	var reloaded model.Channel
	require.NoError(t, db.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusAutoDisabled, reloaded.Status)
}

func TestApplyChannelMonitorStatusMutationNilContextDoesNotPanic(t *testing.T) {
	setupControllerChannelMonitorTestDB(t)
	channel := &model.Channel{
		Id:      11,
		Type:    constant.ChannelTypeOpenAI,
		Name:    "enabled",
		Key:     "test-key",
		Status:  common.ChannelStatusEnabled,
		AutoBan: common.GetPointer(1),
	}
	require.NoError(t, model.DB.Create(channel).Error)

	oldAutomaticDisable := common.AutomaticDisableChannelEnabled
	oldErrorWriter := gin.DefaultErrorWriter
	common.AutomaticDisableChannelEnabled = true
	var logBuffer bytes.Buffer
	common.LogWriterMu.Lock()
	gin.DefaultErrorWriter = &logBuffer
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = oldAutomaticDisable
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = oldErrorWriter
		common.LogWriterMu.Unlock()
	})

	assert.NotPanics(t, func() {
		applyChannelMonitorStatusMutation(channel, testResult{
			localErr:    errors.New("upstream failed"),
			newAPIError: types.NewError(errors.New("upstream failed"), types.ErrorCodeChannelInvalidKey),
		}, 100)
	})

	logOutput, err := io.ReadAll(&logBuffer)
	require.NoError(t, err)
	assert.Contains(t, string(logOutput), "channel monitor skipped auto-disable for channel #11: missing monitor test context")
}
