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
	"github.com/QuantumNous/new-api/service"
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
	oldSecret := common.CryptoSecret
	oldStable := common.CryptoSecretStable
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	common.CryptoSecret = "controller-channel-monitor-test-secret"
	common.CryptoSecretStable = true
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedisEnabled
		common.CryptoSecret = oldSecret
		common.CryptoSecretStable = oldStable
	})

	require.NoError(t, db.AutoMigrate(
		&model.Channel{},
		&model.Ability{},
		&model.ChannelMonitorLog{},
		&model.User{},
		&model.Log{},
		&model.AccountPool{},
		&model.AccountPoolAccount{},
		&model.AccountPoolProxy{},
		&model.AccountPoolChannelBinding{},
	))
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

func createControllerAccountPoolMonitorChannel(t *testing.T, db *gorm.DB, status int) *model.Channel {
	t.Helper()

	channel := &model.Channel{
		Type:    constant.ChannelTypeOpenAI,
		Name:    "account-pool-monitor-channel",
		Key:     "account-pool-monitor-key",
		Status:  status,
		Models:  "gpt-4o-mini",
		Group:   "default",
		AutoBan: common.GetPointer(0),
		OtherSettings: monitorSettingsJSON(t, dto.ChannelOtherSettings{
			ChannelMonitorEnabled: true,
		}),
	}
	require.NoError(t, db.Create(channel).Error)
	return channel
}

func createControllerAccountPoolMonitorPool(t *testing.T) model.AccountPool {
	t.Helper()

	pool, err := service.AccountPoolService{}.CreatePool(service.AccountPoolCreateParams{
		Name:     "monitor-pool",
		Platform: model.AccountPoolPlatformOpenAI,
	})
	require.NoError(t, err)
	return pool
}

func createControllerAccountPoolMonitorBinding(t *testing.T, poolID int, channelID int) model.AccountPoolChannelBinding {
	t.Helper()

	bindingView, err := service.AccountPoolService{}.CreateBinding(service.AccountPoolBindingCreateParams{
		PoolID:    poolID,
		ChannelID: channelID,
	})
	require.NoError(t, err)
	_, err = service.AccountPoolService{}.ActivateBinding(poolID, bindingView.Id)
	require.NoError(t, err)
	var binding model.AccountPoolChannelBinding
	require.NoError(t, model.DB.First(&binding, bindingView.Id).Error)
	return binding
}

