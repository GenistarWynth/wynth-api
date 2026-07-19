package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"golang.org/x/sync/singleflight"
)

const (
	accountPoolXAIQuotaDefaultBaseURL = "https://cli-chat-proxy.grok.com/v1"
	accountPoolXAIQuotaProbeModel     = "grok-4.5"
	accountPoolXAIQuotaTimeout        = 20 * time.Second
	accountPoolXAIQuotaMaxBodyBytes   = int64(1 << 20)

	AccountPoolXAIMediaReasonEligible     = "eligible"
	AccountPoolXAIMediaReasonFreeTier     = "billing_free_tier"
	AccountPoolXAIMediaReasonForbidden    = "billing_forbidden"
	AccountPoolXAIMediaReasonInconclusive = "billing_inconclusive"
	AccountPoolXAIMediaReasonUnobserved   = "billing_unobserved"

	AccountPoolXAIFreeUsageSourceLogs24h         = "logs_24h"
	AccountPoolXAIFreeUsageSourceCounterEstimate = "counter_estimate"
)

const accountPoolXAIFreeUsageWindowSeconds = int64(24 * time.Hour / time.Second)

type AccountPoolXAIQuotaWindow struct {
	Limit     *int64 `json:"limit,omitempty"`
	Remaining *int64 `json:"remaining,omitempty"`
	ResetUnix *int64 `json:"reset_unix,omitempty"`
	ResetAt   string `json:"reset_at,omitempty"`
}

type AccountPoolXAIBillingSnapshot struct {
	UsagePercent      *float64 `json:"usage_percent,omitempty"`
	MonthlyLimitCents *float64 `json:"monthly_limit_cents,omitempty"`
	UsedCents         *float64 `json:"used_cents,omitempty"`
	UsedPercent       *float64 `json:"used_percent,omitempty"`
	Plan              string   `json:"plan,omitempty"`
	WeeklyStatusCode  int      `json:"weekly_status_code,omitempty"`
	MonthlyStatusCode int      `json:"monthly_status_code,omitempty"`
	Partial           bool     `json:"partial,omitempty"`
}

type AccountPoolXAIFreeUsageEstimate struct {
	Source             string `json:"source"`
	WindowSeconds      int64  `json:"window_seconds"`
	ObservationSeconds int64  `json:"observation_seconds"`
	Requests           int64  `json:"requests"`
	PromptTokens       int64  `json:"prompt_tokens"`
	CompletionTokens   int64  `json:"completion_tokens"`
	Tokens             int64  `json:"tokens"`
	Estimated          bool   `json:"estimated"`
}

type AccountPoolXAIQuotaSnapshot struct {
	Source                 string                           `json:"source"`
	Model                  string                           `json:"model,omitempty"`
	Billing                *AccountPoolXAIBillingSnapshot   `json:"billing,omitempty"`
	Requests               *AccountPoolXAIQuotaWindow       `json:"requests,omitempty"`
	Tokens                 *AccountPoolXAIQuotaWindow       `json:"tokens,omitempty"`
	RetryAfterSeconds      *int                             `json:"retry_after_seconds,omitempty"`
	StatusCode             int                              `json:"status_code,omitempty"`
	HeadersObserved        bool                             `json:"headers_observed"`
	MediaEligible          *bool                            `json:"media_eligible,omitempty"`
	MediaEligibilityReason string                           `json:"media_eligibility_reason,omitempty"`
	FetchedAt              int64                            `json:"fetched_at"`
	ProbeError             string                           `json:"probe_error,omitempty"`
	FreeUsage24hEstimate   *AccountPoolXAIFreeUsageEstimate `json:"free_usage_24h_estimate,omitempty"`
}

type accountPoolXAIQuotaHTTPClientFactory func(string) (*http.Client, error)

type accountPoolXAIQuotaProber struct {
	baseURL       string
	clientFactory accountPoolXAIQuotaHTTPClientFactory
	now           func() time.Time
	flight        singleflight.Group
}

var (
	defaultAccountPoolXAIQuotaProber = newDefaultAccountPoolXAIQuotaProber()
	accountPoolXAIFreeUsageNow       = time.Now
	accountPoolXAIUsage24hLoader     = model.GetAccountPoolUsage24h
)

