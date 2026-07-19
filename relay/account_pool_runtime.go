package relay

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func applyAccountPoolRuntimeSelection(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request) *types.NewAPIError {
	err := service.ApplyAccountPoolRuntimeSelection(c, info, request)
	if err == nil {
		return nil
	}
	if shouldRecordAccountPoolRuntimeAttempt(info) {
		service.ForgetSelectedAccountPoolRuntimeAffinity(c)
	}
	// Account-pool selection errors should allow the outer channel retry loop
	// to try another channel. Do not add ErrOptionWithSkipRetry here.
	return types.NewErrorWithStatusCode(
		err,
		types.ErrorCodeGetChannelFailed,
		http.StatusServiceUnavailable,
	)
}

type accountPoolRuntimeRequestFactory func() (dto.Request, *types.NewAPIError)
type accountPoolRuntimeAttemptFunc func(dto.Request) *types.NewAPIError

type accountPoolRuntimeRelaySnapshot struct {
	apiKey                  string
	upstreamModelName       string
	channelSettingProxy     string
	runtimeProxy            string
	runtimeBaseURL          string
	runtimeAccountID        string
	runtimeAnthropicOAuth   bool
	runtimeGeminiOAuth      bool
	runtimeGeminiOAuthType  string
	runtimeGeminiProjectID  string
	runtimeVertexSA         bool
	runtimeVertexProjectID  string
	runtimeVertexLocation   string
	runtimeHeadersOverride  map[string]interface{}
	runtimeAccountHeaders   map[string]interface{}
	useRuntimeHeaders       bool
	isStream                bool
	upstreamRequestBodySize int64
	requestConversionChain  []types.RelayFormat
	finalRequestRelayFormat types.RelayFormat
}