func channelMonitorTestContext(channel *model.Channel) *gin.Context {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("id", 1)
	if channel != nil {
		ctx.Set("channel_id", channel.Id)
		ctx.Set("channel_name", channel.Name)
		ctx.Set("channel_type", channel.Type)
	}
	ctx.Set("original_model", "gpt-4o-mini")
	return ctx
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

func TestGetChannelMonitorDetail(t *testing.T) {
	db := setupControllerChannelMonitorTestDB(t)
	gin.SetMode(gin.TestMode)

	channel := &model.Channel{
		Id:     51,
		Type:   constant.ChannelTypeOpenAI,
		Name:   "detail-monitored-channel",
		Key:    "secret-key",
		Status: common.ChannelStatusEnabled,
		Models: "gpt-4o-mini",
		Group:  "default",
		OtherSettings: monitorSettingsJSON(t, dto.ChannelOtherSettings{
			ChannelMonitorEnabled:         true,
			ChannelMonitorIntervalMinutes: 4,
		}),
	}
	require.NoError(t, db.Create(channel).Error)
	checkedAtBase := common.GetTimestamp() - 100
	for i := 1; i <= 4; i++ {
		status := model.ChannelMonitorStatusSuccess
		if i == 4 {
			status = model.ChannelMonitorStatusDegraded
		}
		require.NoError(t, model.RecordChannelMonitorLog(model.ChannelMonitorLog{
			ChannelID:           channel.Id,
			Model:               "gpt-4o-mini",
			Status:              status,
			LatencyMS:           int64(100 * i),
			EndpointLatencyMS:   int64(10 * i),
			FirstTokenLatencyMS: int64(50 * i),
			PromptTokens:        i,
			CompletionTokens:    i + 10,
			Message:             "monitor point",
			CheckedAt:           checkedAtBase + int64(i),
		}))
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "51"}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/channel/51/monitor", nil)

	GetChannelMonitorDetail(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool                       `json:"success"`
		Data    model.ChannelMonitorDetail `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	assert.Equal(t, channel.Id, response.Data.ChannelID)
	assert.Equal(t, model.ChannelMonitorStatusDegraded, response.Data.Info.LatestStatus)
	assert.Equal(t, int64(400), response.Data.Info.LatestLatencyMS)
	assert.Equal(t, int64(40), response.Data.Info.LatestEndpointLatencyMS)
	assert.Equal(t, int64(200), response.Data.Info.LatestFirstTokenLatencyMS)
	assert.Equal(t, int64(4), response.Data.Info.SevenDaySuccesses)
	require.Len(t, response.Data.RecentRecords, 4)
	assert.Equal(t, checkedAtBase+1, response.Data.RecentRecords[0].CheckedAt)
	assert.Equal(t, 14, response.Data.RecentRecords[3].CompletionTokens)

	var detailEnvelope struct {
		Data struct {
			Info struct {
				LatestModel            string `json:"latest_model"`
				LatestPromptTokens     int    `json:"latest_prompt_tokens"`
				LatestCompletionTokens int    `json:"latest_completion_tokens"`
			} `json:"info"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &detailEnvelope))
	assert.Equal(t, "gpt-4o-mini", detailEnvelope.Data.Info.LatestModel)
	assert.Equal(t, 4, detailEnvelope.Data.Info.LatestPromptTokens)
	assert.Equal(t, 14, detailEnvelope.Data.Info.LatestCompletionTokens)
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

func TestResolveChannelMonitorProbeModelUsesConfiguredModelWhenInChannelModels(t *testing.T) {
	channel := monitorChannel(t, 1, common.ChannelStatusEnabled, dto.ChannelOtherSettings{
		ChannelMonitorModel: "claude-3",
	})
	channel.Models = "gpt-4o-mini,claude-3"

	assert.Equal(t, "claude-3", resolveChannelMonitorProbeModel(channel))
}

func TestResolveChannelMonitorProbeModelFallsBackWhenModelNotInChannelModels(t *testing.T) {
	channel := monitorChannel(t, 2, common.ChannelStatusEnabled, dto.ChannelOtherSettings{
		ChannelMonitorModel: "claude-3",
	})
	channel.Models = "gpt-4o-mini"

	assert.Equal(t, "", resolveChannelMonitorProbeModel(channel))
}

func TestResolveChannelMonitorProbeModelHandlesNilChannelAndUnsetModel(t *testing.T) {
	assert.Equal(t, "", resolveChannelMonitorProbeModel(nil))

	channel := monitorChannel(t, 3, common.ChannelStatusEnabled, dto.ChannelOtherSettings{})
	channel.Models = "gpt-4o-mini"
	assert.Equal(t, "", resolveChannelMonitorProbeModel(channel))
}

func TestRunChannelMonitorProbeUsesSyntheticAccountPoolSchedulability(t *testing.T) {
	db := setupControllerChannelMonitorTestDB(t)
	channel := createControllerAccountPoolMonitorChannel(t, db, common.ChannelStatusManuallyDisabled)
	modelMapping := `{"gpt-4o-mini":"account-upstream-model"}`
	channel.ModelMapping = &modelMapping
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("model_mapping", modelMapping).Error)
	pool := createControllerAccountPoolMonitorPool(t)
	createControllerAccountPoolMonitorBinding(t, pool.Id, channel.Id)
	_, err := service.AccountPoolService{}.CreateAccount(service.AccountPoolAccountCreateParams{
		PoolID:          pool.Id,
		Name:            "schedulable",
		SupportedModels: []string{"account-upstream-model"},
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-account-pool-monitor",
		},
	})
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("status", common.ChannelStatusEnabled).Error)
	channel.Status = common.ChannelStatusEnabled
	channel.Key = ""

	runChannelMonitorProbe(channel, 1)

	var log model.ChannelMonitorLog
	require.NoError(t, db.First(&log, "channel_id = ?", channel.Id).Error)
	assert.Equal(t, model.ChannelMonitorStatusSuccess, log.Status)
	assert.Equal(t, "gpt-4o-mini", log.Model)
	assert.Empty(t, log.Message)
}

func TestRunChannelMonitorProbeMarksAccountPoolChannelFailedWhenNoAccountSchedulable(t *testing.T) {
	db := setupControllerChannelMonitorTestDB(t)
	channel := createControllerAccountPoolMonitorChannel(t, db, common.ChannelStatusManuallyDisabled)
	pool := createControllerAccountPoolMonitorPool(t)
	createControllerAccountPoolMonitorBinding(t, pool.Id, channel.Id)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("status", common.ChannelStatusEnabled).Error)
	channel.Status = common.ChannelStatusEnabled
	channel.Key = ""

	runChannelMonitorProbe(channel, 1)

	var log model.ChannelMonitorLog
	require.NoError(t, db.First(&log, "channel_id = ?", channel.Id).Error)
	assert.Equal(t, model.ChannelMonitorStatusFailed, log.Status)
	assert.Equal(t, "gpt-4o-mini", log.Model)
	assert.Contains(t, log.Message, "no schedulable account")
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

