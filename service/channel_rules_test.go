package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
)

func TestShouldDisableChannelUsesChannelOnlyStatusAndKeyword(t *testing.T) {
	originalEnabled := common.AutomaticDisableChannelEnabled
	originalRanges := operation_setting.AutomaticDisableStatusCodeRanges
	originalKeywords := operation_setting.AutomaticDisableKeywords
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = originalEnabled
		operation_setting.AutomaticDisableStatusCodeRanges = originalRanges
		operation_setting.AutomaticDisableKeywords = originalKeywords
	})
	common.AutomaticDisableChannelEnabled = true
	operation_setting.AutomaticDisableStatusCodeRanges = []operation_setting.StatusCodeRange{{Start: 401, End: 401}}
	operation_setting.AutomaticDisableKeywords = []string{"global failure"}

	settings := dto.ChannelOtherSettings{
		ChannelAutoDisableStatusCodes: "404",
		ChannelFailureKeywords:        "Model Not Found\nAccount Suspended",
	}

	statusErr := types.NewErrorWithStatusCode(errors.New("missing"), types.ErrorCodeBadResponse, 404)
	require.True(t, ShouldDisableChannelWithSettings(statusErr, settings))

	keywordErr := types.NewErrorWithStatusCode(errors.New("upstream: ACCOUNT SUSPENDED"), types.ErrorCodeBadResponse, 400)
	require.True(t, ShouldDisableChannelWithSettings(keywordErr, settings))
}

func TestShouldDisableChannelEmptyChannelRulesUseGlobalOnly(t *testing.T) {
	originalEnabled := common.AutomaticDisableChannelEnabled
	originalRanges := operation_setting.AutomaticDisableStatusCodeRanges
	originalKeywords := operation_setting.AutomaticDisableKeywords
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = originalEnabled
		operation_setting.AutomaticDisableStatusCodeRanges = originalRanges
		operation_setting.AutomaticDisableKeywords = originalKeywords
	})
	common.AutomaticDisableChannelEnabled = true
	operation_setting.AutomaticDisableStatusCodeRanges = []operation_setting.StatusCodeRange{{Start: 401, End: 401}}
	operation_setting.AutomaticDisableKeywords = []string{"global failure"}

	globalErr := types.NewErrorWithStatusCode(errors.New("GLOBAL FAILURE"), types.ErrorCodeBadResponse, 400)
	require.True(t, ShouldDisableChannelWithSettings(globalErr, dto.ChannelOtherSettings{}))

	channelOnlyErr := types.NewErrorWithStatusCode(errors.New("account suspended"), types.ErrorCodeBadResponse, 404)
	require.False(t, ShouldDisableChannelWithSettings(channelOnlyErr, dto.ChannelOtherSettings{}))
}
