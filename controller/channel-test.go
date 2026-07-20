package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/samber/lo"
	"github.com/tidwall/gjson"

	"github.com/gin-gonic/gin"
)

type testResult struct {
	context             *gin.Context
	localErr            error
	newAPIError         *types.NewAPIError
	testedModel         string
	upstreamAttempted   bool
	endpointLatencyMS   int64
	firstTokenLatencyMS int64
	promptTokens        int
	completionTokens    int
}

type channelTestOptions struct {
	recordConsumeLog bool
}

func defaultChannelTestOptions() channelTestOptions {
	return channelTestOptions{
		recordConsumeLog: true,
	}
}

func channelUsesCodexCLIIdentity(channel *model.Channel) bool {
	if channel == nil {
		return false
	}
	return dto.NormalizeClientIdentityPreset(channel.GetOtherSettings().ClientIdentityPreset) == dto.ClientIdentityPresetCodexCLI
}

func normalizeChannelTestEndpoint(channel *model.Channel, modelName, endpointType string) string {
	normalized := strings.TrimSpace(endpointType)
	if normalized != "" {
		return normalized
	}
	if strings.HasSuffix(modelName, ratio_setting.CompactModelSuffix) {
		return string(constant.EndpointTypeOpenAIResponseCompact)
	}
	// Native Codex channel type, and any channel simulating Codex CLI identity,
	// must probe the OpenAI Responses protocol. Upstream policy for codex_cli
	// fingerprints rejects /v1/chat/completions with codex_requires_responses_protocol.
	if channel != nil && channel.Type == constant.ChannelTypeCodex {
		return string(constant.EndpointTypeOpenAIResponse)
	}
	if channelUsesCodexCLIIdentity(channel) {
		return string(constant.EndpointTypeOpenAIResponse)
	}
	return normalized
}

func selectAutomaticChannelTestModel(channel *model.Channel) string {
	if channel != nil && channel.TestModel != nil {
		if testModel := strings.TrimSpace(*channel.TestModel); testModel != "" {
			return testModel
		}
	}

	if channel != nil {
		models := channel.GetModels()
		for _, modelName := range models {
			modelName = strings.TrimSpace(modelName)
			if modelName != "" && !isSpecializedChannelTestModel(modelName) {
				return modelName
			}
		}
		for _, modelName := range models {
			if modelName = strings.TrimSpace(modelName); modelName != "" {
				return modelName
			}
		}
	}

	return "gpt-4o-mini"
}

func isSpecializedChannelTestModel(modelName string) bool {
	normalized := strings.ToLower(strings.TrimSpace(modelName))
	if normalized == "" {
		return true
	}
	if strings.Contains(normalized, "rerank") || strings.Contains(normalized, "moderation") {
		return true
	}
	if strings.Contains(normalized, "embedding") ||
		strings.Contains(normalized, "embed") ||
		strings.HasPrefix(normalized, "m3e") ||
		strings.Contains(normalized, "bge-") {
		return true
	}
	if common.IsImageGenerationModel(normalized) ||
		strings.Contains(normalized, "image") ||
		strings.HasPrefix(normalized, "imagen") {
		return true
	}
	if strings.Contains(normalized, "audio") ||
		strings.Contains(normalized, "speech") ||
		strings.Contains(normalized, "tts") ||
		strings.Contains(normalized, "transcribe") ||
		strings.Contains(normalized, "whisper") ||
		strings.Contains(normalized, "video") ||
		strings.Contains(normalized, "sora") {
		return true
	}
	return false
}

func resolveChannelTestUserID(c *gin.Context) (int, error) {
	if c != nil {
		if userID := c.GetInt("id"); userID > 0 {
			return userID, nil
		}
	}

	var rootUser model.User
	if err := model.DB.Select("id").Where("role = ?", common.RoleRootUser).First(&rootUser).Error; err != nil {
		return 0, fmt.Errorf("failed to resolve channel test user: %w", err)
	}
	if rootUser.Id == 0 {
		return 0, errors.New("failed to resolve channel test user")
	}
	return rootUser.Id, nil
}

func testChannel(ctx context.Context, channel *model.Channel, testUserID int, testModel string, endpointType string, isStream bool) testResult {
	return testChannelWithOptions(ctx, channel, testUserID, testModel, endpointType, isStream, defaultChannelTestOptions())
}

func resolveChannelTestModel(channel *model.Channel, testModel string) string {
	testModel = strings.TrimSpace(testModel)
	if testModel != "" {
		return testModel
	}
	if channel != nil && channel.TestModel != nil && *channel.TestModel != "" {
		return strings.TrimSpace(*channel.TestModel)
	}
	if channel != nil {
		models := channel.GetModels()
		if len(models) > 0 {
			testModel = strings.TrimSpace(models[0])
		}
	}
	if testModel == "" {
		testModel = "gpt-4o-mini"
	}
	return testModel
}

// resolveChannelMonitorProbeModel returns the configured monitor model only when
// it is one of the channel's models (after mapping); otherwise "" so the caller
// falls back to the default test-model resolution instead of a guaranteed failure.
func resolveChannelMonitorProbeModel(channel *model.Channel) string {
	if channel == nil {
		return ""
	}
	configured := strings.TrimSpace(model.NormalizeChannelMonitorSettings(model.GetChannelMonitorSettingsReadOnly(channel)).ChannelMonitorModel)
	if configured == "" {
		return ""
	}
	for _, m := range channel.GetModels() {
		if strings.TrimSpace(m) == configured {
			return configured
		}
	}
	return ""
}

