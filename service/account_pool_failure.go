package service

import (
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"gorm.io/gorm"
)

const (
	accountPoolRateLimitCooldownSeconds    = int64(60)
	accountPoolTemporaryDisableSeconds     = int64(60)
	accountPoolLastErrorMaxLength          = 1024
	accountPoolTempDisabledReasonMaxLength = 512
	accountPoolMaskedRuntimeSecret         = "***"
)

var accountPoolRuntimeSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)(sk-[A-Za-z0-9][A-Za-z0-9._-]*)`),
	regexp.MustCompile(`(?i)(access[_ -]?token[:=]\s*)[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)(refresh[_ -]?token[:=]\s*)[A-Za-z0-9._~+/=-]+`),
}

// classifyTransportError returns true if the transport error is persistent
// (a structural network failure unlikely to self-resolve), or false if it
// is transient (a timeout or temporary connectivity blip).
func classifyTransportError(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(sanitizeAccountPoolFailureMessage(err, accountPoolLastErrorMaxLength))
	persistentPhrases := []string{
		"connection refused",
		"no route to host",
		"network is unreachable",
		"no such host",
		"proxy authentication required",
		"authentication failed",
	}
	for _, phrase := range persistentPhrases {
		if strings.Contains(msg, phrase) {
			return true
		}
	}
	return false
}

// classifyAccountPoolFailure is a pure function that maps an upstream error onto
// a GORM Updates map for model.AccountPoolAccount. It always includes the base
// bookkeeping fields (last_error, last_failure_at, failure_count) and adds
// cooldown/status fields appropriate to the error's HTTP status code and error code.
func classifyAccountPoolFailure(account model.AccountPoolAccount, err *types.NewAPIError, isOAuth bool, now int64) map[string]any {
	if err == nil {
		return nil
	}

	// monotonic keeps the larger of two int64 cooldown timestamps so that an
	// earlier failure's longer cooldown is never shortened by a newer failure.
	monotonic := func(existing, computed int64) int64 {
		if existing > computed {
			return existing
		}
		return computed
	}

	updates := map[string]any{
		"last_error":      sanitizeAccountPoolFailureMessage(err, accountPoolLastErrorMaxLength),
		"last_failure_at": now,
		"failure_count":   gorm.Expr("failure_count + ?", 1),
	}

	// Use upstream status code when available; fall back to the error's StatusCode.
	code := err.GetUpstreamStatusCode()
	if code == 0 {
		code = err.StatusCode
	}

	cfg := accountPoolFailureConfig()

	// Case 1 — Network / transport error. Check first because code may be 0.
	if err.GetErrorCode() == types.ErrorCodeDoRequestFailed {
		persistent := classifyTransportError(err)
		reason := sanitizeAccountPoolFailureMessage(err, accountPoolTempDisabledReasonMaxLength)
		var disableUntil int64
		if persistent {
			disableUntil = now + int64(cfg.TransportPersistentMinutes*60)
		} else {
			disableUntil = now + int64(cfg.TransportTransientSeconds)
		}
		updates["temp_disabled_until"] = monotonic(account.TempDisabledUntil, disableUntil)
		updates["temp_disabled_reason"] = reason
		return updates
	}

	switch code {
	// Case 2 — Auth failure: immediately expire the account.
	case http.StatusUnauthorized, http.StatusForbidden:
		updates["status"] = model.AccountPoolAccountStatusExpired
		updates["rate_limited_until"] = int64(0)
		updates["temp_disabled_until"] = int64(0)
		updates["overload_until"] = int64(0)
		updates["temp_disabled_reason"] = ""

	// Case 3 — Rate limited.
	case http.StatusTooManyRequests:
		resetAt, ok := parseAccountPool429ResetAt(err.GetUpstreamHeader(), err.GetUpstreamBody(), now)
		if ok {
			updates["rate_limited_until"] = monotonic(account.RateLimitedUntil, resetAt)
		} else if cfg.RateLimit429FallbackEnabled {
			fb := int64(clampRateLimit429CooldownSeconds(cfg.RateLimit429FallbackSeconds))
			updates["rate_limited_until"] = monotonic(account.RateLimitedUntil, now+fb)
		}
		updates["temp_disabled_reason"] = sanitizeAccountPoolFailureMessage(err, accountPoolTempDisabledReasonMaxLength)
		// status stays enabled; do NOT touch temp_disabled_until or overload_until

	// Case 4 — Request timeout: brief temporary disable.
	case http.StatusRequestTimeout:
		reason := sanitizeAccountPoolFailureMessage(err, accountPoolTempDisabledReasonMaxLength)
		updates["temp_disabled_until"] = monotonic(account.TempDisabledUntil, now+int64(cfg.TransportTransientSeconds))
		updates["temp_disabled_reason"] = reason

	// Case 5 — Overload (Claude/Anthropic-specific 529).
	case 529:
		reason := sanitizeAccountPoolFailureMessage(err, accountPoolTempDisabledReasonMaxLength)
		updates["overload_until"] = monotonic(account.OverloadUntil, now+int64(cfg.OverloadCooldownMinutes*60))
		updates["temp_disabled_reason"] = reason

	// Case 6 — Other 5xx (500-599 except 529).
	default:
		if code >= 500 && code <= 599 {
			reason := sanitizeAccountPoolFailureMessage(err, accountPoolTempDisabledReasonMaxLength)
			updates["temp_disabled_until"] = monotonic(account.TempDisabledUntil, now+60)
			updates["temp_disabled_reason"] = reason
			break
		}

		// Case 7 — 400 with a disabling phrase in the error message or body.
		if code == http.StatusBadRequest {
			combined := strings.ToLower(sanitizeAccountPoolFailureMessage(err, accountPoolLastErrorMaxLength))
			bodyLower := strings.ToLower(string(err.GetUpstreamBody()))
			hasPhrase := false
			for _, phrase := range []string{
				"organization has been disabled",
				"organization disabled",
				"credit balance",
				"identity verification",
			} {
				if strings.Contains(combined, phrase) || strings.Contains(bodyLower, phrase) {
					hasPhrase = true
					break
				}
			}
			if hasPhrase {
				updates["status"] = model.AccountPoolAccountStatusExpired
				updates["rate_limited_until"] = int64(0)
				updates["temp_disabled_until"] = int64(0)
				updates["overload_until"] = int64(0)
				updates["temp_disabled_reason"] = ""
			}
			// else: fall through — base fields only (Case 9)
			break
		}

		// Case 8 — Out-of-range code (not a network error, not 5xx, not 4xx in 100-599).
		if code < 100 || code > 599 {
			reason := sanitizeAccountPoolFailureMessage(err, accountPoolTempDisabledReasonMaxLength)
			updates["temp_disabled_until"] = monotonic(account.TempDisabledUntil, now+60)
			updates["temp_disabled_reason"] = reason
			break
		}

		// Case 9 — Any other code (400 without phrase already handled above,
		// 402, 404, 409, 410, 422, etc.): base fields only, no cooldown.
	}

	return updates
}

// RecordAccountPoolRuntimeAttemptFailure loads the account inside a transaction,
// classifies the upstream error, and applies the resulting updates atomically.
func RecordAccountPoolRuntimeAttemptFailure(accountID int, err *types.NewAPIError, now int64) error {
	if accountID <= 0 || err == nil {
		return nil
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var account model.AccountPoolAccount
		if err2 := tx.First(&account, accountID).Error; err2 != nil {
			return err2
		}
		credential, decryptErr := DecryptAccountPoolCredentialConfig(account.CredentialConfig)
		isOAuth := false
		if decryptErr == nil {
			isOAuth = accountPoolHasOAuthRuntimeCredential(credential, AccountPoolTokenState{})
		}
		updates := classifyAccountPoolFailure(account, err, isOAuth, now)
		if len(updates) == 0 {
			return nil
		}
		return tx.Model(&model.AccountPoolAccount{}).
			Where("id = ?", accountID).
			Updates(updates).Error
	})
}

func sanitizeAccountPoolFailureMessage(err *types.NewAPIError, maxLen int) string {
	if err == nil {
		return ""
	}
	message := err.MaskSensitiveErrorWithStatusCode()
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

func truncateAccountPoolFailureMessage(message string, maxLen int) string {
	if maxLen <= 0 || len(message) <= maxLen {
		return message
	}
	end := 0
	for end < len(message) {
		_, size := utf8.DecodeRuneInString(message[end:])
		if end+size > maxLen {
			break
		}
		end += size
	}
	return message[:end]
}