type accountPoolXAIBillingProbePart struct {
	snapshot *AccountPoolXAIBillingSnapshot
	status   int
	err      error
}

type accountPoolXAIBillingPayload struct {
	Config *struct {
		CreditUsagePercent *float64        `json:"creditUsagePercent"`
		MonthlyLimit       json.RawMessage `json:"monthlyLimit"`
		Used               json.RawMessage `json:"used"`
	} `json:"config"`
}

func newAccountPoolXAIQuotaProber(
	baseURL string,
	clientFactory accountPoolXAIQuotaHTTPClientFactory,
	now func() time.Time,
) *accountPoolXAIQuotaProber {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = accountPoolXAIQuotaDefaultBaseURL
	}
	if clientFactory == nil {
		clientFactory = GetHttpClientWithProxy
	}
	if now == nil {
		now = time.Now
	}
	return &accountPoolXAIQuotaProber{
		baseURL:       strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		clientFactory: clientFactory,
		now:           now,
	}
}

func newDefaultAccountPoolXAIQuotaProber() *accountPoolXAIQuotaProber {
	return newAccountPoolXAIQuotaProber(accountPoolXAIQuotaDefaultBaseURL, nil, nil)
}

func (s AccountPoolService) ProbeXAIQuota(ctx context.Context, poolID int, accountID int) (AccountPoolXAIQuotaSnapshot, error) {
	return defaultAccountPoolXAIQuotaProber.Probe(ctx, poolID, accountID)
}