func testChannelWithOptions(ctx context.Context, channel *model.Channel, testUserID int, testModel string, endpointType string, isStream bool, options channelTestOptions) (result testResult) {
	if ctx == nil {
		ctx = context.Background()
	}
	tik := time.Now()
	var unsupportedTestChannelTypes = []int{
		constant.ChannelTypeMidjourney,
		constant.ChannelTypeMidjourneyPlus,
		constant.ChannelTypeSunoAPI,
		constant.ChannelTypeKling,
		constant.ChannelTypeJimeng,
		constant.ChannelTypeDoubaoVideo,
		constant.ChannelTypeVidu,
	}
	if lo.Contains(unsupportedTestChannelTypes, channel.Type) {
		channelTypeName := constant.GetChannelTypeName(channel.Type)
		return testResult{
			localErr: fmt.Errorf("%s channel test is not supported", channelTypeName),
		}
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	testModel = resolveChannelTestModel(channel, testModel)
	defer func() {
		if result.testedModel == "" {
			result.testedModel = testModel
		}
	}()

	endpointType = normalizeChannelTestEndpoint(channel, testModel, endpointType)

	requestPath := "/v1/chat/completions"

	// 如果指定了端点类型，使用指定的端点类型
	if endpointType != "" {
		if endpointInfo, ok := common.GetDefaultEndpointInfo(constant.EndpointType(endpointType)); ok {
			requestPath = endpointInfo.Path
		}
	} else {
		// 如果没有指定端点类型，使用原有的自动检测逻辑

		if strings.Contains(strings.ToLower(testModel), "rerank") {
			requestPath = "/v1/rerank"
		}

		// 先判断是否为 Embedding 模型
		if strings.Contains(strings.ToLower(testModel), "embedding") ||
			strings.HasPrefix(testModel, "m3e") || // m3e 系列模型
			strings.Contains(testModel, "bge-") || // bge 系列模型
			strings.Contains(testModel, "embed") ||
			channel.Type == constant.ChannelTypeMokaAI { // 其他 embedding 模型
			requestPath = "/v1/embeddings" // 修改请求路径
		}

		// VolcEngine 图像生成模型
		if channel.Type == constant.ChannelTypeVolcEngine && strings.Contains(testModel, "seedream") {
			requestPath = "/v1/images/generations"
		}

		// responses-only models
		if strings.Contains(strings.ToLower(testModel), "codex") {
			requestPath = "/v1/responses"
		}

		// responses compaction models (must use /v1/responses/compact)
		if strings.HasSuffix(testModel, ratio_setting.CompactModelSuffix) {
			requestPath = "/v1/responses/compact"
		}
	}
	if strings.HasPrefix(requestPath, "/v1/responses/compact") {
		testModel = ratio_setting.WithCompactModelSuffix(testModel)
	}

	c.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, requestPath, nil)

	cache, err := model.GetUserCache(testUserID)
	if err != nil {
		return testResult{
			localErr:    err,
			newAPIError: nil,
		}
	}
	cache.WriteContext(c)
	c.Set("id", testUserID)

	//c.Request.Header.Set("Authorization", "Bearer "+channel.Key)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("channel", channel.Type)
	c.Set("base_url", channel.GetBaseURL())
	group, _ := model.GetUserGroup(testUserID, false)
	c.Set("group", group)

	newAPIError := middleware.SetupContextForSelectedChannel(c, channel, testModel)
	if newAPIError != nil {
		return testResult{
			context:     c,
			localErr:    newAPIError,
			newAPIError: newAPIError,
		}
	}

	// Determine relay format based on endpoint type or request path
	var relayFormat types.RelayFormat
	if endpointType != "" {
		// 根据指定的端点类型设置 relayFormat
		switch constant.EndpointType(endpointType) {
		case constant.EndpointTypeOpenAI:
			relayFormat = types.RelayFormatOpenAI
		case constant.EndpointTypeOpenAIResponse:
			relayFormat = types.RelayFormatOpenAIResponses
		case constant.EndpointTypeOpenAIResponseCompact:
			relayFormat = types.RelayFormatOpenAIResponsesCompaction
		case constant.EndpointTypeAnthropic:
			relayFormat = types.RelayFormatClaude
		case constant.EndpointTypeGemini:
			relayFormat = types.RelayFormatGemini
		case constant.EndpointTypeJinaRerank:
			relayFormat = types.RelayFormatRerank
		case constant.EndpointTypeImageGeneration:
			relayFormat = types.RelayFormatOpenAIImage
		case constant.EndpointTypeEmbeddings:
			relayFormat = types.RelayFormatEmbedding
		default:
			relayFormat = types.RelayFormatOpenAI
		}
	} else {
		// 根据请求路径自动检测
		relayFormat = types.RelayFormatOpenAI
		if c.Request.URL.Path == "/v1/embeddings" {
			relayFormat = types.RelayFormatEmbedding
		}
		if c.Request.URL.Path == "/v1/images/generations" {
			relayFormat = types.RelayFormatOpenAIImage
		}
		if c.Request.URL.Path == "/v1/messages" {
			relayFormat = types.RelayFormatClaude
		}
		if strings.Contains(c.Request.URL.Path, "/v1beta/models") {
			relayFormat = types.RelayFormatGemini
		}
		if c.Request.URL.Path == "/v1/rerank" || c.Request.URL.Path == "/rerank" {
			relayFormat = types.RelayFormatRerank
		}
		if c.Request.URL.Path == "/v1/responses" {
			relayFormat = types.RelayFormatOpenAIResponses
		}
		if strings.HasPrefix(c.Request.URL.Path, "/v1/responses/compact") {
			relayFormat = types.RelayFormatOpenAIResponsesCompaction
		}
	}

	request := buildTestRequest(testModel, endpointType, channel, isStream)

	info, err := relaycommon.GenRelayInfo(c, relayFormat, request, nil)

	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewError(err, types.ErrorCodeGenRelayInfoFailed),
		}
	}

	info.IsChannelTest = true
	info.InitChannelMeta(c)

	err = attachTestBillingRequestInput(info, request)
	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewError(err, types.ErrorCodeJsonMarshalFailed),
		}
	}

	err = helper.ModelMappedHelper(c, info, request)
	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewError(err, types.ErrorCodeChannelModelMappedError),
		}
	}

	testModel = info.UpstreamModelName
	// 更新请求中的模型名称
	request.SetModelName(testModel)

	apiType, _ := common.ChannelType2APIType(channel.Type)
	if info.RelayMode == relayconstant.RelayModeResponsesCompact &&
		apiType != constant.APITypeOpenAI &&
		apiType != constant.APITypeCodex {
		return testResult{
			context:     c,
			localErr:    fmt.Errorf("responses compaction test only supports openai/codex channels, got api type %d", apiType),
			newAPIError: types.NewError(fmt.Errorf("unsupported api type: %d", apiType), types.ErrorCodeInvalidApiType),
		}
	}
	adaptor := relay.GetAdaptor(apiType)
	if adaptor == nil {
		return testResult{
			context:     c,
			localErr:    fmt.Errorf("invalid api type: %d, adaptor is nil", apiType),
			newAPIError: types.NewError(fmt.Errorf("invalid api type: %d, adaptor is nil", apiType), types.ErrorCodeInvalidApiType),
		}
	}

	//// 创建一个用于日志的 info 副本，移除 ApiKey
	//logInfo := info
	//logInfo.ApiKey = ""
	common.SysLog(fmt.Sprintf("testing channel %d with model %s , info %+v ", channel.Id, testModel, info.ToString()))

	priceData, err := helper.ModelPriceHelper(c, info, 0, request.GetTokenCountMeta())
	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewError(err, types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusBadRequest)),
		}
	}

	adaptor.Init(info)

	var convertedRequest any
	// 根据 RelayMode 选择正确的转换函数
	switch info.RelayMode {
	case relayconstant.RelayModeEmbeddings:
		// Embedding 请求 - request 已经是正确的类型
		if embeddingReq, ok := request.(*dto.EmbeddingRequest); ok {
			convertedRequest, err = adaptor.ConvertEmbeddingRequest(c, info, *embeddingReq)
		} else {
			return testResult{
				context:     c,
				localErr:    errors.New("invalid embedding request type"),
				newAPIError: types.NewError(errors.New("invalid embedding request type"), types.ErrorCodeConvertRequestFailed),
			}
		}
	case relayconstant.RelayModeImagesGenerations:
		// 图像生成请求 - request 已经是正确的类型
		if imageReq, ok := request.(*dto.ImageRequest); ok {
			convertedRequest, err = adaptor.ConvertImageRequest(c, info, *imageReq)
		} else {
			return testResult{
				context:     c,
				localErr:    errors.New("invalid image request type"),
				newAPIError: types.NewError(errors.New("invalid image request type"), types.ErrorCodeConvertRequestFailed),
			}
		}
	case relayconstant.RelayModeRerank:
		// Rerank 请求 - request 已经是正确的类型
		if rerankReq, ok := request.(*dto.RerankRequest); ok {
			convertedRequest, err = adaptor.ConvertRerankRequest(c, info.RelayMode, *rerankReq)
		} else {
			return testResult{
				context:     c,
				localErr:    errors.New("invalid rerank request type"),
				newAPIError: types.NewError(errors.New("invalid rerank request type"), types.ErrorCodeConvertRequestFailed),
			}
		}
	case relayconstant.RelayModeResponses:
		// Response 请求 - request 已经是正确的类型
		if responseReq, ok := request.(*dto.OpenAIResponsesRequest); ok {
			convertedRequest, err = adaptor.ConvertOpenAIResponsesRequest(c, info, *responseReq)
		} else {
			return testResult{
				context:     c,
				localErr:    errors.New("invalid response request type"),
				newAPIError: types.NewError(errors.New("invalid response request type"), types.ErrorCodeConvertRequestFailed),
			}
		}
	case relayconstant.RelayModeResponsesCompact:
		// Response compaction request - convert to OpenAIResponsesRequest before adapting
		switch req := request.(type) {
		case *dto.OpenAIResponsesCompactionRequest:
			convertedRequest, err = adaptor.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{
				Model:              req.Model,
				Input:              req.Input,
				Instructions:       req.Instructions,
				PreviousResponseID: req.PreviousResponseID,
			})
		case *dto.OpenAIResponsesRequest:
			convertedRequest, err = adaptor.ConvertOpenAIResponsesRequest(c, info, *req)
		default:
			return testResult{
				context:     c,
				localErr:    errors.New("invalid response compaction request type"),
				newAPIError: types.NewError(errors.New("invalid response compaction request type"), types.ErrorCodeConvertRequestFailed),
			}
		}
	default:
		// Chat/Completion 等其他请求类型
		if generalReq, ok := request.(*dto.GeneralOpenAIRequest); ok {
			convertedRequest, err = adaptor.ConvertOpenAIRequest(c, info, generalReq)
		} else {
			return testResult{
				context:     c,
				localErr:    errors.New("invalid general request type"),
				newAPIError: types.NewError(errors.New("invalid general request type"), types.ErrorCodeConvertRequestFailed),
			}
		}
	}

	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewError(err, types.ErrorCodeConvertRequestFailed),
		}
	}
	jsonData, err := common.Marshal(convertedRequest)
	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewError(err, types.ErrorCodeJsonMarshalFailed),
		}
	}

	//jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings)
	//if err != nil {
	//	return testResult{
	//		context:     c,
	//		localErr:    err,
	//		newAPIError: types.NewError(err, types.ErrorCodeConvertRequestFailed),
	//	}
	//}

	if len(info.ParamOverride) > 0 {
		jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
		if err != nil {
			if fixedErr, ok := relaycommon.AsParamOverrideReturnError(err); ok {
				return testResult{
					context:     c,
					localErr:    fixedErr,
					newAPIError: relaycommon.NewAPIErrorFromParamOverride(fixedErr),
				}
			}
			return testResult{
				context:     c,
				localErr:    err,
				newAPIError: types.NewError(err, types.ErrorCodeChannelParamOverrideInvalid),
			}
		}
	}

	requestBody := bytes.NewBuffer(jsonData)
	c.Request.Body = io.NopCloser(bytes.NewBuffer(jsonData))
	endpointStart := time.Now()
	resp, err := adaptor.DoRequest(c, info, requestBody)
	endpointLatencyMS := time.Since(endpointStart).Milliseconds()
	if err != nil {
		return testResult{
			context:           c,
			localErr:          err,
			newAPIError:       types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError),
			upstreamAttempted: true,
			endpointLatencyMS: endpointLatencyMS,
		}
	}
	var httpResp *http.Response
	if resp != nil {
		httpResp = resp.(*http.Response)
		if httpResp.StatusCode != http.StatusOK {
			err := service.RelayErrorHandler(c.Request.Context(), httpResp, true)
			common.SysError(fmt.Sprintf(
				"channel test bad response: channel_id=%d name=%s type=%d model=%s endpoint_type=%s status=%d err=%v",
				channel.Id,
				channel.Name,
				channel.Type,
				testModel,
				endpointType,
				httpResp.StatusCode,
				err,
			))
			return testResult{
				context:           c,
				localErr:          err,
				newAPIError:       types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError),
				upstreamAttempted: true,
				endpointLatencyMS: endpointLatencyMS,
			}
		}
	}
	// Capture the RAW upstream body as the adaptor reads it, so probe validation
	// judges what the upstream actually returned — not the adaptor's re-rendered
	// stream output. A stream-ignoring JSON reply and a broken HTML page both
	// collapse to "data: [DONE]" after re-rendering, so validating that would let
	// a healthy channel fail and a broken one pass; the raw body distinguishes
	// them (SSE frames / a JSON object = healthy, HTML/garbage = failed).
	var rawUpstreamBody bytes.Buffer
	if httpResp != nil && httpResp.Body != nil {
		// Keep a handle to the real body: wrapping it in NopCloser makes the
		// adaptor's Close a no-op, and the stream scanner returns at [DONE]
		// without reading to EOF, so without this defer the upstream connection
		// would leak on every healthy streaming probe.
		upstreamBody := httpResp.Body
		defer upstreamBody.Close()
		httpResp.Body = io.NopCloser(io.TeeReader(upstreamBody, &rawUpstreamBody))
	}
	usageA, respErr := adaptor.DoResponse(c, httpResp, info)
	if respErr != nil {
		return testResult{
			context:             c,
			localErr:            respErr,
			newAPIError:         respErr,
			upstreamAttempted:   true,
			endpointLatencyMS:   endpointLatencyMS,
			firstTokenLatencyMS: channelTestFirstTokenLatencyMS(info),
		}
	}
	usage, usageErr := coerceTestUsage(usageA, isStream, info.GetEstimatePromptTokens())
	if usageErr != nil {
		return testResult{
			context:             c,
			localErr:            usageErr,
			newAPIError:         types.NewOpenAIError(usageErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError),
			upstreamAttempted:   true,
			endpointLatencyMS:   endpointLatencyMS,
			firstTokenLatencyMS: channelTestFirstTokenLatencyMS(info),
		}
	}
	httpResult := w.Result()
	respBody, err := readTestResponseBody(httpResult.Body, isStream)
	if err != nil {
		return testResult{
			context:             c,
			localErr:            err,
			newAPIError:         types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError),
			upstreamAttempted:   true,
			endpointLatencyMS:   endpointLatencyMS,
			firstTokenLatencyMS: channelTestFirstTokenLatencyMS(info),
			promptTokens:        usage.PromptTokens,
			completionTokens:    usage.CompletionTokens,
		}
	}
	if bodyErr := validateTestResponseBody(rawUpstreamBody.Bytes(), isStream); bodyErr != nil {
		return testResult{
			context:             c,
			localErr:            bodyErr,
			newAPIError:         types.NewOpenAIError(bodyErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError),
			upstreamAttempted:   true,
			endpointLatencyMS:   endpointLatencyMS,
			firstTokenLatencyMS: channelTestFirstTokenLatencyMS(info),
			promptTokens:        usage.PromptTokens,
			completionTokens:    usage.CompletionTokens,
		}
	}
	info.SetEstimatePromptTokens(usage.PromptTokens)

	quota, tieredResult := settleTestQuota(info, priceData, usage)
	tok := time.Now()
	milliseconds := tok.Sub(tik).Milliseconds()
	consumedTime := float64(milliseconds) / 1000.0
	other := buildTestLogOther(c, info, priceData, usage, tieredResult)
	recordChannelTestConsumeLog(options, c, testUserID, model.RecordConsumeLogParams{
		ChannelId:        channel.Id,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		ModelName:        info.OriginModelName,
		TokenName:        "模型测试",
		Quota:            quota,
		Content:          "模型测试",
		UseTimeSeconds:   int(consumedTime),
		IsStream:         info.IsStream,
		Group:            info.UsingGroup,
		Other:            other,
	})
	common.SysLog(fmt.Sprintf("testing channel #%d, response: \n%s", channel.Id, string(respBody)))
	return testResult{
		context:             c,
		localErr:            nil,
		newAPIError:         nil,
		upstreamAttempted:   true,
		endpointLatencyMS:   endpointLatencyMS,
		firstTokenLatencyMS: channelTestFirstTokenLatencyMS(info),
		promptTokens:        usage.PromptTokens,
		completionTokens:    usage.CompletionTokens,
	}
}

