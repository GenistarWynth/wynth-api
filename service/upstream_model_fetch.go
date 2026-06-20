package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

const fetchModelsResponseBodyLimitBytes int64 = 10 << 20

type FetchChannelUpstreamModelIDsOptions struct {
	AllowPrivateIP bool
}

type fetchOpenAIModelsResponse struct {
	Data []fetchOpenAIModel `json:"data"`
}

type fetchOpenAIModel struct {
	ID string `json:"id"`
}

type fetchGeminiModelsResponse struct {
	Models        []fetchGeminiModel `json:"models"`
	NextPageToken string             `json:"nextPageToken"`
}

type fetchGeminiModel struct {
	Name any `json:"name"`
}

type fetchOllamaTagsResponse struct {
	Models []fetchOllamaModel `json:"models"`
}

type fetchOllamaModel struct {
	Name string `json:"name"`
}

func FetchChannelUpstreamModelIDs(channel *model.Channel) ([]string, error) {
	return FetchChannelUpstreamModelIDsWithOptions(channel, FetchChannelUpstreamModelIDsOptions{})
}

func FetchChannelUpstreamModelIDsWithOptions(channel *model.Channel, options FetchChannelUpstreamModelIDsOptions) ([]string, error) {
	if channel == nil {
		return nil, fmt.Errorf("channel is required")
	}

	baseURL := constant.ChannelBaseURLs[channel.Type]
	if channel.GetBaseURL() != "" {
		baseURL = channel.GetBaseURL()
	}

	if channel.Type == constant.ChannelTypeOllama {
		key := strings.TrimSpace(strings.Split(channel.Key, "\n")[0])
		models, err := fetchOllamaModels(baseURL, key, channel.GetSetting().Proxy, options)
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(models))
		for _, item := range models {
			names = append(names, item.Name)
		}
		return normalizeFetchedModelNames(names), nil
	}

	if channel.Type == constant.ChannelTypeGemini {
		key, _, apiErr := channel.GetNextEnabledKey()
		if apiErr != nil {
			return nil, fmt.Errorf("获取渠道密钥失败: %w", apiErr)
		}
		models, err := fetchGeminiModels(baseURL, strings.TrimSpace(key), channel.GetSetting().Proxy, options)
		if err != nil {
			return nil, err
		}
		return normalizeFetchedModelNames(models), nil
	}

	url := buildFetchModelsURL(channel.Type, baseURL)
	key, _, apiErr := channel.GetNextEnabledKey()
	if apiErr != nil {
		return nil, fmt.Errorf("获取渠道密钥失败: %w", apiErr)
	}
	key = strings.TrimSpace(key)

	headers, err := BuildFetchModelsHeaders(channel, key)
	if err != nil {
		return nil, err
	}

	body, err := fetchModelsResponseBody(http.MethodGet, url, channel.GetSetting().Proxy, headers, options)
	if err != nil {
		return nil, err
	}

	var result fetchOpenAIModelsResponse
	if err := common.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(result.Data))
	for _, item := range result.Data {
		ids = append(ids, item.ID)
	}
	return normalizeFetchedModelNames(ids), nil
}

func BuildFetchModelsHeaders(channel *model.Channel, key string) (http.Header, error) {
	if channel == nil {
		return nil, fmt.Errorf("channel is required")
	}

	var headers http.Header
	switch channel.Type {
	case constant.ChannelTypeAnthropic:
		headers = buildClaudeFetchModelsAuthHeader(key)
	default:
		headers = buildFetchModelsAuthHeader(key)
	}

	headerOverride := channel.GetHeaderOverride()
	for k, v := range headerOverride {
		if isFetchModelsHeaderPassthroughRuleKey(k) {
			continue
		}
		str, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("invalid header override for key %s", k)
		}
		if strings.Contains(str, "{api_key}") {
			str = strings.ReplaceAll(str, "{api_key}", key)
		}
		headers.Set(k, str)
	}

	return headers, nil
}

func buildFetchModelsURL(channelType int, baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	switch channelType {
	case constant.ChannelTypeAli:
		return fmt.Sprintf("%s/compatible-mode/v1/models", baseURL)
	case constant.ChannelTypeZhipu_v4:
		if plan, ok := constant.ChannelSpecialBases[baseURL]; ok && plan.OpenAIBaseURL != "" {
			return fmt.Sprintf("%s/models", strings.TrimRight(plan.OpenAIBaseURL, "/"))
		}
		return fmt.Sprintf("%s/api/paas/v4/models", baseURL)
	case constant.ChannelTypeVolcEngine:
		if plan, ok := constant.ChannelSpecialBases[baseURL]; ok && plan.OpenAIBaseURL != "" {
			return fmt.Sprintf("%s/v1/models", strings.TrimRight(plan.OpenAIBaseURL, "/"))
		}
		return fmt.Sprintf("%s/v1/models", baseURL)
	case constant.ChannelTypeMoonshot:
		if plan, ok := constant.ChannelSpecialBases[baseURL]; ok && plan.OpenAIBaseURL != "" {
			return fmt.Sprintf("%s/models", strings.TrimRight(plan.OpenAIBaseURL, "/"))
		}
		return fmt.Sprintf("%s/v1/models", baseURL)
	default:
		return fmt.Sprintf("%s/v1/models", baseURL)
	}
}

