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

func RecordAccountPoolRuntimeAttemptFailure(accountID int, err *types.NewAPIError, now int64) error {
	if accountID <= 0 || err == nil {
		return nil
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	updates := accountPoolFailureUpdate(err, now)
	if len(updates) == 0 {
		return nil
	}
	return model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", accountID).
		Updates(updates).Error
}

func accountPoolFailureUpdate(err *types.NewAPIError, now int64) map[string]any {
	if err == nil {
		return nil
	}
	message := sanitizeAccountPoolFailureMessage(err, accountPoolLastErrorMaxLength)
	updates := map[string]any{
		"last_error":      message,
		"last_failure_at": now,
		"failure_count":   gorm.Expr("failure_count + ?", 1),
	}
	switch err.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		updates["status"] = model.AccountPoolAccountStatusExpired
		updates["rate_limited_until"] = int64(0)
		updates["temp_disabled_until"] = int64(0)
		updates["temp_disabled_reason"] = ""
		return updates
	case http.StatusTooManyRequests:
		updates["status"] = model.AccountPoolAccountStatusEnabled
		updates["rate_limited_until"] = now + accountPoolRateLimitCooldownSeconds
		updates["temp_disabled_until"] = int64(0)
		updates["temp_disabled_reason"] = ""
		return updates
	}
	if shouldTemporarilyDisableAccountPoolAccount(err) {
		reason := sanitizeAccountPoolFailureMessage(err, accountPoolTempDisabledReasonMaxLength)
		updates["status"] = model.AccountPoolAccountStatusEnabled
		updates["temp_disabled_until"] = now + accountPoolTemporaryDisableSeconds
		updates["temp_disabled_reason"] = reason
	}
	return updates
}

func shouldTemporarilyDisableAccountPoolAccount(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	if err.GetErrorCode() == types.ErrorCodeDoRequestFailed {
		return true
	}
	statusCode := err.StatusCode
	if statusCode < 100 || statusCode > 599 {
		return true
	}
	return statusCode >= http.StatusInternalServerError
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
