package relay

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests protect the account-pool routing contract for the synchronous
// non-chat relay handlers that previously hard-rejected pooled channels via
// rejectUnsupportedAccountPoolRuntime (embedding, image, audio, rerank,
// gemini-embedding). They verify the user-visible contract — pooled channels now
// route through pool selection and the selected account's credential reaches the
// wire — using a real httptest upstream rather than re-testing the shared
// runAccountPoolRuntimeAttempts machinery (already covered in
// account_pool_runtime_test.go). The upstream returns 401 so the handler stops
// after the request is sent without exercising billing or DB quota state.

// capturedAuth records the Authorization header the upstream observed.
type capturedAuth struct {
	mu     sync.Mutex
	values []string
}

func (c *capturedAuth) record(h string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values = append(c.values, h)
}

func (c *capturedAuth) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.values))
	copy(out, c.values)
	return out
}

// newAccountPoolUpstream returns an httptest server that records the inbound
// Authorization header and always answers 401, so the relay handler stops right
// after sending the upstream request (no billing path is taken).
func newAccountPoolUpstream(t *testing.T, capture *capturedAuth) *httptest.Server {
	t.Helper()
	// The relay path uses the shared package HTTP client; initialize it so DoRequest
	// can reach the httptest upstream.
	service.InitHttpClient()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"unauthorized","type":"invalid_request_error"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// setAccountPoolUpstreamChannelContext wires the gin context so InitChannelMeta
// resolves an OpenAI channel pointed at the httptest upstream.
func setAccountPoolUpstreamChannelContext(ctx *gin.Context, channelID int, baseURL string) {
	common.SetContextKey(ctx, constant.ContextKeyChannelId, channelID)
	common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(ctx, constant.ContextKeyChannelKey, "sk-channel")
	common.SetContextKey(ctx, constant.ContextKeyChannelBaseUrl, baseURL)
	common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "text-embedding-3-small")
}

// TestAccountPoolEmbeddingHelperInjectsPooledAccountCredential verifies that an
// embedding channel WITH an enabled pool binding is no longer rejected: the
// request routes through pool selection and the selected account's API key — not
// the channel key — reaches the upstream Authorization header.
func TestAccountPoolEmbeddingHelperInjectsPooledAccountCredential(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	capture := &capturedAuth{}
	upstream := newAccountPoolUpstream(t, capture)

	ctx := newAccountPoolRelayTestContext("/v1/embeddings")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 0)
	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name: "embedding-pool-account",
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-embedding-account",
		},
	})
	setAccountPoolUpstreamChannelContext(ctx, channel.Id, upstream.URL)

	request := &dto.EmbeddingRequest{Model: "text-embedding-3-small", Input: "hello"}
	info := relaycommon.GenRelayInfoEmbedding(ctx, request)

	newAPIError := EmbeddingHelper(ctx, info)

	// 401 from the upstream surfaces as an error, but the routing contract is what
	// matters: the pooled account's credential was injected and sent on the wire.
	require.NotNil(t, newAPIError)
	auths := capture.all()
	require.Len(t, auths, 1, "pooled channel must make exactly one upstream attempt (AccountRetryTimes=0)")
	assert.Equal(t, "Bearer sk-embedding-account", auths[0], "selected pooled account key must reach the upstream, not the channel key")
}

// TestAccountPoolEmbeddingHelperNonPooledChannelUsesChannelKey is the regression
// guard: a channel WITHOUT a pool binding still works exactly as before — a single
// transparent attempt that sends the channel credential, with no pool selection.
func TestAccountPoolEmbeddingHelperNonPooledChannelUsesChannelKey(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	capture := &capturedAuth{}
	upstream := newAccountPoolUpstream(t, capture)

	ctx := newAccountPoolRelayTestContext("/v1/embeddings")
	channel := createAccountPoolRelayTestChannel(t) // no pool binding
	setAccountPoolUpstreamChannelContext(ctx, channel.Id, upstream.URL)

	request := &dto.EmbeddingRequest{Model: "text-embedding-3-small", Input: "hello"}
	info := relaycommon.GenRelayInfoEmbedding(ctx, request)

	newAPIError := EmbeddingHelper(ctx, info)

	require.NotNil(t, newAPIError)
	auths := capture.all()
	require.Len(t, auths, 1, "non-pooled channel runs exactly one attempt")
	assert.Equal(t, "Bearer sk-channel", auths[0], "non-pooled channel must use the channel credential unchanged")
	assert.Zero(t, service.GetSelectedAccountPoolAccountID(ctx), "no pooled account selected for a non-pooled channel")
}

// TestAccountPoolRerankHelperInjectsPooledAccountCredential verifies the same
// routing contract for the rerank handler, exercising the second representative
// wrapped handler. The shared selection/retry/credential machinery is covered in
// account_pool_runtime_test.go; this asserts the rerank entry point participates.
func TestAccountPoolRerankHelperInjectsPooledAccountCredential(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	capture := &capturedAuth{}
	upstream := newAccountPoolUpstream(t, capture)

	ctx := newAccountPoolRelayTestContext("/v1/rerank")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 0)
	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name: "rerank-pool-account",
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-rerank-account",
		},
	})
	common.SetContextKey(ctx, constant.ContextKeyChannelId, channel.Id)
	common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(ctx, constant.ContextKeyChannelKey, "sk-channel")
	common.SetContextKey(ctx, constant.ContextKeyChannelBaseUrl, upstream.URL)
	common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "rerank-model")

	request := &dto.RerankRequest{Model: "rerank-model", Query: "q", Documents: []any{"a", "b"}}
	info := relaycommon.GenRelayInfoRerank(ctx, request)

	newAPIError := RerankHelper(ctx, info)

	require.NotNil(t, newAPIError)
	auths := capture.all()
	require.Len(t, auths, 1, "pooled rerank channel must make exactly one upstream attempt")
	assert.Equal(t, "Bearer sk-rerank-account", auths[0], "selected pooled account key must reach the rerank upstream")
}
