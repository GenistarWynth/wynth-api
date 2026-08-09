package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type logVisibilityResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Total int         `json:"total"`
		Items []model.Log `json:"items"`
	} `json:"data"`
}

type tokenLogVisibilityResponse struct {
	Success bool        `json:"success"`
	Data    []model.Log `json:"data"`
}

type failAfterAttemptBodyStorage struct {
	common.BodyStorage
	failSeek atomic.Bool
}

func (s *failAfterAttemptBodyStorage) Seek(offset int64, whence int) (int64, error) {
	if s.failSeek.Load() {
		return 0, errors.New("injected body storage seek failure")
	}
	return s.BodyStorage.Seek(offset, whence)
}

func runSuccessfulLogVisibilityFailover(t *testing.T, requestID string) relayFailoverResult {
	t.Helper()
	return runRelayFailover(t, relayFailoverOptions{
		candidates: []relayFailoverCandidate{
			enabledRelayFailoverCandidate(1, "default", 200),
			enabledRelayFailoverCandidate(2, "default", 100),
		},
		upstreams: map[int]relayFailoverUpstream{
			1: {statusCode: http.StatusInternalServerError},
		},
		initialChannelID: 1,
		usingGroup:       "default",
		requestID:        requestID,
	})
}

func querySelfLogs(t *testing.T, userID int, role int, requestID string) logVisibilityResponse {
	t.Helper()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/api/log/self?p=1&page_size=100&request_id="+url.QueryEscape(requestID),
		nil,
	)
	c.Set("id", userID)
	c.Set("role", role)
	GetUserLogs(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response logVisibilityResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	return response
}

func queryAllLogs(t *testing.T, requestID string, logType int) logVisibilityResponse {
	t.Helper()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/api/log/?p=1&page_size=100&request_id="+url.QueryEscape(requestID)+"&type="+strconv.Itoa(logType),
		nil,
	)
	GetAllLogs(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response logVisibilityResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	return response
}

