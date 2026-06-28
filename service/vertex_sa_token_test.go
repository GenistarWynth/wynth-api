package service

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestServiceAccountJSON builds a minimal but valid GCP service-account JSON
// from the given RSA key, returning the raw JSON and the matching public key.
func newTestServiceAccountJSON(t *testing.T, tokenURI string) (string, *rsa.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	sa := map[string]any{
		"type":           "service_account",
		"project_id":     "test-project-123",
		"private_key_id": "key-abc",
		"private_key":    pemKey,
		"client_email":   "svc@test-project-123.iam.gserviceaccount.com",
		"token_uri":      tokenURI,
	}
	data, err := common.Marshal(sa)
	require.NoError(t, err)
	return string(data), &key.PublicKey
}

func TestMintVertexServiceAccountTokenSignsValidJWT(t *testing.T) {
	var (
		gotAssertion string
		gotGrant     string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotGrant = r.Form.Get("grant_type")
		gotAssertion = r.Form.Get("assertion")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"ya29.minted-token","expires_in":3600}`))
	}))
	defer server.Close()

	saJSON, pubKey := newTestServiceAccountJSON(t, server.URL)

	result, err := MintVertexServiceAccountToken(context.Background(), []byte(saJSON), "")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "ya29.minted-token", result.AccessToken)
	assert.True(t, result.ExpiresAt.After(time.Now()))

	assert.Equal(t, vertexSAJWTBearerGrantType, gotGrant)
	require.NotEmpty(t, gotAssertion)

	// Verify the assertion is a correctly RS256-signed JWT against the SA public key.
	parts := strings.Split(gotAssertion, ".")
	require.Len(t, parts, 3)
	signingInput := parts[0] + "." + parts[1]
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)
	digest := sha256.Sum256([]byte(signingInput))
	require.NoError(t, rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, digest[:], signature))

	// Verify header claims.
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err)
	var header map[string]any
	require.NoError(t, common.Unmarshal(headerJSON, &header))
	assert.Equal(t, "RS256", header["alg"])
	assert.Equal(t, "JWT", header["typ"])
	assert.Equal(t, "key-abc", header["kid"])

	// Verify payload claims (iss/scope/aud/exp).
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var claims map[string]any
	require.NoError(t, common.Unmarshal(claimsJSON, &claims))
	assert.Equal(t, "svc@test-project-123.iam.gserviceaccount.com", claims["iss"])
	assert.Equal(t, vertexSAScope, claims["scope"])
	assert.Equal(t, server.URL, claims["aud"])
	iat, iatOK := claims["iat"].(float64)
	require.True(t, iatOK)
	exp, expOK := claims["exp"].(float64)
	require.True(t, expOK)
	assert.Equal(t, int64(3600), int64(exp)-int64(iat))
}

func TestExtractVertexServiceAccountInfo(t *testing.T) {
	saJSON, _ := newTestServiceAccountJSON(t, "https://oauth2.example/token")
	info, err := ExtractVertexServiceAccountInfo([]byte(saJSON))
	require.NoError(t, err)
	assert.Equal(t, "test-project-123", info.ProjectID)
	assert.Equal(t, "https://oauth2.example/token", info.TokenURI)
	assert.Equal(t, "svc@test-project-123.iam.gserviceaccount.com", info.ClientEmail)
}

func TestExtractVertexServiceAccountInfoDefaultsTokenURI(t *testing.T) {
	saJSON, _ := newTestServiceAccountJSON(t, "")
	info, err := ExtractVertexServiceAccountInfo([]byte(saJSON))
	require.NoError(t, err)
	assert.Equal(t, vertexSADefaultTokenURI, info.TokenURI)
}

func TestMintVertexServiceAccountTokenRejectsInvalidJSON(t *testing.T) {
	_, err := MintVertexServiceAccountToken(context.Background(), []byte("{}"), "")
	require.Error(t, err)
}
