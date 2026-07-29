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
	autoBan        int
	abilityEnabled bool
	channelType    int
	supportedPath  string
	keys           []string
	disabledKeys   map[int]int
}

type relayFailoverUpstream struct {
	statusCode       int
	stream           bool
	abortBeforeChunk bool
	abortAfterChunk  bool
	abortAfterWrite  <-chan struct{}
	afterStreamWrite func()
	onAttempt        func()
}

type relayCommitSignalWriter struct {
	gin.ResponseWriter
	onWrite func()
}

func (w *relayCommitSignalWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if n > 0 && w.onWrite != nil {
		w.onWrite()
	}
	return n, err
}

func (w *relayCommitSignalWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

type relayFailoverOptions struct {
	candidates         []relayFailoverCandidate
	upstreams          map[int]relayFailoverUpstream
	initialChannelID   int
	retryTimes         int
	retryStatusRanges  []operation_setting.StatusCodeRange
	usingGroup         string
	autoGroups         []string
	stream             bool
	relayFormat        types.RelayFormat
	requestPath        string
	requestBody        string
	requestContext     context.Context
	groupRatio         *float64
	expectedFinalQuota *int
	contextSetup       func(*gin.Context)
	responseBodies     map[int]string
	requestID          string
	automaticDisable   bool
	disableRanges      []operation_setting.StatusCodeRange
	waitDisabledID     int
}

type relayFailoverResult struct {
	statusCode                 int
	body                       string
	headers                    http.Header
	attempts                   []int
	upstreamRequestPaths       []string
	upstreamRequestBodies      []string
	usedChannels               []string
	errorLogCount              int64
	consumeLogCount            int64
	consumeLogQuota            int
	consumeLogChannelID        int
	consumeLogPromptTokens     int
	consumeLogCompletionTokens int
	consumeLogOther            string
	userQuota                  int
	userUsedQuota              int
	tokenRemainQuota           int
	tokenUsedQuota             int
	userRequestCount           int
	errorLogContent            string
	appLog                     string
	logRequestIDs              []string
	logChannelIDs              []int
	channelStatuses            map[int]int
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
	previousRedisEnabled := common.RedisEnabled
	previousBatchUpdateEnabled := common.BatchUpdateEnabled
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
	previousCacheRatios := ratio_setting.CacheRatio2JSONString()
	previousAutoGroups := setting.AutoGroups2JsonString()
	previousUserUsableGroups := setting.UserUsableGroups2JSONString()
	previousRetryRanges := append([]operation_setting.StatusCodeRange(nil), operation_setting.AutomaticRetryStatusCodeRanges...)
	previousDisableRanges := append([]operation_setting.StatusCodeRange(nil), operation_setting.AutomaticDisableStatusCodeRanges...)
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
		&model.AccountPoolChannelBinding{},
	))

	model.DB = db
	model.LOG_DB = db
	common.MemoryCacheEnabled = true
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	model.InitCommonColumnsForTest()
	common.RetryTimes = opts.retryTimes
	constant.CountToken = false
	constant.StreamingTimeout = 30
	constant.ErrorLogEnabled = true
	common.LogConsumeEnabled = true
	common.AutomaticDisableChannelEnabled = opts.automaticDisable
	common.DataExportEnabled = false
	service.ResetAccountPoolRuntimeForTest()
	service.InitHttpClient()
	operation_setting.AutomaticRetryStatusCodeRanges = opts.retryStatusRanges
	if len(operation_setting.AutomaticRetryStatusCodeRanges) == 0 {
		operation_setting.AutomaticRetryStatusCodeRanges = []operation_setting.StatusCodeRange{{Start: 500, End: 599}}
	}
	if opts.disableRanges != nil {
		operation_setting.AutomaticDisableStatusCodeRanges = append(
			[]operation_setting.StatusCodeRange(nil),
			opts.disableRanges...,
		)
	}
	operation_setting.GetQuotaSetting().EnableFreeModelPreConsume = false
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-5.6-sol":2.5}`))
	require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(`{"gpt-5.6-sol":0.1}`))

	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.MemoryCacheEnabled = previousMemoryCache
		common.RedisEnabled = previousRedisEnabled
		common.BatchUpdateEnabled = previousBatchUpdateEnabled
		common.SetMainDatabaseType(previousMainDBType)
		common.SetLogDatabaseType(previousLogDBType)
		model.InitCommonColumnsForTest()
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
		require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(previousCacheRatios))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(previousAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousUserUsableGroups))
		operation_setting.AutomaticRetryStatusCodeRanges = previousRetryRanges
		operation_setting.AutomaticDisableStatusCodeRanges = previousDisableRanges
		operation_setting.GetQuotaSetting().EnableFreeModelPreConsume = previousFreeModelPreConsume
		model.InitChannelCache()
		require.NoError(t, sqlDB.Close())
	})

	groupRatios := ratio_setting.GetGroupRatioSetting().GroupRatio
	for _, candidate := range opts.candidates {
		groupRatio := 0.0
		if opts.groupRatio != nil {
			groupRatio = *opts.groupRatio
		}
		groupRatios.Set(candidate.group, groupRatio)
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
	require.NoError(t, db.Create(&model.Token{
		Id:             1,
		UserId:         1,
		Key:            "test-token",
		Status:         common.TokenStatusEnabled,
		Name:           "relay-failover-token",
		RemainQuota:    1_000_000,
		UnlimitedQuota: false,
		Group:          "default",
	}).Error)

	var attemptsMu sync.Mutex
	attempts := make([]int, 0, len(opts.candidates))
	upstreamRequestPaths := make([]string, 0, len(opts.candidates))
	upstreamRequestBodies := make([]string, 0, len(opts.candidates))
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

		requestBody, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			http.Error(w, "failed to read request", http.StatusBadRequest)
			return
		}

		attemptsMu.Lock()
		attempts = append(attempts, channelID)
		upstreamRequestPaths = append(upstreamRequestPaths, r.URL.Path)
		upstreamRequestBodies = append(upstreamRequestBodies, string(requestBody))
		attemptsMu.Unlock()

		upstream := opts.upstreams[channelID]
		if upstream.onAttempt != nil {
			upstream.onAttempt()
		}
		if upstream.statusCode != 0 && upstream.statusCode != http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(upstream.statusCode)
			if body := opts.responseBodies[channelID]; body != "" {
				_, _ = fmt.Fprint(w, body)
				return
			}
			_, _ = fmt.Fprintf(w, `{"error":{"message":"channel-%d-failed","type":"upstream_error","code":"channel_%d_failed"}}`, channelID, channelID)
			return
		}
		if upstream.stream {
			if body := opts.responseBodies[channelID]; body != "" && !upstream.abortBeforeChunk && !upstream.abortAfterChunk {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprint(w, body)
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
				return
			}
			streamChunk := fmt.Sprintf("data: {\"id\":\"chatcmpl-%d\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"%s\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"channel-%d\"},\"finish_reason\":null}]}\n\n", channelID, relayFailoverModel, channelID)
			if upstream.abortBeforeChunk {
				hijacker, ok := w.(http.Hijacker)
				if !ok {
					http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
					return
				}
				conn, buffer, hijackErr := hijacker.Hijack()
				if hijackErr != nil {
					return
				}
				_, _ = fmt.Fprint(buffer, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: 1024\r\n\r\n")
				_ = buffer.Flush()
				_ = conn.Close()
				return
			}
			if upstream.abortAfterChunk {
				// The OpenAI stream adapter buffers the latest upstream event so
				// it can inspect final usage. Two events guarantee that the first
				// one reached the downstream before the transport aborts.
				streamBody := streamChunk + streamChunk
				if body := opts.responseBodies[channelID]; body != "" {
					streamBody = body
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Content-Length", strconv.Itoa(len(streamBody)+1024))
				_, _ = fmt.Fprint(w, streamBody)
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
				if upstream.afterStreamWrite != nil {
					upstream.afterStreamWrite()
				}
				if upstream.abortAfterWrite != nil {
					<-upstream.abortAfterWrite
				}
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
		if body := opts.responseBodies[channelID]; body != "" {
			_, _ = fmt.Fprint(w, body)
			return
		}
		_, _ = fmt.Fprintf(w, `{"id":"chatcmpl-%d","object":"chat.completion","created":1,"model":"%s","choices":[{"index":0,"message":{"role":"assistant","content":"channel-%d"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`, channelID, relayFailoverModel, channelID)
	}))
	t.Cleanup(upstreamServer.Close)

	for _, candidate := range opts.candidates {
		baseURL := fmt.Sprintf("%s/channel/%d", upstreamServer.URL, candidate.id)
		keys := candidate.keys
		if len(keys) == 0 {
			keys = []string{fmt.Sprintf("sk-channel-%d", candidate.id)}
		}
		channel := &model.Channel{
			Id:       candidate.id,
			Type:     candidate.channelType,
			Key:      strings.Join(keys, "\n"),
			Status:   candidate.status,
			Name:     fmt.Sprintf("channel-%d", candidate.id),
			BaseURL:  &baseURL,
			Group:    candidate.group,
			Models:   candidate.model,
			Priority: &candidate.priority,
			Weight:   common.GetPointer(uint(100)),
			AutoBan:  common.GetPointer(candidate.autoBan),
		}
		if len(keys) > 1 || len(candidate.disabledKeys) > 0 {
			channel.ChannelInfo = model.ChannelInfo{
				IsMultiKey:         true,
				MultiKeySize:       len(keys),
				MultiKeyStatusList: candidate.disabledKeys,
				MultiKeyMode:       constant.MultiKeyModePolling,
			}
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
	requestID := opts.requestID
	if requestID == "" {
		requestID = "relay-failover-request"
	}
	c.Set(common.RequestIdKey, requestID)
	c.Set("token_name", "relay-failover-token")
	c.Set("username", "relay-failover-user")
	if len(opts.autoGroups) > 0 {
		common.SetContextKey(c, constant.ContextKeyAutoGroup, opts.candidates[0].group)
		common.SetContextKey(c, constant.ContextKeyAutoGroupIndex, 0)
	}
	if opts.contextSetup != nil {
		opts.contextSetup(c)
	}
	middleware.PreserveInitialChannelSetup(c, initialChannel, relayFailoverModel)

	relayFormat := opts.relayFormat
	if relayFormat == "" {
		relayFormat = types.RelayFormatOpenAI
	}
	var appLog bytes.Buffer
	common.LogWriterMu.Lock()
	oldErrorWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &appLog
	common.LogWriterMu.Unlock()
	Relay(c, relayFormat)
	common.LogWriterMu.Lock()
	gin.DefaultErrorWriter = oldErrorWriter
	common.LogWriterMu.Unlock()

	if opts.waitDisabledID > 0 {
		require.Eventually(t, func() bool {
			var channel model.Channel
			if err := db.Select("id", "status").First(&channel, opts.waitDisabledID).Error; err != nil {
				return false
			}
			return channel.Status == common.ChannelStatusAutoDisabled
		}, time.Second, 10*time.Millisecond)
	}
	if opts.expectedFinalQuota != nil {
		require.Eventually(t, func() bool {
			var user model.User
			if err := db.Select("id", "quota", "used_quota").First(&user, 1).Error; err != nil {
				return false
			}
			var token model.Token
			if err := db.Select("id", "remain_quota", "used_quota").First(&token, 1).Error; err != nil {
				return false
			}
			return user.Quota == *opts.expectedFinalQuota && token.RemainQuota == *opts.expectedFinalQuota
		}, time.Second, 10*time.Millisecond)
	}

	var errorLogCount int64
	require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeError).Count(&errorLogCount).Error)
	var errorLog model.Log
	if errorLogCount > 0 {
		require.NoError(t, db.Where("type = ?", model.LogTypeError).Order("id desc").First(&errorLog).Error)
	}
	var consumeLogCount int64
	require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumeLogCount).Error)
	var consumeLog model.Log
	if consumeLogCount > 0 {
		require.NoError(t, db.Where("type = ?", model.LogTypeConsume).Order("id desc").First(&consumeLog).Error)
	}
	var user model.User
	require.NoError(t, db.First(&user, 1).Error)
	var token model.Token
	require.NoError(t, db.First(&token, 1).Error)
	var logs []model.Log
	require.NoError(t, db.Order("id").Find(&logs).Error)
	logRequestIDs := make([]string, 0, len(logs))
	logChannelIDs := make([]int, 0, len(logs))
	for _, logEntry := range logs {
		logRequestIDs = append(logRequestIDs, logEntry.RequestId)
		logChannelIDs = append(logChannelIDs, logEntry.ChannelId)
	}
	channelStatuses := make(map[int]int, len(opts.candidates))
	for _, candidate := range opts.candidates {
		var channel model.Channel
		require.NoError(t, db.Select("id", "status").First(&channel, candidate.id).Error)
		channelStatuses[channel.Id] = channel.Status
	}

	attemptsMu.Lock()
	finalAttempts := append([]int(nil), attempts...)
	attemptsMu.Unlock()
	return relayFailoverResult{
		statusCode:                 recorder.Code,
		body:                       recorder.Body.String(),
		headers:                    recorder.Result().Header.Clone(),
		attempts:                   finalAttempts,
		upstreamRequestPaths:       append([]string(nil), upstreamRequestPaths...),
		upstreamRequestBodies:      append([]string(nil), upstreamRequestBodies...),
		usedChannels:               append([]string(nil), c.GetStringSlice("use_channel")...),
		errorLogCount:              errorLogCount,
		consumeLogCount:            consumeLogCount,
		consumeLogQuota:            consumeLog.Quota,
		consumeLogChannelID:        consumeLog.ChannelId,
		consumeLogPromptTokens:     consumeLog.PromptTokens,
		consumeLogCompletionTokens: consumeLog.CompletionTokens,
		consumeLogOther:            consumeLog.Other,
		userQuota:                  user.Quota,
		userUsedQuota:              user.UsedQuota,
		tokenRemainQuota:           token.RemainQuota,
		tokenUsedQuota:             token.UsedQuota,
		userRequestCount:           user.RequestCount,
		errorLogContent:            errorLog.Content,
		appLog:                     appLog.String(),
		logRequestIDs:              logRequestIDs,
		logChannelIDs:              logChannelIDs,
		channelStatuses:            channelStatuses,
	}
}