func queryTokenLogs(t *testing.T, tokenID int) tokenLogVisibilityResponse {
	t.Helper()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/log/token", nil)
	c.Set("token_id", tokenID)
	GetLogByKey(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response tokenLogVisibilityResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	return response
}

func storedLogTypes(t *testing.T, requestID string) []int {
	t.Helper()

	var logs []model.Log
	require.NoError(t, model.LOG_DB.Where("request_id = ?", requestID).Order("id").Find(&logs).Error)
	logTypes := make([]int, 0, len(logs))
	for _, logEntry := range logs {
		logTypes = append(logTypes, logEntry.Type)
	}
	return logTypes
}

func TestGetUserLogsHidesFailedAttemptWhenAnotherChannelSucceeds(t *testing.T) {
	const requestID = "req-failover-success"
	result := runSuccessfulLogVisibilityFailover(t, requestID)
	require.Equal(t, http.StatusOK, result.statusCode)

	response := querySelfLogs(t, 1, common.RoleCommonUser, requestID)
	require.Len(t, response.Data.Items, 1)
	assert.Equal(t, 1, response.Data.Total)
	assert.Equal(t, model.LogTypeConsume, response.Data.Items[0].Type)
	assert.Equal(t, 2, response.Data.Items[0].ChannelId)
	assert.Equal(t, []int{model.LogTypeRetryError, model.LogTypeConsume}, storedLogTypes(t, requestID))

	tokenResponse := queryTokenLogs(t, 1)
	require.Len(t, tokenResponse.Data, 1)
	assert.Equal(t, model.LogTypeConsume, tokenResponse.Data[0].Type)
	assert.Equal(t, 2, tokenResponse.Data[0].ChannelId)
}

func TestGetUserLogsAdminSeesFailedAttemptWhenAnotherChannelSucceeds(t *testing.T) {
	const requestID = "req-admin-failover-success"
	result := runSuccessfulLogVisibilityFailover(t, requestID)
	require.Equal(t, http.StatusOK, result.statusCode)

	response := querySelfLogs(t, 1, common.RoleAdminUser, requestID)
	require.Len(t, response.Data.Items, 2)
	assert.Equal(t, 2, response.Data.Total)
	assert.Equal(t, model.LogTypeConsume, response.Data.Items[0].Type)
	assert.Equal(t, model.LogTypeError, response.Data.Items[1].Type)
	assert.Contains(t, response.Data.Items[1].Other, "admin_info")
	assert.Contains(t, response.Data.Items[1].Other, "use_channel")
}

func TestGetUserLogsShowsOnlyFinalErrorAfterFailoverExhaustion(t *testing.T) {
	const requestID = "req-failover-exhausted-user-view"
	result := runRelayFailover(t, relayFailoverOptions{
		candidates: []relayFailoverCandidate{
			enabledRelayFailoverCandidate(1, "default", 300),
			enabledRelayFailoverCandidate(2, "default", 200),
			enabledRelayFailoverCandidate(3, "default", 100),
		},
		upstreams: map[int]relayFailoverUpstream{
			1: {statusCode: http.StatusInternalServerError},
			2: {statusCode: http.StatusBadGateway},
			3: {statusCode: 524},
		},
		initialChannelID: 1,
		usingGroup:       "default",
		requestID:        requestID,
	})
	require.Equal(t, 524, result.statusCode)

	response := querySelfLogs(t, 1, common.RoleCommonUser, requestID)
	require.Len(t, response.Data.Items, 1)
	assert.Equal(t, 1, response.Data.Total)
	assert.Equal(t, model.LogTypeError, response.Data.Items[0].Type)
	assert.Equal(t, 3, response.Data.Items[0].ChannelId)
	assert.Contains(t, response.Data.Items[0].Content, "channel-3-failed")
	assert.Equal(t, []int{model.LogTypeRetryError, model.LogTypeRetryError, model.LogTypeError}, storedLogTypes(t, requestID))

	tokenResponse := queryTokenLogs(t, 1)
	require.Len(t, tokenResponse.Data, 1)
	assert.Equal(t, model.LogTypeError, tokenResponse.Data[0].Type)
	assert.Equal(t, 3, tokenResponse.Data[0].ChannelId)
}

func TestRelayTaskLockedViduLocalErrorKeepsTerminalFailureVisible(t *testing.T) {
	const requestID = "req-task-locked-vidu-local-error"
	viduPlatform := constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeVidu))
	result := runRelayTaskFailover(t, relayTaskFailoverOptions{
		candidates: []relayTaskFailoverCandidate{{
			id:          1,
			priority:    100,
			keys:        []string{"vidu-key-1", "vidu-key-2"},
			channelType: constant.ChannelTypeVidu,
		}},
		statuses: map[int][]int{
			1: {http.StatusInternalServerError, http.StatusOK},
		},
		responseBodies: map[int]string{
			1: `{"task_id":"upstream-vidu-failed","state":"failed"}`,
		},
		requestID:        requestID,
		initialChannelID: 1,
		originPlatform:   viduPlatform,
		retryTimes:       1,
		locked:           true,
	})

	assert.Equal(t, http.StatusBadRequest, result.statusCode)
	assert.Contains(t, result.body, "task failed")
	assert.Contains(t, result.body, `"code":"task_failed"`)
	assert.Equal(t, []int{1, 1}, result.attempts)
	assert.Equal(t, []string{"Token vidu-key-1", "Token vidu-key-2"}, result.authorizations)
	assert.Equal(t, []string{"1", "1"}, result.usedChannels)
	assert.Equal(t, []int{model.LogTypeRetryError, model.LogTypeError}, result.errorLogTypes)
	assert.Zero(t, result.consumeLogCount)
	assert.Zero(t, result.taskCount)
	assert.Equal(t, relayTaskInitialQuota, result.user.Quota)
	assert.Equal(t, relayTaskInitialQuota, result.token.RemainQuota)
	assert.Zero(t, result.user.UsedQuota)
	assert.Zero(t, result.token.UsedQuota)
	assert.Zero(t, result.subscriptionUse)

	userResponse := querySelfLogs(t, 1, common.RoleCommonUser, requestID)
	require.Len(t, userResponse.Data.Items, 1)
	assert.Equal(t, model.LogTypeError, userResponse.Data.Items[0].Type)
	assert.Equal(t, 1, userResponse.Data.Items[0].ChannelId)
	assert.Contains(t, userResponse.Data.Items[0].Content, "task failed")
	assert.NotContains(t, userResponse.Data.Items[0].Other, "admin_info")

	adminResponse := querySelfLogs(t, 1, common.RoleAdminUser, requestID)
	require.Len(t, adminResponse.Data.Items, 2)
	for _, logEntry := range adminResponse.Data.Items {
		assert.Equal(t, model.LogTypeError, logEntry.Type)
		assert.Contains(t, logEntry.Other, "admin_info")
		assert.Contains(t, logEntry.Other, "use_channel")
	}
	assert.Contains(t, adminResponse.Data.Items[0].Content, "task failed")
	assert.Contains(t, adminResponse.Data.Items[0].Other, "task_failed")
	assert.Contains(t, adminResponse.Data.Items[1].Content, "upstream-vidu-failed")
	var terminalOther map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(adminResponse.Data.Items[0].Other, &terminalOther))
	terminalAdminInfo, ok := terminalOther["admin_info"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, []interface{}{"1", "1"}, terminalAdminInfo["use_channel"])
	assert.EqualValues(t, 1, terminalAdminInfo["multi_key_index"])
	var retryOther map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(adminResponse.Data.Items[1].Other, &retryOther))
	retryAdminInfo, ok := retryOther["admin_info"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, []interface{}{"1"}, retryAdminInfo["use_channel"])
	assert.EqualValues(t, 0, retryAdminInfo["multi_key_index"])
}

