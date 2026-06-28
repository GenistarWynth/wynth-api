package grokweb

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// imageCardLine builds one grok SSE line carrying an image_chunk cardAttachment.
// jsonData is the nested JSON STRING grok sends; building it via Marshal handles
// the escaping the same way the live service does.
func imageCardLine(t *testing.T, progress int, imageURL string, moderated bool) string {
	t.Helper()
	inner := map[string]any{
		"id": "card-" + imageURL,
		"image_chunk": map[string]any{
			"progress":  progress,
			"imageUuid": "uuid-" + imageURL,
			"imageUrl":  imageURL,
			"moderated": moderated,
		},
	}
	innerJSON, err := common.Marshal(inner)
	require.NoError(t, err)
	frame := map[string]any{
		"result": map[string]any{
			"response": map[string]any{
				"cardAttachment": map[string]any{"jsonData": string(innerJSON)},
			},
		},
	}
	frameJSON, err := common.Marshal(frame)
	require.NoError(t, err)
	return "data: " + string(frameJSON)
}

// newImageHandlerContext builds a gin context + 200 SSE http.Response for image
// handler tests. info carries a grok-2-image model and a default sso credential.
func newImageHandlerContext(t *testing.T, sse string) (*gin.Context, *httptest.ResponseRecorder, *http.Response, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(sse)),
		Header:     http.Header{},
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok-2-image", ApiKey: "sso"}}
	return c, rec, resp, info
}

func TestConvertImageRequestBuildsGrokImageBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	n := uint(3)
	req := dto.ImageRequest{Model: "grok-2-image", Prompt: "a red fox", N: &n}

	out, err := (&Adaptor{}).ConvertImageRequest(c, &relaycommon.RelayInfo{}, req)
	require.NoError(t, err)
	body, ok := out.(*grokChatRequest)
	require.True(t, ok, "expected *grokChatRequest, got %T", out)

	assert.Equal(t, "a red fox", body.Message)
	assert.True(t, body.EnableImageGeneration)
	assert.True(t, body.EnableImageStreaming)
	assert.Equal(t, 3, body.ImageGenerationCount)
	assert.False(t, body.ReturnImageBytes, "image bytes are fetched from the CDN, not inlined")
	assert.True(t, body.DisableSearch, "image gen must not trigger web search")
}

func TestConvertImageRequestDefaultsCountAndRequiresPrompt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	// Missing n → default count.
	out, err := (&Adaptor{}).ConvertImageRequest(c, &relaycommon.RelayInfo{}, dto.ImageRequest{Prompt: "x"})
	require.NoError(t, err)
	assert.Equal(t, defaultImageGenerationCount, out.(*grokChatRequest).ImageGenerationCount)

	// Empty prompt → error (deterministic client error, not an account failure).
	_, err = (&Adaptor{}).ConvertImageRequest(c, &relaycommon.RelayInfo{}, dto.ImageRequest{Prompt: "   "})
	require.Error(t, err)
}

func TestCollectGrokImageURLsFiltersIncompleteModeratedAndDupes(t *testing.T) {
	body := strings.Join([]string{
		imageCardLine(t, 40, "img1.png", false),  // in-progress → skip
		imageCardLine(t, 100, "img1.png", false), // complete → keep
		imageCardLine(t, 100, "img1.png", false), // duplicate → skip
		imageCardLine(t, 100, "bad.png", true),   // moderated → skip
		imageCardLine(t, 100, "img2.png", false), // complete → keep
		"data: [DONE]",
	}, "\n")

	scan, err := collectGrokImageURLs(strings.NewReader(body))
	require.NoError(t, err)
	require.Nil(t, scan.inBandErr)
	assert.Equal(t, []string{"img1.png", "img2.png"}, scan.urls)
	assert.True(t, scan.sawModerated, "the moderated frame should be recorded")
}

func TestCollectGrokImageURLsSurfacesInBandError(t *testing.T) {
	body := `data: {"error":{"message":"rate limited","code":429}}` + "\n"
	scan, err := collectGrokImageURLs(strings.NewReader(body))
	require.NoError(t, err)
	require.NotNil(t, scan.inBandErr)
	assert.Empty(t, scan.urls)
}

