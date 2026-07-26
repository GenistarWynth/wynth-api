package controller

import (
	"context"
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
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const relayFailoverModel = "gpt-5.6-sol"

type relayFailoverCandidate struct {
	id             int
	group          string
	model          string
	priority       int64
	status         int
	abilityEnabled bool
	channelType    int
	supportedPath  string
}

type relayFailoverUpstream struct {
	statusCode      int
	stream          bool
	abortAfterChunk bool
	onAttempt       func()
}

type relayFailoverOptions struct {
	candidates       []relayFailoverCandidate
	upstreams        map[int]relayFailoverUpstream
	initialChannelID int
	retryTimes       int
	usingGroup       string
	autoGroups       []string
	stream           bool
	relayFormat      types.RelayFormat
	requestPath      string
	requestBody      string
	requestContext   context.Context
	contextSetup     func(*gin.Context)
}

type relayFailoverResult struct {
	statusCode       int
	body             string
	attempts         []int
	usedChannels     []string
	errorLogCount    int64
	consumeLogCount  int64
	userRequestCount int
}

func enabledRelayFailoverCandidate(id int, group string, priority int64) relayFailoverCandidate {
	return relayFailoverCandidate{
		id:             id,
		group:          group,
		model:          relayFailoverModel,
		priority:       priority,
		status:         common.ChannelStatusEnabled,
		abilityEnabled: true,
		channelType:    constant.ChannelTypeOpenAI,
	}
}

func runRelayFailover(t *testing.T, opts relayFailoverOptions) relayFailoverResult {
	t.Helper()
	gin.SetMode(gin.TestMode)

	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousMemoryCache := common.MemoryCacheEnabled
	previousMainDBType := common.MainDatabaseType()
	previousLogDBType := common.LogDatabaseType()
	previousRetryTimes := common.RetryTimes
	previousCountToken := constant.CountToken
	previousStreamingTimeout := constant.StreamingTimeout
	previousErrorLogEnabled := constant.ErrorLogEnabled
	previousLogConsumeEnabled := common.LogConsumeEnabled
	previousAutomaticDisable := common.AutomaticDisableChannelEnabled
	previousDataExportEnabled := common.DataExportEnabled
	previousGroupRatios := ratio_setting.GroupRatio2JSONString()
	previousModelRatios := ratio_setting.ModelRatio2JSONString()
	previousAutoGroups := setting.AutoGroups2JsonString()
	previousUserUsableGroups := setting.UserUsableGroups2JSONString()
	previousRetryRanges := append([]operation_setting.StatusCodeRange(nil), operation_setting.AutomaticRetryStatusCodeRanges...)
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
		&model.Log{},
		&model.AccountPoolChannelBinding{},
	))

	model.DB = db
	model.LOG_DB = db
	common.MemoryCacheEnabled = true
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	common.RetryTimes = opts.retryTimes
	constant.CountToken = false
	constant.StreamingTimeout = 30
	constant.ErrorLogEnabled = true
	common.LogConsumeEnabled = true
	common.AutomaticDisableChannelEnabled = false
	common.DataExportEnabled = false
	service.ResetAccountPoolRuntimeForTest()
	service.InitHttpClient()
	operation_setting.AutomaticRetryStatusCodeRanges = []operation_setting.StatusCodeRange{{Start: 500, End: 599}}
	operation_setting.GetQuotaSetting().EnableFreeModelPreConsume = false
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-5.6-sol":2.5}`))

	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.MemoryCacheEnabled = previousMemoryCache
		common.SetMainDatabaseType(previousMainDBType)
		common.SetLogDatabaseType(previousLogDBType)
		common.RetryTimes = previousRetryTimes
		constant.CountToken = previousCountToken
		constant.StreamingTimeout = previousStreamingTimeout
		constant.ErrorLogEnabled = previousErrorLogEnabled
		common.LogConsumeEnabled = previousLogConsumeEnabled
		common.AutomaticDisableChannelEnabled = previousAutomaticDisable
		common.DataExportEnabled = previousDataExportEnabled
		service.ResetAccountPoolRuntimeForTest()
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroupRatios))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousModelRatios))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(previousAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousUserUsableGroups))
		operation_setting.AutomaticRetryStatusCodeRanges = previousRetryRanges
		operation_setting.GetQuotaSetting().EnableFreeModelPreConsume = previousFreeModelPreConsume
		model.InitChannelCache()
		require.NoError(t, sqlDB.Close())
	})

	groupRatios := ratio_setting.GetGroupRatioSetting().GroupRatio
	for _, candidate := range opts.candidates {
		groupRatios.Set(candidate.group, 0)
	}
	if len(opts.autoGroups) > 0 {
		autoGroupsJSON := `["` + strings.Join(opts.autoGroups, `","`) + `"]`
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(autoGroupsJSON))
		usableGroups := make(map[string]string, len(opts.autoGroups))
		for _, group := range opts.autoGroups {
			usableGroups[group] = group
		}
		usableGroupsJSON, marshalErr := common.Marshal(usableGroups)
		require.NoError(t, marshalErr)
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(string(usableGroupsJSON)))
	}

	require.NoError(t, db.Create(&model.User{
		Id:       1,
		Username: "relay-failover-user",
		Password: "not-used-in-test",
		Status:   common.UserStatusEnabled,
		Quota:    1_000_000,
		Group:    "default",
	}).Error)

	var attemptsMu sync.Mutex
	attempts := make([]int, 0, len(opts.candidates))
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(pathParts) < 2 || pathParts[0] != "channel" {
			http.Error(w, "unexpected relay path", http.StatusNotFound)
			return
		}
		channelID, parseErr := strconv.Atoi(pathParts[1])
		if parseErr != nil {
			http.Error(w, "invalid channel id", http.StatusBadRequest)
			return
		}

		attemptsMu.Lock()
		attempts = append(attempts, channelID)
		attemptsMu.Unlock()

		upstream := opts.upstreams[channelID]
		if upstream.onAttempt != nil {
			upstream.onAttempt()
		}
		if upstream.statusCode != 0 && upstream.statusCode != http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(upstream.statusCode)
			_, _ = fmt.Fprintf(w, `{"error":{"message":"channel-%d-failed","type":"upstream_error","code":"channel_%d_failed"}}`, channelID, channelID)
			return
		}
		if upstream.stream {
			streamChunk := fmt.Sprintf("data: {\"id\":\"chatcmpl-%d\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"%s\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"channel-%d\"},\"finish_reason\":null}]}\n\n", channelID, relayFailoverModel, channelID)
			if upstream.abortAfterChunk {
				hijacker, ok := w.(http.Hijacker)
				if !ok {
					http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
					return
				}
				conn, buffer, hijackErr := hijacker.Hijack()
				if hijackErr != nil {
					return
				}
				_, _ = fmt.Fprintf(buffer, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: %d\r\n\r\n%s", len(streamChunk)+1024, streamChunk)
				_ = buffer.Flush()
				_ = conn.Close()
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, streamChunk)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"chatcmpl-%d","object":"chat.completion","created":1,"model":"%s","choices":[{"index":0,"message":{"role":"assistant","content":"channel-%d"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`, channelID, relayFailoverModel, channelID)
	}))
	t.Cleanup(upstreamServer.Close)

	for _, candidate := range opts.candidates {
		baseURL := fmt.Sprintf("%s/channel/%d", upstreamServer.URL, candidate.id)
		channel := &model.Channel{
			Id:       candidate.id,
			Type:     candidate.channelType,
			Key:      fmt.Sprintf("sk-channel-%d", candidate.id),
			Status:   candidate.status,
			Name:     fmt.Sprintf("channel-%d", candidate.id),
			BaseURL:  &baseURL,
			Group:    candidate.group,
			Models:   candidate.model,
			Priority: &candidate.priority,
			Weight:   common.GetPointer(uint(100)),
			AutoBan:  common.GetPointer(0),
		}
		if candidate.channelType == constant.ChannelTypeAdvancedCustom {
			channel.SetOtherSettings(dto.ChannelOtherSettings{
				AdvancedCustom: &dto.AdvancedCustomConfig{
					Routes: []dto.AdvancedCustomRoute{{
						IncomingPath: candidate.supportedPath,
						UpstreamPath: upstreamServer.URL + "/unused",
						Converter:    dto.AdvancedCustomConverterNone,
					}},
				},
			})
		}
		require.NoError(t, db.Create(channel).Error)
		require.NoError(t, db.Create(&model.Ability{
			Group:     candidate.group,
			Model:     candidate.model,
			ChannelId: candidate.id,
			Enabled:   candidate.abilityEnabled,
			Priority:  &candidate.priority,
			Weight:    100,
		}).Error)
	}
	model.InitChannelCache()

	initialChannel, err := model.CacheGetChannel(opts.initialChannelID)
	require.NoError(t, err)
	require.NotNil(t, initialChannel)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestBody := fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"hello"}],"stream":%t}`, relayFailoverModel, opts.stream)
	if opts.requestBody != "" {
		requestBody = opts.requestBody
	}
	requestPath := opts.requestPath
	if requestPath == "" {
		requestPath = "/v1/chat/completions"
	}
	requestContext := opts.requestContext
	if requestContext == nil {
		requestContext = context.Background()
	}
	c.Request = httptest.NewRequest(http.MethodPost, requestPath, strings.NewReader(requestBody)).WithContext(requestContext)
	c.Request.Header.Set("Content-Type", "application/json")

	common.SetContextKey(c, constant.ContextKeyUserId, 1)
	common.SetContextKey(c, constant.ContextKeyUserQuota, 1_000_000)
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, opts.usingGroup)
	common.SetContextKey(c, constant.ContextKeyTokenGroup, opts.usingGroup)
	common.SetContextKey(c, constant.ContextKeyTokenId, 1)
	common.SetContextKey(c, constant.ContextKeyTokenKey, "test-token")
	common.SetContextKey(c, constant.ContextKeyTokenUnlimited, false)
	common.SetContextKey(c, constant.ContextKeyUserSetting, dto.UserSetting{BillingPreference: "wallet_only"})
	common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
	c.Set("token_name", "relay-failover-token")
	c.Set("username", "relay-failover-user")
	if len(opts.autoGroups) > 0 {
		common.SetContextKey(c, constant.ContextKeyAutoGroup, opts.candidates[0].group)
		common.SetContextKey(c, constant.ContextKeyAutoGroupIndex, 0)
	}
	if opts.contextSetup != nil {
		opts.contextSetup(c)
	}
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, initialChannel, relayFailoverModel))

	relayFormat := opts.relayFormat
	if relayFormat == "" {
		relayFormat = types.RelayFormatOpenAI
	}
	Relay(c, relayFormat)

	var errorLogCount int64
	require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeError).Count(&errorLogCount).Error)
	var consumeLogCount int64
	require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumeLogCount).Error)
	var user model.User
	require.NoError(t, db.First(&user, 1).Error)

	attemptsMu.Lock()
	finalAttempts := append([]int(nil), attempts...)
	attemptsMu.Unlock()
	return relayFailoverResult{
		statusCode:       recorder.Code,
		body:             recorder.Body.String(),
		attempts:         finalAttempts,
		usedChannels:     append([]string(nil), c.GetStringSlice("use_channel")...),
		errorLogCount:    errorLogCount,
		consumeLogCount:  consumeLogCount,
		userRequestCount: user.RequestCount,
	}
}