func TestRelayTaskFinalLocalErrorWithoutRetryRemainsVisible(t *testing.T) {
	const requestID = "req-task-final-local-error"
	result := runRelayTaskFailover(t, relayTaskFailoverOptions{
		candidates: []relayTaskFailoverCandidate{{
			id:          1,
			priority:    100,
			channelType: constant.ChannelTypeVidu,
		}},
		statuses: map[int][]int{1: {http.StatusOK}},
		responseBodies: map[int]string{
			1: `{"task_id":"upstream-vidu-failed","state":"failed"}`,
		},
		requestID:        requestID,
		initialChannelID: 1,
		fixed:            true,
	})

	assert.Equal(t, http.StatusBadRequest, result.statusCode)
	assert.Contains(t, result.body, `"code":"task_failed"`)
	assert.Equal(t, []int{1}, result.attempts)
	assert.Equal(t, []int{model.LogTypeError}, result.errorLogTypes)
	assert.Zero(t, result.consumeLogCount)
	assert.Zero(t, result.taskCount)
	assert.Equal(t, relayTaskInitialQuota, result.user.Quota)
	assert.Equal(t, relayTaskInitialQuota, result.token.RemainQuota)
	assert.Zero(t, result.user.UsedQuota)
	assert.Zero(t, result.token.UsedQuota)
	assert.Zero(t, result.subscriptionUse)
	assert.NotContains(t, result.appLog, "channel error")

	userResponse := querySelfLogs(t, 1, common.RoleCommonUser, requestID)
	require.Len(t, userResponse.Data.Items, 1)
	assert.Equal(t, model.LogTypeError, userResponse.Data.Items[0].Type)
	assert.Contains(t, userResponse.Data.Items[0].Content, "task failed")

	adminResponse := querySelfLogs(t, 1, common.RoleAdminUser, requestID)
	require.Len(t, adminResponse.Data.Items, 1)
	assert.Equal(t, model.LogTypeError, adminResponse.Data.Items[0].Type)
	assert.Contains(t, adminResponse.Data.Items[0].Other, "admin_info")
}