func TestShouldUseStreamForAutomaticChannelTestUsesSupportedChannels(t *testing.T) {
	assert.True(t, shouldUseStreamForAutomaticChannelTest(&model.Channel{Type: constant.ChannelTypeOpenAI}))
	assert.True(t, shouldUseStreamForAutomaticChannelTest(&model.Channel{Type: constant.ChannelTypeCodex}))
	assert.False(t, shouldUseStreamForAutomaticChannelTest(&model.Channel{Type: constant.ChannelTypeMidjourney}))
	assert.False(t, shouldUseStreamForAutomaticChannelTest(nil))
}

func TestShouldUseStreamForAutomaticChannelTestSkipsGeneratedUpstreamChannels(t *testing.T) {
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID: 7,
	})

	assert.False(t, shouldUseStreamForAutomaticChannelTest(channel))
}

func TestSelectAutomaticChannelTestModelPrefersTextModel(t *testing.T) {
	channel := &model.Channel{
		Models: "text-embedding-3-large,bge-reranker-v2-m3,gpt-4o-mini",
	}

	assert.Equal(t, "gpt-4o-mini", selectAutomaticChannelTestModel(channel))
}

func TestSelectAutomaticChannelTestModelHonorsExplicitTestModel(t *testing.T) {
	explicit := "bge-reranker-v2-m3"
	channel := &model.Channel{
		TestModel: &explicit,
		Models:    "text-embedding-3-large,gpt-4o-mini",
	}

	assert.Equal(t, explicit, selectAutomaticChannelTestModel(channel))
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

func TestApplyChannelMonitorStatusMutationIgnoresLocalChannelErrors(t *testing.T) {
	db := setupControllerChannelMonitorTestDB(t)
	gin.SetMode(gin.TestMode)
	channel := &model.Channel{
		Id:      13,
		Type:    constant.ChannelTypeOpenAI,
		Name:    "enabled-local-channel-error",
		Key:     "test-key",
		Status:  common.ChannelStatusEnabled,
		AutoBan: common.GetPointer(1),
	}
	require.NoError(t, db.Create(channel).Error)

	oldAutomaticDisable := common.AutomaticDisableChannelEnabled
	oldErrorLogEnabled := constant.ErrorLogEnabled
	common.AutomaticDisableChannelEnabled = true
	constant.ErrorLogEnabled = true
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = oldAutomaticDisable
		constant.ErrorLogEnabled = oldErrorLogEnabled
	})

	applyChannelMonitorStatusMutation(channel, testResult{
		context:     channelMonitorTestContext(channel),
		localErr:    errors.New("param override failed"),
		newAPIError: types.NewError(errors.New("param override failed"), types.ErrorCodeChannelParamOverrideInvalid),
	}, 100)

	var reloaded model.Channel
	require.NoError(t, db.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusEnabled, reloaded.Status)

	var errorLogCount int64
	require.NoError(t, db.Model(&model.Log{}).Count(&errorLogCount).Error)
	assert.Zero(t, errorLogCount)
}

func TestApplyChannelMonitorStatusMutationIgnoresLocalTimeout(t *testing.T) {
	db := setupControllerChannelMonitorTestDB(t)
	gin.SetMode(gin.TestMode)
	channel := &model.Channel{
		Id:      15,
		Type:    constant.ChannelTypeOpenAI,
		Name:    "enabled-local-timeout",
		Key:     "test-key",
		Status:  common.ChannelStatusEnabled,
		AutoBan: common.GetPointer(1),
	}
	require.NoError(t, db.Create(channel).Error)

	oldAutomaticDisable := common.AutomaticDisableChannelEnabled
	oldDisableThreshold := common.ChannelDisableThreshold
	oldErrorLogEnabled := constant.ErrorLogEnabled
	common.AutomaticDisableChannelEnabled = true
	common.ChannelDisableThreshold = 1
	constant.ErrorLogEnabled = true
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = oldAutomaticDisable
		common.ChannelDisableThreshold = oldDisableThreshold
		constant.ErrorLogEnabled = oldErrorLogEnabled
	})

	applyChannelMonitorStatusMutation(channel, testResult{
		context:     channelMonitorTestContext(channel),
		localErr:    errors.New("local setup stalled"),
		newAPIError: types.NewError(errors.New("local setup stalled"), types.ErrorCodeGenRelayInfoFailed),
	}, 2_000)

	var reloaded model.Channel
	require.NoError(t, db.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusEnabled, reloaded.Status)

	var errorLogCount int64
	require.NoError(t, db.Model(&model.Log{}).Count(&errorLogCount).Error)
	assert.Zero(t, errorLogCount)
}

