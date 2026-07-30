package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useTestSessionSecret(t *testing.T) {
	t.Helper()
	previous := common.SessionSecret
	common.SessionSecret = "test-session-secret-with-sufficient-entropy"
	t.Cleanup(func() { common.SessionSecret = previous })
}

func TestAccessTokenRoundTripAndPurposeIsolation(t *testing.T) {
	useTestSessionSecret(t)
	identity := AuthIdentity{UserID: 42, SessionID: "session-1", UserAuthVersion: 3, SessionVersion: 2}

	token, expiresAt, err := IssueAccessToken(identity)
	require.NoError(t, err)
	assert.Positive(t, expiresAt)

	parsed, err := ParseAccessToken(token)
	require.NoError(t, err)
	assert.Equal(t, identity, parsed)

	proof, _, err := IssueSecurityProof(identity, "2fa", []string{"channel.key.read"})
	require.NoError(t, err)
	_, err = ParseAccessToken(proof)
	assert.ErrorIs(t, err, ErrAuthTokenInvalid)
}

func TestAccessTokenRejectsTampering(t *testing.T) {
	useTestSessionSecret(t)
	identity := AuthIdentity{UserID: 42, SessionID: "session-1", UserAuthVersion: 1, SessionVersion: 1}
	token, _, err := IssueAccessToken(identity)
	require.NoError(t, err)

	tamperAt := len(token) - 2
	replacement := "x"
	if token[tamperAt] == 'x' {
		replacement = "y"
	}
	tampered := token[:tamperAt] + replacement + token[tamperAt+1:]
	_, err = ParseAccessToken(tampered)
	assert.ErrorIs(t, err, ErrAuthTokenInvalid)

	_, internal, err := ParseDashboardAccessToken(tampered)
	assert.True(t, internal)
	assert.ErrorIs(t, err, ErrAuthTokenInvalid)
}

func TestDashboardAccessTokenClassification(t *testing.T) {
	useTestSessionSecret(t)

	identity, internal, err := ParseDashboardAccessToken("opaque.key.with-dots")
	require.NoError(t, err)
	assert.False(t, internal)
	assert.Empty(t, identity)

	external := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": "external-issuer",
		"aud": authTokenAudience,
		"exp": time.Now().Add(time.Minute).Unix(),
	})
	externalRaw, err := external.SignedString([]byte("external-secret"))
	require.NoError(t, err)
	_, internal, err = ParseDashboardAccessToken(externalRaw)
	require.NoError(t, err)
	assert.False(t, internal)

	unknownUse := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":       authTokenIssuer,
		"aud":       authTokenAudience,
		"token_use": "third_party",
		"exp":       time.Now().Add(time.Minute).Unix(),
	})
	unknownUseRaw, err := unknownUse.SignedString([]byte("external-secret"))
	require.NoError(t, err)
	_, internal, err = ParseDashboardAccessToken(unknownUseRaw)
	require.NoError(t, err)
	assert.False(t, internal)

	proof, _, err := IssueSecurityProof(AuthIdentity{
		UserID: 42, SessionID: "session-1", UserAuthVersion: 1, SessionVersion: 1,
	}, "2fa", []string{"channel.key.read"})
	require.NoError(t, err)
	_, internal, err = ParseDashboardAccessToken(proof)
	assert.True(t, internal)
	assert.ErrorIs(t, err, ErrAuthTokenInvalid)

	expiredClaims := authClaims{
		TokenUse:        accessTokenUse,
		SessionID:       "expired-session",
		UserAuthVersion: 1,
		SessionVersion:  1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    authTokenIssuer,
			Subject:   "42",
			Audience:  jwt.ClaimStrings{authTokenAudience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
			NotBefore: jwt.NewNumericDate(time.Now().Add(-2 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Minute)),
			ID:        "expired-token",
		},
	}
	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, expiredClaims).SignedString(authSigningKey(accessTokenUse))
	require.NoError(t, err)
	_, internal, err = ParseDashboardAccessToken(expired)
	assert.True(t, internal)
	assert.ErrorIs(t, err, ErrAuthTokenExpired)
}