func TestRelayTaskPreUpstreamLocalErrorAfterRetryKeepsTerminalFailureVisible(t *testing.T) {
	const requestID = "req-task-pre-upstream-local-error"
	result := runRelayTaskFailover(t, relayTaskFailoverOptions{
		candidates: []relayTaskFailoverCandidate{
			{id: 1, priority: 200},
			{id: 2, priority: 100, channelType: constant.ChannelTypeAzure, autoBan: 1},
		},
		statuses:         map[int][]int{1: {http.StatusInternalServerError}},
		requestID:        requestID,
		initialChannelID: 1,
		automaticDisable: true,
		disableRanges: []operation_setting.StatusCodeRange{
			{Start: http.StatusBadRequest, End: http.StatusBadRequest},
		},
	})

	assert.Equal(t, http.StatusBadRequest, result.statusCode)
	assert.Contains(t, result.body, "invalid api platform")
	assert.Contains(t, result.body, `"code":"invalid_api_platform"`)
	assert.Equal(t, []int{1}, result.attempts)
	assert.Equal(t, []string{"1", "2"}, result.usedChannels)
	assert.Equal(t, []int{model.LogTypeRetryError, model.LogTypeError}, result.errorLogTypes)
	assert.Zero(t, result.consumeLogCount)
	assert.Zero(t, result.taskCount)
	assert.Equal(t, relayTaskInitialQuota, result.user.Quota)
	assert.Equal(t, relayTaskInitialQuota, result.token.RemainQuota)
	assert.Zero(t, result.user.UsedQuota)
	assert.Zero(t, result.token.UsedQuota)
	assert.Zero(t, result.subscriptionUse)
	assert.NotContains(t, result.appLog, "channel error (channel #2")

	var secondChannel model.Channel
	require.NoError(t, model.DB.First(&secondChannel, 2).Error)
	assert.Equal(t, common.ChannelStatusEnabled, secondChannel.Status)

	userResponse := querySelfLogs(t, 1, common.RoleCommonUser, requestID)
	require.Len(t, userResponse.Data.Items, 1)
	assert.Equal(t, model.LogTypeError, userResponse.Data.Items[0].Type)
	assert.Equal(t, 2, userResponse.Data.Items[0].ChannelId)
	assert.Contains(t, userResponse.Data.Items[0].Content, "invalid api platform")
	assert.NotContains(t, userResponse.Data.Items[0].Other, "admin_info")

	adminResponse := querySelfLogs(t, 1, common.RoleAdminUser, requestID)
	require.Len(t, adminResponse.Data.Items, 2)
	assert.Equal(t, model.LogTypeError, adminResponse.Data.Items[0].Type)
	assert.Equal(t, 2, adminResponse.Data.Items[0].ChannelId)
	assert.Contains(t, adminResponse.Data.Items[0].Other, "invalid_api_platform")
	assert.Equal(t, model.LogTypeError, adminResponse.Data.Items[1].Type)
	assert.Equal(t, 1, adminResponse.Data.Items[1].ChannelId)
	assert.Contains(t, adminResponse.Data.Items[1].Other, "admin_info")
}

