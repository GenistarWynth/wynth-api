package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// SecureVerificationRequired preserves the channel-key proof scope used by
// existing callers.
func SecureVerificationRequired() gin.HandlerFunc {
	return SecureVerificationRequiredForScope("channel.key.read")
}

// SecureVerificationRequiredForScope protects a route with an action-specific
// proof scope.
func SecureVerificationRequiredForScope(requiredScope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !RequireSecureVerification(c, requiredScope) {
			return
		}
		c.Next()
	}
}

// RequireSecureVerification validates an action-specific proof and marks the
// authenticated request as step-up verified.
func RequireSecureVerification(c *gin.Context, requiredScope string) bool {
	if !RequireSecurityProof(c, requiredScope, []string{"2fa", "passkey"}) {
		return false
	}
	c.Set("secure_verified", true)
	return true
}

// RequireSecurityProof validates a proof against the authenticated dashboard
// session and writes the shared proof error contract on failure.
func RequireSecurityProof(c *gin.Context, requiredScope string, allowedMethods []string) bool {
	identity, ok := GetSessionAuthIdentity(c)
	if !ok {
		securityProofError(c, "SECURITY_PROOF_INVALID", "安全验证状态无效")
		return false
	}
	raw := strings.TrimSpace(c.GetHeader("X-Security-Proof"))
	if raw == "" {
		securityProofError(c, "SECURITY_PROOF_REQUIRED", "需要安全验证")
		return false
	}
	if _, err := service.VerifySecurityProof(raw, identity, requiredScope, allowedMethods); err != nil {
		writeSecurityProofError(c, err)
		return false
	}
	return true
}

// RequireAccountPoolCredentialsExportProof validates and atomically consumes a
// passkey proof bound to the exact account pool before credentials are loaded.
func RequireAccountPoolCredentialsExportProof(c *gin.Context, poolID int) bool {
	identity, ok := GetSessionAuthIdentity(c)
	if !ok {
		securityProofError(c, "SECURITY_PROOF_INVALID", "安全验证状态无效")
		return false
	}
	raw := strings.TrimSpace(c.GetHeader("X-Security-Proof"))
	if raw == "" {
		securityProofError(c, "SECURITY_PROOF_REQUIRED", "需要安全验证")
		return false
	}
	if err := service.ConsumeAccountPoolCredentialsExportProof(raw, identity, poolID); err != nil {
		writeSecurityProofError(c, err)
		return false
	}
	c.Set("secure_verified", true)
	return true
}

func writeSecurityProofError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrAuthTokenExpired):
		securityProofError(c, "SECURITY_PROOF_EXPIRED", "安全验证已过期")
	case errors.Is(err, service.ErrProofScope), errors.Is(err, service.ErrProofResource):
		securityProofError(c, "SECURITY_PROOF_SCOPE_MISMATCH", "安全验证范围不匹配")
	case errors.Is(err, service.ErrProofMethod):
		securityProofError(c, "SECURITY_PROOF_METHOD_MISMATCH", "安全验证方式不匹配")
	default:
		securityProofError(c, "SECURITY_PROOF_INVALID", "安全验证状态无效")
	}
}

func securityProofError(c *gin.Context, code, message string) {
	c.JSON(http.StatusForbidden, gin.H{
		"success": false,
		"message": message,
		"code":    code,
	})
	c.Abort()
}