func (p *accountPoolXAIQuotaProber) Probe(ctx context.Context, poolID int, accountID int) (AccountPoolXAIQuotaSnapshot, error) {
	if p == nil {
		return AccountPoolXAIQuotaSnapshot{}, errors.New("xai quota prober is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	key := strconv.Itoa(poolID) + ":" + strconv.Itoa(accountID)
	resultChannel := p.flight.DoChan(key, func() (any, error) {
		probeCtx, cancel := context.WithTimeout(context.Background(), accountPoolXAIQuotaTimeout)
		defer cancel()
		return p.probe(probeCtx, poolID, accountID)
	})
	select {
	case <-ctx.Done():
		return AccountPoolXAIQuotaSnapshot{}, ctx.Err()
	case result := <-resultChannel:
		if result.Err != nil {
			return AccountPoolXAIQuotaSnapshot{}, result.Err
		}
		snapshot, ok := result.Val.(AccountPoolXAIQuotaSnapshot)
		if !ok {
			return AccountPoolXAIQuotaSnapshot{}, errors.New("xai quota probe returned an invalid result")
		}
		return snapshot, nil
	}
}

func (p *accountPoolXAIQuotaProber) probe(ctx context.Context, poolID int, accountID int) (AccountPoolXAIQuotaSnapshot, error) {
	pool, account, credential, tokenState, err := loadAccountPoolXAIQuotaAccount(poolID, accountID)
	if err != nil {
		return AccountPoolXAIQuotaSnapshot{}, err
	}
	proxyURL, err := ResolveAccountPoolRuntimeProxyURL(account.ProxyID, pool.DefaultProxyID)
	if err != nil {
		return AccountPoolXAIQuotaSnapshot{}, fmt.Errorf("resolve xai quota proxy: %w", err)
	}
	accessToken, err := ResolveAccountPoolRuntimeCredential(ctx, AccountPoolRuntimeCredentialRequest{
		AccountID:  account.Id,
		Credential: credential,
		TokenState: tokenState,
		ProxyURL:   proxyURL,
		Platform:   model.AccountPoolPlatformXAI,
		Now:        p.now().Unix(),
	})
	if err != nil {
		return AccountPoolXAIQuotaSnapshot{}, fmt.Errorf("acquire xai quota credential: %w", err)
	}
	if strings.TrimSpace(accessToken) == "" {
		return AccountPoolXAIQuotaSnapshot{}, errors.New("xai quota access token is unavailable")
	}
	client, err := p.clientFactory(proxyURL)
	if err != nil {
		return AccountPoolXAIQuotaSnapshot{}, fmt.Errorf("create xai quota client: %w", err)
	}
	if client == nil {
		client = http.DefaultClient
	}
	clientCopy := *client
	clientCopy.Timeout = accountPoolXAIQuotaTimeout

	billing, billingErr := p.probeBilling(ctx, &clientCopy, accessToken)
	mediaEligible, mediaReason := accountPoolXAIMediaEligibility(billing)
	now := p.now().UTC()
	snapshot := AccountPoolXAIQuotaSnapshot{
		Source:                 "billing_probe",
		Billing:                billing,
		MediaEligible:          mediaEligible,
		MediaEligibilityReason: mediaReason,
		FetchedAt:              now.Unix(),
	}
	if billing != nil {
		snapshot.StatusCode = preferredAccountPoolXAIBillingStatus(billing)
	}
	if billingErr == nil && accountPoolXAIBillingHasAuthoritativeQuota(billing) {
		if err := persistAccountPoolXAIQuotaSnapshot(account, snapshot, now); err != nil {
			return AccountPoolXAIQuotaSnapshot{}, err
		}
		return enrichAccountPoolXAIFreeUsage24hEstimate(snapshot, account, now), nil
	}

	activeSnapshot, activeErr := p.probeUsage(ctx, &clientCopy, accessToken)
	if activeErr != nil {
		snapshot.ProbeError = sanitizeAccountPoolRuntimeErrorMessage(activeErr.Error(), 240)
		_ = persistAccountPoolXAIQuotaSnapshot(account, snapshot, now)
		if billingErr != nil {
			return AccountPoolXAIQuotaSnapshot{}, errors.New("xai quota billing and active probes failed")
		}
		return AccountPoolXAIQuotaSnapshot{}, activeErr
	}
	activeSnapshot.Source = "hybrid_probe"
	activeSnapshot.Billing = billing
	activeSnapshot.MediaEligible = mediaEligible
	activeSnapshot.MediaEligibilityReason = mediaReason
	activeSnapshot.FetchedAt = now.Unix()
	if err := persistAccountPoolXAIQuotaSnapshot(account, activeSnapshot, now); err != nil {
		return AccountPoolXAIQuotaSnapshot{}, err
	}
	return enrichAccountPoolXAIFreeUsage24hEstimate(activeSnapshot, account, now), nil
}

func enrichAccountPoolXAIFreeUsage24hEstimate(snapshot AccountPoolXAIQuotaSnapshot, account model.AccountPoolAccount, now time.Time) AccountPoolXAIQuotaSnapshot {
	if snapshot.MediaEligibilityReason == AccountPoolXAIMediaReasonFreeTier && account.Id > 0 && account.PoolID > 0 {
		usage, err := accountPoolXAIUsage24hLoader(account.PoolID, account.Id, now.Unix())
		if err == nil && usage.HasLogs {
			snapshot.FreeUsage24hEstimate = &AccountPoolXAIFreeUsageEstimate{
				Source:             AccountPoolXAIFreeUsageSourceLogs24h,
				WindowSeconds:      accountPoolXAIFreeUsageWindowSeconds,
				ObservationSeconds: accountPoolXAIFreeUsageWindowSeconds,
				Requests:           usage.Requests,
				PromptTokens:       usage.PromptTokens,
				CompletionTokens:   usage.CompletionTokens,
				Tokens:             accountPoolXAISaturatingCounterSum(usage.PromptTokens, usage.CompletionTokens),
			}
			return snapshot
		}
	}
	snapshot.FreeUsage24hEstimate = buildAccountPoolXAIFreeUsage24hEstimate(account, snapshot, now)
	return snapshot
}

func buildAccountPoolXAIFreeUsage24hEstimate(account model.AccountPoolAccount, snapshot AccountPoolXAIQuotaSnapshot, now time.Time) *AccountPoolXAIFreeUsageEstimate {
	if snapshot.MediaEligibilityReason != AccountPoolXAIMediaReasonFreeTier {
		return nil
	}
	nowUnix := now.Unix()
	if account.CreatedTime <= 0 || account.CreatedTime >= nowUnix {
		return nil
	}
	observationSeconds := nowUnix - account.CreatedTime
	estimate := &AccountPoolXAIFreeUsageEstimate{
		WindowSeconds:      accountPoolXAIFreeUsageWindowSeconds,
		ObservationSeconds: observationSeconds,
	}
	if observationSeconds > accountPoolXAIFreeUsageWindowSeconds &&
		account.LastSuccessAt > 0 && account.LastSuccessAt < nowUnix-accountPoolXAIFreeUsageWindowSeconds {
		estimate.Source = AccountPoolXAIFreeUsageSourceCounterEstimate
		return estimate
	}

	requests := max(account.SuccessCount, int64(0))
	promptTokens := max(account.TotalPromptTokens, int64(0))
	completionTokens := max(account.TotalCompletionTokens, int64(0))
	if observationSeconds <= accountPoolXAIFreeUsageWindowSeconds {
		estimate.Source = AccountPoolXAIFreeUsageSourceCounterEstimate
		estimate.Requests = requests
		estimate.PromptTokens = promptTokens
		estimate.CompletionTokens = completionTokens
	} else {
		estimate.Source = AccountPoolXAIFreeUsageSourceCounterEstimate
		estimate.Estimated = true
		estimate.Requests = accountPoolXAIProrateCounter(requests, accountPoolXAIFreeUsageWindowSeconds, observationSeconds)
		estimate.PromptTokens = accountPoolXAIProrateCounter(promptTokens, accountPoolXAIFreeUsageWindowSeconds, observationSeconds)
		estimate.CompletionTokens = accountPoolXAIProrateCounter(completionTokens, accountPoolXAIFreeUsageWindowSeconds, observationSeconds)
	}
	estimate.Tokens = accountPoolXAISaturatingCounterSum(estimate.PromptTokens, estimate.CompletionTokens)
	return estimate
}

func accountPoolXAIProrateCounter(value int64, numerator int64, denominator int64) int64 {
	if value <= 0 || numerator <= 0 || denominator <= 0 {
		return 0
	}
	if numerator >= denominator {
		return value
	}
	high, low := bits.Mul64(uint64(value), uint64(numerator))
	quotient, _ := bits.Div64(high, low, uint64(denominator))
	return int64(quotient)
}

func accountPoolXAISaturatingCounterSum(left int64, right int64) int64 {
	if left <= 0 {
		return max(right, int64(0))
	}
	if right <= 0 {
		return left
	}
	if left > math.MaxInt64-right {
		return math.MaxInt64
	}
	return left + right
}

func loadAccountPoolXAIQuotaAccount(poolID int, accountID int) (model.AccountPool, model.AccountPoolAccount, AccountPoolCredentialConfig, AccountPoolTokenState, error) {
	pool, err := getAccountPoolExistingPool(poolID)
	if err != nil {
		return pool, model.AccountPoolAccount{}, AccountPoolCredentialConfig{}, AccountPoolTokenState{}, err
	}
	if pool.Platform != model.AccountPoolPlatformXAI {
		return pool, model.AccountPoolAccount{}, AccountPoolCredentialConfig{}, AccountPoolTokenState{}, errors.New("account pool is not an xai pool")
	}
	account, err := getAccountPoolAccountForPool(poolID, accountID)
	if err != nil {
		return pool, account, AccountPoolCredentialConfig{}, AccountPoolTokenState{}, err
	}
	credential, err := DecryptAccountPoolCredentialConfig(account.CredentialConfig)
	if err != nil {
		return pool, account, credential, AccountPoolTokenState{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(credential.Type), AccountPoolCredentialTypeOAuth) {
		return pool, account, credential, AccountPoolTokenState{}, errors.New("account is not an xai oauth account")
	}
	tokenState, err := DecryptAccountPoolTokenState(account.TokenState)
	return pool, account, credential, tokenState, err
}

func (p *accountPoolXAIQuotaProber) probeBilling(ctx context.Context, client *http.Client, token string) (*AccountPoolXAIBillingSnapshot, error) {
	weekly := p.fetchBilling(ctx, client, token, true)
	monthly := p.fetchBilling(ctx, client, token, false)
	billing := &AccountPoolXAIBillingSnapshot{
		WeeklyStatusCode:  weekly.status,
		MonthlyStatusCode: monthly.status,
		Partial:           weekly.err != nil || monthly.err != nil,
	}
	if weekly.snapshot != nil {
		billing.UsagePercent = weekly.snapshot.UsagePercent
	}
	if monthly.snapshot != nil {
		billing.MonthlyLimitCents = monthly.snapshot.MonthlyLimitCents
		billing.UsedCents = monthly.snapshot.UsedCents
		billing.UsedPercent = monthly.snapshot.UsedPercent
		billing.Plan = monthly.snapshot.Plan
	}
	if weekly.err != nil && monthly.err != nil {
		return billing, errors.New("xai billing endpoints are unavailable")
	}
	return billing, nil
}

func (p *accountPoolXAIQuotaProber) fetchBilling(ctx context.Context, client *http.Client, token string, credits bool) accountPoolXAIBillingProbePart {
	target := p.baseURL + "/billing"
	if credits {
		target += "?format=credits"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return accountPoolXAIBillingProbePart{err: err}
	}
	applyAccountPoolXAIQuotaHeaders(req.Header, token)
	resp, err := client.Do(req)
	if err != nil {
		return accountPoolXAIBillingProbePart{err: errors.New("xai billing request failed")}
	}
	defer resp.Body.Close()
	part := accountPoolXAIBillingProbePart{status: resp.StatusCode}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		part.err = fmt.Errorf("xai billing returned status %d", resp.StatusCode)
		return part
	}
	var payload accountPoolXAIBillingPayload
	if err := common.DecodeJson(io.LimitReader(resp.Body, accountPoolXAIQuotaMaxBodyBytes), &payload); err != nil {
		part.err = errors.New("xai billing response is invalid")
		return part
	}
	part.snapshot = buildAccountPoolXAIBillingSnapshot(payload.Config, credits)
	return part
}

func buildAccountPoolXAIBillingSnapshot(config *struct {
	CreditUsagePercent *float64        `json:"creditUsagePercent"`
	MonthlyLimit       json.RawMessage `json:"monthlyLimit"`
	Used               json.RawMessage `json:"used"`
}, credits bool) *AccountPoolXAIBillingSnapshot {
	snapshot := &AccountPoolXAIBillingSnapshot{}
	if config == nil {
		return snapshot
	}
	if credits {
		snapshot.UsagePercent = config.CreditUsagePercent
		return snapshot
	}
	snapshot.MonthlyLimitCents = accountPoolXAIJSONFloat(config.MonthlyLimit)
	snapshot.UsedCents = accountPoolXAIJSONFloat(config.Used)
	if snapshot.MonthlyLimitCents != nil && *snapshot.MonthlyLimitCents > 0 && snapshot.UsedCents != nil {
		included := math.Min(*snapshot.UsedCents, *snapshot.MonthlyLimitCents)
		usedPercent := included / *snapshot.MonthlyLimitCents * 100
		snapshot.UsedPercent = &usedPercent
	}
	if snapshot.MonthlyLimitCents != nil {
		switch math.Round(*snapshot.MonthlyLimitCents) {
		case 15_000:
			snapshot.Plan = "SuperGrok"
		case 150_000:
			snapshot.Plan = "SuperGrok Heavy"
		}
	}
	return snapshot
}

func accountPoolXAIJSONFloat(raw json.RawMessage) *float64 {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var object struct {
		Val any `json:"val"`
	}
	if err := common.Unmarshal(raw, &object); err == nil && object.Val != nil {
		return accountPoolXAIAnyFloat(object.Val)
	}
	var value any
	if err := common.Unmarshal(raw, &value); err != nil {
		return nil
	}
	return accountPoolXAIAnyFloat(value)
}

func accountPoolXAIAnyFloat(value any) *float64 {
	var result float64
	switch value := value.(type) {
	case float64:
		result = value
	case json.Number:
		parsed, err := value.Float64()
		if err != nil {
			return nil
		}
		result = parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return nil
		}
		result = parsed
	default:
		return nil
	}
	return &result
}

func (p *accountPoolXAIQuotaProber) probeUsage(ctx context.Context, client *http.Client, token string) (AccountPoolXAIQuotaSnapshot, error) {
	body, err := common.Marshal(map[string]any{
		"model":  accountPoolXAIQuotaProbeModel,
		"input":  "hi",
		"stream": true,
	})
	if err != nil {
		return AccountPoolXAIQuotaSnapshot{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return AccountPoolXAIQuotaSnapshot{}, err
	}
	applyAccountPoolXAIQuotaHeaders(req.Header, token)
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		return AccountPoolXAIQuotaSnapshot{}, errors.New("xai active quota request failed")
	}
	defer resp.Body.Close()
	snapshot := observeAccountPoolXAIQuotaHeaders(resp.Header, resp.StatusCode, p.now())
	snapshot.Source = "active_probe"
	snapshot.Model = accountPoolXAIQuotaProbeModel
	snapshot.FetchedAt = p.now().Unix()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == http.StatusTooManyRequests {
		return snapshot, nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return snapshot, fmt.Errorf("xai active quota probe returned status %d", resp.StatusCode)
	}
	return snapshot, nil
}

func applyAccountPoolXAIQuotaHeaders(headers http.Header, token string) {
	headers.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "application/json")
	headers.Set("x-xai-token-auth", "xai-grok-cli")
	headers.Set("x-grok-client-version", "0.2.93")
	headers.Set("User-Agent", "grok-pager/0.2.93 grok-shell/0.2.93 (macos; aarch64)")
}

