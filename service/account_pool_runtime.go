package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

// These service-local Gin context keys are prefixed with account_pool_ to avoid
// collisions with the shared constant.ContextKey* namespace.
const (
	accountPoolAttemptedAccountIDsContextKey    = "account_pool_attempted_account_ids"
	accountPoolSelectedPoolIDContextKey         = "account_pool_selected_pool_id"
	accountPoolSelectedBindingIDContextKey      = "account_pool_selected_binding_id"
	accountPoolSelectedAccountIDContextKey      = "account_pool_selected_account_id"
	accountPoolSelectedRetryTimesContextKey     = "account_pool_selected_retry_times"
	accountPoolSelectedAffinityKeyContextKey    = "account_pool_selected_affinity_key"
	accountPoolSelectedRuntimeOptionsContextKey = "account_pool_selected_runtime_options"
	accountPoolSelectedRequestQuotaContextKey   = "account_pool_selected_request_quota"
	accountPoolSelectedPlatformContextKey       = "account_pool_selected_platform"
)

func ApplyAccountPoolRuntimeSelection(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request) error {
	ReleaseAccountPoolRuntimeSelection(c)
	clearSelectedAccountPoolRuntimeSelection(c)
	if info != nil {
		info.RuntimeAccountID = ""
		info.RuntimeAnthropicOAuth = false
		info.RuntimeGeminiOAuth = false
		info.RuntimeGeminiOAuthType = ""
		info.RuntimeGeminiProjectID = ""
	}
	if c == nil || info == nil || info.ChannelMeta == nil {
		return nil
	}
	affinityKey := BuildAccountPoolRuntimeAffinityKey(c, info, request)
	selection, release, err := SelectAccountPoolAccountWithLease(AccountPoolSelectionRequest{
		ChannelID:            info.ChannelId,
		RequestModel:         info.OriginModelName,
		ChannelUpstreamModel: info.UpstreamModelName,
		AttemptedAccountIDs:  GetAccountPoolAttemptedAccountIDs(c),
		AffinityKey:          affinityKey,
	})
	if err != nil {
		if errors.Is(err, ErrAccountPoolBindingNotRuntimeEnabled) {
			return nil
		}
		// Phase 2C must map ErrAccountPoolNoSchedulableAccount to a retriable 503.
		return err
	}
	releaseStored := false
	defer func() {
		if !releaseStored {
			release()
		}
	}()

	c.Set(accountPoolSelectedPoolIDContextKey, selection.PoolID)
	c.Set(accountPoolSelectedBindingIDContextKey, selection.BindingID)
	c.Set(accountPoolSelectedAccountIDContextKey, selection.AccountID)
	c.Set(accountPoolSelectedRetryTimesContextKey, selection.AccountRetryTimes)
	c.Set(accountPoolSelectedAffinityKeyContextKey, affinityKey)
	c.Set(accountPoolSelectedRuntimeOptionsContextKey, selection.RuntimeOptions)
	c.Set(accountPoolSelectedRequestQuotaContextKey, selection.RequestQuota)
	c.Set(accountPoolSelectedPlatformContextKey, selection.Platform)
	AddAccountPoolAttemptedAccountID(c, selection.AccountID)

	runtimeCredential, err := ResolveAccountPoolRuntimeCredential(accountPoolRuntimeContext(c), AccountPoolRuntimeCredentialRequest{
		AccountID:         selection.AccountID,
		Credential:        selection.Credential,
		TokenState:        selection.TokenState,
		ProxyURL:          selection.ProxyURL,
		Platform:          selection.Platform,
		SkipFailureRecord: info.IsChannelTest,
	})
	if err != nil {
		return err
	}
	if runtimeCredential == "" {
		return errors.New("account pool selected account has no runtime credential")
	}

	info.ApiKey = runtimeCredential
	info.RuntimeAccountID = accountPoolRuntimeAccountIdentifier(selection, runtimeCredential)
	info.UpstreamModelName = selection.UpstreamModelName
	if selection.ProxyURL != "" {
		info.RuntimeProxy = selection.ProxyURL
	}
	// Signal to the Claude adaptor whether to use OAuth Bearer auth instead of x-api-key.
	if selection.Platform == model.AccountPoolPlatformAnthropic {
		info.RuntimeAnthropicOAuth = accountPoolHasOAuthRuntimeCredential(selection.Credential, selection.TokenState)
	} else {
		info.RuntimeAnthropicOAuth = false
	}
	// Signal to the Gemini adaptor whether to use OAuth Bearer auth instead of x-goog-api-key.
	if selection.Platform == model.AccountPoolPlatformGemini {
		info.RuntimeGeminiOAuth = accountPoolHasOAuthRuntimeCredential(selection.Credential, selection.TokenState)
	} else {
		info.RuntimeGeminiOAuth = false
	}
	// For Gemini Code Assist OAuth accounts, detect or reuse the GCP project id.
	// RuntimeGeminiOAuthType is set ONLY after the project id is resolved so that
	// on detection failure the field is left at its reset ("") value.
	if selection.Platform == model.AccountPoolPlatformGemini &&
		accountPoolHasOAuthRuntimeCredential(selection.Credential, selection.TokenState) &&
		strings.EqualFold(strings.TrimSpace(selection.Credential.OAuthType), AccountPoolGeminiOAuthTypeCodeAssist) {

		projectID := strings.TrimSpace(selection.TokenState.ProjectID)
		if projectID == "" {
			var detectErr error
			projectID, detectErr = accountPoolDetectGeminiCodeAssistProject(
				accountPoolRuntimeContext(c),
				runtimeCredential,
				selection.ProxyURL,
			)
			if detectErr != nil {
				return fmt.Errorf("gemini code assist project detection failed: %w", detectErr)
			}
			// Best-effort cache: ignore error (worst case we re-detect next call).
			_ = cacheAccountPoolGeminiProject(selection.AccountID, projectID)
		}
		// Detection and caching succeeded — now it is safe to mark the type.
		info.RuntimeGeminiOAuthType = AccountPoolGeminiOAuthTypeCodeAssist
		info.RuntimeGeminiProjectID = projectID
	}
	if request != nil {
		request.SetModelName(selection.UpstreamModelName)
	}
	setAccountPoolRuntimeLeaseRelease(c, release)
	releaseStored = true
	return nil
}

