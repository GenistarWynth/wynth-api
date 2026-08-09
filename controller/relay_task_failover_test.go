package controller

import (
	"bytes"
	"context"
	"fmt"
	"io"
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
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
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
	id             int
	priority       int64
	weight         *uint
	keys           []string
	disabledKeys   map[int]int
	channelType    int
	organization   string
	other          string
	createdTime    int64
	setting        string
	otherSetting   string
	paramOverride  string
	headerOverride string
	modelMapping   string
	statusMapping  string
	autoBan        int
}

type relayTaskFailoverOptions struct {
	candidates       []relayTaskFailoverCandidate
	statuses         map[int][]int
	responseBodies   map[int]string
	requestID        string
	initialChannelID int
	originChannelID  int
	originPlatform   constant.TaskPlatform
	retryTimes       int
	locked           bool
	fixed            bool
	paid             bool
	automaticDisable bool
	disableRanges    []operation_setting.StatusCodeRange
	contextSetup     func(*gin.Context)
	onAttempt        func(channelID int)
}

type relayTaskFailoverResult struct {
	statusCode          int
	body                string
	attempts            []int
	authorizations      []string
	requestHeaders      []http.Header
	requestBodies       []string
	usedChannels        []string
	errorLogCount       int64
	errorLogTypes       []int
	errorLogContent     string
	consumeLogCount     int64
	consumeQuota        int
	taskCount           int64
	subscriptionUse     int64
	user                model.User
	token               model.Token
	channelID           int
	channelType         int
	channelName         string
	channelBaseURL      string
	channelKey          string
	channelSetting      dto.ChannelSettings
	channelOtherSetting dto.ChannelOtherSettings
	paramOverride       map[string]interface{}
	headerOverride      map[string]interface{}
	organization        string
	apiVersion          string
	region              string
	isMultiKey          bool
	multiKeyIndex       int
	appLog              string
}