func channelTestFirstTokenLatencyMS(info *relaycommon.RelayInfo) int64 {
	if info == nil || !info.IsStream || !info.HasSendResponse() {
		return 0
	}
	milliseconds := info.FirstResponseTime.Sub(info.StartTime).Milliseconds()
	if milliseconds < 0 {
		return 0
	}
	return milliseconds
}

func recordChannelTestConsumeLog(options channelTestOptions, c *gin.Context, testUserID int, params model.RecordConsumeLogParams) {
	if !options.recordConsumeLog {
		return
	}
	model.RecordConsumeLog(c, testUserID, params)
}

func attachTestBillingRequestInput(info *relaycommon.RelayInfo, request dto.Request) error {
	if info == nil {
		return nil
	}

	input, err := helper.BuildBillingExprRequestInputFromRequest(request, info.RequestHeaders)
	if err != nil {
		return err
	}
	info.BillingRequestInput = &input
	return nil
}

func settleTestQuota(info *relaycommon.RelayInfo, priceData types.PriceData, usage *dto.Usage) (int, *billingexpr.TieredResult) {
	if usage != nil && info != nil && info.TieredBillingSnapshot != nil {
		isClaudeUsageSemantic := usage.UsageSemantic == "anthropic" || info.GetFinalRequestRelayFormat() == types.RelayFormatClaude
		usedVars := billingexpr.UsedVars(info.TieredBillingSnapshot.ExprString)
		if ok, quota, result := service.TryTieredSettle(info, service.BuildTieredTokenParams(usage, isClaudeUsageSemantic, usedVars)); ok {
			return quota, result
		}
	}

	quota := 0
	if !priceData.UsePrice {
		quota = usage.PromptTokens + int(math.Round(float64(usage.CompletionTokens)*priceData.CompletionRatio))
		quota = int(math.Round(float64(quota) * priceData.ModelRatio))
		if priceData.ModelRatio != 0 && quota <= 0 {
			quota = 1
		}
		return quota, nil
	}

	return int(priceData.ModelPrice * common.QuotaPerUnit), nil
}

