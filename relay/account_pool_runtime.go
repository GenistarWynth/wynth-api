package relay

import (
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
	runtimeAccountID        string
	runtimeHeadersOverride  map[string]interface{}
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
	snapshot := snapshotAccountPoolRuntimeRelay(info)
	poolModeRetryIndex := 0
	var poolModeLastAccountID int
	for attemptIndex := 0; ; attemptIndex++ {
		// Pool-mode same-account retry: reuse the previously selected account
		// without re-running selection. The relay snapshot is restored to undo any
		// state mutations from the previous attempt, but selection is NOT re-run,
		// so the same ApiKey/UpstreamModelName/etc. remain from the prior selection.
		isPoolModeRetry := poolModeRetryIndex > 0 && poolModeLastAccountID > 0
		restoreAccountPoolRuntimeRelay(info, snapshot)
		request, newAPIError := requestFactory()
		if newAPIError != nil {
			return newAPIError
		}
		if !isPoolModeRetry {
			if newAPIError := applyAccountPoolRuntimeSelection(c, info, request); newAPIError != nil {
				selectedAccountID := service.GetSelectedAccountPoolAccountID(c)
				accountRetryTimes := service.GetSelectedAccountPoolAccountRetryTimes(c)
				if shouldRecordAccountPoolRuntimeAttempt(info) && selectedAccountID > 0 && !types.IsSkipRetryError(newAPIError) {
					_ = service.RecordAccountPoolRuntimeAttemptFailure(selectedAccountID, newAPIError, common.GetTimestamp())
					service.ForgetSelectedAccountPoolRuntimeAffinity(c)
				}
				if !shouldRetryAccountPoolRuntimeAttempt(info, selectedAccountID, accountRetryTimes, attemptIndex, newAPIError) {
					return newAPIError
				}
				continue
			}
			poolModeLastAccountID = service.GetSelectedAccountPoolAccountID(c)
			poolModeRetryIndex = 0
		}
		selectedAccountID := service.GetSelectedAccountPoolAccountID(c)
		accountRetryTimes := service.GetSelectedAccountPoolAccountRetryTimes(c)

		newAPIError = func() *types.NewAPIError {
			defer service.ReleaseAccountPoolRuntimeSelection(c)
			return attempt(request)
		}()
		if newAPIError == nil {
			if shouldRecordAccountPoolRuntimeAttempt(info) && selectedAccountID > 0 {
				now := common.GetTimestamp()
				_ = service.RecordAccountPoolRuntimeAttemptSuccess(selectedAccountID, now)
				service.RememberSelectedAccountPoolRuntimeAffinity(c, now)
			}
			return nil
		}

		// Check if pool-mode same-account retry applies for this failure.
		if shouldRecordAccountPoolRuntimeAttempt(info) && !types.IsSkipRetryError(newAPIError) &&
			!info.HasSendResponse() {
			runtimeOpts := service.GetSelectedAccountPoolRuntimeOptions(c)
			if runtimeOpts.PoolMode && poolModeRetryIndex < runtimeOpts.PoolModeRetryCount &&
				accountPoolRuntimeStatusCodeInList(newAPIError.StatusCode, runtimeOpts.PoolModeRetryStatusCodes) {
				// Pool-mode retry: same account, no failure recorded, no attempted-set update.
				poolModeRetryIndex++
				continue
			}
		}

		// Normal failure path: record failure and fall through to next-account retry.
		poolModeRetryIndex = 0
		poolModeLastAccountID = 0
		if shouldRecordAccountPoolRuntimeAttempt(info) && selectedAccountID > 0 && !types.IsSkipRetryError(newAPIError) {
			_ = service.RecordAccountPoolRuntimeAttemptFailure(selectedAccountID, newAPIError, common.GetTimestamp())
			service.ForgetSelectedAccountPoolRuntimeAffinity(c)
		}
		if !shouldRetryAccountPoolRuntimeAttempt(info, selectedAccountID, accountRetryTimes, attemptIndex, newAPIError) {
			return newAPIError
		}
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
	snapshot.runtimeAccountID = info.RuntimeAccountID
	snapshot.runtimeHeadersOverride = cloneAccountPoolRuntimeHeadersOverride(info.RuntimeHeadersOverride)
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
	info.RuntimeAccountID = snapshot.runtimeAccountID
	info.RuntimeHeadersOverride = cloneAccountPoolRuntimeHeadersOverride(snapshot.runtimeHeadersOverride)
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