func runAccountPoolRuntimeAttempts(
	c *gin.Context,
	info *relaycommon.RelayInfo,
	requestFactory accountPoolRuntimeRequestFactory,
	attempt accountPoolRuntimeAttemptFunc,
) *types.NewAPIError {
	if requestFactory == nil || attempt == nil {
		return nil
	}

	// Per-user concurrency enforcement: acquire once before the attempt loop so
	// it bounds concurrent REQUESTS per user (not per attempt/retry).
	// Channel-test traffic is exempt, consistent with shouldRecordAccountPoolRuntimeAttempt.
	if !shouldSkipAccountPoolUserConcurrency(info) {
		channelID := accountPoolRuntimeChannelID(c, info)
		if channelID > 0 {
			bindingID, maxUserConc, err := service.GetAccountPoolRuntimeUserConcurrencyConfig(channelID)
			if err == nil && bindingID > 0 && maxUserConc > 0 {
				userID := 0
				if info != nil {
					userID = info.UserId
				}
				release, acquired := service.TryAcquireAccountPoolUserSlot(bindingID, userID, maxUserConc)
				if !acquired {
					return types.NewErrorWithStatusCode(
						errors.New("account pool per-user concurrency limit reached"),
						types.ErrorCodeGetChannelFailed,
						http.StatusServiceUnavailable,
					)
				}
				defer release()
			}
		}
	}

	// FIX 3: Function-level deferred release is the panic-safe backstop.  It
	// releases whatever lease is current when the function returns, covering both
	// the success path and any non-pool-mode give-up path.  Pool-mode iterations
	// skip applyAccountPoolRuntimeSelection, so they retain the existing lease
	// through all same-account retries; the next non-pool-mode iteration calls
	// applyAccountPoolRuntimeSelection, which itself calls
	// ReleaseAccountPoolRuntimeSelection at the top, handing off cleanly.
	defer service.ReleaseAccountPoolRuntimeSelection(c)

	// preSelectionSnapshot captures info state before any account-pool selection.
	// It is used to reset info at the start of each normal (non-pool-mode) iteration
	// so that every inter-account retry begins from a clean channel-level state.
	preSelectionSnapshot := snapshotAccountPoolRuntimeRelay(info)
	// postSelectionSnapshot captures info state after a successful selection.
	// Pool-mode same-account retries restore from this snapshot so that the
	// selected account's ApiKey, UpstreamModelName, RuntimeProxy, and runtime
	// headers are re-applied before each attempt — the pre-selection snapshot
	// would otherwise wipe those credentials back to channel-level values.
	postSelectionSnapshot := preSelectionSnapshot
	// selectedUpstreamModelName remembers the account's mapped upstream model so
	// that the rebuilt request can be updated to target the same model on each
	// pool-mode retry (matching what ApplyAccountPoolRuntimeSelection did on the
	// initial selection).
	selectedUpstreamModelName := ""
	poolModeRetryIndex := 0
	var poolModeLastAccountID int
	// normalAttempts counts only inter-account retry iterations (not pool-mode
	// same-account retries) so that pool-mode retries do not consume the
	// AccountRetryTimes budget.
	normalAttempts := 0
	for {
		// Pool-mode same-account retry: reuse the previously selected account
		// without re-running selection. Restore from the post-selection snapshot
		// so that the selected account's ApiKey/UpstreamModelName/RuntimeProxy/
		// runtime headers are preserved across retries (not wiped by the
		// pre-selection snapshot).
		isPoolModeRetry := poolModeRetryIndex > 0 && poolModeLastAccountID > 0
		if isPoolModeRetry {
			restoreAccountPoolRuntimeRelay(info, postSelectionSnapshot)
		} else {
			restoreAccountPoolRuntimeRelay(info, preSelectionSnapshot)
		}
		request, newAPIError := requestFactory()
		if newAPIError != nil {
			return newAPIError
		}
		if isPoolModeRetry {
			// Re-apply the selected account's upstream model to the freshly built
			// request so the rebuilt request targets the account's model, not the
			// original channel upstream model.
			if request != nil && selectedUpstreamModelName != "" {
				request.SetModelName(selectedUpstreamModelName)
			}
		}
		if !isPoolModeRetry {
			if newAPIError := applyAccountPoolRuntimeSelection(c, info, request); newAPIError != nil {
				selectedAccountID := service.GetSelectedAccountPoolAccountID(c)
				accountRetryTimes := service.GetSelectedAccountPoolAccountRetryTimes(c)
				if shouldRecordAccountPoolRuntimeFailure(c, info) && selectedAccountID > 0 && !types.IsSkipRetryError(newAPIError) {
					// Selection failed before we know the upstream model; pass "".
					_ = service.RecordAccountPoolRuntimeAttemptFailure(selectedAccountID, newAPIError, common.GetTimestamp(), service.GetSelectedAccountPoolPlatform(c), "")
					service.ForgetSelectedAccountPoolRuntimeAffinity(c)
				}
				if !shouldRetryAccountPoolRuntimeAttempt(info, selectedAccountID, accountRetryTimes, normalAttempts, newAPIError) {
					return newAPIError
				}
				normalAttempts++
				continue
			}
			// Capture the post-selection state so pool-mode retries can restore it.
			postSelectionSnapshot = snapshotAccountPoolRuntimeRelay(info)
			selectedUpstreamModelName = info.UpstreamModelName
			poolModeLastAccountID = service.GetSelectedAccountPoolAccountID(c)
			poolModeRetryIndex = 0
		}
		selectedAccountID := service.GetSelectedAccountPoolAccountID(c)
		accountRetryTimes := service.GetSelectedAccountPoolAccountRetryTimes(c)

		// FIX 3: No per-attempt defer here. The function-level defer above is the
		// single release point. Pool-mode iterations hold the lease across retries;
		// non-pool-mode iterations hand off via applyAccountPoolRuntimeSelection.
		newAPIError = attempt(request)
		if newAPIError == nil {
			if shouldRecordAccountPoolRuntimeAttempt(info) && selectedAccountID > 0 {
				now := common.GetTimestamp()
				upstreamModel := ""
				if info != nil {
					upstreamModel = info.UpstreamModelName
				}
				_ = service.RecordAccountPoolRuntimeAttemptSuccess(selectedAccountID, now, upstreamModel)
				service.RememberSelectedAccountPoolRuntimeAffinity(c, now)
				if service.GetSelectedAccountPoolRequestQuota(c) > 0 {
					_ = service.IncrementAccountPoolAccountRequestQuota(selectedAccountID, now)
				}
			}
			return nil
		}

		// FIX 1: Guard against nil info before calling info.HasSendResponse().
		// shouldRecordAccountPoolRuntimeAttempt returns true when info==nil, so
		// info may be nil here; mirror the safe pattern used in
		// shouldRetryAccountPoolRuntimeAttempt.
		// Check if pool-mode same-account retry applies for this failure.
		if shouldRecordAccountPoolRuntimeAttempt(info) && !types.IsSkipRetryError(newAPIError) &&
			(info == nil || !info.HasSendResponse()) {
			runtimeOpts := service.GetSelectedAccountPoolRuntimeOptions(c)
			if runtimeOpts.PoolMode && poolModeRetryIndex < runtimeOpts.PoolModeRetryCount &&
				accountPoolRuntimeStatusCodeInList(newAPIError.StatusCode, runtimeOpts.PoolModeRetryStatusCodes) {
				// Pool-mode retry: same account, no failure recorded, no attempted-set update.
				// normalAttempts is NOT incremented — pool-mode retries don't consume the budget.
				poolModeRetryIndex++
				continue
			}
		}

		// Normal failure path: record failure and fall through to next-account retry.
		poolModeRetryIndex = 0
		poolModeLastAccountID = 0
		if shouldRecordAccountPoolRuntimeFailure(c, info) && selectedAccountID > 0 && !types.IsSkipRetryError(newAPIError) {
			upstreamModel := ""
			if info != nil {
				upstreamModel = info.UpstreamModelName
			}
			_ = service.RecordAccountPoolRuntimeAttemptFailure(selectedAccountID, newAPIError, common.GetTimestamp(), service.GetSelectedAccountPoolPlatform(c), upstreamModel)
			service.ForgetSelectedAccountPoolRuntimeAffinity(c)
		}
		if !shouldRetryAccountPoolRuntimeAttempt(info, selectedAccountID, accountRetryTimes, normalAttempts, newAPIError) {
			return newAPIError
		}
		// FIX 2: Advance the normal (inter-account) retry counter only here.
		normalAttempts++
	}
}