func accountPoolRuntimeAccountIdentifier(selection AccountPoolSelectionResult, runtimeCredential string) string {
	if accountID := strings.TrimSpace(selection.AccountIdentifier); accountID != "" {
		return accountID
	}
	// Only attempt JWT extraction for OpenAI/Codex — other platforms don't embed chatgpt_account_id.
	if selection.Platform != "" && selection.Platform != model.AccountPoolPlatformOpenAI {
		return ""
	}
	accountID, ok := ExtractCodexAccountIDFromJWT(runtimeCredential)
	if !ok {
		return ""
	}
	return accountID
}

func accountPoolRuntimeContext(c *gin.Context) context.Context {
	if c == nil || c.Request == nil {
		return context.Background()
	}
	return c.Request.Context()
}

func GetAccountPoolAttemptedAccountIDs(c *gin.Context) map[int]struct{} {
	if c == nil {
		return map[int]struct{}{}
	}
	if attempted, ok := c.Get(accountPoolAttemptedAccountIDsContextKey); ok {
		if accountIDs, ok := attempted.(map[int]struct{}); ok && accountIDs != nil {
			return accountIDs
		}
	}
	return map[int]struct{}{}
}

func AddAccountPoolAttemptedAccountID(c *gin.Context, accountID int) {
	if c == nil || accountID <= 0 {
		return
	}
	accountIDs := GetAccountPoolAttemptedAccountIDs(c)
	accountIDs[accountID] = struct{}{}
	c.Set(accountPoolAttemptedAccountIDsContextKey, accountIDs)
}

func GetSelectedAccountPoolAccountID(c *gin.Context) int {
	if c == nil {
		return 0
	}
	return c.GetInt(accountPoolSelectedAccountIDContextKey)
}

func GetSelectedAccountPoolAccountRetryTimes(c *gin.Context) int {
	if c == nil {
		return 0
	}
	return c.GetInt(accountPoolSelectedRetryTimesContextKey)
}

func RememberSelectedAccountPoolRuntimeAffinity(c *gin.Context, now int64) {
	if c == nil {
		return
	}
	key := c.GetString(accountPoolSelectedAffinityKeyContextKey)
	if key == "" {
		return
	}
	rememberAccountPoolRuntimeAffinity(
		key,
		c.GetInt(accountPoolSelectedBindingIDContextKey),
		c.GetInt(accountPoolSelectedAccountIDContextKey),
		now,
	)
}

func ForgetSelectedAccountPoolRuntimeAffinity(c *gin.Context) {
	if c == nil {
		return
	}
	forgetAccountPoolRuntimeAffinity(c.GetString(accountPoolSelectedAffinityKeyContextKey))
}

func clearSelectedAccountPoolRuntimeSelection(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(accountPoolSelectedPoolIDContextKey, 0)
	c.Set(accountPoolSelectedBindingIDContextKey, 0)
	c.Set(accountPoolSelectedAccountIDContextKey, 0)
	c.Set(accountPoolSelectedRetryTimesContextKey, 0)
	c.Set(accountPoolSelectedAffinityKeyContextKey, "")
	c.Set(accountPoolSelectedRuntimeOptionsContextKey, "")
	c.Set(accountPoolSelectedRequestQuotaContextKey, int64(0))
	c.Set(accountPoolSelectedPlatformContextKey, "")
}

// GetSelectedAccountPoolPlatform returns the platform string of the currently selected
// account pool account (e.g. "anthropic", "openai", or ""). Returns "" when no account
// is selected or when the platform was not recorded during selection.
func GetSelectedAccountPoolPlatform(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return c.GetString(accountPoolSelectedPlatformContextKey)
}

// GetSelectedAccountPoolRuntimeOptions retrieves and parses the runtime options
// for the currently selected account pool account from the gin context.
func GetSelectedAccountPoolRuntimeOptions(c *gin.Context) accountPoolRuntimeOptions {
	if c == nil {
		return accountPoolRuntimeOptions{}
	}
	raw := c.GetString(accountPoolSelectedRuntimeOptionsContextKey)
	opts, err := parseAccountPoolRuntimeOptions(raw)
	if err != nil {
		return accountPoolRuntimeOptions{}
	}
	return opts
}

// GetSelectedAccountPoolRequestQuota returns the RequestQuota of the currently selected
// account pool account. Returns 0 when no account is selected or quota is unset (unlimited).
func GetSelectedAccountPoolRequestQuota(c *gin.Context) int64 {
	if c == nil {
		return 0
	}
	v, _ := c.Get(accountPoolSelectedRequestQuotaContextKey)
	if q, ok := v.(int64); ok {
		return q
	}
	return 0
}
