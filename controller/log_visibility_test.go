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
	"github.com/QuantumNous/new-api/model"
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
