package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitialChannelSetupHandoffIsTypedAndConsumedOnce(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	channel := &model.Channel{Id: 7, Type: constant.ChannelTypeOpenAI, Key: "sk-first-attempt"}

	PreserveInitialChannelSetup(c, channel, "gpt-5")

	setup, ok := TakeInitialChannelSetup(c)
	require.True(t, ok)
	assert.Equal(t, 7, setup.ChannelID)
	assert.Same(t, channel, setup.Channel)
	assert.Nil(t, setup.Error)
	assert.Equal(t, "sk-first-attempt", common.GetContextKeyString(c, constant.ContextKeyChannelKey))

	_, ok = TakeInitialChannelSetup(c)
	assert.False(t, ok)
}

func TestInitialChannelSetupHandoffRetainsNoKeyFailure(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	channel := &model.Channel{
		Id:   9,
		Type: constant.ChannelTypeOpenAI,
		Key:  "disabled-first\ndisabled-second",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 2,
			MultiKeyStatusList: map[int]int{
				0: common.ChannelStatusAutoDisabled,
				1: common.ChannelStatusManuallyDisabled,
			},
		},
	}

	PreserveInitialChannelSetup(c, channel, "gpt-5")

	setup, ok := TakeInitialChannelSetup(c)
	require.True(t, ok)
	assert.Equal(t, 9, setup.ChannelID)
	require.NotNil(t, setup.Error)
	assert.Contains(t, setup.Error.Error(), "no enabled keys")
	assert.Empty(t, common.GetContextKeyString(c, constant.ContextKeyChannelKey))
}

func TestSetupContextForSelectedChannelClearsPreviousOptionalStateBeforeNoKeyFailure(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	organization := "org-first"
	first := &model.Channel{
		Id:                 1,
		Type:               constant.ChannelTypeAzure,
		Key:                "sk-first",
		Other:              "2026-01-01",
		OpenAIOrganization: &organization,
	}
	require.Nil(t, SetupContextForSelectedChannel(c, first, "gpt-5"))

	second := &model.Channel{
		Id:   2,
		Type: constant.ChannelTypeOpenAI,
		Key:  "disabled",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 1,
			MultiKeyStatusList: map[int]int{
				0: common.ChannelStatusAutoDisabled,
			},
		},
	}
	setupErr := SetupContextForSelectedChannel(c, second, "gpt-5")

	require.NotNil(t, setupErr)
	assert.Contains(t, setupErr.Error(), "no enabled keys")
	assert.Equal(t, 2, common.GetContextKeyInt(c, constant.ContextKeyChannelId))
	assert.Empty(t, common.GetContextKeyString(c, constant.ContextKeyChannelKey))
	assert.Empty(t, common.GetContextKeyString(c, constant.ContextKeyChannelOrganization))
	assert.Empty(t, c.GetString("api_version"))
}