// accountPoolRuntimeStatusCodeInList returns true if statusCode is found in codes.
// If codes is empty, returns false (no match).
func accountPoolRuntimeStatusCodeInList(statusCode int, codes []int) bool {
	for _, code := range codes {
		if code == statusCode {
			return true
		}
	}
	return false
}

func shouldRecordAccountPoolRuntimeFailure(c *gin.Context, info *relaycommon.RelayInfo) bool {
	if !shouldRecordAccountPoolRuntimeAttempt(info) {
		return false
	}
	return c == nil || c.Request == nil || c.Request.Context().Err() == nil
}

func shouldRecordAccountPoolRuntimeAttempt(info *relaycommon.RelayInfo) bool {
	return info == nil || !info.IsChannelTest
}

func snapshotAccountPoolRuntimeRelay(info *relaycommon.RelayInfo) accountPoolRuntimeRelaySnapshot {
	snapshot := accountPoolRuntimeRelaySnapshot{}
	if info == nil {
		return snapshot
	}
	if info.ChannelMeta != nil {
		snapshot.apiKey = info.ApiKey
		snapshot.upstreamModelName = info.UpstreamModelName
		snapshot.channelSettingProxy = info.ChannelSetting.Proxy
	}
	snapshot.runtimeProxy = info.RuntimeProxy
	snapshot.runtimeBaseURL = info.RuntimeBaseURL
	snapshot.runtimeAccountID = info.RuntimeAccountID
	snapshot.runtimeAnthropicOAuth = info.RuntimeAnthropicOAuth
	snapshot.runtimeGeminiOAuth = info.RuntimeGeminiOAuth
	snapshot.runtimeGeminiOAuthType = info.RuntimeGeminiOAuthType
	snapshot.runtimeGeminiProjectID = info.RuntimeGeminiProjectID
	snapshot.runtimeVertexSA = info.RuntimeVertexServiceAccount
	snapshot.runtimeVertexProjectID = info.RuntimeVertexProjectID
	snapshot.runtimeVertexLocation = info.RuntimeVertexLocation
	snapshot.runtimeHeadersOverride = cloneAccountPoolRuntimeHeadersOverride(info.RuntimeHeadersOverride)
	snapshot.runtimeAccountHeaders = cloneAccountPoolRuntimeHeadersOverride(info.RuntimeAccountHeadersOverride)
	snapshot.useRuntimeHeaders = info.UseRuntimeHeadersOverride
	snapshot.isStream = info.IsStream
	snapshot.upstreamRequestBodySize = info.UpstreamRequestBodySize
	snapshot.finalRequestRelayFormat = info.FinalRequestRelayFormat
	if len(info.RequestConversionChain) > 0 {
		snapshot.requestConversionChain = append([]types.RelayFormat(nil), info.RequestConversionChain...)
	}
	return snapshot
}