func observeAccountPoolXAIQuotaHeaders(headers http.Header, statusCode int, now time.Time) AccountPoolXAIQuotaSnapshot {
	requests := parseAccountPoolXAIQuotaWindow(headers, "requests")
	tokens := parseAccountPoolXAIQuotaWindow(headers, "tokens")
	retryAfter := parseAccountPoolXAIRetryAfter(headers.Get("retry-after"), now)
	return AccountPoolXAIQuotaSnapshot{
		Requests:          requests,
		Tokens:            tokens,
		RetryAfterSeconds: retryAfter,
		StatusCode:        statusCode,
		HeadersObserved:   requests != nil || tokens != nil || retryAfter != nil,
	}
}

func parseAccountPoolXAIQuotaWindow(headers http.Header, dimension string) *AccountPoolXAIQuotaWindow {
	window := &AccountPoolXAIQuotaWindow{
		Limit:     parseAccountPoolXAIInt64(headers.Get("x-ratelimit-limit-" + dimension)),
		Remaining: parseAccountPoolXAIInt64(headers.Get("x-ratelimit-remaining-" + dimension)),
		ResetUnix: parseAccountPoolXAIReset(headers.Get("x-ratelimit-reset-" + dimension)),
	}
	if window.Limit == nil && window.Remaining == nil && window.ResetUnix == nil {
		return nil
	}
	if window.ResetUnix != nil {
		window.ResetAt = time.Unix(*window.ResetUnix, 0).UTC().Format(time.RFC3339)
	}
	return window
}

