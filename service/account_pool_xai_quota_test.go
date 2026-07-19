package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountPoolXAIQuotaProbeFallsBackToActiveProbeForFreeTierAndPersistsSnapshot(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	now := time.Unix(2_000_000_000, 0).UTC()
	service := AccountPoolService{}
	pool, account := createAccountPoolXAIQuotaTestAccount(t, service, now)

	var mu sync.Mutex
	paths := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.RequestURI())
		mu.Unlock()
		assert.Equal(t, "Bearer access-token", r.Header.Get("Authorization"))
		switch {
		case r.URL.Path == "/v1/billing" && r.URL.Query().Get("format") == "credits":
			_, _ = io.WriteString(w, `{"config":{"currentPeriod":{"type":"WEEKLY","start":"2026-07-18T00:00:00Z","end":"2026-07-19T00:00:00Z"}}}`)
		case r.URL.Path == "/v1/billing":
			_, _ = io.WriteString(w, `{"config":{"billingPeriodStart":"2026-07-01T00:00:00Z","billingPeriodEnd":"2026-08-01T00:00:00Z"}}`)
		case r.URL.Path == "/v1/responses":
			assert.Equal(t, "xai-grok-cli", r.Header.Get("x-xai-token-auth"))
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			assert.Contains(t, string(body), `"input":"hi"`)
			w.Header().Set("X-Ratelimit-Limit-Requests", "10")
			w.Header().Set("X-Ratelimit-Remaining-Requests", "7")
			w.Header().Set("X-Ratelimit-Reset-Requests", "2000000600")
			w.Header().Set("X-Ratelimit-Limit-Tokens", "1000")
			w.Header().Set("X-Ratelimit-Remaining-Tokens", "900")
			_, _ = io.WriteString(w, `{"id":"resp_probe"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	prober := newAccountPoolXAIQuotaProber(
		server.URL+"/v1",
		func(string) (*http.Client, error) { return server.Client(), nil },
		func() time.Time { return now },
	)
	result, err := prober.Probe(context.Background(), pool.Id, account.Id)
	require.NoError(t, err)
	assert.Equal(t, "hybrid_probe", result.Source)
	assert.Equal(t, http.StatusOK, result.StatusCode)
	assert.True(t, result.HeadersObserved)
	require.NotNil(t, result.Requests)
	require.NotNil(t, result.Requests.Remaining)
	assert.Equal(t, int64(7), *result.Requests.Remaining)
	require.NotNil(t, result.MediaEligible)
	assert.False(t, *result.MediaEligible)
	assert.Equal(t, AccountPoolXAIMediaReasonFreeTier, result.MediaEligibilityReason)
	assert.Equal(t, now.Unix(), result.FetchedAt)

	mu.Lock()
	assert.ElementsMatch(t, []string{"/v1/billing?format=credits", "/v1/billing", "/v1/responses"}, paths)
	mu.Unlock()

	persisted, err := service.GetXAIQuotaSnapshot(pool.Id, account.Id)
	require.NoError(t, err)
	require.NotNil(t, persisted)
	assert.Equal(t, result.FetchedAt, persisted.FetchedAt)
	assert.Equal(t, int64(7), *persisted.Requests.Remaining)
	accounts, err := service.ListAccounts(pool.Id)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.NotNil(t, accounts[0].XAIQuota)
	assert.Equal(t, result.FetchedAt, accounts[0].XAIQuota.FetchedAt)

	stored, err := getAccountPoolAccountForPool(pool.Id, account.Id)
	require.NoError(t, err)
	var options AccountPoolRuntimeOptions
	require.NoError(t, common.UnmarshalJsonStr(stored.RuntimeOptions, &options))
	require.NotNil(t, options.XAIQuota)
	assert.True(t, options.PoolMode, "quota persistence must preserve unrelated runtime options")
}

func TestAccountPoolXAIQuotaProbePersistsExhaustionInExistingRateLimitCooldown(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	now := time.Unix(2_000_000_000, 0).UTC()
	service := AccountPoolService{}
	pool, account := createAccountPoolXAIQuotaTestAccount(t, service, now)
	resetAt := now.Add(10 * time.Minute).Unix()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/billing":
			_, _ = io.WriteString(w, `{"config":{}}`)
		case r.URL.Path == "/v1/responses":
			w.Header().Set("X-Ratelimit-Limit-Requests", "10")
			w.Header().Set("X-Ratelimit-Remaining-Requests", "0")
			w.Header().Set("X-Ratelimit-Reset-Requests", "2000000600")
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"quota exhausted"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	prober := newAccountPoolXAIQuotaProber(
		server.URL+"/v1",
		func(string) (*http.Client, error) { return server.Client(), nil },
		func() time.Time { return now },
	)
	result, err := prober.Probe(context.Background(), pool.Id, account.Id)
	require.NoError(t, err)
	assert.Equal(t, http.StatusTooManyRequests, result.StatusCode)

	stored, err := getAccountPoolAccountForPool(pool.Id, account.Id)
	require.NoError(t, err)
	assert.Equal(t, resetAt, stored.RateLimitedUntil)
	assert.False(t, stored.IsSchedulableAt(now.Unix()))
}

func TestAccountPoolXAIQuotaProbeUsesAuthoritativePaidBillingWithoutConsumingModelQuota(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	now := time.Unix(2_000_000_000, 0).UTC()
	service := AccountPoolService{}
	pool, account := createAccountPoolXAIQuotaTestAccount(t, service, now)
	activeCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/billing" && r.URL.Query().Get("format") == "credits":
			_, _ = io.WriteString(w, `{"config":{"currentPeriod":{"type":"WEEKLY"},"creditUsagePercent":12.5}}`)
		case r.URL.Path == "/v1/billing":
			_, _ = io.WriteString(w, `{"config":{"monthlyLimit":{"val":15000},"used":{"val":7500}}}`)
		case r.URL.Path == "/v1/responses":
			activeCalls++
			http.Error(w, "active probe must not run", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	prober := newAccountPoolXAIQuotaProber(
		server.URL+"/v1",
		func(string) (*http.Client, error) { return server.Client(), nil },
		func() time.Time { return now },
	)
	result, err := prober.Probe(context.Background(), pool.Id, account.Id)
	require.NoError(t, err)
	assert.Equal(t, "billing_probe", result.Source)
	assert.Equal(t, 0, activeCalls)
	require.NotNil(t, result.Billing)
	require.NotNil(t, result.Billing.UsagePercent)
	assert.Equal(t, 12.5, *result.Billing.UsagePercent)
	assert.Equal(t, "SuperGrok", result.Billing.Plan)
	require.NotNil(t, result.MediaEligible)
	assert.True(t, *result.MediaEligible)
	assert.Equal(t, AccountPoolXAIMediaReasonEligible, result.MediaEligibilityReason)
}

func TestAccountPoolXAIQuotaProbeErrorIsBoundedAndDoesNotExposeAccessToken(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	now := time.Unix(2_000_000_000, 0).UTC()
	service := AccountPoolService{}
	pool, account := createAccountPoolXAIQuotaTestAccount(t, service, now)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, strings.Repeat("upstream-failure ", 10_000)+"access-token")
	}))
	t.Cleanup(server.Close)

	prober := newAccountPoolXAIQuotaProber(
		server.URL+"/v1",
		func(string) (*http.Client, error) { return server.Client(), nil },
		func() time.Time { return now },
	)
	_, err := prober.Probe(context.Background(), pool.Id, account.Id)
	require.Error(t, err)
	assert.Less(t, len(err.Error()), 512)
	assert.NotContains(t, err.Error(), "access-token")
}

func createAccountPoolXAIQuotaTestAccount(
	t *testing.T,
	service AccountPoolService,
	now time.Time,
) (model.AccountPool, AccountPoolAccountView) {
	t.Helper()
	pool, err := service.CreatePool(AccountPoolCreateParams{Name: "xai-pool", Platform: model.AccountPoolPlatformXAI})
	require.NoError(t, err)
	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "xai-oauth",
		Credential: AccountPoolCredentialConfig{
			Type:         AccountPoolCredentialTypeOAuth,
			RefreshToken: "refresh-token",
		},
		TokenState: AccountPoolTokenState{
			AccessToken:  "access-token",
			RefreshToken: "refresh-token",
			ExpiresAt:    now.Add(time.Hour).Unix(),
			Version:      1,
		},
	})
	require.NoError(t, err)
	runtimeOptions, err := common.Marshal(AccountPoolRuntimeOptions{PoolMode: true})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", account.Id).
		Update("runtime_options", string(runtimeOptions)).Error)
	return pool, account
}
