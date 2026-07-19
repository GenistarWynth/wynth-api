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
	previousEstimateNow := accountPoolXAIFreeUsageNow
	accountPoolXAIFreeUsageNow = func() time.Time { return now }
	t.Cleanup(func() { accountPoolXAIFreeUsageNow = previousEstimateNow })
	service := AccountPoolService{}
	pool, account := createAccountPoolXAIQuotaTestAccount(t, service, now)
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).Where("id = ?", account.Id).Updates(map[string]any{
		"created_time":            now.Add(-6 * time.Hour).Unix(),
		"last_success_at":         now.Add(-time.Hour).Unix(),
		"success_count":           int64(12),
		"total_prompt_tokens":     int64(120),
		"total_completion_tokens": int64(30),
	}).Error)

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
	require.NotNil(t, result.FreeUsage24hEstimate)
	assert.Equal(t, AccountPoolXAIFreeUsageSourceCountersSinceCreation, result.FreeUsage24hEstimate.Source)
	assert.False(t, result.FreeUsage24hEstimate.Estimated)
	assert.Equal(t, int64(12), result.FreeUsage24hEstimate.Requests)
	assert.Equal(t, int64(150), result.FreeUsage24hEstimate.Tokens)

	mu.Lock()
	assert.ElementsMatch(t, []string{"/v1/billing?format=credits", "/v1/billing", "/v1/responses"}, paths)
	mu.Unlock()

	persisted, err := service.GetXAIQuotaSnapshot(pool.Id, account.Id)
	require.NoError(t, err)
	require.NotNil(t, persisted)
	assert.Equal(t, result.FetchedAt, persisted.FetchedAt)
	assert.Equal(t, int64(7), *persisted.Requests.Remaining)
	require.NotNil(t, persisted.FreeUsage24hEstimate)
	assert.Equal(t, int64(12), persisted.FreeUsage24hEstimate.Requests)
	accounts, err := service.ListAccounts(pool.Id)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.NotNil(t, accounts[0].XAIQuota)
	assert.Equal(t, result.FetchedAt, accounts[0].XAIQuota.FetchedAt)
	require.NotNil(t, accounts[0].XAIQuota.FreeUsage24hEstimate)
	assert.Equal(t, int64(150), accounts[0].XAIQuota.FreeUsage24hEstimate.Tokens)

	stored, err := getAccountPoolAccountForPool(pool.Id, account.Id)
	require.NoError(t, err)
	var options AccountPoolRuntimeOptions
	require.NoError(t, common.UnmarshalJsonStr(stored.RuntimeOptions, &options))
	require.NotNil(t, options.XAIQuota)
	assert.Nil(t, options.XAIQuota.FreeUsage24hEstimate, "read-time local estimates must not be persisted into the upstream snapshot")
	assert.True(t, options.PoolMode, "quota persistence must preserve unrelated runtime options")
}

func TestAccountPoolXAIFreeUsage24hEstimateUsesAvailableCounters(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	freeSnapshot := AccountPoolXAIQuotaSnapshot{MediaEligibilityReason: AccountPoolXAIMediaReasonFreeTier}

	tests := []struct {
		name    string
		account model.AccountPoolAccount
		want    *AccountPoolXAIFreeUsageEstimate
	}{
		{
			name: "new account uses counters since creation",
			account: model.AccountPoolAccount{
				CreatedTime:           now.Add(-6 * time.Hour).Unix(),
				LastSuccessAt:         now.Add(-time.Hour).Unix(),
				SuccessCount:          12,
				TotalPromptTokens:     120,
				TotalCompletionTokens: 30,
			},
			want: &AccountPoolXAIFreeUsageEstimate{
				Source:             AccountPoolXAIFreeUsageSourceCountersSinceCreation,
				WindowSeconds:      int64(24 * time.Hour / time.Second),
				ObservationSeconds: int64(6 * time.Hour / time.Second),
				Requests:           12,
				PromptTokens:       120,
				CompletionTokens:   30,
				Tokens:             150,
				Estimated:          false,
			},
		},
		{
			name: "old active account uses lifetime average projection",
			account: model.AccountPoolAccount{
				CreatedTime:           now.Add(-48 * time.Hour).Unix(),
				LastSuccessAt:         now.Add(-time.Hour).Unix(),
				SuccessCount:          200,
				TotalPromptTokens:     1000,
				TotalCompletionTokens: 500,
			},
			want: &AccountPoolXAIFreeUsageEstimate{
				Source:             AccountPoolXAIFreeUsageSourceLifetimeProrated,
				WindowSeconds:      int64(24 * time.Hour / time.Second),
				ObservationSeconds: int64(48 * time.Hour / time.Second),
				Requests:           100,
				PromptTokens:       500,
				CompletionTokens:   250,
				Tokens:             750,
				Estimated:          true,
			},
		},
		{
			name: "old inactive account is known zero",
			account: model.AccountPoolAccount{
				CreatedTime:           now.Add(-48 * time.Hour).Unix(),
				LastSuccessAt:         now.Add(-25 * time.Hour).Unix(),
				SuccessCount:          200,
				TotalPromptTokens:     1000,
				TotalCompletionTokens: 500,
			},
			want: &AccountPoolXAIFreeUsageEstimate{
				Source:             AccountPoolXAIFreeUsageSourceNoRecentSuccess,
				WindowSeconds:      int64(24 * time.Hour / time.Second),
				ObservationSeconds: int64(48 * time.Hour / time.Second),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, buildAccountPoolXAIFreeUsage24hEstimate(test.account, freeSnapshot, now))
		})
	}

	paidSnapshot := AccountPoolXAIQuotaSnapshot{MediaEligibilityReason: AccountPoolXAIMediaReasonEligible}
	assert.Nil(t, buildAccountPoolXAIFreeUsage24hEstimate(tests[0].account, paidSnapshot, now))
	assert.Nil(t, buildAccountPoolXAIFreeUsage24hEstimate(model.AccountPoolAccount{}, freeSnapshot, now))
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