func parseAccountPoolXAIInt64(raw string) *int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return nil
	}
	return &value
}

func parseAccountPoolXAIReset(raw string) *int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if value, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if value > 1_000_000_000_000 {
			value /= 1000
		}
		return &value
	}
	if value, err := time.Parse(time.RFC3339, raw); err == nil {
		unix := value.Unix()
		return &unix
	}
	return nil
}

func parseAccountPoolXAIRetryAfter(raw string, now time.Time) *int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if value, err := strconv.Atoi(raw); err == nil {
		if value < 0 {
			value = 0
		}
		return &value
	}
	if value, err := http.ParseTime(raw); err == nil {
		seconds := int(value.Sub(now).Seconds())
		if seconds < 0 {
			seconds = 0
		}
		return &seconds
	}
	return nil
}

func accountPoolXAIBillingHasAuthoritativeQuota(billing *AccountPoolXAIBillingSnapshot) bool {
	return billing != nil && (billing.UsagePercent != nil || billing.UsedPercent != nil ||
		(billing.MonthlyLimitCents != nil && *billing.MonthlyLimitCents > 0) || strings.TrimSpace(billing.Plan) != "")
}

func accountPoolXAIMediaEligibility(billing *AccountPoolXAIBillingSnapshot) (*bool, string) {
	if billing == nil {
		return nil, AccountPoolXAIMediaReasonUnobserved
	}
	if billing.WeeklyStatusCode == http.StatusForbidden || billing.MonthlyStatusCode == http.StatusForbidden {
		eligible := false
		return &eligible, AccountPoolXAIMediaReasonForbidden
	}
	if billing.Partial || billing.WeeklyStatusCode < http.StatusOK || billing.WeeklyStatusCode >= http.StatusMultipleChoices ||
		billing.MonthlyStatusCode < http.StatusOK || billing.MonthlyStatusCode >= http.StatusMultipleChoices {
		return nil, AccountPoolXAIMediaReasonInconclusive
	}
	if accountPoolXAIBillingHasAuthoritativeQuota(billing) {
		eligible := true
		return &eligible, AccountPoolXAIMediaReasonEligible
	}
	eligible := false
	return &eligible, AccountPoolXAIMediaReasonFreeTier
}

