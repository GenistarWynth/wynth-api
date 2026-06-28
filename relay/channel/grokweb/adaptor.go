// Best-effort reverse-engineered grok.com web chat adaptor.
//
// WARNING: THIS IS A FRAGILE, REVERSE-ENGINEERED, BEST-EFFORT WEB PROXY against
// grok.com's private, undocumented browser API. Live grok.com calls are NOT
// verifiable in-repo; behaviour is mirrored from the grok2api Python reference
// (.codex/external/grok2api-src) and exercised against an httptest mock.
//
// Slice 1 scope: TEXT-only OpenAI chat <-> grok web SSE translation. The
// credential (SSO token) is read from info.ApiKey so a later account-pool slice
// can inject pooled credentials without touching this adaptor.
package grokweb

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
}

// grokCredential is the optional structured form of info.ApiKey. A plain string
// token is also accepted (treated as the sso token with no cf_clearance).
type grokCredential struct {
	SSO         string `json:"sso"`
	CFClearance string `json:"cf_clearance"`
}

// parseCredential extracts the sso token and optional cf_clearance from
// info.ApiKey. info.ApiKey may be a bare token or a JSON object
// {"sso":"...","cf_clearance":"..."}.
func parseCredential(apiKey string) (sso, cfClearance string) {
	key := strings.TrimSpace(apiKey)
	if strings.HasPrefix(key, "{") {
		var cred grokCredential
		if err := common.UnmarshalJsonStr(key, &cred); err == nil {
			return strings.TrimSpace(cred.SSO), strings.TrimSpace(cred.CFClearance)
		}
	}
	return key, ""
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	base := strings.TrimSpace(info.ChannelBaseUrl)
	if base == "" {
		base = defaultBaseURL
	}
	return strings.TrimRight(base, "/") + chatPath, nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	sso, cfClearance := parseCredential(info.ApiKey)
	if sso == "" {
		return errors.New("grok-web: sso token is required (set channel key to the grok.com sso cookie)")
	}
	applyGrokHeaders(req, sso, cfClearance)
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("grok-web: request is nil")
	}
	modeID := modelToModeID(request.Model)
	return buildGrokRequest(request, modeID), nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		service.CloseResponseBodyGracefully(resp)
		return nil, classifyHTTPError(resp.StatusCode, resp.Header, body)
	}
	if info.IsStream {
		return grokWebStreamHandler(c, info, resp)
	}
	return grokWebHandler(c, info, resp)
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}

// ── Unsupported endpoints (Slice 1 = chat only) ─────────────────────────────

func (a *Adaptor) ConvertClaudeRequest(*gin.Context, *relaycommon.RelayInfo, *dto.ClaudeRequest) (any, error) {
	return nil, errors.New("grok-web: /v1/messages endpoint not supported")
}

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	return nil, errors.New("grok-web: gemini endpoint not supported")
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	return nil, errors.New("grok-web: audio endpoint not supported")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	return nil, errors.New("grok-web: image endpoint not supported (deferred to a later slice)")
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, errors.New("grok-web: rerank endpoint not supported")
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return nil, errors.New("grok-web: embedding endpoint not supported")
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	return nil, errors.New("grok-web: /v1/responses endpoint not supported")
}