func TestRelayExhaustsEligibleChannelsBeyondRetryTimes(t *testing.T) {
	candidates := []relayFailoverCandidate{
		enabledRelayFailoverCandidate(1, "default", 500),
		enabledRelayFailoverCandidate(2, "default", 400),
		enabledRelayFailoverCandidate(3, "default", 300),
		enabledRelayFailoverCandidate(4, "default", 200),
		enabledRelayFailoverCandidate(5, "default", 100),
	}
	upstreams := map[int]relayFailoverUpstream{}
	for channelID := 1; channelID <= 4; channelID++ {
		upstreams[channelID] = relayFailoverUpstream{statusCode: http.StatusInternalServerError}
	}

	result := runRelayFailover(t, relayFailoverOptions{
		candidates:       candidates,
		upstreams:        upstreams,
		initialChannelID: 1,
		retryTimes:       2,
		usingGroup:       "default",
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Contains(t, result.body, "channel-5")
	assert.Equal(t, []int{1, 2, 3, 4, 5}, result.attempts)
	assert.Equal(t, []string{"1", "2", "3", "4", "5"}, result.usedChannels)
	assert.EqualValues(t, 4, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
	assert.Equal(t, 1, result.userRequestCount)
}

func TestRelayReturnsFinalErrorOnlyAfterTotalExhaustion(t *testing.T) {
	candidates := []relayFailoverCandidate{
		enabledRelayFailoverCandidate(1, "default", 300),
		enabledRelayFailoverCandidate(2, "default", 200),
		enabledRelayFailoverCandidate(3, "default", 100),
	}
	upstreams := map[int]relayFailoverUpstream{
		1: {statusCode: http.StatusInternalServerError},
		2: {statusCode: http.StatusInternalServerError},
		3: {statusCode: http.StatusInternalServerError},
	}

	result := runRelayFailover(t, relayFailoverOptions{
		candidates:       candidates,
		upstreams:        upstreams,
		initialChannelID: 1,
		retryTimes:       1,
		usingGroup:       "default",
	})

	assert.Equal(t, http.StatusInternalServerError, result.statusCode)
	assert.Contains(t, result.body, "channel-3-failed")
	assert.NotContains(t, result.body, "channel-1-failed")
	assert.Equal(t, []int{1, 2, 3}, result.attempts)
	assert.Equal(t, []string{"1", "2", "3"}, result.usedChannels)
	assert.EqualValues(t, 3, result.errorLogCount)
	assert.Zero(t, result.consumeLogCount)
	assert.Zero(t, result.userRequestCount)
}

func TestRelayExcludesIneligibleChannelsAndReachesLowerPriority(t *testing.T) {
	candidates := []relayFailoverCandidate{
		enabledRelayFailoverCandidate(1, "default", 500),
		enabledRelayFailoverCandidate(2, "default", 100),
		{
			id: 10, group: "default", model: relayFailoverModel, priority: 1_000,
			status: common.ChannelStatusManuallyDisabled, abilityEnabled: true, channelType: constant.ChannelTypeOpenAI,
		},
		{
			id: 11, group: "default", model: relayFailoverModel, priority: 950,
			status: common.ChannelStatusAutoDisabled, abilityEnabled: true, channelType: constant.ChannelTypeOpenAI,
		},
		{
			id: 12, group: "default", model: relayFailoverModel, priority: 900,
			status: common.ChannelStatusEnabled, abilityEnabled: false, channelType: constant.ChannelTypeOpenAI,
		},
		{
			id: 13, group: "default", model: "different-model", priority: 850,
			status: common.ChannelStatusEnabled, abilityEnabled: true, channelType: constant.ChannelTypeOpenAI,
		},
		{
			id: 14, group: "other-group", model: relayFailoverModel, priority: 800,
			status: common.ChannelStatusEnabled, abilityEnabled: true, channelType: constant.ChannelTypeOpenAI,
		},
		{
			id: 15, group: "default", model: relayFailoverModel, priority: 750,
			status: common.ChannelStatusEnabled, abilityEnabled: true, channelType: constant.ChannelTypeAdvancedCustom,
			supportedPath: "/v1/responses",
		},
	}
	upstreams := map[int]relayFailoverUpstream{
		1:  {statusCode: http.StatusInternalServerError},
		10: {statusCode: http.StatusInternalServerError},
		11: {statusCode: http.StatusInternalServerError},
		12: {statusCode: http.StatusInternalServerError},
		13: {statusCode: http.StatusInternalServerError},
		14: {statusCode: http.StatusInternalServerError},
		15: {statusCode: http.StatusInternalServerError},
	}

	result := runRelayFailover(t, relayFailoverOptions{
		candidates:       candidates,
		upstreams:        upstreams,
		initialChannelID: 1,
		retryTimes:       10,
		usingGroup:       "default",
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Contains(t, result.body, "channel-2")
	assert.Equal(t, []int{1, 2}, result.attempts)
	assert.Equal(t, []string{"1", "2"}, result.usedChannels)
	assert.EqualValues(t, 1, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
}

func TestRelayAffinityFirstFailureExhaustsRemainingCandidates(t *testing.T) {
	candidates := []relayFailoverCandidate{
		enabledRelayFailoverCandidate(1, "default", 300),
		enabledRelayFailoverCandidate(2, "default", 300),
		enabledRelayFailoverCandidate(3, "default", 100),
	}
	upstreams := map[int]relayFailoverUpstream{
		1: {statusCode: http.StatusInternalServerError},
		2: {statusCode: http.StatusInternalServerError},
	}

	result := runRelayFailover(t, relayFailoverOptions{
		candidates:       candidates,
		upstreams:        upstreams,
		initialChannelID: 2,
		retryTimes:       1,
		usingGroup:       "default",
		contextSetup: func(c *gin.Context) {
			c.Set("channel_affinity_skip_retry_on_failure", true)
		},
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Contains(t, result.body, "channel-3")
	assert.Equal(t, []int{2, 1, 3}, result.attempts)
	assert.Equal(t, []string{"2", "1", "3"}, result.usedChannels)
	assert.EqualValues(t, 2, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
}

func TestRelayStaysInResolvedAutoGroup(t *testing.T) {
	candidates := []relayFailoverCandidate{
		enabledRelayFailoverCandidate(1, "primary", 200),
		enabledRelayFailoverCandidate(2, "primary", 100),
		enabledRelayFailoverCandidate(3, "fallback", 300),
	}
	upstreams := map[int]relayFailoverUpstream{
		1: {statusCode: http.StatusInternalServerError},
		2: {statusCode: http.StatusInternalServerError},
	}

	result := runRelayFailover(t, relayFailoverOptions{
		candidates:       candidates,
		upstreams:        upstreams,
		initialChannelID: 1,
		retryTimes:       10,
		usingGroup:       "auto",
		autoGroups:       []string{"primary", "fallback"},
	})

	assert.Equal(t, http.StatusInternalServerError, result.statusCode)
	assert.Contains(t, result.body, "channel-2-failed")
	assert.Equal(t, []int{1, 2}, result.attempts)
	assert.Equal(t, []string{"1", "2"}, result.usedChannels)
	assert.EqualValues(t, 2, result.errorLogCount)
	assert.Zero(t, result.consumeLogCount)
}

func TestRelayNonRetryableErrorDoesNotFanOut(t *testing.T) {
	candidates := []relayFailoverCandidate{
		enabledRelayFailoverCandidate(1, "default", 200),
		enabledRelayFailoverCandidate(2, "default", 100),
	}

	result := runRelayFailover(t, relayFailoverOptions{
		candidates: candidates,
		upstreams: map[int]relayFailoverUpstream{
			1: {statusCode: http.StatusBadRequest},
		},
		initialChannelID: 1,
		retryTimes:       10,
		usingGroup:       "default",
	})

	assert.Equal(t, http.StatusBadRequest, result.statusCode)
	assert.Equal(t, []int{1}, result.attempts)
	assert.Equal(t, []string{"1"}, result.usedChannels)
	assert.EqualValues(t, 1, result.errorLogCount)
	assert.Zero(t, result.consumeLogCount)
}

func TestRelayRetriesPreCommitStreamFailure(t *testing.T) {
	candidates := []relayFailoverCandidate{
		enabledRelayFailoverCandidate(1, "default", 200),
		enabledRelayFailoverCandidate(2, "default", 100),
	}

	result := runRelayFailover(t, relayFailoverOptions{
		candidates: candidates,
		upstreams: map[int]relayFailoverUpstream{
			1: {statusCode: http.StatusInternalServerError},
			2: {stream: true},
		},
		initialChannelID: 1,
		retryTimes:       0,
		usingGroup:       "default",
		stream:           true,
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Contains(t, result.body, "channel-2")
	assert.Equal(t, []int{1, 2}, result.attempts)
	assert.Equal(t, []string{"1", "2"}, result.usedChannels)
	assert.EqualValues(t, 1, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
}

func TestRelayDoesNotRetryAfterStreamCommit(t *testing.T) {
	candidates := []relayFailoverCandidate{
		enabledRelayFailoverCandidate(1, "default", 200),
		enabledRelayFailoverCandidate(2, "default", 100),
	}

	result := runRelayFailover(t, relayFailoverOptions{
		candidates: candidates,
		upstreams: map[int]relayFailoverUpstream{
			1: {stream: true, abortAfterChunk: true},
		},
		initialChannelID: 1,
		retryTimes:       10,
		usingGroup:       "default",
		stream:           true,
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.NotEmpty(t, result.body)
	assert.Contains(t, result.body, "data:")
	assert.NotContains(t, result.body, "channel-2")
	assert.NotContains(t, result.body, `"error"`)
	assert.Equal(t, []int{1}, result.attempts)
	assert.Equal(t, []string{"1"}, result.usedChannels)
	assert.EqualValues(t, 1, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
}

func TestRelayAudioRecordsCommittedAbnormalStreamAsChannelFailure(t *testing.T) {
	candidates := []relayFailoverCandidate{
		enabledRelayFailoverCandidate(1, "default", 200),
		enabledRelayFailoverCandidate(2, "default", 100),
	}

	result := runRelayFailover(t, relayFailoverOptions{
		candidates: candidates,
		upstreams: map[int]relayFailoverUpstream{
			1: {stream: true, abortAfterChunk: true},
		},
		initialChannelID: 1,
		retryTimes:       10,
		usingGroup:       "default",
		relayFormat:      types.RelayFormatOpenAIAudio,
		requestPath:      "/v1/audio/speech",
		requestBody:      fmt.Sprintf(`{"model":"%s","input":"hello","voice":"alloy","stream_format":"sse"}`, relayFailoverModel),
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Equal(t, []int{1}, result.attempts)
	assert.Equal(t, []string{"1"}, result.usedChannels)
	assert.EqualValues(t, 1, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
}

func TestRelayCancellationStopsCandidateTraversal(t *testing.T) {
	requestContext, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	candidates := []relayFailoverCandidate{
		enabledRelayFailoverCandidate(1, "default", 300),
		enabledRelayFailoverCandidate(2, "default", 200),
		enabledRelayFailoverCandidate(3, "default", 100),
	}

	result := runRelayFailover(t, relayFailoverOptions{
		candidates: candidates,
		upstreams: map[int]relayFailoverUpstream{
			1: {statusCode: http.StatusInternalServerError, onAttempt: cancel},
			2: {statusCode: http.StatusInternalServerError},
			3: {statusCode: http.StatusInternalServerError},
		},
		initialChannelID: 1,
		retryTimes:       10,
		usingGroup:       "default",
		requestContext:   requestContext,
	})

	assert.Equal(t, []int{1}, result.attempts)
	assert.Equal(t, []string{"1"}, result.usedChannels)
	assert.LessOrEqual(t, result.errorLogCount, int64(1))
	assert.Zero(t, result.consumeLogCount)
}
