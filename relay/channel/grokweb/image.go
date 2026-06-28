// Best-effort reverse-engineered grok.com web IMAGE generation.
//
// WARNING: FRAGILE / REVERSE-ENGINEERED / BEST-EFFORT. See the package doc in
// constants.go. grok.com has no public images endpoint: images are generated via
// the SAME app-chat SSE endpoint with image flags set, and the final image is
// referenced by a path in an `image_chunk` card frame that must be fetched from
// the asset CDN with the SSO cookie. Shapes mirror grok2api xai_chat.py
// (_handle_card / image_chunk) and xai_assets.py (download). Live grok.com is NOT
// verifiable in-repo; all tests run against httptest mocks — the inline shapes
// here are UNVERIFIABLE against the real service and may break without notice.
package grokweb

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// assetHTTPClient fetches generated images from the asset CDN. It is a package
// var so tests can inject a client; the default has no proxy (a proxy-aware
// download that reuses the pooled account's proxy is a documented follow-up).
var assetHTTPClient = http.DefaultClient

// buildGrokImageRequest builds the grok web chat body for an image-generation
// request: the prompt is the message, and the image flags instruct grok to
// generate `count` images and stream their progress cards. returnImageBytes is
// kept false to match grok2api: the bytes are fetched from the asset CDN, not
// inlined (the inline path is unverified against the live service).
func buildGrokImageRequest(prompt, modeID string, count int) *grokChatRequest {
	if count <= 0 {
		count = defaultImageGenerationCount
	}
	return &grokChatRequest{
		Message:               prompt,
		ModeID:                modeID,
		CollectionIDs:         []string{},
		Connectors:            []string{},
		FileAttachments:       []string{},
		ImageAttachments:      []string{},
		DeviceEnvInfo: grokDeviceEnvInfo{
			DarkModeEnabled:  false,
			DevicePixelRatio: 2,
			ScreenHeight:     1329,
			ScreenWidth:      2056,
			ViewportHeight:   1083,
			ViewportWidth:    2056,
		},
		ToolOverrides:         grokToolOverrides{},
		ResponseMetadata:      map[string]any{},
		DisableMemory:         true,
		// Image generation must not be diluted by web search.
		DisableSearch:         true,
		EnableImageGeneration: true,
		EnableImageStreaming:  true,
		ImageGenerationCount:  count,
		ReturnImageBytes:      false,
		SendFinalMetadata:     true,
		Temporary:             true,
	}
}

// grokImageCard is the decoded form of cardAttachment.jsonData for an image
// generation frame. Mirror of grok2api _handle_card: a final image is signalled
// by progress == 100 with moderated == false, and imageUrl is a CDN-relative path.
type grokImageCard struct {
	ImageChunk *struct {
		Progress  int    `json:"progress"`
		ImageUuid string `json:"imageUuid"`
		ImageURL  string `json:"imageUrl"`
		Moderated bool   `json:"moderated"`
	} `json:"image_chunk"`
}

// grokImageScan is the outcome of scanning a grok image-generation SSE stream.
type grokImageScan struct {
	urls         []string         // CDN-relative paths of completed, non-moderated images
	sawModerated bool             // at least one completed image was content-moderated
	inBandErr    *grokInBandError // an in-band error frame, if any
}

// collectGrokImageURLs scans a grok web SSE stream and returns the CDN-relative
// paths of completed (progress==100, non-moderated) generated images, in order,
// plus whether any completed image was moderated (so the caller can distinguish a
// deterministic content rejection from a transient empty result) and any in-band
// error frame.
func collectGrokImageURLs(body io.Reader) (grokImageScan, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	scan := grokImageScan{}
	seen := make(map[string]struct{})
	for scanner.Scan() {
		kind, payload := classifyLine(scanner.Text())
		if kind == "done" {
			break
		}
		if kind != "data" {
			continue
		}
		var frame grokSSEFrame
		if err := common.UnmarshalJsonStr(payload, &frame); err != nil {
			continue
		}
		if frame.Error != nil {
			scan.inBandErr = frame.Error
			return scan, nil
		}
		if frame.Result == nil || frame.Result.Response == nil {
			continue
		}
		card := frame.Result.Response.CardAttachment
		if card == nil || strings.TrimSpace(card.JsonData) == "" {
			continue
		}
		var imageCard grokImageCard
		if err := common.UnmarshalJsonStr(card.JsonData, &imageCard); err != nil {
			continue
		}
		chunk := imageCard.ImageChunk
		if chunk == nil || chunk.Progress != 100 {
			continue
		}
		if chunk.Moderated {
			scan.sawModerated = true
			continue
		}
		path := strings.TrimSpace(chunk.ImageURL)
		if path == "" {
			continue
		}
		if _, dup := seen[path]; dup {
			continue
		}
		seen[path] = struct{}{}
		scan.urls = append(scan.urls, path)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return scan, err
	}
	return scan, nil
}

