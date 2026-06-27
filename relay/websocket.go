package relay

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// markSkipRetry flags an error so the account-pool retry loop will not switch
// pooled accounts. Used once a live upstream socket has been established, after
// which retrying on a different account would corrupt the client session.
func markSkipRetry(err *types.NewAPIError) {
	if err == nil {
		return
	}
	types.ErrOptionWithSkipRetry()(err)
}

// wssDialError builds the error for a failed upstream WebSocket dial. When the
// handshake was REJECTED by the upstream (a non-101 response captured in
// DoWssRequest onto info), the upstream status/header/body are attached so
// account-pool failure classification can apply a 401/403/429/5xx-specific
// cooldown instead of a generic transport cooldown. A pure transport failure
// (no upstream response) yields a plain ErrorCodeDoRequestFailed.
func wssDialError(info *relaycommon.RelayInfo, err error) *types.NewAPIError {
	apiErr := types.NewError(err, types.ErrorCodeDoRequestFailed)
	if info != nil && info.WsHandshakeStatusCode > 0 {
		apiErr.SetUpstreamResponse(info.WsHandshakeHeader, info.WsHandshakeBody, info.WsHandshakeStatusCode)
	}
	return apiErr
}

func WssHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	info.InitChannelMeta(c)
	statusCodeMappingStr := c.GetString("status_code_mapping")

	// Realtime/WebSocket relay now participates in account-pool runtime selection.
	// runAccountPoolRuntimeAttempts injects the chosen pooled account's credential/
	// proxy/model into info before each attempt and is a transparent pass-through for
	// non-pooled channels (single attempt, no recording). For pooled channels it can
	// retry another account when the UPSTREAM HANDSHAKE/DIAL fails, which is the only
	// failure that surfaces here before any frame flows: the realtime pump
	// (OpenaiRealtimeHandler) handles its own mid-session errors internally and never
	// returns a retryable error, and HasSendResponse() is set once the first upstream
	// frame is read — so the retry loop can never switch accounts on a live socket.
	// There is no request body to rebuild per attempt, so the factory yields nil.
	return runAccountPoolRuntimeAttempts(c, info, func() (dto.Request, *types.NewAPIError) {
		return nil, nil
	}, func(_ dto.Request) *types.NewAPIError {
		adaptor := GetAdaptor(info.ApiType)
		if adaptor == nil {
			return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
		}
		adaptor.Init(info)

		resp, err := adaptor.DoRequest(c, info, nil)
		if err != nil {
			// Handshake/dial failure occurs before any frame flows, so the
			// account-pool loop may safely retry on another pooled account. Attach
			// any captured upstream handshake rejection (401/403/429/...) so failure
			// classification applies a status/platform-specific cooldown rather than
			// a generic transport cooldown.
			return wssDialError(info, err)
		}

		if resp != nil {
			info.TargetWs = resp.(*websocket.Conn)
			defer info.TargetWs.Close()
		}

		usage, newAPIError := adaptor.DoResponse(c, nil, info)
		if newAPIError != nil {
			// reset status code 重置状态码
			service.ResetStatusCode(newAPIError, statusCodeMappingStr)
			// The upstream socket was already established by the dial above; any
			// error from here is mid/post-session, and switching to another pooled
			// account on a live client socket is never valid. Mark skip-retry so the
			// invariant holds by construction (not by luck of the pump's return
			// contract), even if a future change makes the pump return an error.
			markSkipRetry(newAPIError)
			return newAPIError
		}
		if realtimeUsage, ok := usage.(*dto.RealtimeUsage); ok && realtimeUsage != nil {
			service.PostWssConsumeQuota(c, info, info.UpstreamModelName, realtimeUsage, "")
		} else {
			common.SysError(fmt.Sprintf("realtime usage has unexpected type %T; quota not posted", usage))
		}
		return nil
	})
}
