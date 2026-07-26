package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
