package common

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecretEnvelopeRoundTripDoesNotExposePlaintext(t *testing.T) {
	setStableCryptoSecretForEnvelopeTest(t, "account-pool-secret-for-tests")

	encrypted, err := EncryptSecretString("sk-test-secret")
	require.NoError(t, err)
	require.NotEmpty(t, encrypted)
	assert.NotContains(t, encrypted, "sk-test-secret")

	decrypted, err := DecryptSecretString(encrypted)
	require.NoError(t, err)
	assert.Equal(t, "sk-test-secret", decrypted)
}

func TestSecretEnvelopeRejectsWrongKey(t *testing.T) {
	oldSecret := CryptoSecret
	oldStable := CryptoSecretStable
	CryptoSecret = "first-secret"
	CryptoSecretStable = true
	t.Cleanup(func() {
		CryptoSecret = oldSecret
		CryptoSecretStable = oldStable
	})

	encrypted, err := EncryptSecretString("refresh-token")
	require.NoError(t, err)

	CryptoSecret = "second-secret"
	_, err = DecryptSecretString(encrypted)
	require.Error(t, err)
}

func TestSecretEnvelopeKeepsEmptyValueEmpty(t *testing.T) {
	encrypted, err := EncryptSecretString("")
	require.NoError(t, err)
	assert.Empty(t, encrypted)

	decrypted, err := DecryptSecretString("")
	require.NoError(t, err)
	assert.Empty(t, decrypted)
}

func TestSecretEnvelopeHasVersionedShape(t *testing.T) {
	setStableCryptoSecretForEnvelopeTest(t, "shape-secret")

	encrypted, err := EncryptSecretString("plain")
	require.NoError(t, err)
	assert.True(t, strings.Contains(encrypted, `"v":1`) || strings.Contains(encrypted, `"v": 1`))
	assert.Contains(t, encrypted, `"alg":"AES-256-GCM"`)
}

func TestSecretEnvelopeRejectsUnstableDefaultSecret(t *testing.T) {
	oldSecret := CryptoSecret
	oldStable := CryptoSecretStable
	CryptoSecret = "process-random-default-secret"
	CryptoSecretStable = false
	t.Cleanup(func() {
		CryptoSecret = oldSecret
		CryptoSecretStable = oldStable
	})

	encrypted, err := EncryptSecretString("refresh-token")
	require.Error(t, err)
	assert.Empty(t, encrypted)
	assert.ErrorContains(t, err, "CRYPTO_SECRET or SESSION_SECRET")

	_, err = DecryptSecretString(`{"v":1,"alg":"AES-256-GCM","nonce":"AAAAAAAAAAAAAAAA","ciphertext":"AAAA"}`)
	require.Error(t, err)
	assert.ErrorContains(t, err, "CRYPTO_SECRET or SESSION_SECRET")
}

func setStableCryptoSecretForEnvelopeTest(t *testing.T, secret string) {
	t.Helper()
	oldSecret := CryptoSecret
	oldStable := CryptoSecretStable
	CryptoSecret = secret
	CryptoSecretStable = true
	t.Cleanup(func() {
		CryptoSecret = oldSecret
		CryptoSecretStable = oldStable
	})
}