func buildTestLogOther(c *gin.Context, info *relaycommon.RelayInfo, priceData types.PriceData, usage *dto.Usage, tieredResult *billingexpr.TieredResult) map[string]interface{} {
	other := service.GenerateTextOtherInfo(c, info, priceData.ModelRatio, priceData.GroupRatioInfo.GroupRatio, priceData.CompletionRatio,
		usage.PromptTokensDetails.CachedTokens, priceData.CacheRatio, priceData.ModelPrice, priceData.GroupRatioInfo.GroupSpecialRatio)
	other["is_channel_test"] = true
	if tieredResult != nil {
		service.InjectTieredBillingInfo(other, info, tieredResult)
	}
	return other
}

func coerceTestUsage(usageAny any, isStream bool, estimatePromptTokens int) (*dto.Usage, error) {
	switch u := usageAny.(type) {
	case *dto.Usage:
		return u, nil
	case dto.Usage:
		return &u, nil
	case nil:
		if !isStream {
			return nil, errors.New("usage is nil")
		}
		usage := &dto.Usage{
			PromptTokens: estimatePromptTokens,
		}
		usage.TotalTokens = usage.PromptTokens
		return usage, nil
	default:
		if !isStream {
			return nil, fmt.Errorf("invalid usage type: %T", usageAny)
		}
		usage := &dto.Usage{
			PromptTokens: estimatePromptTokens,
		}
		usage.TotalTokens = usage.PromptTokens
		return usage, nil
	}
}