func preferredAccountPoolXAIBillingStatus(billing *AccountPoolXAIBillingSnapshot) int {
	if billing == nil {
		return 0
	}
	if billing.WeeklyStatusCode == http.StatusForbidden || billing.MonthlyStatusCode == http.StatusForbidden {
		return http.StatusForbidden
	}
	if billing.WeeklyStatusCode != 0 {
		return billing.WeeklyStatusCode
	}
	return billing.MonthlyStatusCode
}

func persistAccountPoolXAIQuotaSnapshot(account model.AccountPoolAccount, snapshot AccountPoolXAIQuotaSnapshot, observedAt time.Time) error {
	snapshot.FreeUsage24hEstimate = nil
	options, err := parseAccountPoolRuntimeOptions(account.RuntimeOptions)
	if err != nil {
		return fmt.Errorf("parse account pool runtime options: %w", err)
	}
	options.XAIQuota = &snapshot
	encoded, err := common.Marshal(options)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"runtime_options": string(encoded),
		"updated_time":    observedAt.Unix(),
	}
	resetAt, exhausted := accountPoolXAIQuotaExhaustedUntil(snapshot, observedAt)
	if exhausted && resetAt > account.RateLimitedUntil {
		updates["rate_limited_until"] = resetAt
	}
	if !exhausted && account.RateLimitedUntil > observedAt.Unix() && accountPoolXAIQuotaShowsRecovery(snapshot) {
		updates["rate_limited_until"] = int64(0)
	}
	tx := model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ? AND pool_id = ? AND runtime_options = ? AND rate_limited_until = ?", account.Id, account.PoolID, account.RuntimeOptions, account.RateLimitedUntil).
		Updates(updates)
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return errors.New("account changed while persisting xai quota snapshot")
	}
	return nil
}

