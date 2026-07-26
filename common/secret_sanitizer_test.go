package common

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeSecretsMasksCredentialFormsWithoutRemovingSafeDiagnostics(t *testing.T) {
	testCases := []struct {
		name   string
		input  string
		secret string
	}{
		{name: "authorization", input: "Authorization: Bearer bearer-secret-value", secret: "bearer-secret-value"},
		{name: "cookie", input: "Cookie: session=secret-cookie; refresh=second-secret", secret: "secret-cookie"},
		{name: "json token", input: `{"access_token":"access-secret-value","code":"denied"}`, secret: "access-secret-value"},
		{name: "url userinfo", input: "grpc://user:secret-password@provider.example/v1 failed", secret: "secret-password"},
		{name: "credential label", input: "credential provider-secret-value unavailable", secret: "provider-secret-value"},
		{name: "query parameter", input: "provider failed ?api_key=query-secret-value&region=west", secret: "query-secret-value"},
		{name: "basic authorization", input: "Authorization: Basic dXNlcjpiYXNpYy1zZWNyZXQ=", secret: "dXNlcjpiYXNpYy1zZWNyZXQ="},
		{name: "json set cookie", input: `{"Set-Cookie":"session=json-cookie-secret; Path=/","code":"denied"}`, secret: "json-cookie-secret"},
		{name: "hyphenated json api key", input: `{"X-Goog-Api-Key":"google-provider-secret","status":401}`, secret: "google-provider-secret"},
		{name: "provider token header", input: "X-Auth-Token: provider-auth-secret", secret: "provider-auth-secret"},
		{name: "mixed case json password", input: `{"PaSs-WoRd":"mixed-password-secret","status":"denied"}`, secret: "mixed-password-secret"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			sanitized := SanitizeSecrets(testCase.input)
			assert.NotContains(t, sanitized, testCase.secret)
			assert.Contains(t, sanitized, "[REDACTED]")
		})
	}

	const safe = "provider reported token count 2048 in region west"
	assert.Equal(t, safe, SanitizeSecrets(safe))

	const cookieWithDiagnostic = "Cookie: session=secret-cookie; provider capacity exhausted in region west"
	sanitizedCookie := SanitizeSecrets(cookieWithDiagnostic)
	assert.NotContains(t, sanitizedCookie, "secret-cookie")
	assert.Contains(t, sanitizedCookie, "provider capacity exhausted in region west")

	const multipleSetCookies = "Set-Cookie: session=secret-cookie; Path=/; HttpOnly, refresh=second-secret; Secure"
	sanitizedSetCookies := SanitizeSecrets(multipleSetCookies)
	assert.NotContains(t, sanitizedSetCookies, "secret-cookie")
	assert.NotContains(t, sanitizedSetCookies, "second-secret")
	assert.Contains(t, sanitizedSetCookies, "Path=/")
	assert.Contains(t, sanitizedSetCookies, "HttpOnly")
	assert.Contains(t, sanitizedSetCookies, "Secure")
}

func TestSanitizeSecretsBoundsLargeAdversarialInput(t *testing.T) {
	const secret = "large-adversarial-secret"
	input := strings.Repeat(`{"Set-Cookie":"session=`+secret+`; Path=/","Authorization":"Basic dXNlcjpwYXNz"}`+"\n", 100_000)

	started := time.Now()
	sanitized := SanitizeSecrets(input)

	require.Less(t, time.Since(started), 2*time.Second)
	assert.LessOrEqual(t, len(sanitized), 70_000)
	assert.NotContains(t, sanitized, secret)
	assert.NotContains(t, sanitized, "dXNlcjpwYXNz")
	assert.Contains(t, sanitized, "[truncated")
}