func TestRelayTaskBodyStorageLocalErrorWithoutRetryRemainsVisible(t *testing.T) {
	const requestID = "req-task-body-storage-local-error"
	bodyStorage, err := common.CreateBodyStorage([]byte(relayTaskFailoverRequestBody()))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, bodyStorage.Close())
	})
	failingStorage := &failAfterAttemptBodyStorage{BodyStorage: bodyStorage}
	failingStorage.failSeek.Store(true)

	result := runRelayTaskFailover(t, relayTaskFailoverOptions{
		candidates:       []relayTaskFailoverCandidate{{id: 1, priority: 100}},
		requestID:        requestID,
		initialChannelID: 1,
		fixed:            true,
		contextSetup: func(c *gin.Context) {
			c.Set(common.KeyBodyStorage, failingStorage)
		},
	})

	assert.Equal(t, http.StatusBadRequest, result.statusCode)
	assert.Contains(t, result.body, `"code":"read_request_body_failed"`)
	assert.Empty(t, result.attempts)
	assert.Equal(t, []string{"1"}, result.usedChannels)
	assert.Equal(t, []int{model.LogTypeError}, result.errorLogTypes)
	assert.Zero(t, result.consumeLogCount)
	assert.Zero(t, result.taskCount)
	assert.Equal(t, relayTaskInitialQuota, result.user.Quota)
	assert.Equal(t, relayTaskInitialQuota, result.token.RemainQuota)
	assert.NotContains(t, result.appLog, "channel error")

	userResponse := querySelfLogs(t, 1, common.RoleCommonUser, requestID)
	require.Len(t, userResponse.Data.Items, 1)
	assert.Equal(t, model.LogTypeError, userResponse.Data.Items[0].Type)
	assert.Equal(t, 1, userResponse.Data.Items[0].ChannelId)
	assert.Contains(t, userResponse.Data.Items[0].Content, "injected body storage seek failure")

	adminResponse := querySelfLogs(t, 1, common.RoleAdminUser, requestID)
	require.Len(t, adminResponse.Data.Items, 1)
	assert.Equal(t, model.LogTypeError, adminResponse.Data.Items[0].Type)
	assert.Contains(t, adminResponse.Data.Items[0].Other, "read_request_body_failed")
	assert.Contains(t, adminResponse.Data.Items[0].Other, "admin_info")
}

func TestRelayTaskBodyStorageLocalErrorAfterRetryKeepsTerminalFailureVisible(t *testing.T) {
	const requestID = "req-task-body-storage-local-error-after-retry"
	bodyStorage, err := common.CreateBodyStorage([]byte(relayTaskFailoverRequestBody()))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, bodyStorage.Close())
	})
	failingStorage := &failAfterAttemptBodyStorage{BodyStorage: bodyStorage}

	result := runRelayTaskFailover(t, relayTaskFailoverOptions{
		candidates: []relayTaskFailoverCandidate{
			{id: 1, priority: 200},
			{id: 2, priority: 100, autoBan: 1},
		},
		statuses:         map[int][]int{1: {http.StatusInternalServerError}},
		requestID:        requestID,
		initialChannelID: 1,
		automaticDisable: true,
		disableRanges: []operation_setting.StatusCodeRange{
			{Start: http.StatusBadRequest, End: http.StatusBadRequest},
		},
		contextSetup: func(c *gin.Context) {
			c.Set(common.KeyBodyStorage, failingStorage)
		},
		onAttempt: func(channelID int) {
			if channelID == 1 {
				failingStorage.failSeek.Store(true)
			}
		},
	})

	assert.Equal(t, http.StatusBadRequest, result.statusCode)
	assert.Contains(t, result.body, `"code":"read_request_body_failed"`)
	assert.Equal(t, []int{1}, result.attempts)
	assert.Equal(t, []string{"1", "2"}, result.usedChannels)
	assert.Equal(t, []int{model.LogTypeRetryError, model.LogTypeError}, result.errorLogTypes)
	assert.Zero(t, result.consumeLogCount)
	assert.Zero(t, result.taskCount)
	assert.Equal(t, relayTaskInitialQuota, result.user.Quota)
	assert.Equal(t, relayTaskInitialQuota, result.token.RemainQuota)
	assert.NotContains(t, result.appLog, "channel error (channel #2")

	var secondChannel model.Channel
	require.NoError(t, model.DB.First(&secondChannel, 2).Error)
	assert.Equal(t, common.ChannelStatusEnabled, secondChannel.Status)

	userResponse := querySelfLogs(t, 1, common.RoleCommonUser, requestID)
	require.Len(t, userResponse.Data.Items, 1)
	assert.Equal(t, model.LogTypeError, userResponse.Data.Items[0].Type)
	assert.Equal(t, 2, userResponse.Data.Items[0].ChannelId)
	assert.Contains(t, userResponse.Data.Items[0].Content, "injected body storage seek failure")

	adminResponse := querySelfLogs(t, 1, common.RoleAdminUser, requestID)
	require.Len(t, adminResponse.Data.Items, 2)
	assert.Equal(t, model.LogTypeError, adminResponse.Data.Items[0].Type)
	assert.Equal(t, 2, adminResponse.Data.Items[0].ChannelId)
	assert.Contains(t, adminResponse.Data.Items[0].Other, "read_request_body_failed")
	assert.Equal(t, model.LogTypeError, adminResponse.Data.Items[1].Type)
	assert.Equal(t, 1, adminResponse.Data.Items[1].ChannelId)
	assert.Contains(t, adminResponse.Data.Items[1].Other, "admin_info")
}