func responsesFailoverRequestBody(stream bool) string {
	return fmt.Sprintf(`{"model":"%s","input":"hello","stream":%t}`, relayFailoverModel, stream)
}

func successfulResponsesStreamBody(channelID int) string {
	return fmt.Sprintf(
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-%d\",\"status\":\"in_progress\",\"model\":\"%s\"}}\n\n"+
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-%d\",\"status\":\"completed\",\"model\":\"%s\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"channel-%d\"}]}],\"usage\":{\"input_tokens\":2,\"output_tokens\":3,\"total_tokens\":5}}}\n\n",
		channelID,
		relayFailoverModel,
		channelID,
		relayFailoverModel,
		channelID,
	)
}

func successfulResponsesBody(channelID int) string {
	return fmt.Sprintf(
		`{"id":"resp-%d","status":"completed","model":"%s","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"channel-%d"}]}],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`,
		channelID,
		relayFailoverModel,
		channelID,
	)
}

func remoteCompactionV2RequestBody() string {
	return `{
		"model":"gpt-5.6-sol",
		"instructions":"You are Codex.",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Retained conversation text"}]},
			{"type":"compaction_trigger"}
		],
		"tools":[],
		"tool_choice":"auto",
		"parallel_tool_calls":true,
		"reasoning":{"effort":"high","summary":"auto"},
		"store":false,
		"stream":true,
		"include":["reasoning.encrypted_content"],
		"prompt_cache_key":"thread-test",
		"client_metadata":{
			"x-codex-installation-id":"install-test",
			"session_id":"session-test",
			"thread_id":"thread-test",
			"turn_id":"turn-test",
			"x-codex-window-id":"thread-test:0",
			"x-codex-turn-metadata":"{\"installation_id\":\"install-test\",\"session_id\":\"session-test\",\"thread_id\":\"thread-test\",\"turn_id\":\"turn-test\",\"window_id\":\"thread-test:0\",\"request_kind\":\"compaction\",\"compaction\":{\"trigger\":\"manual\",\"reason\":\"user_requested\",\"implementation\":\"responses_compaction_v2\",\"phase\":\"standalone_turn\",\"strategy\":\"memento\"}}"
		}
	}`
}

