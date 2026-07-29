package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type controlledRefundFunding struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int64
	once    sync.Once
}

func (f *controlledRefundFunding) Source() string {
	return BillingSourceWallet
}

func (f *controlledRefundFunding) PreConsume(int) error {
	return nil
}

func (f *controlledRefundFunding) Settle(int) error {
	return nil
}

func (f *controlledRefundFunding) Refund() error {
	f.calls.Add(1)
	f.once.Do(func() { close(f.started) })
	<-f.release
	return nil
}

func TestBillingSessionWaitRefundTracksAsyncCompletion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	funding := &controlledRefundFunding{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	session := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{
			UserId:       1,
			IsPlayground: true,
		},
		funding:          funding,
		preConsumedQuota: 1,
		tokenConsumed:    1,
	}

	session.Refund(c)
	session.Refund(c)
	select {
	case <-funding.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for asynchronous refund")
	}

	cancelledWait, cancelWait := context.WithCancel(context.Background())
	cancelWait()
	require.ErrorIs(t, session.WaitRefund(cancelledWait), context.Canceled)

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- session.WaitRefund(context.Background())
	}()
	close(funding.release)
	select {
	case err := <-waitDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for refund completion")
	}
	assert.Equal(t, int64(1), funding.calls.Load())
	require.NoError(t, session.WaitRefund(context.Background()))
}

func TestBillingSessionWaitRefundReturnsWhenRefundWasNotScheduled(t *testing.T) {
	session := &BillingSession{}
	waitContext, cancelWait := context.WithCancel(context.Background())
	cancelWait()

	require.NoError(t, session.WaitRefund(waitContext))
}