func restoreAccountPoolRuntimeRelay(info *relaycommon.RelayInfo, snapshot accountPoolRuntimeRelaySnapshot) {
	if info == nil {
		return
	}
	if info.ChannelMeta != nil {
		info.ApiKey = snapshot.apiKey
		info.UpstreamModelName = snapshot.upstreamModelName
		info.ChannelSetting.Proxy = snapshot.channelSettingProxy
	}
	info.RuntimeProxy = snapshot.runtimeProxy
	info.RuntimeBaseURL = snapshot.runtimeBaseURL
	info.RuntimeAccountID = snapshot.runtimeAccountID
	info.RuntimeAnthropicOAuth = snapshot.runtimeAnthropicOAuth
	info.RuntimeGeminiOAuth = snapshot.runtimeGeminiOAuth
	info.RuntimeGeminiOAuthType = snapshot.runtimeGeminiOAuthType
	info.RuntimeGeminiProjectID = snapshot.runtimeGeminiProjectID
	info.RuntimeVertexServiceAccount = snapshot.runtimeVertexSA
	info.RuntimeVertexProjectID = snapshot.runtimeVertexProjectID
	info.RuntimeVertexLocation = snapshot.runtimeVertexLocation
	info.RuntimeHeadersOverride = cloneAccountPoolRuntimeHeadersOverride(snapshot.runtimeHeadersOverride)
	info.RuntimeAccountHeadersOverride = cloneAccountPoolRuntimeHeadersOverride(snapshot.runtimeAccountHeaders)
	info.UseRuntimeHeadersOverride = snapshot.useRuntimeHeaders
	info.IsStream = snapshot.isStream
	info.UpstreamRequestBodySize = snapshot.upstreamRequestBodySize
	info.FinalRequestRelayFormat = snapshot.finalRequestRelayFormat
	if len(snapshot.requestConversionChain) > 0 {
		info.RequestConversionChain = append([]types.RelayFormat(nil), snapshot.requestConversionChain...)
	} else {
		info.RequestConversionChain = nil
	}
}

func cloneAccountPoolRuntimeHeadersOverride(headers map[string]interface{}) map[string]interface{} {
	if headers == nil {
		return nil
	}
	cloned := make(map[string]interface{}, len(headers))
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
}

func shouldRetryAccountPoolRuntimeAttempt(info *relaycommon.RelayInfo, selectedAccountID int, accountRetryTimes int, attemptIndex int, err *types.NewAPIError) bool {
	if err == nil || selectedAccountID <= 0 || accountRetryTimes <= 0 || attemptIndex >= accountRetryTimes {
		return false
	}
	if types.IsSkipRetryError(err) {
		return false
	}
	if info != nil && info.HasSendResponse() {
		return false
	}
	if err.GetErrorCode() == types.ErrorCodeDoRequestFailed {
		return true
	}
	statusCode := err.StatusCode
	if statusCode < 100 || statusCode > 599 {
		return true
	}
	switch statusCode {
	case http.StatusRequestTimeout, http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return true
	}
	return statusCode >= http.StatusInternalServerError
}

// shouldSkipAccountPoolUserConcurrency reports whether per-user concurrency
// enforcement must be skipped. A nil info means the request is not a channel-test
// and enforcement applies; channel-test traffic is always exempt so that admin
// health checks are never blocked by user quota. Consistent with shouldRecordAccountPoolRuntimeAttempt.
func shouldSkipAccountPoolUserConcurrency(info *relaycommon.RelayInfo) bool {
	return info != nil && info.IsChannelTest
}

func accountPoolRuntimeChannelID(c *gin.Context, info *relaycommon.RelayInfo) int {
	if info != nil && info.ChannelMeta != nil && info.ChannelId > 0 {
		return info.ChannelId
	}
	return common.GetContextKeyInt(c, constant.ContextKeyChannelId)
}

func rejectUnsupportedAccountPoolRuntime(c *gin.Context, info *relaycommon.RelayInfo, relayName string) *types.NewAPIError {
	channelID := accountPoolRuntimeChannelID(c, info)
	if channelID <= 0 {
		return nil
	}
	enabled, err := service.AccountPoolRuntimeEnabledForChannel(channelID)
	if err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeGetChannelFailed, http.StatusServiceUnavailable)
	}
	if !enabled {
		return nil
	}
	return types.NewErrorWithStatusCode(
		fmt.Errorf("account pool runtime does not support %s relay yet", relayName),
		types.ErrorCodeGetChannelFailed,
		http.StatusServiceUnavailable,
	)
}
