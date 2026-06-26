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

// accountPoolRuntimeOptions carries per-account-pool-account runtime
// configuration that influences relay behavior (e.g. pool-mode retry logic).
// It is serialized to the account_pool_accounts.runtime_options TEXT column.
type accountPoolRuntimeOptions struct {
	PoolMode                 bool  `json:"pool_mode"`
	PoolModeRetryCount       int   `json:"pool_mode_retry_count"`
	PoolModeRetryStatusCodes []int `json:"pool_mode_retry_status_codes"`
}

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