func readTestResponseBody(body io.ReadCloser, isStream bool) ([]byte, error) {
	defer func() { _ = body.Close() }()
	const maxStreamLogBytes = 8 << 10
	if isStream {
		return io.ReadAll(io.LimitReader(body, maxStreamLogBytes))
	}
	return io.ReadAll(body)
}

func detectErrorFromTestResponseBody(respBody []byte) error {
	b := bytes.TrimSpace(respBody)
	if len(b) == 0 {
		return nil
	}
	if message := detectErrorMessageFromJSONBytes(b); message != "" {
		return fmt.Errorf("upstream error: %s", message)
	}

	for _, line := range bytes.Split(b, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		if message := detectErrorMessageFromJSONBytes(payload); message != "" {
			return fmt.Errorf("upstream error: %s", message)
		}
	}

	return nil
}

func validateStreamTestResponseBody(respBody []byte) error {
	b := bytes.TrimSpace(respBody)
	if len(b) == 0 {
		return errors.New("stream response body is empty")
	}

	for _, line := range bytes.Split(b, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}

		return nil
	}

	// Some OpenAI-compatible / sub2api upstreams ignore stream=true and reply
	// with a plain JSON completion body. Upstream errors were already caught by
	// detectErrorFromTestResponseBody (see validateTestResponseBody), so accept a
	// valid non-error JSON object as a healthy probe — first-token latency just
	// stays 0 rather than flipping the channel to Failed.
	if common.GetJsonType(b) == "object" {
		return nil
	}

	return errors.New("stream response body does not contain a valid stream event")
}

func validateTestResponseBody(respBody []byte, isStream bool) error {
	if bodyErr := detectErrorFromTestResponseBody(respBody); bodyErr != nil {
		return bodyErr
	}
	if isStream {
		return validateStreamTestResponseBody(respBody)
	}
	return nil
}

func shouldUseStreamForAutomaticChannelTest(channel *model.Channel) bool {
	if channel == nil {
		return false
	}
	// Channels that simulate Codex CLI identity must stream-probe. Strict
	// codex-only upstreams reject non-stream chat/completions probes and often
	// expect streaming Responses traffic from real Codex clients.
	if channelUsesCodexCLIIdentity(channel) {
		return true
	}
	// Generated upstream-source channels also stream-probe (when their type
	// supports stream options) so first-token latency gets recorded like any
	// other channel. Upstreams that ignore stream=true and return a plain JSON
	// body are still treated as healthy — see validateStreamTestResponseBody.
	return relaycommon.SupportsStreamOptions(channel.Type)
}

// resolveChannelTestStream decides whether a manual channel test should stream.
// Explicit ?stream= query wins; otherwise codex_cli identity channels default to
// stream=true so the admin "Test" button matches monitor/automatic probes.
func resolveChannelTestStream(c *gin.Context, channel *model.Channel) bool {
	if c != nil {
		if raw, ok := c.GetQuery("stream"); ok {
			isStream, _ := strconv.ParseBool(raw)
			return isStream
		}
	}
	if channelUsesCodexCLIIdentity(channel) {
		return true
	}
	return false
}

func resolveChannelMonitorUpstreamModel(channel *model.Channel, testModel string) (string, error) {
	upstreamModel := strings.TrimSpace(testModel)
	if channel == nil || channel.ModelMapping == nil || strings.TrimSpace(*channel.ModelMapping) == "" || strings.TrimSpace(*channel.ModelMapping) == "{}" {
		return upstreamModel, nil
	}
	modelMap := make(map[string]string)
	if err := common.UnmarshalJsonStr(*channel.ModelMapping, &modelMap); err != nil {
		return "", fmt.Errorf("unmarshal_model_mapping_failed")
	}
	currentModel := upstreamModel
	visitedModels := map[string]bool{currentModel: true}
	for {
		mappedModel, exists := modelMap[currentModel]
		mappedModel = strings.TrimSpace(mappedModel)
		if !exists || mappedModel == "" {
			return currentModel, nil
		}
		if visitedModels[mappedModel] {
			if mappedModel == currentModel {
				return currentModel, nil
			}
			return "", errors.New("model_mapping_contains_cycle")
		}
		visitedModels[mappedModel] = true
		currentModel = mappedModel
	}
}

func testAccountPoolChannelMonitorSchedulability(channel *model.Channel, now int64) (testResult, bool) {
	result := testResult{}
	if channel == nil {
		return result, false
	}
	runtimeEnabled, err := service.AccountPoolRuntimeEnabledForChannel(channel.Id)
	if err != nil {
		result.localErr = err
		return result, true
	}
	if !runtimeEnabled {
		return result, false
	}
	testModel := resolveChannelTestModel(channel, resolveChannelMonitorProbeModel(channel))
	result.testedModel = testModel
	upstreamModel, err := resolveChannelMonitorUpstreamModel(channel, testModel)
	if err != nil {
		result.localErr = err
		return result, true
	}
	schedulability, err := service.CheckAccountPoolChannelSchedulability(service.AccountPoolSchedulabilityRequest{
		ChannelID:            channel.Id,
		RequestModel:         testModel,
		ChannelUpstreamModel: upstreamModel,
		Now:                  now,
	})
	if err != nil {
		result.localErr = err
		return result, true
	}
	if !schedulability.RuntimeEnabled {
		return testResult{}, false
	}
	if !schedulability.Schedulable {
		err := fmt.Errorf("account pool channel has no schedulable account: %s", schedulability.Reason)
		result.newAPIError = types.NewErrorWithStatusCode(err, types.ErrorCodeGetChannelFailed, http.StatusServiceUnavailable)
		result.upstreamAttempted = true
	}
	return result, true
}

func filterDueChannelMonitorCandidates(channels []*model.Channel, latest map[int]model.ChannelMonitorLog, now int64) []*model.Channel {
	candidates := make([]*model.Channel, 0, len(channels))
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		if channel.Status == common.ChannelStatusManuallyDisabled {
			continue
		}
		settings := model.NormalizeChannelMonitorSettings(model.GetChannelMonitorSettingsReadOnly(channel))
		if !settings.ChannelMonitorEnabled {
			continue
		}
		log, ok := latest[channel.Id]
		if !ok {
			candidates = append(candidates, channel)
			continue
		}
		if now-log.CheckedAt >= int64(settings.ChannelMonitorIntervalMinutes)*60 {
			candidates = append(candidates, channel)
		}
	}
	return candidates
}