func runRelayTaskFailover(t *testing.T, opts relayTaskFailoverOptions) relayTaskFailoverResult {
	t.Helper()
	gin.SetMode(gin.TestMode)

	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousMemoryCache := common.MemoryCacheEnabled
	previousRedisEnabled := common.RedisEnabled
	previousBatchUpdateEnabled := common.BatchUpdateEnabled
	previousMainDBType := common.MainDatabaseType()
	previousLogDBType := common.LogDatabaseType()
	previousRetryTimes := common.RetryTimes
	previousErrorLogEnabled := constant.ErrorLogEnabled
	previousLogConsumeEnabled := common.LogConsumeEnabled
	previousAutomaticDisable := common.AutomaticDisableChannelEnabled
	previousDisableRanges := append([]operation_setting.StatusCodeRange(nil), operation_setting.AutomaticDisableStatusCodeRanges...)
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
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	common.RetryTimes = opts.retryTimes
	constant.ErrorLogEnabled = true
	common.LogConsumeEnabled = true
	common.AutomaticDisableChannelEnabled = opts.automaticDisable
	if opts.disableRanges != nil {
		operation_setting.AutomaticDisableStatusCodeRanges = append(
			[]operation_setting.StatusCodeRange(nil),
			opts.disableRanges...,
		)
	}
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
		common.RedisEnabled = previousRedisEnabled
		common.BatchUpdateEnabled = previousBatchUpdateEnabled
		common.SetDatabaseTypes(previousMainDBType, previousLogDBType)
		model.InitCommonColumnsForTest()
		common.RetryTimes = previousRetryTimes
		constant.ErrorLogEnabled = previousErrorLogEnabled
		common.LogConsumeEnabled = previousLogConsumeEnabled
		common.AutomaticDisableChannelEnabled = previousAutomaticDisable
		operation_setting.AutomaticDisableStatusCodeRanges = previousDisableRanges
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
	requestHeaders := make([]http.Header, 0, len(opts.candidates))
	requestBodies := make([]string, 0, len(opts.candidates))
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

		requestBody, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			http.Error(w, "failed to read task request", http.StatusBadRequest)
			return
		}

		attemptsMu.Lock()
		attemptIndex := attemptCounts[channelID]
		attemptCounts[channelID]++
		attempts = append(attempts, channelID)
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		requestHeaders = append(requestHeaders, r.Header.Clone())
		requestBodies = append(requestBodies, string(requestBody))
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
			if body := opts.responseBodies[channelID]; body != "" {
				_, _ = io.WriteString(w, body)
				return
			}
			_, _ = fmt.Fprintf(w, `{"error":{"message":"task-channel-%d-failed","code":"task_channel_%d_failed"}}`, channelID, channelID)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if body := opts.responseBodies[channelID]; body != "" {
			_, _ = io.WriteString(w, body)
			return
		}
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
		channelType := candidate.channelType
		if channelType == 0 {
			channelType = constant.ChannelTypeOpenAI
		}
		channel := &model.Channel{
			Id:          candidate.id,
			Type:        channelType,
			Key:         strings.Join(keys, "\n"),
			Status:      common.ChannelStatusEnabled,
			Name:        fmt.Sprintf("task-channel-%d", candidate.id),
			CreatedTime: candidate.createdTime,
			BaseURL:     &baseURL,
			Other:       candidate.other,
			Group:       "default",
			Models:      relayTaskFailoverModel,
			Priority:    &candidate.priority,
			Weight:      &weight,
			AutoBan:     common.GetPointer(candidate.autoBan),
		}
		if candidate.organization != "" {
			channel.OpenAIOrganization = common.GetPointer(candidate.organization)
		}
		if candidate.setting != "" {
			channel.Setting = common.GetPointer(candidate.setting)
		}
		channel.OtherSettings = candidate.otherSetting
		if candidate.paramOverride != "" {
			channel.ParamOverride = common.GetPointer(candidate.paramOverride)
		}
		if candidate.headerOverride != "" {
			channel.HeaderOverride = common.GetPointer(candidate.headerOverride)
		}
		if candidate.modelMapping != "" {
			channel.ModelMapping = common.GetPointer(candidate.modelMapping)
		}
		if candidate.statusMapping != "" {
			channel.StatusCodeMapping = common.GetPointer(candidate.statusMapping)
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
		originChannelID := opts.originChannelID
		if originChannelID == 0 {
			originChannelID = opts.initialChannelID
		}
		originPlatform := opts.originPlatform
		if originPlatform == "" {
			originPlatform = constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeOpenAI))
		}
		require.NoError(t, db.Create(&model.Task{
			TaskID:    "origin-task",
			UserId:    1,
			ChannelId: originChannelID,
			Platform:  originPlatform,
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
	requestBody := relayTaskFailoverRequestBody()
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
	requestID := opts.requestID
	if requestID == "" {
		requestID = "relay-task-" + t.Name()
	}
	c.Set(common.RequestIdKey, requestID)
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
	middleware.PreserveInitialChannelSetup(c, initialChannel, relayTaskFailoverModel)

	var appLog bytes.Buffer
	common.LogWriterMu.Lock()
	oldErrorWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &appLog
	common.LogWriterMu.Unlock()
	RelayTask(c)
	common.LogWriterMu.Lock()
	gin.DefaultErrorWriter = oldErrorWriter
	common.LogWriterMu.Unlock()

	errorLogTypes := []int{model.LogTypeError, model.LogTypeRetryError}
	var errorLogs []model.Log
	require.NoError(t, db.Where("type IN ?", errorLogTypes).Order("id").Find(&errorLogs).Error)
	errorLogCount := int64(len(errorLogs))
	storedErrorLogTypes := make([]int, 0, len(errorLogs))
	for _, logEntry := range errorLogs {
		storedErrorLogTypes = append(storedErrorLogTypes, logEntry.Type)
	}
	var errorLog model.Log
	if errorLogCount > 0 {
		errorLog = errorLogs[len(errorLogs)-1]
	}
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
	finalRequestHeaders := append([]http.Header(nil), requestHeaders...)
	finalRequestBodies := append([]string(nil), requestBodies...)
	attemptsMu.Unlock()
	channelSetting, _ := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	channelOtherSetting, _ := common.GetContextKeyType[dto.ChannelOtherSettings](c, constant.ContextKeyChannelOtherSetting)
	return relayTaskFailoverResult{
		statusCode:          recorder.Code,
		body:                recorder.Body.String(),
		attempts:            finalAttempts,
		authorizations:      finalAuthorizations,
		requestHeaders:      finalRequestHeaders,
		requestBodies:       finalRequestBodies,
		usedChannels:        append([]string(nil), c.GetStringSlice("use_channel")...),
		errorLogCount:       errorLogCount,
		errorLogTypes:       storedErrorLogTypes,
		errorLogContent:     errorLog.Content,
		consumeLogCount:     consumeLogCount,
		consumeQuota:        consumeQuota,
		taskCount:           taskCount,
		subscriptionUse:     subscriptionUse,
		user:                user,
		token:               token,
		channelID:           common.GetContextKeyInt(c, constant.ContextKeyChannelId),
		channelType:         common.GetContextKeyInt(c, constant.ContextKeyChannelType),
		channelName:         common.GetContextKeyString(c, constant.ContextKeyChannelName),
		channelBaseURL:      common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl),
		channelKey:          common.GetContextKeyString(c, constant.ContextKeyChannelKey),
		channelSetting:      channelSetting,
		channelOtherSetting: channelOtherSetting,
		paramOverride:       common.GetContextKeyStringMap(c, constant.ContextKeyChannelParamOverride),
		headerOverride:      common.GetContextKeyStringMap(c, constant.ContextKeyChannelHeaderOverride),
		organization:        common.GetContextKeyString(c, constant.ContextKeyChannelOrganization),
		apiVersion:          c.GetString("api_version"),
		region:              c.GetString("region"),
		isMultiKey:          common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey),
		multiKeyIndex:       common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex),
		appLog:              appLog.String(),
	}
}

