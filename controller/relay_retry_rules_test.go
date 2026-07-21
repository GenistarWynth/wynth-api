package controller

import (
	"errors"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
)

func TestShouldRetryUsesChannelOnlyStatusCode(t *testing.T) {
	original := operation_setting.AutomaticRetryStatusCodeRanges
	t.Cleanup(func() { operation_setting.AutomaticRetryStatusCodeRanges = original })
	operation_setting.AutomaticRetryStatusCodeRanges = []operation_setting.StatusCodeRange{{Start: 500, End: 503}}

	c, _ := gin.CreateTestContext(nil)
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{
		ChannelRetryStatusCodes: "404, 502-504",
	})
	err := types.NewErrorWithStatusCode(errors.New("model not found"), types.ErrorCodeBadResponse, 404)

	require.True(t, shouldRetry(c, err, 1))
}

func TestShouldRetryEmptyChannelRulesUseGlobalOnly(t *testing.T) {
	original := operation_setting.AutomaticRetryStatusCodeRanges
	t.Cleanup(func() { operation_setting.AutomaticRetryStatusCodeRanges = original })
	operation_setting.AutomaticRetryStatusCodeRanges = []operation_setting.StatusCodeRange{{Start: 500, End: 503}}

	c, _ := gin.CreateTestContext(nil)
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{})
	err := types.NewErrorWithStatusCode(errors.New("model not found"), types.ErrorCodeBadResponse, 404)

	require.False(t, shouldRetry(c, err, 1))
}