func channelMonitorStatusFromResult(result testResult) string {
	if result.newAPIError != nil && result.upstreamAttempted {
		return model.ChannelMonitorStatusFailed
	}
	if result.localErr != nil {
		return model.ChannelMonitorStatusError
	}
	return model.ChannelMonitorStatusSuccess
}

func channelMonitorMessageFromResult(result testResult) string {
	if result.localErr != nil {
		return result.localErr.Error()
	}
	if result.newAPIError != nil {
		return result.newAPIError.Error()
	}
	return ""
}

var channelMonitorBatchLock sync.Mutex
var channelMonitorBatchRunning bool

func runDueChannelMonitorBatch() error {
	testUserID, err := resolveChannelTestUserID(nil)
	if err != nil {
		return err
	}

	channelMonitorBatchLock.Lock()
	if channelMonitorBatchRunning {
		channelMonitorBatchLock.Unlock()
		return errors.New("channel monitor batch is already running")
	}
	channelMonitorBatchRunning = true
	channelMonitorBatchLock.Unlock()

	channels, err := model.GetAllChannels(0, 0, true, false)
	if err != nil {
		channelMonitorBatchLock.Lock()
		channelMonitorBatchRunning = false
		channelMonitorBatchLock.Unlock()
		return err
	}

	channelIDs := make([]int, 0, len(channels))
	for _, channel := range channels {
		if channel != nil {
			channelIDs = append(channelIDs, channel.Id)
		}
	}
	latest, err := model.GetLatestChannelMonitorLogs(channelIDs)
	if err != nil {
		channelMonitorBatchLock.Lock()
		channelMonitorBatchRunning = false
		channelMonitorBatchLock.Unlock()
		return err
	}

	now := common.GetTimestamp()
	candidates := filterDueChannelMonitorCandidates(channels, latest, now)
	if len(candidates) == 0 {
		if _, cleanupErr := model.DeleteOldChannelMonitorLogs(now-model.ChannelMonitorRetentionSeconds, 1000); cleanupErr != nil {
			common.SysError("failed to delete old channel monitor logs: " + cleanupErr.Error())
		}
		channelMonitorBatchLock.Lock()
		channelMonitorBatchRunning = false
		channelMonitorBatchLock.Unlock()
		return nil
	}

	gopool.Go(func() {
		defer func() {
			channelMonitorBatchLock.Lock()
			channelMonitorBatchRunning = false
			channelMonitorBatchLock.Unlock()
		}()

		for _, channel := range candidates {
			runChannelMonitorProbe(channel, testUserID)
			time.Sleep(common.RequestInterval)
		}
		if _, cleanupErr := model.DeleteOldChannelMonitorLogs(common.GetTimestamp()-model.ChannelMonitorRetentionSeconds, 1000); cleanupErr != nil {
			common.SysError("failed to delete old channel monitor logs: " + cleanupErr.Error())
		}
	})
	return nil
}

func runChannelMonitorProbe(channel *model.Channel, testUserID int) {
	if channel == nil {
		return
	}

	tik := time.Now()
	result, handled := testAccountPoolChannelMonitorSchedulability(channel, common.GetTimestamp())
	if !handled {
		result = testChannelWithOptions(context.Background(), channel, testUserID, resolveChannelMonitorProbeModel(channel), "", shouldUseStreamForAutomaticChannelTest(channel), channelTestOptions{
			recordConsumeLog: false,
		})
	}
	milliseconds := time.Since(tik).Milliseconds()

	if err := model.RecordChannelMonitorLog(model.ChannelMonitorLog{
		ChannelID:           channel.Id,
		Model:               result.testedModel,
		Status:              channelMonitorStatusFromResult(result),
		LatencyMS:           milliseconds,
		EndpointLatencyMS:   result.endpointLatencyMS,
		FirstTokenLatencyMS: result.firstTokenLatencyMS,
		PromptTokens:        result.promptTokens,
		CompletionTokens:    result.completionTokens,
		Message:             channelMonitorMessageFromResult(result),
		CheckedAt:           common.GetTimestamp(),
	}); err != nil {
		common.SysError(fmt.Sprintf("failed to record channel monitor log: channel_id=%d, error=%v", channel.Id, err))
	}

	applyChannelMonitorStatusMutation(channel, result, milliseconds)
	channel.UpdateResponseTime(milliseconds)
}

func applyChannelMonitorStatusMutation(channel *model.Channel, result testResult, milliseconds int64) {
	if channel == nil {
		return
	}

	isChannelEnabled := channel.Status == common.ChannelStatusEnabled
	disableThreshold := int64(common.ChannelDisableThreshold * 1000)
	if disableThreshold == 0 {
		disableThreshold = 10000000
	}

	shouldBanChannel := false
	newAPIError := result.newAPIError
	if newAPIError != nil && result.upstreamAttempted {
		shouldBanChannel = service.ShouldDisableChannel(newAPIError)
	}
	if common.AutomaticDisableChannelEnabled && result.upstreamAttempted && !shouldBanChannel && milliseconds > disableThreshold {
		err := fmt.Errorf("响应时间 %.2fs 超过阈值 %.2fs", float64(milliseconds)/1000.0, float64(disableThreshold)/1000.0)
		newAPIError = types.NewOpenAIError(err, types.ErrorCodeChannelResponseTimeExceeded, http.StatusRequestTimeout)
		shouldBanChannel = true
	}

	if isChannelEnabled && shouldBanChannel && channel.GetAutoBan() {
		if result.context == nil {
			common.SysError(fmt.Sprintf("channel monitor skipped auto-disable for channel #%d: missing monitor test context", channel.Id))
			return
		}
		service.DisableChannel(*types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, common.GetContextKeyString(result.context, constant.ContextKeyChannelKey), channel.GetAutoBan()), newAPIError.ErrorWithStatusCode())
	}

	if !isChannelEnabled && channelMonitorStatusFromResult(result) == model.ChannelMonitorStatusSuccess && service.ShouldEnableChannel(newAPIError, channel.Status) {
		usingKey := ""
		if result.context != nil {
			usingKey = common.GetContextKeyString(result.context, constant.ContextKeyChannelKey)
		}
		service.EnableChannel(channel.Id, usingKey, channel.Name)
	}
}

func detectErrorMessageFromJSONBytes(jsonBytes []byte) string {
	if len(jsonBytes) == 0 {
		return ""
	}
	if jsonBytes[0] != '{' && jsonBytes[0] != '[' {
		return ""
	}
	errVal := gjson.GetBytes(jsonBytes, "error")
	if !errVal.Exists() || errVal.Type == gjson.Null {
		return ""
	}

	message := gjson.GetBytes(jsonBytes, "error.message").String()
	if message == "" {
		message = gjson.GetBytes(jsonBytes, "error.error.message").String()
	}
	if message == "" && errVal.Type == gjson.String {
		message = errVal.String()
	}
	if message == "" {
		message = errVal.Raw
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "upstream returned error payload"
	}
	return message
}

