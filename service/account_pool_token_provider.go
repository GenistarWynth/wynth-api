package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

const accountPoolOAuthRefreshSkewSeconds = int64(60)

type AccountPoolRuntimeCredentialRequest struct {
	AccountID         int
	Credential        AccountPoolCredentialConfig
	TokenState        AccountPoolTokenState
	ProxyURL          string
	Platform          string
	Now               int64
	SkipFailureRecord bool
}

type accountPoolOAuthRefreshFunc func(context.Context, string, string) (*CodexOAuthTokenResult, error)
type accountPoolClaudeOAuthRefreshFunc func(context.Context, string, string) (*CodexOAuthTokenResult, error)
type accountPoolXAIOAuthRefreshFunc func(context.Context, string, string, string) (*CodexOAuthTokenResult, error)

// accountPoolGeminiOAuthRefreshFunc carries the account's oauth_type so the Gemini
// refresh can select the correct OAuth client (e.g. antigravity vs gemini-cli).
type accountPoolGeminiOAuthRefreshFunc func(ctx context.Context, oauthType string, refreshToken string, proxyURL string) (*CodexOAuthTokenResult, error)

type accountPoolTokenStateUpdateFunc func(accountID int, oldTokenState string, newTokenState string) (int64, error)

// accountPoolVertexSATokenMintFunc mints a Vertex AI service-account access token
// from the raw SA JSON. It is overridable in tests.
type accountPoolVertexSATokenMintFunc func(ctx context.Context, saJSON []byte, proxyURL string) (*CodexOAuthTokenResult, error)

var (
	accountPoolOAuthRefreshGroup  singleflight.Group
	accountPoolOAuthRefresh       accountPoolOAuthRefreshFunc       = RefreshCodexOAuthTokenWithProxy
	accountPoolClaudeOAuthRefresh accountPoolClaudeOAuthRefreshFunc = RefreshClaudeOAuthTokenWithProxy
	accountPoolGeminiOAuthRefresh accountPoolGeminiOAuthRefreshFunc = RefreshGeminiOAuthTokenForType
	accountPoolXAIOAuthRefresh    accountPoolXAIOAuthRefreshFunc    = RefreshXAIOAuthTokenForClientWithProxy
	accountPoolTokenStateUpdate   accountPoolTokenStateUpdateFunc   = updateAccountPoolRuntimeTokenState
	accountPoolVertexSATokenMint  accountPoolVertexSATokenMintFunc  = MintVertexServiceAccountToken
)

func accountPoolIsServiceAccountCredential(credential AccountPoolCredentialConfig) bool {
	return strings.EqualFold(strings.TrimSpace(credential.Type), AccountPoolCredentialTypeServiceAccount) &&
		strings.TrimSpace(credential.ServiceAccountJSON) != ""
}

