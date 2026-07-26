package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const (
	relayTaskFailoverModel = "sora-2"
	relayTaskInitialQuota  = 2_000_000
)

type relayTaskFailoverCandidate struct {
	id           int
	priority     int64
	weight       *uint
	keys         []string
	disabledKeys map[int]int
}

type relayTaskFailoverOptions struct {
	candidates       []relayTaskFailoverCandidate
	statuses         map[int][]int
	initialChannelID int
	retryTimes       int
	locked           bool
	fixed            bool
	paid             bool
	contextSetup     func(*gin.Context)
	onAttempt        func(channelID int)
}

type relayTaskFailoverResult struct {
	statusCode      int
	body            string
	attempts        []int
	authorizations  []string
	usedChannels    []string
	errorLogCount   int64
	consumeLogCount int64
	consumeQuota    int
	taskCount       int64
	subscriptionUse int64
	user            model.User
	token           model.Token
}

func runRelayTaskFailover(t *testing.T, opts relayTaskFailoverOptions) relayTaskFailoverResult {
	t.Helper()
	gin.SetMode(gin.TestMode)

	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousMemoryCache := common.MemoryCacheEnabled
	previousBatchUpdateEnabled := common.BatchUpdateEnabled
	previousMainDBType := common.MainDatabaseType()
	previousLogDBType := common.LogDatabaseType()
	previousRetryTimes := common.RetryTimes
	previousErrorLogEnabled := constant.ErrorLogEnabled
	previousLogConsumeEnabled := common.LogConsumeEnabled
	previousAutomaticDisable := common.AutomaticDisableChannelEnabled
	previousGroupRatios := ratio_setting.GroupRatio2JSONString()
	previousFreeModelPreConsume := operation_setting.GetQuotaSetting().EnableFreeModelPreConsume

	dsn := "file:" + url.QueryEscape(t.Name()) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&model.Channel{},
		&model.Ability{},
		&model.User{},
		&model.Token{},
		&model.Log{},
		&model.Task{},
		&model.AccountPool{},
		&model.AccountPoolChannelBinding{},
		&model.SubscriptionPlan{},
		&model.UserSubscription{},
		&model.SubscriptionPreConsumeRecord{},
	))

	model.DB = db
	model.LOG_DB = db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	model.InitCommonColumnsForTest()
	common.MemoryCacheEnabled = true
	common.BatchUpdateEnabled = false
	common.RetryTimes = opts.retryTimes
	constant.ErrorLogEnabled = true
	common.LogConsumeEnabled = true
	common.AutomaticDisableChannelEnabled = false
	service.ResetAccountPoolRuntimeForTest()
	service.InitHttpClient()
	operation_setting.GetQuotaSetting().EnableFreeModelPreConsume = false
	groupRatio := 0
	if opts.paid {
		groupRatio = 1
	}
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(fmt.Sprintf(`{"default":%d}`, groupRatio)))

	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.MemoryCacheEnabled = previousMemoryCache
		common.BatchUpdateEnabled = previousBatchUpdateEnabled
		common.SetDatabaseTypes(previousMainDBType, previousLogDBType)
		model.InitCommonColumnsForTest()
		common.RetryTimes = previousRetryTimes
		constant.ErrorLogEnabled = previousErrorLogEnabled
		common.LogConsumeEnabled = previousLogConsumeEnabled
		common.AutomaticDisableChannelEnabled = previousAutomaticDisable
		service.ResetAccountPoolRuntimeForTest()
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroupRatios))
		operation_setting.GetQuotaSetting().EnableFreeModelPreConsume = previousFreeModelPreConsume
		model.InitChannelCache()
		require.NoError(t, sqlDB.Close())
	})

	require.NoError(t, db.Create(&model.User{
		Id:       1,
		Username: "relay-task-user",
		Password: "not-used-in-test",
		Status:   common.UserStatusEnabled,
		Quota:    relayTaskInitialQuota,
		Group:    "default",
	}).Error)
	require.NoError(t, db.Create(&model.Token{
		Id:             1,
		UserId:         1,
		Key:            "relay-task-token",
		Status:         common.TokenStatusEnabled,
		Name:           "relay-task-token",
		RemainQuota:    relayTaskInitialQuota,
		UnlimitedQuota: false,
		Group:          "default",
	}).Error)
	if opts.paid {
		now := time.Now()
		require.NoError(t, db.Create(&model.SubscriptionPlan{
			Id:          1,
			Title:       "relay task failover plan",
			Enabled:     true,
			TotalAmount: relayTaskInitialQuota,
		}).Error)
		require.NoError(t, db.Create(&model.UserSubscription{
			Id:          1,
			UserId:      1,
			PlanId:      1,
			AmountTotal: relayTaskInitialQuota,
			Status:      "active",
			StartTime:   now.Add(-time.Hour).Unix(),
			EndTime:     now.Add(time.Hour).Unix(),
		}).Error)
	}

	var attemptsMu sync.Mutex
	attempts := make([]int, 0, len(opts.candidates))
	authorizations := make([]string, 0, len(opts.candidates))
	attemptCounts := make(map[int]int)
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(pathParts) < 2 || pathParts[0] != "channel" {
			http.Error(w, "unexpected task relay path", http.StatusNotFound)
			return
		}
		channelID, parseErr := strconv.Atoi(pathParts[1])
		if parseErr != nil {
			http.Error(w, "invalid channel id", http.StatusBadRequest)
			return
		}

		attemptsMu.Lock()
		attemptIndex := attemptCounts[channelID]
		attemptCounts[channelID]++
		attempts = append(attempts, channelID)
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		attemptsMu.Unlock()
		if opts.onAttempt != nil {
			opts.onAttempt(channelID)
		}

		statusCode := http.StatusOK
		if configured := opts.statuses[channelID]; attemptIndex < len(configured) {
			statusCode = configured[attemptIndex]
		}
		if statusCode != http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusCode)
			_, _ = fmt.Fprintf(w, `{"error":{"message":"task-channel-%d-failed","code":"task_channel_%d_failed"}}`, channelID, channelID)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"upstream-task-%d","object":"video","model":"%s","status":"queued","progress":0,"seconds":"4","size":"720x1280"}`, channelID, relayTaskFailoverModel)
	}))
	t.Cleanup(upstreamServer.Close)

	for _, candidate := range opts.candidates {
		baseURL := fmt.Sprintf("%s/channel/%d", upstreamServer.URL, candidate.id)
		keys := candidate.keys
		if len(keys) == 0 {
			keys = []string{fmt.Sprintf("key-%d", candidate.id)}
		}
		weight := uint(100)
		if candidate.weight != nil {
			weight = *candidate.weight
		}
		channel := &model.Channel{
			Id:       candidate.id,
			Type:     constant.ChannelTypeOpenAI,
			Key:      strings.Join(keys, "\n"),
			Status:   common.ChannelStatusEnabled,
			Name:     fmt.Sprintf("task-channel-%d", candidate.id),
			BaseURL:  &baseURL,
			Group:    "default",
			Models:   relayTaskFailoverModel,
			Priority: &candidate.priority,
			Weight:   &weight,
			AutoBan:  common.GetPointer(0),
		}
		if len(keys) > 1 || len(candidate.disabledKeys) > 0 {
			channel.ChannelInfo = model.ChannelInfo{
				IsMultiKey:         true,
				MultiKeySize:       len(keys),
				MultiKeyStatusList: candidate.disabledKeys,
				MultiKeyMode:       constant.MultiKeyModePolling,
			}
		}
		require.NoError(t, db.Create(channel).Error)
		require.NoError(t, db.Create(&model.Ability{
			Group:     "default",
			Model:     relayTaskFailoverModel,
			ChannelId: candidate.id,
			Enabled:   true,
			Priority:  &candidate.priority,
			Weight:    weight,
		}).Error)
	}
	model.InitChannelCache()

	initialChannel, err := model.CacheGetChannel(opts.initialChannelID)
	require.NoError(t, err)
	require.NotNil(t, initialChannel)

	path := "/v1/videos"
	if opts.paid {
		path = "/pg/videos"
	}
	if opts.locked {
		path = "/v1/videos/origin-task/remix"
		require.NoError(t, db.Create(&model.Task{
			TaskID:    "origin-task",
			UserId:    1,
			ChannelId: opts.initialChannelID,
			Platform:  constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeOpenAI)),
			Status:    model.TaskStatusSuccess,
			Properties: model.Properties{
				OriginModelName:   relayTaskFailoverModel,
				UpstreamModelName: relayTaskFailoverModel,
			},
			Data: []byte(`{"model":"sora-2","seconds":"4","size":"720x1280"}`),
		}).Error)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestBody := fmt.Sprintf(`{"model":"%s","prompt":"hello","seconds":"4","size":"720x1280"}`, relayTaskFailoverModel)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	if opts.locked {
		c.Params = gin.Params{{Key: "video_id", Value: "origin-task"}}
	}

	common.SetContextKey(c, constant.ContextKeyUserId, 1)
	common.SetContextKey(c, constant.ContextKeyUserQuota, relayTaskInitialQuota)
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
	common.SetContextKey(c, constant.ContextKeyTokenId, 1)
	common.SetContextKey(c, constant.ContextKeyTokenKey, "relay-task-token")
	common.SetContextKey(c, constant.ContextKeyTokenUnlimited, false)
	common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
	billingPreference := "wallet_only"
	if opts.paid {
		billingPreference = "subscription_only"
	}
	common.SetContextKey(c, constant.ContextKeyUserSetting, dto.UserSetting{BillingPreference: billingPreference})
	c.Set("relay_mode", relayconstant.RelayModeVideoSubmit)
	c.Set("token_name", "relay-task-token")
	c.Set("username", "relay-task-user")
	if opts.fixed {
		c.Set("specific_channel_id", strconv.Itoa(opts.initialChannelID))
	}
	if opts.contextSetup != nil {
		opts.contextSetup(c)
	}
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, initialChannel, relayTaskFailoverModel))

	RelayTask(c)

	var errorLogCount int64
	require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeError).Count(&errorLogCount).Error)
	var consumeLogCount int64
	require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumeLogCount).Error)
	var consumeQuota int
	require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Select("quota").Scan(&consumeQuota).Error)
	var taskCount int64
	require.NoError(t, db.Model(&model.Task{}).Where("task_id <> ?", "origin-task").Count(&taskCount).Error)
	var user model.User
	require.NoError(t, db.First(&user, 1).Error)
	var token model.Token
	require.NoError(t, db.First(&token, 1).Error)
	var subscriptionUse int64
	if opts.paid {
		var subscription model.UserSubscription
		require.NoError(t, db.First(&subscription, 1).Error)
		subscriptionUse = subscription.AmountUsed
	}

	attemptsMu.Lock()
	finalAttempts := append([]int(nil), attempts...)
	finalAuthorizations := append([]string(nil), authorizations...)
	attemptsMu.Unlock()
	return relayTaskFailoverResult{
		statusCode:      recorder.Code,
		body:            recorder.Body.String(),
		attempts:        finalAttempts,
		authorizations:  finalAuthorizations,
		usedChannels:    append([]string(nil), c.GetStringSlice("use_channel")...),
		errorLogCount:   errorLogCount,
		consumeLogCount: consumeLogCount,
		consumeQuota:    consumeQuota,
		taskCount:       taskCount,
		subscriptionUse: subscriptionUse,
		user:            user,
		token:           token,
	}
}

func TestRelayTaskLockedChannelRotatesKeyWithoutCrossChannelFailover(t *testing.T) {
	result := runRelayTaskFailover(t, relayTaskFailoverOptions{
		candidates: []relayTaskFailoverCandidate{
			{id: 1, priority: 200, keys: []string{"locked-key-1", "locked-key-2"}},
			{id: 2, priority: 100},
		},
		statuses: map[int][]int{
			1: {http.StatusInternalServerError, http.StatusOK},
		},
		initialChannelID: 1,
		retryTimes:       1,
		locked:           true,
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Contains(t, result.body, `"status":"queued"`)
	assert.Equal(t, []int{1, 1}, result.attempts)
	assert.Equal(t, []string{"Bearer locked-key-1", "Bearer locked-key-2"}, result.authorizations)
	assert.Equal(t, []string{"1", "1"}, result.usedChannels)
	assert.EqualValues(t, 1, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
	assert.EqualValues(t, 1, result.taskCount)
}

func TestRelayTaskFixedChannelUsesDistributorSelectionAndRotatesKeys(t *testing.T) {
	result := runRelayTaskFailover(t, relayTaskFailoverOptions{
		candidates: []relayTaskFailoverCandidate{
			{id: 1, priority: 200, weight: common.GetPointer(uint(100))},
			{id: 2, priority: 200, weight: common.GetPointer(uint(0)), keys: []string{"fixed-key-1", "fixed-key-2"}},
		},
		statuses: map[int][]int{
			2: {http.StatusInternalServerError, http.StatusOK},
		},
		initialChannelID: 2,
		retryTimes:       1,
		fixed:            true,
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Contains(t, result.body, `"status":"queued"`)
	assert.Equal(t, []int{2, 2}, result.attempts)
	assert.Equal(t, []string{"Bearer fixed-key-1", "Bearer fixed-key-2"}, result.authorizations)
	assert.Equal(t, []string{"2", "2"}, result.usedChannels)
	assert.EqualValues(t, 1, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
	assert.EqualValues(t, 1, result.taskCount)
}

func TestRelayTaskAffinitySelectionIsAttemptedBeforeRemainingCandidates(t *testing.T) {
	result := runRelayTaskFailover(t, relayTaskFailoverOptions{
		candidates: []relayTaskFailoverCandidate{
			{id: 1, priority: 200, weight: common.GetPointer(uint(100))},
			{id: 2, priority: 200, weight: common.GetPointer(uint(0))},
			{id: 3, priority: 100},
		},
		statuses: map[int][]int{
			1: {http.StatusInternalServerError},
			2: {http.StatusInternalServerError},
		},
		initialChannelID: 2,
		retryTimes:       0,
		contextSetup: func(c *gin.Context) {
			c.Set("channel_affinity_skip_retry_on_failure", true)
		},
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Contains(t, result.body, `"status":"queued"`)
	assert.Equal(t, []int{2, 1, 3}, result.attempts)
	assert.Equal(t, []string{"2", "1", "3"}, result.usedChannels)
	assert.EqualValues(t, 2, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
	assert.EqualValues(t, 1, result.taskCount)
}

func TestRelayTaskSetupFailureContinuesToNextCandidate(t *testing.T) {
	result := runRelayTaskFailover(t, relayTaskFailoverOptions{
		candidates: []relayTaskFailoverCandidate{
			{id: 1, priority: 300},
			{
				id:       2,
				priority: 200,
				keys:     []string{"disabled-key-1", "disabled-key-2"},
				disabledKeys: map[int]int{
					0: common.ChannelStatusAutoDisabled,
					1: common.ChannelStatusManuallyDisabled,
				},
			},
			{id: 3, priority: 100},
		},
		statuses: map[int][]int{
			1: {http.StatusInternalServerError},
		},
		initialChannelID: 1,
		retryTimes:       0,
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Contains(t, result.body, `"status":"queued"`)
	assert.Equal(t, []int{1, 3}, result.attempts)
	assert.Equal(t, []string{"1", "2", "3"}, result.usedChannels)
	assert.EqualValues(t, 2, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
	assert.EqualValues(t, 1, result.taskCount)
}

func TestRelayTaskExhaustsCandidatesAndPreservesFinalError(t *testing.T) {
	result := runRelayTaskFailover(t, relayTaskFailoverOptions{
		candidates: []relayTaskFailoverCandidate{
			{id: 1, priority: 300},
			{id: 2, priority: 200},
			{id: 3, priority: 100},
		},
		statuses: map[int][]int{
			1: {http.StatusInternalServerError},
			2: {http.StatusInternalServerError},
			3: {http.StatusInternalServerError},
		},
		initialChannelID: 1,
		retryTimes:       0,
	})

	assert.Equal(t, http.StatusInternalServerError, result.statusCode)
	assert.Contains(t, result.body, "task-channel-3-failed")
	assert.NotContains(t, result.body, "task-channel-1-failed")
	assert.Equal(t, []int{1, 2, 3}, result.attempts)
	assert.Equal(t, []string{"1", "2", "3"}, result.usedChannels)
	assert.EqualValues(t, 3, result.errorLogCount)
	assert.Zero(t, result.consumeLogCount)
	assert.Zero(t, result.taskCount)
	assert.Equal(t, relayTaskInitialQuota, result.user.Quota)
	assert.Equal(t, relayTaskInitialQuota, result.token.RemainQuota)
}

func TestRelayTaskPaidFailoverPreConsumesAndSettlesOnce(t *testing.T) {
	result := runRelayTaskFailover(t, relayTaskFailoverOptions{
		candidates: []relayTaskFailoverCandidate{
			{id: 1, priority: 500},
			{id: 2, priority: 400},
			{id: 3, priority: 300},
			{id: 4, priority: 200},
			{id: 5, priority: 100},
		},
		statuses: map[int][]int{
			1: {http.StatusInternalServerError},
			2: {http.StatusInternalServerError},
			3: {http.StatusInternalServerError},
			4: {http.StatusInternalServerError},
		},
		initialChannelID: 1,
		retryTimes:       0,
		paid:             true,
	})

	require.Positive(t, result.consumeQuota)
	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Equal(t, []int{1, 2, 3, 4, 5}, result.attempts)
	assert.Equal(t, []string{"1", "2", "3", "4", "5"}, result.usedChannels)
	assert.EqualValues(t, 4, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
	assert.EqualValues(t, 1, result.taskCount)
	assert.Equal(t, 1, result.user.RequestCount)
	assert.Equal(t, relayTaskInitialQuota, result.user.Quota)
	assert.Equal(t, result.consumeQuota, result.user.UsedQuota)
	assert.Equal(t, relayTaskInitialQuota, result.token.RemainQuota)
	assert.Zero(t, result.token.UsedQuota)
	assert.Equal(t, int64(result.consumeQuota), result.subscriptionUse)
}

func TestRelayTaskSkipsChannelThatBecomesUnavailableBeforeSelection(t *testing.T) {
	result := runRelayTaskFailover(t, relayTaskFailoverOptions{
		candidates: []relayTaskFailoverCandidate{
			{id: 1, priority: 300},
			{id: 2, priority: 200},
			{id: 3, priority: 100},
		},
		statuses: map[int][]int{
			1: {http.StatusInternalServerError},
		},
		initialChannelID: 1,
		retryTimes:       0,
		onAttempt: func(channelID int) {
			if channelID == 1 {
				model.CacheUpdateChannelStatus(2, common.ChannelStatusAutoDisabled)
			}
		},
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Equal(t, []int{1, 3}, result.attempts)
	assert.Equal(t, []string{"1", "3"}, result.usedChannels)
	assert.EqualValues(t, 1, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
}
