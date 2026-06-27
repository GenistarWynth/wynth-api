package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// geminiCodeAssistBaseURL is the Code Assist API base URL.
// It is a package-level var (not const) so tests can override it with an httptest server URL.
var geminiCodeAssistBaseURL = "https://cloudcode-pa.googleapis.com"

// geminiCodeAssistOnboardPollDelay is the wait between onboardUser poll iterations.
// Set to 0 in tests to avoid real sleeps.
var geminiCodeAssistOnboardPollDelay = 1 * time.Second

// geminiCodeAssistMaxPollAttempts is the maximum number of loadCodeAssist/onboardUser
// poll iterations when the initial onboardUser response has done=false.
const geminiCodeAssistMaxPollAttempts = 5

// accountPoolDetectGeminiCodeAssistProject is a mock-friendly seam around
// DetectGeminiCodeAssistProject. Tests stub this to avoid real HTTP calls.
var accountPoolDetectGeminiCodeAssistProject = DetectGeminiCodeAssistProject

// loadCodeAssistRequest is the body sent to the loadCodeAssist endpoint.
type loadCodeAssistRequest struct {
	Metadata geminiCodeAssistMetadata `json:"metadata"`
}

// onboardUserRequest is the body sent to the onboardUser endpoint.
type onboardUserRequest struct {
	TierID   string                   `json:"tierId"`
	Metadata geminiCodeAssistMetadata `json:"metadata"`
}

type geminiCodeAssistMetadata struct {
	IDEType    string `json:"ideType"`
	PluginType string `json:"pluginType"`
}

type geminiCodeAssistTier struct {
	ID string `json:"id"`
}

type loadCodeAssistResponse struct {
	CloudaicompanionProject string               `json:"cloudaicompanionProject"`
	CurrentTier             geminiCodeAssistTier `json:"currentTier"`
	PaidTier                geminiCodeAssistTier `json:"paidTier"`
}

type onboardUserResponse struct {
	Done     bool `json:"done"`
	Response struct {
		CloudaicompanionProject string `json:"cloudaicompanionProject"`
	} `json:"response"`
}

// DetectGeminiCodeAssistProject detects the GCP project id for a Code Assist account.
//
// It POSTs to loadCodeAssist. If the returned cloudaicompanionProject is empty it
// POSTs to onboardUser and polls up to geminiCodeAssistMaxPollAttempts times (each
// separated by geminiCodeAssistOnboardPollDelay). The project id is returned verbatim
// (no "projects/" prefix manipulation) so the caller can use it directly.
func DetectGeminiCodeAssistProject(ctx context.Context, accessToken, proxyURL string) (string, error) {
	client, err := getGeminiOAuthHTTPClient(proxyURL)
	if err != nil {
		return "", fmt.Errorf("gemini code assist: build http client: %w", err)
	}

	resp, err := postGeminiCodeAssist[loadCodeAssistRequest, loadCodeAssistResponse](
		ctx, client, accessToken,
		geminiCodeAssistBaseURL+"/v1internal:loadCodeAssist",
		loadCodeAssistRequest{
			Metadata: geminiCodeAssistMetadata{
				IDEType:    "IDE_UNSPECIFIED",
				PluginType: "GEMINI",
			},
		},
	)
	if err != nil {
		return "", fmt.Errorf("gemini code assist loadCodeAssist: %w", err)
	}

	if project := strings.TrimSpace(resp.CloudaicompanionProject); project != "" {
		return project, nil
	}

	// Project not yet provisioned — run onboardUser and poll.
	tierID := strings.TrimSpace(resp.PaidTier.ID)
	if tierID == "" {
		tierID = strings.TrimSpace(resp.CurrentTier.ID)
	}
	if tierID == "" {
		tierID = "free-tier"
	}

	for attempt := 0; attempt < geminiCodeAssistMaxPollAttempts; attempt++ {
		if attempt > 0 && geminiCodeAssistOnboardPollDelay > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(geminiCodeAssistOnboardPollDelay):
			}
		}

		onboard, err := postGeminiCodeAssist[onboardUserRequest, onboardUserResponse](
			ctx, client, accessToken,
			geminiCodeAssistBaseURL+"/v1internal:onboardUser",
			onboardUserRequest{
				TierID: tierID,
				Metadata: geminiCodeAssistMetadata{
					IDEType:    "IDE_UNSPECIFIED",
					PluginType: "GEMINI",
				},
			},
		)
		if err != nil {
			return "", fmt.Errorf("gemini code assist onboardUser: %w", err)
		}

		if project := strings.TrimSpace(onboard.Response.CloudaicompanionProject); project != "" {
			return project, nil
		}
		if onboard.Done {
			// done=true but project still empty — unexpected but we should stop.
			return "", fmt.Errorf("gemini code assist onboardUser completed but returned empty cloudaicompanionProject")
		}
		// done=false: long-running op in progress, keep polling.
	}

	return "", fmt.Errorf("gemini code assist: cloudaicompanionProject still empty after %d onboardUser attempts", geminiCodeAssistMaxPollAttempts)
}

// postGeminiCodeAssist is a generic POST helper for Code Assist JSON endpoints.
// It marshals reqBody, sends it with the GeminiCLI User-Agent and Bearer token,
// checks the status code first, then decodes the response into respBody.
func postGeminiCodeAssist[Req any, Resp any](ctx context.Context, client *http.Client, accessToken, url string, reqBody Req) (Resp, error) {
	var zero Resp

	bodyBytes, err := common.Marshal(reqBody)
	if err != nil {
		return zero, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return zero, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", geminiOAuthDefaultUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zero, fmt.Errorf("status %d", resp.StatusCode)
	}

	var result Resp
	if err := common.DecodeJson(resp.Body, &result); err != nil {
		return zero, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}

// cacheAccountPoolGeminiProject persists the detected GCP project id into an account's
// token state. This is best-effort: if the state was concurrently updated, the worst
// case is that we re-detect on the next request (Version is NOT bumped).
func cacheAccountPoolGeminiProject(accountID int, projectID string) error {
	if accountID <= 0 || strings.TrimSpace(projectID) == "" {
		return nil
	}

	var account model.AccountPoolAccount
	if err := model.DB.First(&account, accountID).Error; err != nil {
		return fmt.Errorf("cacheAccountPoolGeminiProject: load account: %w", err)
	}

	state, err := DecryptAccountPoolTokenState(account.TokenState)
	if err != nil {
		return fmt.Errorf("cacheAccountPoolGeminiProject: decrypt token state: %w", err)
	}

	state.ProjectID = strings.TrimSpace(projectID)

	encrypted, err := EncryptAccountPoolTokenState(state)
	if err != nil {
		return fmt.Errorf("cacheAccountPoolGeminiProject: encrypt token state: %w", err)
	}

	return model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", accountID).
		Update("token_state", encrypted).Error
}