func successfulRemoteCompactionV2StreamBody(channelID int) string {
	return fmt.Sprintf(
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-compact-%d\",\"status\":\"in_progress\",\"model\":\"%s\"}}\n\n"+
			"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":\"cmp-%d\",\"type\":\"compaction\",\"encrypted_content\":\"ENCRYPTED_CONTEXT_COMPACTION_SUMMARY_%d\"}}\n\n"+
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-compact-%d\",\"status\":\"completed\",\"model\":\"%s\",\"usage\":{\"input_tokens\":1200,\"input_tokens_details\":{\"cached_tokens\":100},\"output_tokens\":20,\"total_tokens\":1220}}}\n\n",
		channelID,
		relayFailoverModel,
		channelID,
		channelID,
		channelID,
		relayFailoverModel,
	)
}

func incompatibleRemoteCompactionV2StreamBody(channelID int, text string) string {
	return fmt.Sprintf(
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-incompatible-%d\",\"status\":\"in_progress\",\"model\":\"%s\"}}\n\n"+
			"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":\"msg-%d\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}}\n\n"+
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-incompatible-%d\",\"status\":\"completed\",\"model\":\"%s\",\"output\":[{\"id\":\"msg-%d\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}],\"usage\":{\"input_tokens\":1200,\"output_tokens\":20,\"total_tokens\":1220}}}\n\n",
		channelID,
		relayFailoverModel,
		channelID,
		text,
		channelID,
		relayFailoverModel,
		channelID,
		text,
	)
}

func responsesEventStream(events ...string) string {
	var body strings.Builder
	for _, event := range events {
		body.WriteString("data: ")
		body.WriteString(event)
		body.WriteString("\n\n")
	}
	return body.String()
}