func TestApplyChannelMonitorStatusMutationDisablesWithoutErrorLog(t *testing.T) {
	db := setupControllerChannelMonitorTestDB(t)
	gin.SetMode(gin.TestMode)
	channel := &model.Channel{
		Id:      14,
		Type:    constant.ChannelTypeOpenAI,
		Name:    "enabled-upstream-channel-error",
		Key:     "test-key",
		Status:  common.ChannelStatusEnabled,
		AutoBan: common.GetPointer(1),
	}
	require.NoError(t, db.Create(channel).Error)

	oldAutomaticDisable := common.AutomaticDisableChannelEnabled
	oldErrorLogEnabled := constant.ErrorLogEnabled
	common.AutomaticDisableChannelEnabled = true
	constant.ErrorLogEnabled = true
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = oldAutomaticDisable
		constant.ErrorLogEnabled = oldErrorLogEnabled
	})

	loggableErr := types.NewError(errors.New("upstream invalid key"), types.ErrorCodeChannelInvalidKey)
	processChannelError(
		channelMonitorTestContext(channel),
		*types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, "", false),
		loggableErr,
	)
	var errorLogCount int64
	require.NoError(t, db.Model(&model.Log{}).Count(&errorLogCount).Error)
	require.Equal(t, int64(1), errorLogCount)

	applyChannelMonitorStatusMutation(channel, testResult{
		context:           channelMonitorTestContext(channel),
		localErr:          errors.New("upstream invalid key"),
		newAPIError:       types.NewError(errors.New("upstream invalid key"), types.ErrorCodeChannelInvalidKey),
		upstreamAttempted: true,
	}, 100)

	var reloaded model.Channel
	require.NoError(t, db.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusAutoDisabled, reloaded.Status)

	require.NoError(t, db.Model(&model.Log{}).Count(&errorLogCount).Error)
	assert.Equal(t, int64(1), errorLogCount)
}

func TestApplyChannelMonitorStatusMutationTimeoutDisablesWithoutErrorLog(t *testing.T) {
	db := setupControllerChannelMonitorTestDB(t)
	gin.SetMode(gin.TestMode)
	channel := &model.Channel{
		Id:      16,
		Type:    constant.ChannelTypeOpenAI,
		Name:    "enabled-upstream-timeout",
		Key:     "test-key",
		Status:  common.ChannelStatusEnabled,
		AutoBan: common.GetPointer(1),
	}
	require.NoError(t, db.Create(channel).Error)

	oldAutomaticDisable := common.AutomaticDisableChannelEnabled
	oldDisableThreshold := common.ChannelDisableThreshold
	oldErrorLogEnabled := constant.ErrorLogEnabled
	common.AutomaticDisableChannelEnabled = true
	common.ChannelDisableThreshold = 1
	constant.ErrorLogEnabled = true
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = oldAutomaticDisable
		common.ChannelDisableThreshold = oldDisableThreshold
		constant.ErrorLogEnabled = oldErrorLogEnabled
	})

	loggableErr := types.NewError(errors.New("upstream invalid key"), types.ErrorCodeChannelInvalidKey)
	processChannelError(
		channelMonitorTestContext(channel),
		*types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, "", false),
		loggableErr,
	)
	var errorLogCount int64
	require.NoError(t, db.Model(&model.Log{}).Count(&errorLogCount).Error)
	require.Equal(t, int64(1), errorLogCount)

	applyChannelMonitorStatusMutation(channel, testResult{
		context:           channelMonitorTestContext(channel),
		upstreamAttempted: true,
	}, 2_000)

	var reloaded model.Channel
	require.NoError(t, db.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusAutoDisabled, reloaded.Status)

	require.NoError(t, db.Model(&model.Log{}).Count(&errorLogCount).Error)
	assert.Equal(t, int64(1), errorLogCount)
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
			localErr:          errors.New("upstream failed"),
			newAPIError:       types.NewError(errors.New("upstream failed"), types.ErrorCodeChannelInvalidKey),
			upstreamAttempted: true,
		}, 100)
	})

	logOutput, err := io.ReadAll(&logBuffer)
	require.NoError(t, err)
	assert.Contains(t, string(logOutput), "channel monitor skipped auto-disable for channel #11: missing monitor test context")
}
