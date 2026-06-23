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
	// Account-pool selection errors should allow the outer channel retry loop
	// to try another channel. Do not add ErrOptionWithSkipRetry here.
	return types.NewErrorWithStatusCode(
		err,
		types.ErrorCodeGetChannelFailed,
		http.StatusServiceUnavailable,
	)
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