func buildFetchModelsAuthHeader(token string) http.Header {
	headers := http.Header{}
	headers.Add("Authorization", fmt.Sprintf("Bearer %s", token))
	return headers
}

func buildClaudeFetchModelsAuthHeader(token string) http.Header {
	headers := http.Header{}
	headers.Add("x-api-key", token)
	headers.Add("anthropic-version", "2023-06-01")
	return headers
}

func isFetchModelsHeaderPassthroughRuleKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	lower := strings.ToLower(key)
	return key == "*" || strings.HasPrefix(lower, "re:") || strings.HasPrefix(lower, "regex:")
}

func normalizeFetchedModelNames(models []string) []string {
	seen := make(map[string]struct{}, len(models))
	normalized := make([]string, 0, len(models))
	for _, modelName := range models {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			continue
		}
		if _, ok := seen[modelName]; ok {
			continue
		}
		seen[modelName] = struct{}{}
		normalized = append(normalized, modelName)
	}
	return normalized
}

func fetchGeminiModels(baseURL, apiKey, proxyURL string, options FetchChannelUpstreamModelIDsOptions) ([]string, error) {
	allModels := make([]string, 0)
	nextPageToken := ""
	maxPages := 100

	for page := 0; page < maxPages; page++ {
		url := fmt.Sprintf("%s/v1beta/models", strings.TrimRight(baseURL, "/"))
		if nextPageToken != "" {
			url = fmt.Sprintf("%s?pageToken=%s", url, nextPageToken)
		}

		headers := http.Header{}
		headers.Set("x-goog-api-key", apiKey)
		body, err := fetchModelsResponseBody(http.MethodGet, url, proxyURL, headers, options)
		if err != nil {
			return nil, err
		}

		var modelsResponse fetchGeminiModelsResponse
		if err = common.Unmarshal(body, &modelsResponse); err != nil {
			return nil, fmt.Errorf("解析响应失败: %v", err)
		}

		for _, model := range modelsResponse.Models {
			modelNameValue, ok := model.Name.(string)
			if !ok {
				continue
			}
			allModels = append(allModels, strings.TrimPrefix(modelNameValue, "models/"))
		}

		nextPageToken = modelsResponse.NextPageToken
		if nextPageToken == "" {
			break
		}
	}

	return allModels, nil
}

func fetchOllamaModels(baseURL, apiKey, proxyURL string, options FetchChannelUpstreamModelIDsOptions) ([]fetchOllamaModel, error) {
	url := fmt.Sprintf("%s/api/tags", strings.TrimRight(baseURL, "/"))

	headers := http.Header{}
	if apiKey != "" {
		headers.Set("Authorization", "Bearer "+apiKey)
	}

	body, err := fetchModelsResponseBody(http.MethodGet, url, proxyURL, headers, options)
	if err != nil {
		return nil, err
	}

	var tagsResponse fetchOllamaTagsResponse
	if err := common.Unmarshal(body, &tagsResponse); err != nil {
		return nil, fmt.Errorf("解析响应失败: %v", err)
	}
	return tagsResponse.Models, nil
}

func fetchModelsResponseBody(method, requestURL, proxyURL string, headers http.Header, options FetchChannelUpstreamModelIDsOptions) ([]byte, error) {
	if err := validateFetchModelsURL(requestURL, options); err != nil {
		return nil, err
	}

	client, err := NewProxyHttpClient(proxyURL)
	if err != nil {
		return nil, err
	}
	client = fetchModelsHTTPClientWithOptions(client, options)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, requestURL, nil)
	if err != nil {
		return nil, err
	}
	for k, values := range headers {
		for _, value := range values {
			req.Header.Add(k, value)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, fetchModelsResponseBodyLimitBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > fetchModelsResponseBodyLimitBytes {
		return nil, fmt.Errorf("response body too large: limit %d bytes", fetchModelsResponseBodyLimitBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code: %d", resp.StatusCode)
	}
	return body, nil
}

func validateFetchModelsURL(requestURL string, options FetchChannelUpstreamModelIDsOptions) error {
	fetchSetting := system_setting.GetFetchSetting()
	allowPrivateIP := fetchSetting.AllowPrivateIp || options.AllowPrivateIP
	if err := common.ValidateURLWithFetchSetting(requestURL, fetchSetting.EnableSSRFProtection, allowPrivateIP, fetchSetting.DomainFilterMode, fetchSetting.IpFilterMode, fetchSetting.DomainList, fetchSetting.IpList, fetchSetting.AllowedPorts, fetchSetting.ApplyIPFilterForDomain); err != nil {
		return fmt.Errorf("request reject: %v", err)
	}
	return nil
}

func fetchModelsHTTPClientWithOptions(client *http.Client, options FetchChannelUpstreamModelIDsOptions) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	if !options.AllowPrivateIP {
		return client
	}
	wrapped := *client
	wrapped.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		urlStr := req.URL.String()
		if err := validateFetchModelsURL(urlStr, options); err != nil {
			return fmt.Errorf("redirect to %s blocked: %v", urlStr, err)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
	return &wrapped
}
