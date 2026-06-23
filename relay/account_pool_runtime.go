package relay

import (
	"net/http"

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