func assertRemoteCompactionV2OutboundRequest(t *testing.T, result relayFailoverResult, attempt int, channelID int) {
	t.Helper()
	require.Greater(t, len(result.upstreamRequestPaths), attempt)
	require.Greater(t, len(result.upstreamRequestBodies), attempt)
	assert.Equal(t, fmt.Sprintf("/channel/%d/v1/responses", channelID), result.upstreamRequestPaths[attempt])

	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.UnmarshalJsonStr(result.upstreamRequestBodies[attempt], &request))
	var input []map[string]any
	require.NoError(t, common.Unmarshal(request.Input, &input))
	require.NotEmpty(t, input)
	assert.Equal(t, map[string]any{"type": "compaction_trigger"}, input[len(input)-1])
	require.NotNil(t, request.Stream)
	assert.True(t, *request.Stream)

	var clientMetadata map[string]string
	require.NoError(t, common.Unmarshal(request.ClientMetadata, &clientMetadata))
	var turnMetadata map[string]any
	require.NoError(t, common.UnmarshalJsonStr(clientMetadata["x-codex-turn-metadata"], &turnMetadata))
	assert.Equal(t, "compaction", turnMetadata["request_kind"])
	compactionMetadata, ok := turnMetadata["compaction"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "responses_compaction_v2", compactionMetadata["implementation"])
}