func TestGrokWebImageHandlerDownloadsAndReturnsB64(t *testing.T) {
	imageBytes := []byte("\x89PNG\r\n\x1a\nFAKEPNGDATA")
	var gotCookie, gotOrigin string
	assetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		gotOrigin = r.Header.Get("Origin")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(imageBytes)
	}))
	defer assetSrv.Close()

	prevBase := assetsBaseURL
	assetsBaseURL = assetSrv.URL
	defer func() { assetsBaseURL = prevBase }()

	sse := strings.Join([]string{
		imageCardLine(t, 100, "users/u/img1.png", false),
		"data: [DONE]",
	}, "\n")

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(sse)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok-2-image", ApiKey: "test-sso-token"},
	}

	usage, apiErr := grokWebImageHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	var imageResp dto.ImageResponse
	require.NoError(t, common.UnmarshalJsonStr(rec.Body.String(), &imageResp))
	require.Len(t, imageResp.Data, 1)
	assert.Equal(t, base64.StdEncoding.EncodeToString(imageBytes), imageResp.Data[0].B64Json)

	// The asset download must carry the SSO cookie and the asset-host Origin.
	assert.Contains(t, gotCookie, "sso=test-sso-token")
	assert.Equal(t, assetSrv.URL, gotOrigin)
}

// A fully-moderated result must be a skip-retry content rejection, not a retryable
// empty response — otherwise the pool would burn other accounts on a prompt that is
// rejected everywhere.
func TestGrokWebImageHandlerModeratedIsSkipRetryPromptBlocked(t *testing.T) {
	sse := strings.Join([]string{
		imageCardLine(t, 100, "blocked.png", true),
		"data: [DONE]",
	}, "\n")
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(sse)),
		Header:     http.Header{},
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok-2-image", ApiKey: "sso"}}

	_, apiErr := grokWebImageHandler(c, info, resp)
	require.NotNil(t, apiErr)
	assert.True(t, types.IsSkipRetryError(apiErr), "moderation rejection must skip retry across accounts")
}

func TestAssetDownloadURL(t *testing.T) {
	prev := assetsBaseURL
	assetsBaseURL = "https://assets.grok.com"
	defer func() { assetsBaseURL = prev }()

	assert.Equal(t, "https://assets.grok.com/users/u/i.png", assetDownloadURL("users/u/i.png"))
	assert.Equal(t, "https://assets.grok.com/users/u/i.png", assetDownloadURL("/users/u/i.png"))
	// Already-absolute URL is passed through unchanged (no double-base).
	assert.Equal(t, "https://assets.grok.com/x/i.png", assetDownloadURL("https://assets.grok.com/x/i.png"))
}

func TestGrokWebImageHandlerReturnsMultipleImages(t *testing.T) {
	assetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo the path so each image's bytes differ, proving per-URL download.
		_, _ = w.Write([]byte("bytes:" + r.URL.Path))
	}))
	defer assetSrv.Close()
	prevBase := assetsBaseURL
	assetsBaseURL = assetSrv.URL
	defer func() { assetsBaseURL = prevBase }()

	sse := strings.Join([]string{
		imageCardLine(t, 100, "a.png", false),
		imageCardLine(t, 100, "b.png", false),
		"data: [DONE]",
	}, "\n")
	c, rec, resp, info := newImageHandlerContext(t, sse)

	_, apiErr := grokWebImageHandler(c, info, resp)
	require.Nil(t, apiErr)
	var imageResp dto.ImageResponse
	require.NoError(t, common.UnmarshalJsonStr(rec.Body.String(), &imageResp))
	require.Len(t, imageResp.Data, 2)
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("bytes:/a.png")), imageResp.Data[0].B64Json)
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("bytes:/b.png")), imageResp.Data[1].B64Json)
}

func TestGrokWebImageHandlerDownloadFailureDoesNotLeakCookie(t *testing.T) {
	assetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer assetSrv.Close()
	prevBase := assetsBaseURL
	assetsBaseURL = assetSrv.URL
	defer func() { assetsBaseURL = prevBase }()

	sse := strings.Join([]string{imageCardLine(t, 100, "x.png", false), "data: [DONE]"}, "\n")
	c, _, resp, info := newImageHandlerContext(t, sse)
	info.ApiKey = "super-secret-sso-token"

	_, apiErr := grokWebImageHandler(c, info, resp)
	require.NotNil(t, apiErr)
	assert.NotContains(t, apiErr.Error(), "super-secret-sso-token", "the sso token must never appear in an error")
}

func TestGrokWebImageHandlerInBandErrorMapsStatus(t *testing.T) {
	sse := `data: {"error":{"message":"rate limited","code":8}}` + "\n"
	c, _, resp, info := newImageHandlerContext(t, sse)
	_, apiErr := grokWebImageHandler(c, info, resp)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
}

func TestGrokWebImageHandlerEmptyStreamIsTypedError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("data: [DONE]\n")),
		Header:     http.Header{},
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok-2-image", ApiKey: "sso"}}

	_, apiErr := grokWebImageHandler(c, info, resp)
	require.NotNil(t, apiErr)
}
