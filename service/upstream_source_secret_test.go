package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpstreamSourceAuthConfigEncryptRoundTrip(t *testing.T) {
	old := common.CryptoSecretStable
	oldSecret := common.CryptoSecret
	common.CryptoSecretStable = true
	common.CryptoSecret = "test-crypto-secret"
	t.Cleanup(func() {
		common.CryptoSecretStable = old
		common.CryptoSecret = oldSecret
	})

	plaintext := `{"email":"a@b.com","password":"p","access_token":"tok","user_id":7}`
	stored, err := WriteUpstreamSourceAuthConfig(plaintext)
	require.NoError(t, err)
	assert.NotEqual(t, plaintext, stored, "stored form must be ciphertext, not plaintext")

	got, err := ReadUpstreamSourceAuthConfig(stored)
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

func TestUpstreamSourceAuthConfigReadsLegacyPlaintext(t *testing.T) {
	old := common.CryptoSecretStable
	common.CryptoSecretStable = true
	t.Cleanup(func() { common.CryptoSecretStable = old })

	legacy := `{"email":"a@b.com","password":"p"}`
	got, err := ReadUpstreamSourceAuthConfig(legacy)
	require.NoError(t, err)
	assert.Equal(t, legacy, got)
}

func TestUpstreamSourceAuthConfigWriteFallsBackWhenCryptoUnstable(t *testing.T) {
	old := common.CryptoSecretStable
	common.CryptoSecretStable = false
	t.Cleanup(func() { common.CryptoSecretStable = old })

	plaintext := `{"email":"a@b.com"}`
	stored, err := WriteUpstreamSourceAuthConfig(plaintext)
	require.NoError(t, err)
	assert.Equal(t, plaintext, stored)
}

func TestUpstreamSourceAuthConfigEmptyStaysEmpty(t *testing.T) {
	got, err := ReadUpstreamSourceAuthConfig("")
	require.NoError(t, err)
	assert.Equal(t, "", got)
	stored, err := WriteUpstreamSourceAuthConfig("")
	require.NoError(t, err)
	assert.Equal(t, "", stored)
}