func accountPoolXAIQuotaExhaustedUntil(snapshot AccountPoolXAIQuotaSnapshot, now time.Time) (int64, bool) {
	exhausted := snapshot.StatusCode == http.StatusTooManyRequests || accountPoolXAIQuotaWindowExhausted(snapshot.Requests) || accountPoolXAIQuotaWindowExhausted(snapshot.Tokens)
	if !exhausted {
		return 0, false
	}
	resetAt := now.Add(time.Minute).Unix()
	for _, window := range []*AccountPoolXAIQuotaWindow{snapshot.Requests, snapshot.Tokens} {
		if window != nil && window.ResetUnix != nil && *window.ResetUnix > resetAt {
			resetAt = *window.ResetUnix
		}
	}
	if snapshot.RetryAfterSeconds != nil {
		retryAt := now.Add(time.Duration(*snapshot.RetryAfterSeconds) * time.Second).Unix()
		if retryAt > resetAt {
			resetAt = retryAt
		}
	}
	return resetAt, true
}

func accountPoolXAIQuotaWindowExhausted(window *AccountPoolXAIQuotaWindow) bool {
	return window != nil && window.Remaining != nil && *window.Remaining <= 0
}

func accountPoolXAIQuotaShowsRecovery(snapshot AccountPoolXAIQuotaSnapshot) bool {
	if snapshot.StatusCode < http.StatusOK || snapshot.StatusCode >= http.StatusMultipleChoices {
		return false
	}
	for _, window := range []*AccountPoolXAIQuotaWindow{snapshot.Requests, snapshot.Tokens} {
		if window != nil && window.Remaining != nil && *window.Remaining > 0 {
			return true
		}
	}
	return false
}

func (s AccountPoolService) GetXAIQuotaSnapshot(poolID int, accountID int) (*AccountPoolXAIQuotaSnapshot, error) {
	_, account, _, _, err := loadAccountPoolXAIQuotaAccount(poolID, accountID)
	if err != nil {
		return nil, err
	}
	options, err := parseAccountPoolRuntimeOptions(account.RuntimeOptions)
	if err != nil {
		return nil, err
	}
	if options.XAIQuota == nil {
		return nil, nil
	}
	snapshot := enrichAccountPoolXAIFreeUsage24hEstimate(*options.XAIQuota, account, accountPoolXAIFreeUsageNow().UTC())
	return &snapshot, nil
}