func ResolveAccountPoolRuntimeCredential(ctx context.Context, req AccountPoolRuntimeCredentialRequest) (string, error) {
	if token := strings.TrimSpace(req.Credential.APIKey); token != "" {
		return token, nil
	}
	now := req.Now
	if now <= 0 {
		now = common.GetTimestamp()
	}
	// Vertex AI service-account credentials mint a short-lived access token via the
	// JWT-bearer flow. A cached, still-valid token is reused; otherwise a fresh
	// token is minted and persisted into token_state via the CAS update path.
	if accountPoolIsServiceAccountCredential(req.Credential) {
		if accountPoolAccessTokenUsable(req.TokenState, now) {
			return strings.TrimSpace(req.TokenState.AccessToken), nil
		}
		if req.AccountID <= 0 {
			return "", errors.New("account pool account id is required for service account mint")
		}
		if ctx == nil {
			ctx = context.Background()
		}
		return mintAccountPoolRuntimeServiceAccountToken(ctx, req.AccountID, req.ProxyURL, now, req.SkipFailureRecord)
	}
	if accountPoolAccessTokenUsable(req.TokenState, now) {
		return strings.TrimSpace(req.TokenState.AccessToken), nil
	}
	if !accountPoolHasOAuthRuntimeCredential(req.Credential, req.TokenState) {
		return "", nil
	}
	refreshToken := accountPoolRuntimeRefreshToken(req.Credential, req.TokenState)
	if refreshToken == "" && strings.TrimSpace(req.TokenState.AccessToken) != "" && req.TokenState.ExpiresAt == 0 {
		return strings.TrimSpace(req.TokenState.AccessToken), nil
	}
	if req.AccountID <= 0 {
		return "", errors.New("account pool account id is required for oauth refresh")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return refreshAccountPoolRuntimeOAuthToken(ctx, req.AccountID, req.ProxyURL, req.Platform, now, req.SkipFailureRecord)
}

func accountPoolAccessTokenUsable(state AccountPoolTokenState, now int64) bool {
	if strings.TrimSpace(state.AccessToken) == "" {
		return false
	}
	if state.ExpiresAt == 0 {
		return true
	}
	return state.ExpiresAt > now+accountPoolOAuthRefreshSkewSeconds
}

func accountPoolHasOAuthRuntimeCredential(credential AccountPoolCredentialConfig, state AccountPoolTokenState) bool {
	return strings.EqualFold(strings.TrimSpace(credential.Type), AccountPoolCredentialTypeOAuth) ||
		strings.TrimSpace(credential.RefreshToken) != "" ||
		strings.TrimSpace(state.AccessToken) != "" ||
		strings.TrimSpace(state.RefreshToken) != ""
}

func accountPoolRuntimeRefreshToken(credential AccountPoolCredentialConfig, state AccountPoolTokenState) string {
	if token := strings.TrimSpace(state.RefreshToken); token != "" {
		return token
	}
	return strings.TrimSpace(credential.RefreshToken)
}

func refreshAccountPoolRuntimeOAuthToken(ctx context.Context, accountID int, proxyURL string, platform string, now int64, skipFailureRecord bool) (string, error) {
	value, err, _ := accountPoolOAuthRefreshGroup.Do(accountPoolOAuthRefreshSingleflightKey(accountID, skipFailureRecord), func() (any, error) {
		// The first waiter owns the refresh context. If that request is cancelled,
		// coalesced waiters receive the same cancellation and can retry another account.
		return refreshAccountPoolRuntimeOAuthTokenOnce(ctx, accountID, proxyURL, platform, now, skipFailureRecord)
	})
	if err != nil {
		return "", err
	}
	token, ok := value.(string)
	if !ok {
		return "", errors.New("account pool oauth refresh returned invalid token")
	}
	return token, nil
}

func accountPoolOAuthRefreshSingleflightKey(accountID int, skipFailureRecord bool) string {
	key := "oauth:" + strconv.Itoa(accountID)
	if skipFailureRecord {
		return key + ":skip_failure_record"
	}
	return key + ":record_failure"
}

func refreshAccountPoolRuntimeOAuthTokenOnce(ctx context.Context, accountID int, proxyURL string, platform string, now int64, skipFailureRecord bool) (string, error) {
	account, credential, state, rawTokenState, err := loadAccountPoolRuntimeTokenState(accountID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(credential.APIKey) != "" {
		return strings.TrimSpace(credential.APIKey), nil
	}
	if accountPoolAccessTokenUsable(state, now) {
		return strings.TrimSpace(state.AccessToken), nil
	}
	refreshToken := accountPoolRuntimeRefreshToken(credential, state)
	if refreshToken == "" {
		err := errors.New("account pool oauth refresh_token is required")
		if !skipFailureRecord {
			_ = expireAccountPoolRuntimeMissingRefreshToken(account.Id, now)
		}
		return "", err
	}

	// Dispatch to the platform-specific refresh function.
	var result *CodexOAuthTokenResult
	switch platform {
	case model.AccountPoolPlatformAnthropic:
		result, err = accountPoolClaudeOAuthRefresh(ctx, refreshToken, proxyURL)
	case model.AccountPoolPlatformGemini:
		result, err = accountPoolGeminiOAuthRefresh(ctx, account.OAuthType, refreshToken, proxyURL)
	case model.AccountPoolPlatformXAI:
		result, err = accountPoolXAIOAuthRefresh(ctx, refreshToken, proxyURL, credential.ClientID)
	case model.AccountPoolPlatformOpenAI, "":
		result, err = accountPoolOAuthRefresh(ctx, refreshToken, proxyURL)
	default:
		return "", fmt.Errorf("account pool oauth refresh is not supported for platform %q", platform)
	}
	if err != nil {
		if !skipFailureRecord {
			_ = markAccountPoolRuntimeTokenRefreshFailure(account.Id, err, now)
		}
		return "", err
	}
	if result == nil || strings.TrimSpace(result.AccessToken) == "" {
		err := errors.New("account pool oauth refresh response missing access_token")
		if !skipFailureRecord {
			_ = markAccountPoolRuntimeTokenRefreshFailure(account.Id, err, now)
		}
		return "", err
	}

	nextState := state.NextVersion()
	nextState.AccessToken = strings.TrimSpace(result.AccessToken)
	nextState.RefreshToken = strings.TrimSpace(result.RefreshToken)
	if nextState.RefreshToken == "" {
		nextState.RefreshToken = refreshToken
	}
	nextState.ExpiresAt = result.ExpiresAt.Unix()
	encryptedState, err := EncryptAccountPoolTokenState(nextState)
	if err != nil {
		return "", err
	}
	rowsAffected, err := accountPoolTokenStateUpdate(account.Id, rawTokenState, encryptedState)
	if err != nil {
		return "", err
	}
	if rowsAffected == 0 {
		_, _, latestState, _, loadErr := loadAccountPoolRuntimeTokenState(account.Id)
		if loadErr != nil {
			return "", loadErr
		}
		if accountPoolAccessTokenUsable(latestState, now) {
			return strings.TrimSpace(latestState.AccessToken), nil
		}
		return "", errors.New("account pool oauth token state changed during refresh")
	}
	return nextState.AccessToken, nil
}

// mintAccountPoolRuntimeServiceAccountToken mints (or reuses a cached) Vertex AI
// service-account access token for the given account, coalescing concurrent
// callers via the shared singleflight group and persisting the minted token into
// token_state via the CAS update path.
func mintAccountPoolRuntimeServiceAccountToken(ctx context.Context, accountID int, proxyURL string, now int64, skipFailureRecord bool) (string, error) {
	value, err, _ := accountPoolOAuthRefreshGroup.Do(accountPoolServiceAccountMintSingleflightKey(accountID, skipFailureRecord), func() (any, error) {
		return mintAccountPoolRuntimeServiceAccountTokenOnce(ctx, accountID, proxyURL, now, skipFailureRecord)
	})
	if err != nil {
		return "", err
	}
	token, ok := value.(string)
	if !ok {
		return "", errors.New("account pool service account mint returned invalid token")
	}
	return token, nil
}

func accountPoolServiceAccountMintSingleflightKey(accountID int, skipFailureRecord bool) string {
	key := "vertex_sa:" + strconv.Itoa(accountID)
	if skipFailureRecord {
		return key + ":skip_failure_record"
	}
	return key + ":record_failure"
}

func mintAccountPoolRuntimeServiceAccountTokenOnce(ctx context.Context, accountID int, proxyURL string, now int64, skipFailureRecord bool) (string, error) {
	account, credential, state, rawTokenState, err := loadAccountPoolRuntimeTokenState(accountID)
	if err != nil {
		return "", err
	}
	if !accountPoolIsServiceAccountCredential(credential) {
		return "", errors.New("account pool service account credential is missing service_account_json")
	}
	if accountPoolAccessTokenUsable(state, now) {
		return strings.TrimSpace(state.AccessToken), nil
	}

	result, err := accountPoolVertexSATokenMint(ctx, []byte(credential.ServiceAccountJSON), proxyURL)
	if err != nil {
		if !skipFailureRecord {
			_ = markAccountPoolRuntimeTokenRefreshFailure(account.Id, err, now)
		}
		return "", err
	}
	if result == nil || strings.TrimSpace(result.AccessToken) == "" {
		err := errors.New("account pool service account mint response missing access_token")
		if !skipFailureRecord {
			_ = markAccountPoolRuntimeTokenRefreshFailure(account.Id, err, now)
		}
		return "", err
	}

	nextState := state.NextVersion()
	nextState.AccessToken = strings.TrimSpace(result.AccessToken)
	// Service-account tokens are minted, not refresh-token-refreshed; clear any
	// refresh token carried in the cached state.
	nextState.RefreshToken = ""
	nextState.ExpiresAt = result.ExpiresAt.Unix()
	encryptedState, err := EncryptAccountPoolTokenState(nextState)
	if err != nil {
		return "", err
	}
	rowsAffected, err := accountPoolTokenStateUpdate(account.Id, rawTokenState, encryptedState)
	if err != nil {
		return "", err
	}
	if rowsAffected == 0 {
		_, _, latestState, _, loadErr := loadAccountPoolRuntimeTokenState(account.Id)
		if loadErr != nil {
			return "", loadErr
		}
		if accountPoolAccessTokenUsable(latestState, now) {
			return strings.TrimSpace(latestState.AccessToken), nil
		}
		return "", errors.New("account pool service account token state changed during mint")
	}
	return nextState.AccessToken, nil
}

func updateAccountPoolRuntimeTokenState(accountID int, oldTokenState string, newTokenState string) (int64, error) {
	tx := model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ? AND token_state = ?", accountID, oldTokenState).
		Update("token_state", newTokenState)
	return tx.RowsAffected, tx.Error
}

func loadAccountPoolRuntimeTokenState(accountID int) (model.AccountPoolAccount, AccountPoolCredentialConfig, AccountPoolTokenState, string, error) {
	var account model.AccountPoolAccount
	if err := model.DB.First(&account, accountID).Error; err != nil {
		return account, AccountPoolCredentialConfig{}, AccountPoolTokenState{}, "", err
	}
	credential, err := DecryptAccountPoolCredentialConfig(account.CredentialConfig)
	if err != nil {
		return account, credential, AccountPoolTokenState{}, account.TokenState, fmt.Errorf("decrypt account pool credential: %w", err)
	}
	state, err := DecryptAccountPoolTokenState(account.TokenState)
	if err != nil {
		return account, credential, state, account.TokenState, fmt.Errorf("decrypt account pool token state: %w", err)
	}
	return account, credential, state, account.TokenState, nil
}

func markAccountPoolRuntimeTokenRefreshFailure(accountID int, err error, now int64) error {
	if accountID <= 0 || err == nil {
		return nil
	}
	message := sanitizeAccountPoolRuntimeErrorMessage(err.Error(), accountPoolLastErrorMaxLength)
	reason := sanitizeAccountPoolRuntimeErrorMessage(err.Error(), accountPoolTempDisabledReasonMaxLength)
	return model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ? AND status = ?", accountID, model.AccountPoolAccountStatusEnabled).
		Updates(map[string]any{
			"temp_disabled_until":  now + accountPoolTemporaryDisableSeconds,
			"temp_disabled_reason": reason,
			"last_error":           message,
		}).Error
}

func expireAccountPoolRuntimeMissingRefreshToken(accountID int, now int64) error {
	if accountID <= 0 {
		return nil
	}
	const message = "account pool oauth refresh_token is required"
	return model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ? AND status = ?", accountID, model.AccountPoolAccountStatusEnabled).
		Updates(map[string]any{
			"status":               model.AccountPoolAccountStatusExpired,
			"rate_limited_until":   int64(0),
			"temp_disabled_until":  int64(0),
			"overload_until":       int64(0),
			"temp_disabled_reason": "",
			"last_error":           message,
			"last_failure_at":      now,
			"failure_count":        gorm.Expr("failure_count + ?", 1),
			"updated_time":         now,
		}).Error
}

func sanitizeAccountPoolRuntimeErrorMessage(message string, maxLen int) string {
	message = common.MaskSensitiveInfo(message)
	for _, pattern := range accountPoolRuntimeSecretPatterns {
		message = pattern.ReplaceAllStringFunc(message, func(match string) string {
			lower := strings.ToLower(match)
			for _, prefix := range []string{"bearer ", "access_token:", "access token:", "access-token:", "refresh_token:", "refresh token:", "refresh-token:"} {
				if strings.HasPrefix(lower, prefix) {
					return match[:len(prefix)] + accountPoolMaskedRuntimeSecret
				}
			}
			return accountPoolMaskedRuntimeSecret
		})
	}
	return truncateAccountPoolFailureMessage(message, maxLen)
}
