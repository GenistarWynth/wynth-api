package service

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// accountPoolFailureState tracks consecutive-failure counters and per-status
// window data for a single account pool account. It is serialized to the
// account_pool_accounts.failure_state TEXT column.
type accountPoolFailureState struct {
	ConsecutiveFailures int   `json:"consecutive_failures"`
	LastStatus          int   `json:"last_status"`
	HTTP403Count        int   `json:"http403_count"`
	HTTP403WindowStart  int64 `json:"http403_window_start"`
	Last401At           int64 `json:"last401_at"`
}

// marshal serializes the state to a JSON string for storage.
func (s accountPoolFailureState) marshal() (string, error) {
	b, err := common.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// parseAccountPoolFailureState deserializes a JSON string from the
// failure_state column. Empty or whitespace-only input returns the zero value
// and nil error.
func parseAccountPoolFailureState(raw string) (accountPoolFailureState, error) {
	if strings.TrimSpace(raw) == "" {
		return accountPoolFailureState{}, nil
	}
	var s accountPoolFailureState
	if err := common.UnmarshalJsonStr(raw, &s); err != nil {
		return accountPoolFailureState{}, err
	}
	return s, nil
}

// parseAccountPoolModelRateLimits deserializes a JSON map of model→resetAt unix seconds
// from the model_rate_limits TEXT column. Empty or whitespace-only input returns an empty
// map and nil error. The map key is the upstream model name; value is the unix timestamp
// after which the per-model block expires.
func parseAccountPoolModelRateLimits(raw string) (map[string]int64, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]int64{}, nil
	}
	m := map[string]int64{}
	if err := common.UnmarshalJsonStr(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// marshalAccountPoolModelRateLimits serializes a model→resetAt map to a JSON string
// suitable for storage in the model_rate_limits TEXT column. An empty or nil map is
// stored as an empty string (not "{}") so the column is visually blank when unused.
func marshalAccountPoolModelRateLimits(m map[string]int64) (string, error) {
	if len(m) == 0 {
		return "", nil
	}
	b, err := common.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// accountPoolModelRateLimited reports whether the given upstream model name is currently
// rate-limited on an account. Returns true if and only if raw can be parsed, the model
// key is present, and the stored resetAt timestamp is strictly greater than now.
func accountPoolModelRateLimited(raw string, model string, now int64) bool {
	if strings.TrimSpace(raw) == "" || model == "" {
		return false
	}
	m, err := parseAccountPoolModelRateLimits(raw)
	if err != nil {
		return false
	}
	resetAt, ok := m[model]
	return ok && resetAt > now
}

// accountPoolRuntimeOptions carries per-account-pool-account runtime
// configuration that influences relay behavior (e.g. pool-mode retry logic).
// It is serialized to the account_pool_accounts.runtime_options TEXT column.
type AccountPoolRuntimeOptions struct {
	PoolMode                 bool                         `json:"pool_mode"`
	PoolModeRetryCount       int                          `json:"pool_mode_retry_count"`
	PoolModeRetryStatusCodes []int                        `json:"pool_mode_retry_status_codes"`
	XAIQuota                 *AccountPoolXAIQuotaSnapshot `json:"xai_quota,omitempty"`
}

type accountPoolRuntimeOptions = AccountPoolRuntimeOptions

// parseAccountPoolRuntimeOptions deserializes a JSON string from the
// runtime_options column. Empty or whitespace-only input returns the zero
// value and nil error.
func parseAccountPoolRuntimeOptions(raw string) (accountPoolRuntimeOptions, error) {
	if strings.TrimSpace(raw) == "" {
		return accountPoolRuntimeOptions{}, nil
	}
	var opts accountPoolRuntimeOptions
	if err := common.UnmarshalJsonStr(raw, &opts); err != nil {
		return accountPoolRuntimeOptions{}, err
	}
	return opts, nil
}
