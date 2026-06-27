package service

import (
	"fmt"
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
		"no such host is known", // Windows: GetAddrInfoW error
		"actively refused",      // Windows: connection refused (WSAECONNREFUSED)
		"unreachable host",      // Windows: WSAEHOSTUNREACH
		"a socket operation was attempted to an unreachable network", // Windows: WSAENETUNREACH
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
//
// platform selects provider-specific classification logic ("anthropic", "openai", or "").
// Empty string and "openai" are treated identically (OpenAI/Codex behavior).
func classifyAccountPoolFailure(account model.AccountPoolAccount, err *types.NewAPIError, isOAuth bool, now int64, platform string) map[string]any {
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

	// Parse the persisted failure state so escalation counters survive restarts.
	fs, _ := parseAccountPoolFailureState(account.FailureState)

	// writeFS marshals the updated failure state into updates["failure_state"].
	writeFS := func() {
		raw, marshalErr := fs.marshal()
		if marshalErr == nil {
			updates["failure_state"] = raw
		}
	}

	// Case 1 — Network / transport error. Check first because code may be 0.
	if err.GetErrorCode() == types.ErrorCodeDoRequestFailed {
		persistent := classifyTransportError(err)
		reason := sanitizeAccountPoolFailureMessage(err, accountPoolTempDisabledReasonMaxLength)
		var disableUntil int64
		if persistent {
			// Persistent transport errors use a flat cooldown of TransportPersistentMinutes*60s,
			// NOT the 5xx tier ladder. ConsecutiveFailures is still incremented and the hard cap
			// still applies: once ConsecutiveFailures >= Escalation5xxHardCapCount → expire.
			fs.ConsecutiveFailures++
			if fs.ConsecutiveFailures >= cfg.Escalation5xxHardCapCount {
				updates["status"] = model.AccountPoolAccountStatusExpired
				updates["rate_limited_until"] = int64(0)
				updates["temp_disabled_until"] = int64(0)
				updates["overload_until"] = int64(0)
				updates["temp_disabled_reason"] = ""
				fs.LastStatus = code
				writeFS()
				return updates
			}
			disableUntil = now + int64(cfg.TransportPersistentMinutes*60)
			fs.LastStatus = code
			writeFS()
		} else {
			// Transient errors use a flat cooldown; do NOT increment ConsecutiveFailures.
			disableUntil = now + int64(cfg.TransportTransientSeconds)
		}
		updates["temp_disabled_until"] = monotonic(account.TempDisabledUntil, disableUntil)
		updates["temp_disabled_reason"] = reason
		return updates
	}

	switch code {
	// Case 2a — 401 Unauthorized: two-strike for OAuth accounts; immediate expire for API-key accounts.
	case http.StatusUnauthorized:
		if isOAuth {
			// Precise token-version threading for refresh-race detection is a documented
			// future refinement; two-strike provides sufficient tolerance for now.
			if fs.Last401At > 0 && now-fs.Last401At <= int64(cfg.OAuth401RestrikeWindowMinutes*60) {
				// Second 401 within the restrike window → expire.
				updates["status"] = model.AccountPoolAccountStatusExpired
				updates["rate_limited_until"] = int64(0)
				updates["temp_disabled_until"] = int64(0)
				updates["overload_until"] = int64(0)
				updates["temp_disabled_reason"] = ""
			} else {
				// First 401 (or outside window): cool down, record timestamp.
				fs.Last401At = now
				fs.LastStatus = code
				updates["temp_disabled_until"] = monotonic(account.TempDisabledUntil, now+int64(cfg.OAuth401CooldownMinutes*60))
				writeFS()
			}
		} else {
			// Non-OAuth (API-key) account: a 401 means the key is invalid → expire immediately.
			updates["status"] = model.AccountPoolAccountStatusExpired
			updates["rate_limited_until"] = int64(0)
			updates["temp_disabled_until"] = int64(0)
			updates["overload_until"] = int64(0)
			updates["temp_disabled_reason"] = ""
		}

	// Case 2b — 403 Forbidden: three-strike within a rolling window before expiry.
	case http.StatusForbidden:
		window := int64(cfg.HTTP403WindowMinutes * 60)
		if fs.HTTP403WindowStart == 0 || now-fs.HTTP403WindowStart > window {
			// Start a new window.
			fs.HTTP403WindowStart = now
			fs.HTTP403Count = 1
		} else {
			fs.HTTP403Count++
		}
		if fs.HTTP403Count >= cfg.HTTP403Threshold {
			// Threshold reached → expire and clear cooldowns.
			updates["status"] = model.AccountPoolAccountStatusExpired
			updates["rate_limited_until"] = int64(0)
			updates["temp_disabled_until"] = int64(0)
			updates["overload_until"] = int64(0)
			updates["temp_disabled_reason"] = ""
		} else {
			// Under threshold: apply cooldown only.
			updates["temp_disabled_until"] = monotonic(account.TempDisabledUntil, now+int64(cfg.HTTP403CooldownMinutes*60))
		}
		fs.LastStatus = code
		writeFS()

	// Case 3 — Rate limited.
	case http.StatusTooManyRequests:
		if platform == model.AccountPoolPlatformAnthropic {
			// Anthropic: use per-window header parser. If no usable reset is present,
			// do NOT set rate_limited_until (no fallback for Anthropic — a 429 without a
			// reset header is often not a real limit exhaustion).
			resetAt, ok := parseAnthropic429ResetAt(err.GetUpstreamHeader(), now)
			if ok {
				updates["rate_limited_until"] = monotonic(account.RateLimitedUntil, resetAt)
			}
			// If !ok: base bookkeeping only — do not apply any cooldown.
		} else if platform == model.AccountPoolPlatformGemini {
			// Gemini: parse the JSON body for retryDelay / quotaResetDelay / daily-quota message.
			// Unlike Anthropic, Gemini DOES use the configurable fallback when no reset is parseable.
			resetAt, ok := parseGemini429ResetAt(err.GetUpstreamBody(), now)
			if ok {
				updates["rate_limited_until"] = monotonic(account.RateLimitedUntil, resetAt)
			} else if cfg.RateLimit429FallbackEnabled {
				fb := int64(clampRateLimit429CooldownSeconds(cfg.RateLimit429FallbackSeconds))
				updates["rate_limited_until"] = monotonic(account.RateLimitedUntil, now+fb)
			}
		} else {
			// OpenAI / Codex / default path (unchanged).
			resetAt, ok := parseAccountPool429ResetAt(err.GetUpstreamHeader(), err.GetUpstreamBody(), now)
			if ok {
				updates["rate_limited_until"] = monotonic(account.RateLimitedUntil, resetAt)
			} else if cfg.RateLimit429FallbackEnabled {
				fb := int64(clampRateLimit429CooldownSeconds(cfg.RateLimit429FallbackSeconds))
				updates["rate_limited_until"] = monotonic(account.RateLimitedUntil, now+fb)
			}
		}
		// temp_disabled_reason must NOT be set for 429 — last_error already records the message.
		// status stays enabled; do NOT touch temp_disabled_until or overload_until.

	// Case 4 — Request timeout: brief temporary disable.
	case http.StatusRequestTimeout:
		reason := sanitizeAccountPoolFailureMessage(err, accountPoolTempDisabledReasonMaxLength)
		updates["temp_disabled_until"] = monotonic(account.TempDisabledUntil, now+int64(cfg.TransportTransientSeconds))
		updates["temp_disabled_reason"] = reason

	// Case 5 — Overload (Claude/Anthropic-specific 529).
	case 529:
		// 529 does NOT increment ConsecutiveFailures (overload is load-side, not account-health).
		// temp_disabled_reason must NOT be set — overload_until is its own axis and
		// last_error already records the message (consistent with the 429 branch).
		updates["overload_until"] = monotonic(account.OverloadUntil, now+int64(cfg.OverloadCooldownMinutes*60))

	// Case 6 — Other 5xx (500-599 except 529): escalating tiered cooldown.
	default:
		if code >= 500 && code <= 599 {
			reason := sanitizeAccountPoolFailureMessage(err, accountPoolTempDisabledReasonMaxLength)
			fs.ConsecutiveFailures++
			tier := cfg.Escalation5xxTiersSeconds[min(fs.ConsecutiveFailures-1, len(cfg.Escalation5xxTiersSeconds)-1)]
			fs.LastStatus = code
			if fs.ConsecutiveFailures >= cfg.Escalation5xxHardCapCount {
				updates["status"] = model.AccountPoolAccountStatusExpired
				updates["rate_limited_until"] = int64(0)
				updates["temp_disabled_until"] = int64(0)
				updates["overload_until"] = int64(0)
				updates["temp_disabled_reason"] = ""
				writeFS()
			} else {
				updates["temp_disabled_until"] = monotonic(account.TempDisabledUntil, now+int64(tier))
				updates["temp_disabled_reason"] = reason
				writeFS()
			}
			break
		}

		// Case 7 — 400 with a disabling phrase in the error message or body.
		if code == http.StatusBadRequest {
			combined := strings.ToLower(sanitizeAccountPoolFailureMessage(err, accountPoolLastErrorMaxLength))
			bodyLower := strings.ToLower(string(err.GetUpstreamBody()))
			hasPhrase := false

			// Platform-gated phrase lists: Anthropic-specific and Gemini-specific phrases extend the base set.
			var phrases []string
			if platform == model.AccountPoolPlatformAnthropic {
				phrases = []string{
					"credit balance is too low",
					"account is not active",
					// Also check the generic OpenAI-era phrases in case they appear in Anthropic responses.
					"organization has been disabled",
					"organization disabled",
					"credit balance",
					"identity verification",
				}
			} else if platform == model.AccountPoolPlatformGemini {
				phrases = []string{
					"api key not valid",
					"api_key_invalid",
					"api key expired",
					"permission_denied",
				}
			} else {
				phrases = []string{
					"organization has been disabled",
					"organization disabled",
					"credit balance",
					"identity verification",
				}
			}
			for _, phrase := range phrases {
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
// classifies the upstream error, applies the resulting updates atomically, and sets
// an in-process fast-path block so the just-failed account is excluded immediately
// by the scheduler without waiting for DB cooldown reads to propagate.
//
// platform selects provider-specific classification ("anthropic", "openai", or "").
// Empty string and "openai" are treated identically.
func RecordAccountPoolRuntimeAttemptFailure(accountID int, err *types.NewAPIError, now int64, platform string) error {
	if accountID <= 0 || err == nil {
		return nil
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	var updates map[string]any
	txErr := model.DB.Transaction(func(tx *gorm.DB) error {
		var account model.AccountPoolAccount
		if err2 := tx.First(&account, accountID).Error; err2 != nil {
			return err2
		}
		credential, decryptErr := DecryptAccountPoolCredentialConfig(account.CredentialConfig)
		isOAuth := false
		if decryptErr == nil {
			isOAuth = accountPoolHasOAuthRuntimeCredential(credential, AccountPoolTokenState{})
		} else {
			common.SysError(fmt.Sprintf("account pool: failed to decrypt credential for account %d during failure classification: %v", accountID, decryptErr))
		}
		updates = classifyAccountPoolFailure(account, err, isOAuth, now, platform)
		if len(updates) == 0 {
			return nil
		}
		return tx.Model(&model.AccountPoolAccount{}).
			Where("id = ?", accountID).
			Updates(updates).Error
	})
	if txErr != nil {
		return txErr
	}
	if len(updates) == 0 {
		// The failure produced no actionable state change (no cooldown/status update);
		// do not set an in-process block for a no-op.
		return nil
	}

	// Set the in-process fast-path block only when the updates contain a real sidelining
	// signal (expiry or a non-zero cooldown timestamp). No-cooldown codes (404, 402, etc.)
	// only write bookkeeping fields and must NOT set a block that contradicts the DB.
	hasSideliningSignal := false
	if st, ok := updates["status"]; ok && st == model.AccountPoolAccountStatusExpired {
		hasSideliningSignal = true
	}
	for _, field := range []string{"rate_limited_until", "temp_disabled_until", "overload_until"} {
		if v, ok := updates[field]; ok {
			if ts, ok2 := v.(int64); ok2 && ts > now {
				hasSideliningSignal = true
			}
		}
	}
	if !hasSideliningSignal {
		return nil
	}

	// blockUntil = max cooldown timestamp from the updates, floored at now+floor, capped at now+cap.
	blockUntil := int64(0)
	for _, field := range []string{"rate_limited_until", "temp_disabled_until", "overload_until"} {
		if v, ok := updates[field]; ok {
			if ts, ok2 := v.(int64); ok2 && ts > blockUntil {
				blockUntil = ts
			}
		}
	}
	if blockUntil < now+accountPoolRuntimeBlockFloorSeconds {
		blockUntil = now + accountPoolRuntimeBlockFloorSeconds
	}
	blockCap := now + accountPoolRuntimeBlockCapSeconds
	if blockUntil > blockCap {
		blockUntil = blockCap
	}
	blockAccountPoolRuntime(accountID, blockUntil)
	return nil
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