func TestRelayRemoteCompactionV2AcceptsValidCompactionOutput(t *testing.T) {
	result := runRelayFailover(t, relayFailoverOptions{
		candidates: []relayFailoverCandidate{
			enabledRelayFailoverCandidate(1, "default", 100),
		},
		upstreams: map[int]relayFailoverUpstream{
			1: {stream: true},
		},
		initialChannelID: 1,
		usingGroup:       "default",
		stream:           true,
		relayFormat:      types.RelayFormatOpenAIResponses,
		requestPath:      "/v1/responses",
		requestBody:      remoteCompactionV2RequestBody(),
		responseBodies: map[int]string{
			1: successfulRemoteCompactionV2StreamBody(1),
		},
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Equal(t, []int{1}, result.attempts)
	assertRemoteCompactionV2OutboundRequest(t, result, 0, 1)
	assert.Contains(t, result.body, `"type":"compaction"`)
	assert.Contains(t, result.body, `"encrypted_content":"ENCRYPTED_CONTEXT_COMPACTION_SUMMARY_1"`)
	assert.Contains(t, result.body, `"type":"response.completed"`)
	assert.EqualValues(t, 0, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
}

func TestRelayRemoteCompactionV2RetriesIncompatibleMessageWithoutLeak(t *testing.T) {
	groupRatio := 1.0
	expectedFinalQuota := 1_000_000 - 3175
	result := runRelayFailover(t, relayFailoverOptions{
		candidates: []relayFailoverCandidate{
			enabledRelayFailoverCandidate(1, "default", 200),
			enabledRelayFailoverCandidate(2, "default", 100),
		},
		upstreams: map[int]relayFailoverUpstream{
			1: {stream: true},
			2: {stream: true},
		},
		initialChannelID:   1,
		usingGroup:         "default",
		stream:             true,
		relayFormat:        types.RelayFormatOpenAIResponses,
		requestPath:        "/v1/responses",
		requestBody:        remoteCompactionV2RequestBody(),
		groupRatio:         &groupRatio,
		expectedFinalQuota: &expectedFinalQuota,
		responseBodies: map[int]string{
			1: incompatibleRemoteCompactionV2StreamBody(1, "IGNORED_COMPACT_REPLY"),
			2: successfulRemoteCompactionV2StreamBody(2),
		},
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Equal(t, []int{1, 2}, result.attempts)
	assert.Equal(t, []string{"1", "2"}, result.usedChannels)
	assertRemoteCompactionV2OutboundRequest(t, result, 0, 1)
	assertRemoteCompactionV2OutboundRequest(t, result, 1, 2)
	assert.NotContains(t, result.body, "IGNORED_COMPACT_REPLY")
	assert.NotContains(t, result.body, "resp-incompatible-1")
	assert.Contains(t, result.body, `"encrypted_content":"ENCRYPTED_CONTEXT_COMPACTION_SUMMARY_2"`)
	assert.EqualValues(t, 1, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
	assert.Equal(t, 2, result.consumeLogChannelID)
	assert.Equal(t, 1200, result.consumeLogPromptTokens)
	assert.Equal(t, 20, result.consumeLogCompletionTokens)
	assert.Equal(t, 3175, result.consumeLogQuota)
	assert.Equal(t, expectedFinalQuota, result.userQuota)
	assert.Equal(t, result.consumeLogQuota, result.userUsedQuota)
	assert.Equal(t, 1, result.userRequestCount)
	assert.Equal(t, expectedFinalQuota, result.tokenRemainQuota)
	assert.Equal(t, result.consumeLogQuota, result.tokenUsedQuota)
}

func TestRelayRemoteCompactionV2ExhaustionReturnsSanitizedError(t *testing.T) {
	const secret = "sk-remote-compaction-secret"
	groupRatio := 1.0
	expectedFinalQuota := 1_000_000
	result := runRelayFailover(t, relayFailoverOptions{
		candidates: []relayFailoverCandidate{
			enabledRelayFailoverCandidate(1, "default", 200),
			enabledRelayFailoverCandidate(2, "default", 100),
		},
		upstreams: map[int]relayFailoverUpstream{
			1: {stream: true},
			2: {stream: true},
		},
		initialChannelID:   1,
		usingGroup:         "default",
		stream:             true,
		relayFormat:        types.RelayFormatOpenAIResponses,
		requestPath:        "/v1/responses",
		requestBody:        remoteCompactionV2RequestBody(),
		groupRatio:         &groupRatio,
		expectedFinalQuota: &expectedFinalQuota,
		responseBodies: map[int]string{
			1: incompatibleRemoteCompactionV2StreamBody(1, "Authorization: Bearer "+secret),
			2: incompatibleRemoteCompactionV2StreamBody(2, "still not a compaction"),
		},
		requestID: "req-remote-compaction-exhausted",
	})

	assert.Equal(t, http.StatusBadGateway, result.statusCode)
	assert.Equal(t, []int{1, 2}, result.attempts)
	assert.Equal(t, []string{"1", "2"}, result.usedChannels)
	assert.Contains(t, result.body, "expected exactly one compaction output item")
	assert.Equal(t, 1, strings.Count(result.body, `"error":`))
	assert.NotContains(t, result.body, secret)
	assert.NotContains(t, result.body, "still not a compaction")
	assert.EqualValues(t, 2, result.errorLogCount)
	assert.EqualValues(t, 0, result.consumeLogCount)
	assert.Equal(t, []int{1, 2}, result.logChannelIDs)
	assert.Equal(t, expectedFinalQuota, result.userQuota)
	assert.Zero(t, result.userUsedQuota)
	assert.Zero(t, result.userRequestCount)
	assert.Equal(t, expectedFinalQuota, result.tokenRemainQuota)
	assert.Zero(t, result.tokenUsedQuota)
}

func TestRelayResponsesMetadataPreludeTransportFailureRetriesSameGroup(t *testing.T) {
	metadataPrelude := responsesEventStream(
		`{"type":"response.created","response":{"id":"resp-metadata-only","status":"in_progress","model":"gpt-5.6-sol"}}`,
		`{"type":"response.in_progress","response":{"id":"resp-metadata-only","status":"in_progress","model":"gpt-5.6-sol"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg-metadata-only","role":"assistant","content":[]}}`,
	)
	result := runRelayFailover(t, relayFailoverOptions{
		candidates: []relayFailoverCandidate{
			enabledRelayFailoverCandidate(1, "default", 200),
			enabledRelayFailoverCandidate(2, "default", 100),
		},
		upstreams: map[int]relayFailoverUpstream{
			1: {stream: true, abortAfterChunk: true},
			2: {stream: true},
		},
		initialChannelID: 1,
		usingGroup:       "default",
		stream:           true,
		relayFormat:      types.RelayFormatOpenAIResponses,
		requestPath:      "/v1/responses",
		requestBody:      responsesFailoverRequestBody(true),
		responseBodies: map[int]string{
			1: metadataPrelude,
			2: successfulResponsesStreamBody(2),
		},
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Equal(t, []int{1, 2}, result.attempts)
	assert.Equal(t, []string{"1", "2"}, result.usedChannels)
	assert.NotContains(t, result.body, "resp-metadata-only")
	assert.NotContains(t, result.body, "msg-metadata-only")
	assert.Contains(t, result.body, "channel-2")
	assert.EqualValues(t, 1, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
}

func TestRelayResponsesMetadataPreludeCleanEOFRetriesSameGroup(t *testing.T) {
	metadataPrelude := responsesEventStream(
		`{"type":"response.created","response":{"id":"resp-clean-eof","status":"in_progress","model":"gpt-5.6-sol"}}`,
		`{"type":"response.in_progress","response":{"id":"resp-clean-eof","status":"in_progress","model":"gpt-5.6-sol"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg-clean-eof","role":"assistant","content":[]}}`,
	)
	result := runRelayFailover(t, relayFailoverOptions{
		candidates: []relayFailoverCandidate{
			enabledRelayFailoverCandidate(1, "default", 200),
			enabledRelayFailoverCandidate(2, "default", 100),
		},
		upstreams: map[int]relayFailoverUpstream{
			1: {stream: true},
			2: {stream: true},
		},
		initialChannelID: 1,
		usingGroup:       "default",
		stream:           true,
		relayFormat:      types.RelayFormatOpenAIResponses,
		requestPath:      "/v1/responses",
		requestBody:      responsesFailoverRequestBody(true),
		responseBodies: map[int]string{
			1: metadataPrelude,
			2: successfulResponsesStreamBody(2),
		},
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Equal(t, []int{1, 2}, result.attempts)
	assert.Equal(t, []string{"1", "2"}, result.usedChannels)
	assert.NotContains(t, result.body, "resp-clean-eof")
	assert.NotContains(t, result.body, "msg-clean-eof")
	assert.Contains(t, result.body, "channel-2")
	assert.EqualValues(t, 1, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
}

func TestRelayResponsesClientGoneAfterMetadataDoesNotRetry(t *testing.T) {
	requestContext, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	metadataPrelude := responsesEventStream(
		`{"type":"response.created","response":{"id":"resp-client-gone","status":"in_progress","model":"gpt-5.6-sol"}}`,
		`{"type":"response.in_progress","response":{"id":"resp-client-gone","status":"in_progress","model":"gpt-5.6-sol"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg-client-gone","role":"assistant","content":[]}}`,
	)
	result := runRelayFailover(t, relayFailoverOptions{
		candidates: []relayFailoverCandidate{
			enabledRelayFailoverCandidate(1, "default", 200),
			enabledRelayFailoverCandidate(2, "default", 100),
		},
		upstreams: map[int]relayFailoverUpstream{
			1: {stream: true, abortAfterChunk: true, afterStreamWrite: cancel},
			2: {stream: true},
		},
		initialChannelID: 1,
		usingGroup:       "default",
		stream:           true,
		relayFormat:      types.RelayFormatOpenAIResponses,
		requestPath:      "/v1/responses",
		requestBody:      responsesFailoverRequestBody(true),
		requestContext:   requestContext,
		responseBodies: map[int]string{
			1: metadataPrelude,
			2: successfulResponsesStreamBody(2),
		},
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Equal(t, []int{1}, result.attempts)
	assert.Equal(t, []string{"1"}, result.usedChannels)
	assert.Empty(t, result.body)
	assert.NotContains(t, result.body, "resp-client-gone")
	assert.NotContains(t, result.body, "channel-2")
	assert.EqualValues(t, 0, result.errorLogCount)
	require.EqualValues(t, 1, result.consumeLogCount)

	var other map[string]any
	require.NoError(t, common.UnmarshalJsonStr(result.consumeLogOther, &other))
	streamStatus, ok := other["stream_status"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "error", streamStatus["status"])
	assert.Equal(t, "client_gone", streamStatus["end_reason"])
	assert.Equal(t, "context canceled", streamStatus["end_error"])
}

func TestRelayResponsesExhaustsSameGroupPastChannelNoRetryStatus(t *testing.T) {
	candidates := []relayFailoverCandidate{
		enabledRelayFailoverCandidate(180, "OpenAI", 400),
		enabledRelayFailoverCandidate(150, "OpenAI", 300),
		enabledRelayFailoverCandidate(199, "OpenAI", 200),
		enabledRelayFailoverCandidate(198, "OpenAI", 100),
	}
	candidates[2].autoBan = 1

	result := runRelayFailover(t, relayFailoverOptions{
		candidates: candidates,
		upstreams: map[int]relayFailoverUpstream{
			180: {statusCode: http.StatusTooManyRequests},
			150: {statusCode: http.StatusBadGateway},
			199: {statusCode: 524},
			198: {stream: true},
		},
		initialChannelID: 180,
		retryTimes:       0,
		retryStatusRanges: []operation_setting.StatusCodeRange{
			{Start: http.StatusTooManyRequests, End: http.StatusTooManyRequests},
			{Start: http.StatusInternalServerError, End: 599},
		},
		usingGroup:  "OpenAI",
		stream:      true,
		relayFormat: types.RelayFormatOpenAIResponses,
		requestPath: "/v1/responses",
		requestBody: responsesFailoverRequestBody(true),
		responseBodies: map[int]string{
			198: successfulResponsesStreamBody(198),
		},
		requestID:        "req-responses-524-failover",
		automaticDisable: true,
		disableRanges: []operation_setting.StatusCodeRange{
			{Start: 524, End: 524},
		},
		waitDisabledID: 199,
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Equal(t, []int{180, 150, 199, 198}, result.attempts)
	assert.Equal(t, []string{"180", "150", "199", "198"}, result.usedChannels)
	assert.Contains(t, result.body, "channel-198")
	assert.NotContains(t, result.body, "channel-180-failed")
	assert.NotContains(t, result.body, "channel-150-failed")
	assert.NotContains(t, result.body, "channel-199-failed")
	assert.NotContains(t, result.body, `"error"`)
	assert.EqualValues(t, 3, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
	assert.Equal(t, []int{180, 150, 199, 198}, result.logChannelIDs)
	assert.Equal(t, []string{
		"req-responses-524-failover",
		"req-responses-524-failover",
		"req-responses-524-failover",
		"req-responses-524-failover",
	}, result.logRequestIDs)
	assert.Equal(t, common.ChannelStatusAutoDisabled, result.channelStatuses[199])
}

func TestRelayResponsesSemanticFailureRetriesSameGroup(t *testing.T) {
	candidates := []relayFailoverCandidate{
		enabledRelayFailoverCandidate(1, "default", 200),
		enabledRelayFailoverCandidate(2, "default", 100),
	}
	failed := "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"rate_limit_exceeded\",\"type\":\"rate_limit_exceeded\",\"message\":\"Concurrency limit exceeded for account, please retry later\"}}}\n\n"

	result := runRelayFailover(t, relayFailoverOptions{
		candidates: candidates,
		upstreams: map[int]relayFailoverUpstream{
			1: {stream: true},
			2: {stream: true},
		},
		initialChannelID:  1,
		retryTimes:        0,
		retryStatusRanges: []operation_setting.StatusCodeRange{{Start: http.StatusTooManyRequests, End: http.StatusTooManyRequests}},
		usingGroup:        "default",
		stream:            true,
		relayFormat:       types.RelayFormatOpenAIResponses,
		requestPath:       "/v1/responses",
		requestBody:       responsesFailoverRequestBody(true),
		responseBodies: map[int]string{
			1: failed,
			2: successfulResponsesStreamBody(2),
		},
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Equal(t, []int{1, 2}, result.attempts)
	assert.Equal(t, []string{"1", "2"}, result.usedChannels)
	assert.Contains(t, result.body, "channel-2")
	assert.NotContains(t, result.body, "response.failed")
	assert.NotContains(t, result.body, "Concurrency limit exceeded")
	assert.NotContains(t, result.body, `"error"`)
	assert.EqualValues(t, 1, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
	assert.Equal(t, 1, result.userRequestCount)
}

func TestRelayResponsesSemanticFailureAfterMeaningfulOutputDoesNotRetry(t *testing.T) {
	service.InitTokenEncoders()
	candidates := []relayFailoverCandidate{
		enabledRelayFailoverCandidate(1, "default", 200),
		enabledRelayFailoverCandidate(2, "default", 100),
	}
	failed := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-1\",\"status\":\"in_progress\",\"model\":\"gpt-5.6-sol\"}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial-a\"}\n\n" +
		"data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"type\":\"server_error\",\"message\":\"upstream failed after output\"}}}\n\n"

	result := runRelayFailover(t, relayFailoverOptions{
		candidates: candidates,
		upstreams: map[int]relayFailoverUpstream{
			1: {stream: true},
			2: {stream: true},
		},
		initialChannelID: 1,
		retryTimes:       10,
		usingGroup:       "default",
		stream:           true,
		relayFormat:      types.RelayFormatOpenAIResponses,
		requestPath:      "/v1/responses",
		requestBody:      responsesFailoverRequestBody(true),
		responseBodies: map[int]string{
			1: failed,
			2: successfulResponsesStreamBody(2),
		},
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Equal(t, []int{1}, result.attempts)
	assert.Equal(t, []string{"1"}, result.usedChannels)
	assert.Contains(t, result.body, "partial-a")
	assert.Contains(t, result.body, "response.failed")
	assert.NotContains(t, result.body, "channel-2")
	assert.Equal(t, 1, strings.Count(result.body, "event: response.failed"))
	assert.EqualValues(t, 1, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
	assert.Equal(t, 1, result.userRequestCount)
}

func TestRelayResponsesEmptyTerminalRetriesSameGroup(t *testing.T) {
	tests := []struct {
		name     string
		terminal string
	}{
		{
			name:     "completed",
			terminal: "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-1\",\"status\":\"completed\",\"model\":\"gpt-5.6-sol\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0,\"total_tokens\":1}}}\n\n",
		},
		{
			name:     "incomplete",
			terminal: "data: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"resp-1\",\"status\":\"incomplete\",\"model\":\"gpt-5.6-sol\",\"output\":[],\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := runRelayFailover(t, relayFailoverOptions{
				candidates: []relayFailoverCandidate{
					enabledRelayFailoverCandidate(1, "default", 200),
					enabledRelayFailoverCandidate(2, "default", 100),
				},
				upstreams: map[int]relayFailoverUpstream{
					1: {stream: true},
					2: {stream: true},
				},
				initialChannelID: 1,
				retryTimes:       0,
				usingGroup:       "default",
				stream:           true,
				relayFormat:      types.RelayFormatOpenAIResponses,
				requestPath:      "/v1/responses",
				requestBody:      responsesFailoverRequestBody(true),
				responseBodies: map[int]string{
					1: "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-1\",\"status\":\"in_progress\",\"model\":\"gpt-5.6-sol\"}}\n\n" + test.terminal,
					2: successfulResponsesStreamBody(2),
				},
			})

			assert.Equal(t, http.StatusOK, result.statusCode)
			assert.Equal(t, []int{1, 2}, result.attempts)
			assert.Contains(t, result.body, "channel-2")
			assert.NotContains(t, result.body, "resp-1")
			assert.EqualValues(t, 1, result.errorLogCount)
			assert.EqualValues(t, 1, result.consumeLogCount)
		})
	}
}

func TestRelayResponsesIncompleteAfterMeaningfulOutputDoesNotRetry(t *testing.T) {
	service.InitTokenEncoders()
	incomplete := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-1\",\"status\":\"in_progress\",\"model\":\"gpt-5.6-sol\"}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial-a\"}\n\n" +
		"data: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"resp-1\",\"status\":\"incomplete\",\"model\":\"gpt-5.6-sol\",\"output\":[],\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n"

	result := runRelayFailover(t, relayFailoverOptions{
		candidates: []relayFailoverCandidate{
			enabledRelayFailoverCandidate(1, "default", 200),
			enabledRelayFailoverCandidate(2, "default", 100),
		},
		upstreams: map[int]relayFailoverUpstream{
			1: {stream: true},
			2: {stream: true},
		},
		initialChannelID: 1,
		retryTimes:       10,
		usingGroup:       "default",
		stream:           true,
		relayFormat:      types.RelayFormatOpenAIResponses,
		requestPath:      "/v1/responses",
		requestBody:      responsesFailoverRequestBody(true),
		responseBodies: map[int]string{
			1: incomplete,
			2: successfulResponsesStreamBody(2),
		},
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Equal(t, []int{1}, result.attempts)
	assert.Contains(t, result.body, "partial-a")
	assert.Contains(t, result.body, "response.incomplete")
	assert.NotContains(t, result.body, "channel-2")
	assert.EqualValues(t, 1, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
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
		1: {statusCode: http.StatusBadRequest},
		2: {statusCode: http.StatusGatewayTimeout},
		3: {statusCode: 524},
	}

	result := runRelayFailover(t, relayFailoverOptions{
		candidates:       candidates,
		upstreams:        upstreams,
		initialChannelID: 1,
		retryTimes:       1,
		usingGroup:       "default",
	})

	assert.Equal(t, 524, result.statusCode)
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

func TestRelayChannelNoRetryStatusStillExhaustsSameGroup(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{name: "bad request", statusCode: http.StatusBadRequest},
		{name: "gateway timeout", statusCode: http.StatusGatewayTimeout},
		{name: "outside configured retry ranges", statusCode: http.StatusTeapot},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := runRelayFailover(t, relayFailoverOptions{
				candidates: []relayFailoverCandidate{
					enabledRelayFailoverCandidate(1, "default", 200),
					enabledRelayFailoverCandidate(2, "default", 100),
				},
				upstreams: map[int]relayFailoverUpstream{
					1: {statusCode: test.statusCode},
				},
				initialChannelID: 1,
				retryTimes:       10,
				retryStatusRanges: []operation_setting.StatusCodeRange{
					{Start: http.StatusInternalServerError, End: http.StatusServiceUnavailable},
				},
				usingGroup:  "default",
				relayFormat: types.RelayFormatOpenAIResponses,
				requestPath: "/v1/responses",
				requestBody: responsesFailoverRequestBody(false),
				responseBodies: map[int]string{
					2: successfulResponsesBody(2),
				},
			})

			assert.Equal(t, http.StatusOK, result.statusCode)
			assert.Equal(t, []int{1, 2}, result.attempts)
			assert.Equal(t, []string{"1", "2"}, result.usedChannels)
			assert.Contains(t, result.body, "channel-2")
			assert.NotContains(t, result.body, "channel-1-failed")
			assert.NotContains(t, result.body, `"error"`)
			assert.EqualValues(t, 1, result.errorLogCount)
			assert.EqualValues(t, 1, result.consumeLogCount)
		})
	}
}

func TestRelaySingleChannelNoRetryStatusReturnsFinalError(t *testing.T) {
	result := runRelayFailover(t, relayFailoverOptions{
		candidates:       []relayFailoverCandidate{enabledRelayFailoverCandidate(1, "default", 100)},
		upstreams:        map[int]relayFailoverUpstream{1: {statusCode: 524}},
		initialChannelID: 1,
		retryTimes:       10,
		usingGroup:       "default",
	})

	assert.Equal(t, 524, result.statusCode)
	assert.Contains(t, result.body, "channel-1-failed")
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

func TestRelayOrdinaryDistributorNoKeyContinuesWithoutUpstreamRequest(t *testing.T) {
	first := enabledRelayFailoverCandidate(1, "default", 200)
	first.keys = []string{"credential-first", "credential-second"}
	first.disabledKeys = map[int]int{
		0: common.ChannelStatusAutoDisabled,
		1: common.ChannelStatusManuallyDisabled,
	}

	result := runRelayFailover(t, relayFailoverOptions{
		candidates: []relayFailoverCandidate{
			first,
			enabledRelayFailoverCandidate(2, "default", 100),
		},
		upstreams:        map[int]relayFailoverUpstream{},
		initialChannelID: 1,
		retryTimes:       0,
		usingGroup:       "default",
	})

	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Contains(t, result.body, "channel-2")
	assert.NotContains(t, result.body, "credential-first")
	assert.NotContains(t, result.body, "credential-second")
	assert.Equal(t, []int{2}, result.attempts)
	assert.Equal(t, []string{"1", "2"}, result.usedChannels)
	assert.EqualValues(t, 1, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
	assert.Equal(t, 1, result.userRequestCount)
}

func TestRelayFailedStreamAttemptThenJSONStartsClean(t *testing.T) {
	candidates := []relayFailoverCandidate{
		enabledRelayFailoverCandidate(1, "default", 200),
		enabledRelayFailoverCandidate(2, "default", 100),
	}

	result := runRelayFailover(t, relayFailoverOptions{
		candidates: candidates,
		upstreams: map[int]relayFailoverUpstream{
			1: {stream: true, abortBeforeChunk: true},
			2: {},
		},
		initialChannelID: 1,
		retryTimes:       0,
		usingGroup:       "default",
		stream:           false,
	})

	expected := fmt.Sprintf(
		`{"id":"chatcmpl-2","object":"chat.completion","created":1,"model":"%s","choices":[{"index":0,"message":{"role":"assistant","content":"channel-2"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`,
		relayFailoverModel,
	)
	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Equal(t, expected, result.body)
	assert.Equal(t, "application/json", result.headers.Get("Content-Type"))
	assert.Empty(t, result.headers.Get("Cache-Control"))
	assert.Empty(t, result.headers.Get("Connection"))
	assert.Empty(t, result.headers.Get("Transfer-Encoding"))
	assert.Empty(t, result.headers.Get("X-Accel-Buffering"))
	assert.Equal(t, []int{1, 2}, result.attempts)
	assert.Equal(t, []string{"1", "2"}, result.usedChannels)
	assert.EqualValues(t, 1, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
	assert.Equal(t, 1, result.userRequestCount)
}

func TestRelayFailedStreamAttemptThenStreamStartsClean(t *testing.T) {
	candidates := []relayFailoverCandidate{
		enabledRelayFailoverCandidate(1, "default", 200),
		enabledRelayFailoverCandidate(2, "default", 100),
	}

	result := runRelayFailover(t, relayFailoverOptions{
		candidates: candidates,
		upstreams: map[int]relayFailoverUpstream{
			1: {stream: true, abortBeforeChunk: true},
			2: {stream: true},
		},
		initialChannelID: 1,
		retryTimes:       0,
		usingGroup:       "default",
		stream:           true,
	})

	expectedChunk := fmt.Sprintf(
		`data: {"id":"chatcmpl-2","object":"chat.completion.chunk","created":1,"model":"%s","choices":[{"index":0,"delta":{"role":"assistant","content":"channel-2"},"finish_reason":null}]}`,
		relayFailoverModel,
	)
	assert.Equal(t, http.StatusOK, result.statusCode)
	assert.Contains(t, result.body, expectedChunk)
	assert.Equal(t, 1, strings.Count(result.body, expectedChunk))
	assert.Equal(t, 1, strings.Count(result.body, "data: [DONE]"))
	assert.NotContains(t, result.body, "chatcmpl-1")
	assert.Equal(t, "text/event-stream", result.headers.Get("Content-Type"))
	assert.Equal(t, "no-cache", result.headers.Get("Cache-Control"))
	assert.Equal(t, "keep-alive", result.headers.Get("Connection"))
	assert.Equal(t, "chunked", result.headers.Get("Transfer-Encoding"))
	assert.Equal(t, "no", result.headers.Get("X-Accel-Buffering"))
	assert.Equal(t, []int{1, 2}, result.attempts)
	assert.Equal(t, []string{"1", "2"}, result.usedChannels)
	assert.EqualValues(t, 1, result.errorLogCount)
	assert.EqualValues(t, 1, result.consumeLogCount)
	assert.Equal(t, 1, result.userRequestCount)
}

func TestRelayDoesNotRetryAfterStreamCommit(t *testing.T) {
	downstreamWrite := make(chan struct{})
	var downstreamWriteOnce sync.Once
	candidates := []relayFailoverCandidate{
		enabledRelayFailoverCandidate(1, "default", 200),
		enabledRelayFailoverCandidate(2, "default", 100),
	}

	result := runRelayFailover(t, relayFailoverOptions{
		candidates: candidates,
		upstreams: map[int]relayFailoverUpstream{
			1: {stream: true, abortAfterChunk: true, abortAfterWrite: downstreamWrite},
		},
		initialChannelID: 1,
		retryTimes:       10,
		usingGroup:       "default",
		stream:           true,
		contextSetup: func(c *gin.Context) {
			c.Writer = &relayCommitSignalWriter{
				ResponseWriter: c.Writer,
				onWrite: func() {
					downstreamWriteOnce.Do(func() { close(downstreamWrite) })
				},
			}
		},
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
	downstreamWrite := make(chan struct{})
	var downstreamWriteOnce sync.Once
	candidates := []relayFailoverCandidate{
		enabledRelayFailoverCandidate(1, "default", 200),
		enabledRelayFailoverCandidate(2, "default", 100),
	}

	result := runRelayFailover(t, relayFailoverOptions{
		candidates: candidates,
		upstreams: map[int]relayFailoverUpstream{
			1: {stream: true, abortAfterChunk: true, abortAfterWrite: downstreamWrite},
		},
		initialChannelID: 1,
		retryTimes:       10,
		usingGroup:       "default",
		relayFormat:      types.RelayFormatOpenAIAudio,
		requestPath:      "/v1/audio/speech",
		requestBody:      fmt.Sprintf(`{"model":"%s","input":"hello","voice":"alloy","stream_format":"sse"}`, relayFailoverModel),
		contextSetup: func(c *gin.Context) {
			c.Writer = &relayCommitSignalWriter{
				ResponseWriter: c.Writer,
				onWrite: func() {
					downstreamWriteOnce.Do(func() { close(downstreamWrite) })
				},
			}
		},
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

func TestRelayFinalUpstreamErrorSanitizesOrdinaryResponseLogAndDatabase(t *testing.T) {
	const safe = "provider capacity exhausted in region west"
	secrets := []string{
		"basic-auth-secret",
		"json-cookie-secret",
		"google-provider-secret",
		"query-token-secret",
		"proxy-password-secret",
	}
	message := safe +
		` Authorization: Basic basic-auth-secret` +
		` {"Set-Cookie":"session=json-cookie-secret; Path=/","X-Goog-Api-Key":"google-provider-secret"}` +
		` https://user:proxy-password-secret@provider.example/v1?access_token=query-token-secret`

	result := runRelayFailover(t, relayFailoverOptions{
		candidates:       []relayFailoverCandidate{enabledRelayFailoverCandidate(1, "default", 100)},
		upstreams:        map[int]relayFailoverUpstream{1: {statusCode: http.StatusInternalServerError}},
		initialChannelID: 1,
		usingGroup:       "default",
		responseBodies: map[int]string{
			1: `{"error":{"message":` + strconv.Quote(message) + `,"type":"provider_error","code":"capacity_exhausted"}}`,
		},
	})

	assert.Equal(t, http.StatusInternalServerError, result.statusCode)
	for _, sink := range []string{result.body, result.appLog, result.errorLogContent} {
		assert.Contains(t, sink, safe)
		for _, secret := range secrets {
			assert.NotContains(t, sink, secret)
		}
	}
	assert.Contains(t, result.body, "capacity_exhausted")
}