func buildTestRequest(model string, endpointType string, channel *model.Channel, isStream bool) dto.Request {
	testResponsesInput := json.RawMessage(`[{"role":"user","content":"hi"}]`)

	// 根据端点类型构建不同的测试请求
	if endpointType != "" {
		switch constant.EndpointType(endpointType) {
		case constant.EndpointTypeEmbeddings:
			// 返回 EmbeddingRequest
			return &dto.EmbeddingRequest{
				Model: model,
				Input: []any{"hello world"},
			}
		case constant.EndpointTypeImageGeneration:
			// 返回 ImageRequest
			return &dto.ImageRequest{
				Model:  model,
				Prompt: "a cute cat",
				N:      lo.ToPtr(uint(1)),
				Size:   "1024x1024",
			}
		case constant.EndpointTypeJinaRerank:
			// 返回 RerankRequest
			return &dto.RerankRequest{
				Model:     model,
				Query:     "What is Deep Learning?",
				Documents: []any{"Deep Learning is a subset of machine learning.", "Machine learning is a field of artificial intelligence."},
				TopN:      lo.ToPtr(2),
			}
		case constant.EndpointTypeOpenAIResponse:
			// 返回 OpenAIResponsesRequest
			return &dto.OpenAIResponsesRequest{
				Model:  model,
				Input:  json.RawMessage(`[{"role":"user","content":"hi"}]`),
				Stream: lo.ToPtr(isStream),
			}
		case constant.EndpointTypeOpenAIResponseCompact:
			// 返回 OpenAIResponsesCompactionRequest
			return &dto.OpenAIResponsesCompactionRequest{
				Model: model,
				Input: testResponsesInput,
			}
		case constant.EndpointTypeAnthropic, constant.EndpointTypeGemini, constant.EndpointTypeOpenAI:
			// 返回 GeneralOpenAIRequest
			maxTokens := uint(16)
			if constant.EndpointType(endpointType) == constant.EndpointTypeGemini {
				maxTokens = 3000
			}
			req := &dto.GeneralOpenAIRequest{
				Model:  model,
				Stream: lo.ToPtr(isStream),
				Messages: []dto.Message{
					{
						Role:    "user",
						Content: "hi",
					},
				},
				MaxTokens: lo.ToPtr(maxTokens),
			}
			if isStream {
				req.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
			}
			return req
		}
	}

	// 自动检测逻辑（保持原有行为）
	if strings.Contains(strings.ToLower(model), "rerank") {
		return &dto.RerankRequest{
			Model:     model,
			Query:     "What is Deep Learning?",
			Documents: []any{"Deep Learning is a subset of machine learning.", "Machine learning is a field of artificial intelligence."},
			TopN:      lo.ToPtr(2),
		}
	}

	// 先判断是否为 Embedding 模型
	if strings.Contains(strings.ToLower(model), "embedding") ||
		strings.HasPrefix(model, "m3e") ||
		strings.Contains(model, "bge-") {
		// 返回 EmbeddingRequest
		return &dto.EmbeddingRequest{
			Model: model,
			Input: []any{"hello world"},
		}
	}

	// Responses compaction models (must use /v1/responses/compact)
	if strings.HasSuffix(model, ratio_setting.CompactModelSuffix) {
		return &dto.OpenAIResponsesCompactionRequest{
			Model: model,
			Input: testResponsesInput,
		}
	}

	// Responses-only models (e.g. codex series)
	if strings.Contains(strings.ToLower(model), "codex") {
		return &dto.OpenAIResponsesRequest{
			Model:  model,
			Input:  json.RawMessage(`[{"role":"user","content":"hi"}]`),
			Stream: lo.ToPtr(isStream),
		}
	}

	// Chat/Completion 请求 - 返回 GeneralOpenAIRequest
	testRequest := &dto.GeneralOpenAIRequest{
		Model:  model,
		Stream: lo.ToPtr(isStream),
		Messages: []dto.Message{
			{
				Role:    "user",
				Content: "hi",
			},
		},
	}
	if isStream {
		testRequest.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
	}

	if dto.IsOpenAIReasoningOModel(model) {
		testRequest.MaxCompletionTokens = lo.ToPtr(uint(16))
	} else if strings.Contains(model, "thinking") {
		if !strings.Contains(model, "claude") {
			testRequest.MaxTokens = lo.ToPtr(uint(50))
		}
	} else if strings.Contains(model, "gemini") {
		testRequest.MaxTokens = lo.ToPtr(uint(3000))
	} else {
		testRequest.MaxTokens = lo.ToPtr(uint(16))
	}

	return testRequest
}