func TestGetUserLogsRetainsLastAttemptWhenNextChannelCannotStart(t *testing.T) {
	const requestID = "req-failover-next-attempt-local-failure"
	requestBody := `{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"hello"}],"stream":false}`
	bodyStorage, err := common.CreateBodyStorage([]byte(requestBody))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, bodyStorage.Close())
	})
	failingStorage := &failAfterAttemptBodyStorage{BodyStorage: bodyStorage}

	result := runRelayFailover(t, relayFailoverOptions{
		candidates: []relayFailoverCandidate{
			enabledRelayFailoverCandidate(1, "default", 200),
			enabledRelayFailoverCandidate(2, "default", 100),
		},
		upstreams: map[int]relayFailoverUpstream{
			1: {
				statusCode: http.StatusInternalServerError,
				onAttempt:  func() { failingStorage.failSeek.Store(true) },
			},
		},
		initialChannelID: 1,
		usingGroup:       "default",
		requestBody:      requestBody,
		requestID:        requestID,
		contextSetup: func(c *gin.Context) {
			c.Set(common.KeyBodyStorage, failingStorage)
		},
	})
	require.Equal(t, http.StatusBadRequest, result.statusCode)
	assert.Equal(t, []int{1}, result.attempts)

	response := querySelfLogs(t, 1, common.RoleCommonUser, requestID)
	require.Len(t, response.Data.Items, 1)
	assert.Equal(t, model.LogTypeError, response.Data.Items[0].Type)
	assert.Equal(t, 1, response.Data.Items[0].ChannelId)
}

func TestGetLogsAdminSeesEveryErrorAfterFailoverExhaustion(t *testing.T) {
	const requestID = "req-failover-exhausted-admin-view"
	result := runRelayFailover(t, relayFailoverOptions{
		candidates: []relayFailoverCandidate{
			enabledRelayFailoverCandidate(1, "default", 300),
			enabledRelayFailoverCandidate(2, "default", 200),
			enabledRelayFailoverCandidate(3, "default", 100),
		},
		upstreams: map[int]relayFailoverUpstream{
			1: {statusCode: http.StatusInternalServerError},
			2: {statusCode: http.StatusBadGateway},
			3: {statusCode: 524},
		},
		initialChannelID: 1,
		usingGroup:       "default",
		requestID:        requestID,
	})
	require.Equal(t, 524, result.statusCode)

	for _, response := range []logVisibilityResponse{
		querySelfLogs(t, 1, common.RoleAdminUser, requestID),
		queryAllLogs(t, requestID, model.LogTypeError),
	} {
		require.Len(t, response.Data.Items, 3)
		assert.Equal(t, 3, response.Data.Total)
		for _, logEntry := range response.Data.Items {
			assert.Equal(t, model.LogTypeError, logEntry.Type)
			assert.Contains(t, logEntry.Other, "admin_info")
			assert.Contains(t, logEntry.Other, "use_channel")
		}
	}
}
