package common

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecretEnvelopeRoundTripDoesNotExposePlaintext(t *testing.T) {
	oldSecret := CryptoSecret
	CryptoSecret = "account-pool-secret-for-tests"
	t.Cleanup(func() { CryptoSecret = oldSecret })

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
	CryptoSecret = "first-secret"
	encrypted, err := EncryptSecretString("refresh-token")
	require.NoError(t, err)

	CryptoSecret = "second-secret"
	t.Cleanup(func() { CryptoSecret = oldSecret })
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
	oldSecret := CryptoSecret
	CryptoSecret = "shape-secret"
	t.Cleanup(func() { CryptoSecret = oldSecret })

	encrypted, err := EncryptSecretString("plain")
	require.NoError(t, err)
	assert.True(t, strings.Contains(encrypted, `"v":1`) || strings.Contains(encrypted, `"v": 1`))
	assert.Contains(t, encrypted, `"alg":"AES-256-GCM"`)
}