func relayTaskFailoverRequestBody() string {
	return fmt.Sprintf(`{"model":"%s","prompt":"hello","seconds":"4","size":"720x1280"}`, relayTaskFailoverModel)
}

func TestRelayTaskLockedOriginReplacesEveryDistributorSettingBeforeFirstAndRotatedAttempts(t *testing.T) {
	result := runRelayTaskFailover(t, relayTaskFailoverOptions{
		candidates: []relayTaskFailoverCandidate{
			{
				id:             1,
				priority:       100,
				keys:           []string{"origin-key-1", "origin-key-2"},
				organization:   "origin-organization",
				createdTime:    111,
				setting:        `{"proxy":"","system_prompt":"origin-system"}`,
				otherSetting:   `{"client_identity_preset":"origin-client"}`,
				paramOverride:  `{"temperature":0.25}`,
				headerOverride: `{"X-Channel-Origin":"origin-header"}`,
				modelMapping:   `{"sora-2":"origin-upstream-model"}`,
				statusMapping:  `{"502":503}`,
			},
			{
				id:             2,
				priority:       200,
				channelType:    constant.ChannelTypeAzure,
				keys:           []string{"distributor-key"},
				organization:   "distributor-organization",
				other:          "distributor-api-version",
				createdTime:    222,
				setting:        `{"proxy":"http://127.0.0.1:1","system_prompt":"distributor-system"}`,
				otherSetting:   `{"client_identity_preset":"distributor-client"}`,
				paramOverride:  `{"temperature":0.75}`,
				headerOverride: `{"X-Channel-Origin":"distributor-header"}`,
				modelMapping:   `{"sora-2":"distributor-upstream-model"}`,
				statusMapping:  `{"502":504}`,
			},
		},
		statuses: map[int][]int{
			1: {http.StatusInternalServerError, http.StatusOK},
		},
		initialChannelID: 2,
		originChannelID:  1,
		retryTimes:       1,
		locked:           true,
		contextSetup: func(c *gin.Context) {
			c.Set("region", "distributor-region")
		},
	})

	require.Equal(t, http.StatusOK, result.statusCode)
	assert.Equal(t, []int{1, 1}, result.attempts)
	assert.Equal(t, []string{"Bearer origin-key-1", "Bearer origin-key-2"}, result.authorizations)
	require.Len(t, result.requestHeaders, 2)
	require.Len(t, result.requestBodies, 2)
	for attempt := range result.requestHeaders {
		assert.Equal(t, "origin-header", result.requestHeaders[attempt].Get("X-Channel-Origin"))
		assert.Equal(t, "origin-organization", result.requestHeaders[attempt].Get("OpenAI-Organization"))
		assert.Contains(t, result.requestBodies[attempt], `"model":"origin-upstream-model"`)
		assert.NotContains(t, result.requestBodies[attempt], "distributor-upstream-model")
		assert.NotContains(t, result.requestHeaders[attempt].Get("Authorization"), "distributor-key")
	}

	assert.Equal(t, 1, result.channelID)
	assert.Equal(t, constant.ChannelTypeOpenAI, result.channelType)
	assert.Equal(t, "task-channel-1", result.channelName)
	assert.Contains(t, result.channelBaseURL, "/channel/1")
	assert.Equal(t, "origin-key-2", result.channelKey)
	assert.Equal(t, "origin-organization", result.organization)
	assert.Equal(t, "origin-system", result.channelSetting.SystemPrompt)
	assert.Empty(t, result.channelSetting.Proxy)
	assert.Equal(t, "origin-client", result.channelOtherSetting.ClientIdentityPreset)
	assert.EqualValues(t, 0.25, result.paramOverride["temperature"])
	assert.Equal(t, "origin-header", result.headerOverride["X-Channel-Origin"])
	assert.Empty(t, result.apiVersion)
	assert.Empty(t, result.region)
	assert.True(t, result.isMultiKey)
	assert.Equal(t, 1, result.multiKeyIndex)
}

