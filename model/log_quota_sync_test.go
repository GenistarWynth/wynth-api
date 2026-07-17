package model

import (
	"math"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetQuotaDataCacheForTest() {
	CacheQuotaDataLock.Lock()
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()
}

func snapshotQuotaDataCacheForTest() []QuotaData {
	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()

	rows := make([]QuotaData, 0, len(CacheQuotaData))
	for _, row := range CacheQuotaData {
		rows = append(rows, *row)
	}
	return rows
}

// blockDefaultGoPool occupies the only worker so quota work submitted with
// gopool.Go cannot run until after the caller's cache snapshot. The returned
// function releases the worker and drains every queued task deterministically.
func blockDefaultGoPool(t *testing.T) func() {
	t.Helper()

	gopool.SetCap(1)
	blockerStarted := make(chan struct{})
	releaseBlocker := make(chan struct{})
	gopool.Go(func() {
		close(blockerStarted)
		<-releaseBlocker
	})
	<-blockerStarted
	require.Equal(t, int32(1), gopool.WorkerCount())

	var once sync.Once
	unblockAndDrain := func() {
		once.Do(func() {
			close(releaseBlocker)
			drained := make(chan struct{})
			gopool.Go(func() {
				close(drained)
			})
			<-drained
			gopool.SetCap(math.MaxInt32)
		})
	}
	t.Cleanup(unblockAndDrain)
	return unblockAndDrain
}

func prepareQuotaLogReturnTest(t *testing.T, nodeName string) {
	t.Helper()

	oldGinMode := gin.Mode()
	oldDataExportEnabled := common.DataExportEnabled
	oldLogConsumeEnabled := common.LogConsumeEnabled
	oldNodeName := common.NodeName
	common.DataExportEnabled = true
	common.LogConsumeEnabled = true
	common.NodeName = nodeName
	gin.SetMode(gin.TestMode)
	resetQuotaDataCacheForTest()
	t.Cleanup(func() {
		common.DataExportEnabled = oldDataExportEnabled
		common.LogConsumeEnabled = oldLogConsumeEnabled
		common.NodeName = oldNodeName
		gin.SetMode(oldGinMode)
		resetQuotaDataCacheForTest()
	})
}

func TestRecordConsumeLogAggregatesQuotaDataBeforeReturn(t *testing.T) {
	truncateTables(t)
	prepareQuotaLogReturnTest(t, "consume-node")
	unblockAndDrain := blockDefaultGoPool(t)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("username", "alice")
	RecordConsumeLog(ctx, 101, RecordConsumeLogParams{
		ChannelId:        7,
		PromptTokens:     1000,
		CompletionTokens: 200,
		ModelName:        "gpt-sync",
		Quota:            1234,
		TokenId:          9,
		Group:            "vip",
		Other: map[string]interface{}{
			"cache_tokens":       300,
			"cache_write_tokens": 100,
		},
	})

	rowsAtReturn := snapshotQuotaDataCacheForTest()
	unblockAndDrain()

	require.Len(t, rowsAtReturn, 1, "quota cache must be updated before RecordConsumeLog returns")
	row := rowsAtReturn[0]
	assert.Equal(t, 1, row.Count)
	assert.Equal(t, 101, row.UserID)
	assert.Equal(t, "alice", row.Username)
	assert.Equal(t, "gpt-sync", row.ModelName)
	assert.Equal(t, 1234, row.Quota)
	assert.Equal(t, 1200, row.TokenUsed)
	assert.Equal(t, 600, row.InputTokens)
	assert.Equal(t, 300, row.CacheReadTokens)
	assert.Equal(t, 100, row.CacheCreationTokens)
	assert.Equal(t, "vip", row.UseGroup)
	assert.Equal(t, 9, row.TokenID)
	assert.Equal(t, 7, row.ChannelID)
	assert.Equal(t, "consume-node", row.NodeName)
}

func TestRecordTaskBillingLogAggregatesQuotaDataBeforeReturn(t *testing.T) {
	truncateTables(t)
	prepareQuotaLogReturnTest(t, "current-node")

	tests := []struct {
		name         string
		userID       int
		tokenID      int
		username     string
		affCode      string
		nodeName     string
		expectedNode string
	}{
		{name: "falls back to current node", userID: 201, tokenID: 301, username: "task-fallback", affCode: "aff-201", expectedNode: "current-node"},
		{name: "preserves originating node", userID: 202, tokenID: 302, username: "task-origin", affCode: "aff-202", nodeName: "origin-node", expectedNode: "origin-node"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetQuotaDataCacheForTest()
			require.NoError(t, DB.Create(&User{Id: tt.userID, Username: tt.username, AffCode: tt.affCode}).Error)
			require.NoError(t, DB.Create(&Token{Id: tt.tokenID, UserId: tt.userID, Key: tt.affCode, Name: "task-token"}).Error)
			unblockAndDrain := blockDefaultGoPool(t)

			RecordTaskBillingLog(RecordTaskBillingLogParams{
				UserId:    tt.userID,
				LogType:   LogTypeConsume,
				Content:   "task completed",
				ChannelId: 17,
				ModelName: "video-sync",
				Quota:     4321,
				TokenId:   tt.tokenID,
				Group:     "task-group",
				NodeName:  tt.nodeName,
			})

			rowsAtReturn := snapshotQuotaDataCacheForTest()
			unblockAndDrain()

			require.Len(t, rowsAtReturn, 1, "quota cache must be updated before RecordTaskBillingLog returns")
			row := rowsAtReturn[0]
			assert.Equal(t, 1, row.Count)
			assert.Equal(t, tt.userID, row.UserID)
			assert.Equal(t, tt.username, row.Username)
			assert.Equal(t, "video-sync", row.ModelName)
			assert.Equal(t, 4321, row.Quota)
			assert.Equal(t, "task-group", row.UseGroup)
			assert.Equal(t, tt.tokenID, row.TokenID)
			assert.Equal(t, 17, row.ChannelID)
			assert.Equal(t, tt.expectedNode, row.NodeName)
		})
	}
}