func TestChannel(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	channel, err := model.CacheGetChannel(channelId)
	if err != nil {
		channel, err = model.GetChannelById(channelId, true)
		if err != nil {
			common.ApiError(c, err)
			return
		}
	}
	//defer func() {
	//	if channel.ChannelInfo.IsMultiKey {
	//		go func() { _ = channel.SaveChannelInfo() }()
	//	}
	//}()
	testModel := c.Query("model")
	endpointType := c.Query("endpoint_type")
	isStream := resolveChannelTestStream(c, channel)
	testUserID, err := resolveChannelTestUserID(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	tik := time.Now()
	requestCtx := context.Background()
	if c.Request != nil {
		requestCtx = c.Request.Context()
	}
	result := testChannel(requestCtx, channel, testUserID, testModel, endpointType, isStream)
	if result.localErr != nil {
		resp := gin.H{
			"success": false,
			"message": result.localErr.Error(),
			"time":    0.0,
		}
		if result.newAPIError != nil {
			resp["error_code"] = result.newAPIError.GetErrorCode()
		}
		c.JSON(http.StatusOK, resp)
		return
	}
	tok := time.Now()
	milliseconds := tok.Sub(tik).Milliseconds()
	go channel.UpdateResponseTime(milliseconds)
	consumedTime := float64(milliseconds) / 1000.0
	if result.newAPIError != nil {
		c.JSON(http.StatusOK, gin.H{
			"success":                false,
			"message":                result.newAPIError.Error(),
			"time":                   consumedTime,
			"endpoint_latency_ms":    result.endpointLatencyMS,
			"first_token_latency_ms": result.firstTokenLatencyMS,
			"error_code":             result.newAPIError.GetErrorCode(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success":                true,
		"message":                "",
		"time":                   consumedTime,
		"endpoint_latency_ms":    result.endpointLatencyMS,
		"first_token_latency_ms": result.firstTokenLatencyMS,
	})
}

// channelTestSummary records the outcome of one channel test cycle so the
// system task can persist a per-run result for history.
type channelTestSummary struct {
	Tested    int `json:"tested"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Disabled  int `json:"disabled"`
	Enabled   int `json:"enabled"`
}

// performChannelTests runs the channel test loop synchronously, honoring ctx
// cancellation so a system-task runner that loses its lease stops promptly. When
// report is non-nil it is called after each channel with (processed, total) so
// the system task can surface progress.
func performChannelTests(ctx context.Context, channels []*model.Channel, testUserID int, allowDisable bool, report func(processed, total int)) channelTestSummary {
	summary := channelTestSummary{}
	var disableThreshold = int64(common.ChannelDisableThreshold * 1000)
	if disableThreshold == 0 {
		disableThreshold = 10000000 // a impossible value
	}

	total := len(channels)
	for index, channel := range channels {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		if report != nil {
			report(index, total) // channels completed before this one
		}
		if channel.Status == common.ChannelStatusManuallyDisabled {
			continue
		}
		isChannelEnabled := channel.Status == common.ChannelStatusEnabled
		tik := time.Now()
		result := testChannel(ctx, channel, testUserID, "", "", shouldUseStreamForAutomaticChannelTest(channel))
		tok := time.Now()
		milliseconds := tok.Sub(tik).Milliseconds()
		if ctx != nil && ctx.Err() != nil {
			break
		}

		summary.Tested++

		shouldBanChannel := false
		newAPIError := result.newAPIError
		// request error disables the channel
		if newAPIError != nil {
			shouldBanChannel = service.ShouldDisableChannel(result.newAPIError)
		}

		// 当错误检查通过，才检查响应时间
		if common.AutomaticDisableChannelEnabled && !shouldBanChannel {
			if milliseconds > disableThreshold {
				err := fmt.Errorf("响应时间 %.2fs 超过阈值 %.2fs", float64(milliseconds)/1000.0, float64(disableThreshold)/1000.0)
				newAPIError = types.NewOpenAIError(err, types.ErrorCodeChannelResponseTimeExceeded, http.StatusRequestTimeout)
				shouldBanChannel = true
			}
		}

		if newAPIError == nil {
			summary.Succeeded++
		} else {
			summary.Failed++
		}

		// disable channel
		if allowDisable && isChannelEnabled && shouldBanChannel && channel.GetAutoBan() {
			processChannelError(result.context, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, common.GetContextKeyString(result.context, constant.ContextKeyChannelKey), channel.GetAutoBan()), newAPIError)
			summary.Disabled++
		}

		// enable channel
		if result.localErr == nil && !isChannelEnabled && service.ShouldEnableChannel(newAPIError, channel.Status) {
			service.EnableChannel(channel.Id, common.GetContextKeyString(result.context, constant.ContextKeyChannelKey), channel.Name)
			summary.Enabled++
		}

		channel.UpdateResponseTime(milliseconds)
		if common.RequestInterval > 0 {
			if ctx == nil {
				time.Sleep(common.RequestInterval)
			} else {
				select {
				case <-ctx.Done():
					return summary
				case <-time.After(common.RequestInterval):
				}
			}
		}
	}
	if report != nil && (ctx == nil || ctx.Err() == nil) {
		report(total, total) // mark complete only when the full set was tested
	}
	return summary
}

// runChannelTestTask runs one synchronous channel test cycle for the system task
// runner (both the scheduled job and the manual "test all channels" trigger go
// through here). It honors ctx cancellation so a runner that loses its lease
// stops promptly. mode selects the channel set: an empty mode falls back to the
// configured monitor ChannelTestMode (scheduled behavior), while a manual
// trigger passes ChannelTestModeScheduledAll to test every channel. When notify
// is set the root user is notified on completion. Cross-instance execution is
// guarded by the system task per-type lock, so no process-local guard is needed.
func runChannelTestTask(ctx context.Context, mode string, notify bool, report func(processed, total int)) (channelTestSummary, error) {
	testUserID, err := resolveChannelTestUserID(nil)
	if err != nil {
		return channelTestSummary{}, err
	}
	channels, err := model.GetAllChannels(0, 0, true, false)
	if err != nil {
		return channelTestSummary{}, err
	}
	if strings.TrimSpace(mode) == "" {
		mode = operation_setting.GetMonitorSetting().ChannelTestMode
	}
	selected := selectChannelsForAutomaticTest(channels, mode)
	allowDisable := mode != operation_setting.ChannelTestModePassiveRecovery
	summary := performChannelTests(ctx, selected, testUserID, allowDisable, report)
	if notify && (ctx == nil || ctx.Err() == nil) {
		service.NotifyRootUser(dto.NotifyTypeChannelTest, "通道测试完成", "所有通道测试已完成")
	}
	return summary, nil
}

func selectChannelsForAutomaticTest(channels []*model.Channel, mode string) []*model.Channel {
	selected := make([]*model.Channel, 0, len(channels))
	for _, channel := range channels {
		if channel.Status == common.ChannelStatusManuallyDisabled {
			continue
		}
		if mode == operation_setting.ChannelTestModePassiveRecovery && channel.Status != common.ChannelStatusAutoDisabled {
			continue
		}
		selected = append(selected, channel)
	}
	return selected
}

// TestAllChannels enqueues a channel_test system task instead of running the
// test loop inline. If any channel_test task is already active, the manual run is
// rejected so the caller does not mistake a scheduled run for this manual one.
func TestAllChannels(c *gin.Context) {
	task, created, err := service.EnqueueSystemTask(model.SystemTaskTypeChannelTest, channelTestTaskPayload{
		Mode:   operation_setting.ChannelTestModeScheduledAll,
		Notify: true,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !created {
		c.JSON(http.StatusConflict, gin.H{
			"success": false,
			"message": "已有通道测试任务正在运行或等待中，不能启动本次手动任务",
			"data": gin.H{
				"task_id": task.TaskID,
				"status":  task.Status,
				"type":    task.Type,
			},
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"task_id": task.TaskID,
			"status":  task.Status,
		},
	})
}

var autoTestChannelsOnce sync.Once

func AutomaticallyTestChannels() {
	// 只在Master节点定时测试渠道
	if !common.IsMasterNode {
		return
	}
	autoTestChannelsOnce.Do(func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if err := runDueChannelMonitorBatch(); err != nil {
				common.SysError("channel monitor batch skipped: " + err.Error())
			}
		}
	})
}
