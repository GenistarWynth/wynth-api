package service

import (
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

var upstreamSourcePlaintextWarnOnce sync.Once

// ReadUpstreamSourceAuthConfig returns the plaintext auth JSON for a stored
// AuthConfig value. It transparently decrypts secret envelopes and passes
// legacy plaintext rows through unchanged.
func ReadUpstreamSourceAuthConfig(stored string) (string, error) {
	if strings.TrimSpace(stored) == "" {
		return "", nil
	}
	if isUpstreamSourceSecretEnvelope(stored) {
		return common.DecryptSecretString(stored)
	}
	return stored, nil
}

// WriteUpstreamSourceAuthConfig encrypts plaintext auth JSON for storage. When
// the crypto secret is not stable it falls back to storing plaintext so the
// feature keeps working (with a one-time warning).
func WriteUpstreamSourceAuthConfig(plaintext string) (string, error) {
	if strings.TrimSpace(plaintext) == "" {
		return "", nil
	}
	if !common.CryptoSecretStable {
		upstreamSourcePlaintextWarnOnce.Do(func() {
			common.SysError("upstream source credentials stored as plaintext: set CRYPTO_SECRET or SESSION_SECRET to enable encryption")
		})
		return plaintext, nil
	}
	return common.EncryptSecretString(plaintext)
}

func isUpstreamSourceSecretEnvelope(stored string) bool {
	var envelope common.SecretEnvelope
	if err := common.UnmarshalJsonStr(stored, &envelope); err != nil {
		return false
	}
	return envelope.Version != 0 && strings.TrimSpace(envelope.Algorithm) != "" && strings.TrimSpace(envelope.Ciphertext) != ""
}