func TestRelayTaskLockedOriginSetupFailureHonorsCancellationWithoutStaleCredentials(t *testing.T) {
	result := runRelayTaskFailover(t, relayTaskFailoverOptions{
		candidates: []relayTaskFailoverCandidate{
			{
				id:             1,
				priority:       100,
				keys:           []string{"disabled-origin-key"},
				disabledKeys:   map[int]int{0: common.ChannelStatusAutoDisabled},
				organization:   "origin-organization",
				headerOverride: `{"X-Channel-Origin":"origin-header"}`,
			},
			{
				id:           2,
				priority:     200,
				channelType:  constant.ChannelTypeAzure,
				keys:         []string{"stale-distributor-key"},
				organization: "stale-distributor-organization",
				other:        "stale-distributor-api-version",
			},
		},
		initialChannelID: 2,
		originChannelID:  1,
		retryTimes:       5,
		locked:           true,
		contextSetup: func(c *gin.Context) {
			requestContext, cancel := context.WithCancel(c.Request.Context())
			cancel()
			c.Request = c.Request.WithContext(requestContext)
		},
	})

	assert.Equal(t, http.StatusInternalServerError, result.statusCode)
	assert.Empty(t, result.attempts)
	assert.Equal(t, []string{"1"}, result.usedChannels)
	assert.Equal(t, 1, result.channelID)
	assert.Equal(t, constant.ChannelTypeOpenAI, result.channelType)
	assert.Empty(t, result.channelKey)
	assert.Equal(t, "origin-organization", result.organization)
	assert.NotContains(t, result.body, "stale-distributor-key")
	assert.NotContains(t, result.body, "stale-distributor-organization")
	assert.Empty(t, result.apiVersion)
	assert.Empty(t, result.region)
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

func TestRelayTaskFinalUpstreamErrorAlwaysSanitizesCredentials(t *testing.T) {
	const secret = "sk-live-ABCDEFGHIJKLMNOPQRST"
	result := runRelayTaskFailover(t, relayTaskFailoverOptions{
		candidates: []relayTaskFailoverCandidate{{id: 1, priority: 100}},
		statuses: map[int][]int{
			1: {http.StatusInternalServerError},
		},
		responseBodies: map[int]string{
			1: `{"error":{"message":"credential ` + secret + ` for provider unavailable","code":"provider_denied"}}`,
		},
		initialChannelID: 1,
		retryTimes:       0,
	})

	assert.Equal(t, http.StatusInternalServerError, result.statusCode)
	assert.Contains(t, result.body, "provider unavailable")
	assert.Contains(t, result.body, "provider_denied")
	assert.NotContains(t, result.body, secret)
	assert.NotContains(t, result.errorLogContent, secret)
	assert.EqualValues(t, 1, result.errorLogCount)
	assert.Zero(t, result.consumeLogCount)
}

func TestRelayTaskFinalSafeUpstreamErrorPreservesDiagnosticMessage(t *testing.T) {
	const safeMessage = "provider capacity exhausted in region west"
	result := runRelayTaskFailover(t, relayTaskFailoverOptions{
		candidates: []relayTaskFailoverCandidate{{id: 1, priority: 100}},
		statuses: map[int][]int{
			1: {http.StatusInternalServerError},
		},
		responseBodies: map[int]string{
			1: `{"error":{"message":"` + safeMessage + `","code":"capacity_exhausted"}}`,
		},
		initialChannelID: 1,
		retryTimes:       0,
	})

	assert.Equal(t, http.StatusInternalServerError, result.statusCode)
	assert.Contains(t, result.body, safeMessage)
	assert.Contains(t, result.errorLogContent, safeMessage)
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

func TestRelayTaskDistributorNoKeyContinuesWithoutUpstreamRequest(t *testing.T) {
	result := runRelayTaskFailover(t, relayTaskFailoverOptions{
		candidates: []relayTaskFailoverCandidate{
			{
				id:       1,
				priority: 200,
				keys:     []string{"credential-first", "credential-second"},
				disabledKeys: map[int]int{
					0: common.ChannelStatusAutoDisabled,
					1: common.ChannelStatusManuallyDisabled,
				},
			},
			{id: 2, priority: 100},
		},
		statuses:         map[int][]int{},
		initialChannelID: 1,
		retryTimes:       0,
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Contains(t, result.body, `"status":"queued"`)
	assert.NotContains(t, result.body, "credential-first")
	assert.NotContains(t, result.body, "credential-second")
	assert.Equal(t, []int{2}, result.attempts)
	assert.Equal(t, []string{"1", "2"}, result.usedChannels)
	assert.EqualValues(t, 1, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
	assert.EqualValues(t, 1, result.taskCount)
}

func TestRelayTaskChannelNoRetryStatusStillExhaustsSameGroup(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{name: "bad request", statusCode: http.StatusBadRequest},
		{name: "gateway timeout", statusCode: http.StatusGatewayTimeout},
		{name: "cloudflare timeout", statusCode: 524},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := runRelayTaskFailover(t, relayTaskFailoverOptions{
				candidates: []relayTaskFailoverCandidate{
					{id: 1, priority: 200},
					{id: 2, priority: 100},
				},
				statuses: map[int][]int{
					1: {test.statusCode},
				},
				initialChannelID: 1,
				retryTimes:       0,
			})

			assert.Equal(t, http.StatusOK, result.statusCode)
			assert.Contains(t, result.body, `"status":"queued"`)
			assert.Equal(t, []int{1, 2}, result.attempts)
			assert.Equal(t, []string{"1", "2"}, result.usedChannels)
			assert.EqualValues(t, 1, result.errorLogCount)
			assert.EqualValues(t, 1, result.consumeLogCount)
			assert.EqualValues(t, 1, result.taskCount)
		})
	}
}

func TestRelayTaskFixedDistributorNoKeyStaysPinnedWithinLegacyRetryLimit(t *testing.T) {
	result := runRelayTaskFailover(t, relayTaskFailoverOptions{
		candidates: []relayTaskFailoverCandidate{
			{
				id:       1,
				priority: 200,
				keys:     []string{"disabled-first", "disabled-second"},
				disabledKeys: map[int]int{
					0: common.ChannelStatusAutoDisabled,
					1: common.ChannelStatusManuallyDisabled,
				},
			},
			{id: 2, priority: 100},
		},
		statuses:         map[int][]int{},
		initialChannelID: 1,
		retryTimes:       1,
		fixed:            true,
	})

	assert.Equal(t, http.StatusInternalServerError, result.statusCode)
	assert.Contains(t, result.body, "no enabled keys")
	assert.Empty(t, result.attempts)
	assert.Equal(t, []string{"1", "1"}, result.usedChannels)
	assert.EqualValues(t, 2, result.errorLogCount)
	assert.Zero(t, result.consumeLogCount)
	assert.Zero(t, result.taskCount)
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
	assert.Equal(t, []int{model.LogTypeRetryError, model.LogTypeRetryError, model.LogTypeError}, result.errorLogTypes)
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
	assert.Equal(t, []int{model.LogTypeRetryError, model.LogTypeRetryError, model.LogTypeRetryError, model.LogTypeRetryError}, result.errorLogTypes)
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