func TestSecurityProofBindsIdentityMethodAndScope(t *testing.T) {
	useTestSessionSecret(t)
	identity := AuthIdentity{UserID: 42, SessionID: "session-1", UserAuthVersion: 3, SessionVersion: 2}
	proof, _, err := IssueSecurityProof(identity, "2fa", []string{"channel.key.read"})
	require.NoError(t, err)

	method, err := VerifySecurityProof(proof, identity, "channel.key.read", []string{"2fa", "passkey"})
	require.NoError(t, err)
	assert.Equal(t, "2fa", method)
	method, err = VerifySecurityProof(proof, identity, "channel.key.read", []string{"2fa", "passkey"})
	require.NoError(t, err)
	assert.Equal(t, "2fa", method, "unrelated generic proofs retain their existing reusable semantics")

	_, err = VerifySecurityProof(proof, identity, "passkey.delete", []string{"2fa"})
	assert.ErrorIs(t, err, ErrProofScope)

	_, err = VerifySecurityProof(proof, identity, "channel.key.read", []string{"passkey"})
	assert.ErrorIs(t, err, ErrProofMethod)

	otherSession := identity
	otherSession.SessionID = "session-2"
	_, err = VerifySecurityProof(proof, otherSession, "channel.key.read", []string{"2fa"})
	assert.True(t, errors.Is(err, ErrAuthTokenInvalid))
}

func TestAccountPoolExportProofIsResourceBoundAndConsumedOnce(t *testing.T) {
	useTestSessionSecret(t)
	previousDB := model.DB
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AuthFlow{}))
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	identity := AuthIdentity{UserID: 42, SessionID: "session-1", UserAuthVersion: 3, SessionVersion: 2}
	proof, _, err := IssueSecurityProof(identity, "passkey", []string{
		SecurityProofScopeAccountPoolCredentialsExport,
		SecurityProofScopeAccountPoolCredentialsExport + ":pool:7",
	})
	require.NoError(t, err)

	err = ConsumeAccountPoolCredentialsExportProof(proof, identity, 8)
	assert.ErrorIs(t, err, ErrProofResource)
	model.DB = nil
	assert.ErrorIs(t, ConsumeAccountPoolCredentialsExportProof(proof, identity, 7), ErrProofStore)
	model.DB = db
	require.NoError(t, ConsumeAccountPoolCredentialsExportProof(proof, identity, 7))
	assert.ErrorIs(t, ConsumeAccountPoolCredentialsExportProof(proof, identity, 7), ErrProofConsumed)
}

func TestAccountPoolExportProofIssuanceRequiresPasskeyAndExactResource(t *testing.T) {
	useTestSessionSecret(t)
	identity := AuthIdentity{UserID: 42, SessionID: "session-1", UserAuthVersion: 3, SessionVersion: 2}

	_, _, err := IssueSecurityProof(identity, "2fa", []string{
		SecurityProofScopeAccountPoolCredentialsExport,
		AccountPoolCredentialsExportResourceScope(7),
	})
	assert.ErrorIs(t, err, ErrProofMethod)

	_, _, err = IssueSecurityProof(identity, "passkey", []string{
		SecurityProofScopeAccountPoolCredentialsExport,
	})
	assert.ErrorIs(t, err, ErrProofResource)
}

func TestAccountPoolExportProofIssuanceFailsClosedWithoutReplayStore(t *testing.T) {
	useTestSessionSecret(t)
	previousDB := model.DB
	model.DB = nil
	t.Cleanup(func() {
		model.DB = previousDB
	})

	identity := AuthIdentity{UserID: 42, SessionID: "session-1", UserAuthVersion: 3, SessionVersion: 2}
	_, _, err := IssueSecurityProof(identity, "passkey", []string{
		SecurityProofScopeAccountPoolCredentialsExport,
		AccountPoolCredentialsExportResourceScope(7),
	})
	assert.ErrorIs(t, err, ErrProofStore)
}