// assetDownloadURL resolves an image path to an absolute asset URL. An
// already-absolute URL is passed through unchanged (mirror of grok2api
// xai_assets.resolve_download_url); a relative/absolute path is joined onto the
// asset CDN base.
func assetDownloadURL(path string) string {
	if strings.Contains(path, "://") {
		return path
	}
	return strings.TrimRight(assetsBaseURL, "/") + "/" + strings.TrimLeft(path, "/")
}

// downloadGrokImage fetches one generated image from the asset CDN using the SSO
// cookie. Returns the raw bytes. Mirror of grok2api xai_assets.resolve_download_url
// (origin/referer set to the asset host).
func downloadGrokImage(path, sso, cfClearance string) ([]byte, error) {
	url := assetDownloadURL(path)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	origin := strings.TrimRight(assetsBaseURL, "/")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	req.Header.Set("Cookie", buildSSOCookie(sso, cfClearance))

	resp, err := assetHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer service.CloseResponseBodyGracefully(resp)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("grok-web: asset download failed with status %d", resp.StatusCode)
	}
	// Cap the read to a sane image size (32 MiB) to avoid unbounded memory.
	return io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024))
}

// grokWebImageHandler relays a grok.com image-generation SSE response to the
// client as a single OpenAI image-generation JSON response. It collects the
// completed image paths from the stream, downloads each from the asset CDN with
// the SSO cookie, and returns them as base64 (b64_json) — grok asset URLs are
// auth-gated and expiring, and Wynth has no media cache, so a bare URL would be
// unusable to the client.
func grokWebImageHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	sso, cfClearance := parseCredential(info.ApiKey)

	scan, err := collectGrokImageURLs(resp.Body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeReadResponseBodyFailed)
	}
	if scan.inBandErr != nil {
		return nil, inBandErrorToAPIError(scan.inBandErr)
	}
	if len(scan.urls) == 0 {
		if scan.sawModerated {
			// Content moderation is a deterministic client-side rejection, not an
			// account problem: skip-retry so the pool does not burn other accounts
			// retrying a prompt that will be rejected everywhere.
			return nil, types.NewError(
				errors.New("grok-web: image prompt rejected by content moderation"),
				types.ErrorCodePromptBlocked,
				types.ErrOptionWithSkipRetry(),
			)
		}
		// No images and no moderation signal: a transient empty result; allow retry.
		return nil, types.NewError(errors.New("grok-web: no image generated"), types.ErrorCodeEmptyResponse)
	}

	data := make([]dto.ImageData, 0, len(scan.urls))
	for _, path := range scan.urls {
		raw, derr := downloadGrokImage(path, sso, cfClearance)
		if derr != nil {
			return nil, types.NewError(derr, types.ErrorCodeDoRequestFailed)
		}
		data = append(data, dto.ImageData{
			B64Json: base64.StdEncoding.EncodeToString(raw),
		})
	}

	imageResp := dto.ImageResponse{
		Created: common.GetTimestamp(),
		Data:    data,
	}
	jsonResp, merr := common.Marshal(imageResp)
	if merr != nil {
		return nil, types.NewError(merr, types.ErrorCodeJsonMarshalFailed)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(http.StatusOK)
	_, _ = c.Writer.Write(jsonResp)

	// grok.com returns no token usage for images. The framework applies the per-image
	// (n) multiplier via PriceData.OtherRatio["n"] ONLY when grok-2-image is deployed
	// with a configured per-image price (UsePrice). DEPLOY REQUIREMENT: configure a
	// price for grok-2-image. As a safety net for the ratio-pricing fallback (where the
	// framework does NOT apply n), completion is scaled by the number of images actually
	// delivered so n>1 is not silently under-billed. PromptTokens (the prompt, sent once)
	// is not scaled.
	imageCount := len(data)
	promptTokens := info.GetEstimatePromptTokens()
	return &dto.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: imageCount,
		TotalTokens:      promptTokens + imageCount,
	}, nil
}
