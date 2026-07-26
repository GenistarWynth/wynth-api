package common

import (
	"regexp"
	"strings"
)

type secretSanitizerRule struct {
	pattern     *regexp.Regexp
	replacement string
}

var cookieHeaderPattern = regexp.MustCompile(`(?im)\b(?:set-cookie|cookie)\s*[:=]\s*[^\r\n]*`)

var safeSetCookieAttributes = map[string]struct{}{
	"comment":  {},
	"domain":   {},
	"expires":  {},
	"max-age":  {},
	"path":     {},
	"priority": {},
	"samesite": {},
	"version":  {},
}

var secretSanitizerRules = []secretSanitizerRule{
	{
		pattern:     regexp.MustCompile(`(?i)("(?:authorization|cookie|credential|(?:access|refresh)[_-]?token|api[_-]?key|client[_ -]?secret|provider[_ -]?secret|password)"\s*:\s*")[^"]*(")`),
		replacement: `${1}[REDACTED]${2}`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/@\s]+@`),
		replacement: `${1}[REDACTED]@`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)([?&](?:access_token|refresh_token|api[_-]?key|token|password|client_secret|provider_secret)=)[^&#\s]+`),
		replacement: `${1}[REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)(authorization\s*[:=]\s*)(?:bearer\s+)?[^,\s;&]+`),
		replacement: `${1}[REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)\bbearer\s+[^,\s;&]+`),
		replacement: `Bearer [REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)(x-api-key\s*[:=]\s*)[^,\s;&]+`),
		replacement: `${1}[REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)((?:access_token|refresh_token|api[_-]?key|password|client_secret|provider_secret|token)\s*[=:]\s*)[^,\s;&]+`),
		replacement: `${1}[REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)\b(credential|client[_ -]?secret|provider[_ -]?secret)(?:\s*[:=]\s*|\s+)[^\s,;]+`),
		replacement: `${1} [REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)\b(token|api[ _-]?key|access[ _-]?token|refresh[ _-]?token)\s+([A-Za-z0-9._~+/-]{12,})`),
		replacement: `${1} [REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(`\bsk-[A-Za-z0-9][A-Za-z0-9_-]{16,}\b`),
		replacement: `[REDACTED]`,
	},
}

// SanitizeSecrets removes credentials while preserving ordinary provider
// diagnostics, status codes, and non-sensitive context.
func SanitizeSecrets(text string) string {
	text = cookieHeaderPattern.ReplaceAllStringFunc(text, sanitizeCookieHeader)
	for _, rule := range secretSanitizerRules {
		text = rule.pattern.ReplaceAllString(text, rule.replacement)
	}
	return text
}

func sanitizeCookieHeader(header string) string {
	separator := strings.IndexAny(header, ":=")
	if separator < 0 {
		return header
	}

	prefix := header[:separator+1]
	value := header[separator+1:]
	isSetCookie := strings.Contains(strings.ToLower(prefix), "set-cookie")
	expectCookie := true
	var sanitized strings.Builder
	sanitized.Grow(len(header))
	sanitized.WriteString(prefix)

	segmentStart := 0
	for index := 0; index <= len(value); index++ {
		if index < len(value) && value[index] != ';' && value[index] != ',' {
			continue
		}

		segment := value[segmentStart:index]
		equals := strings.IndexByte(segment, '=')
		if equals >= 0 {
			name := strings.ToLower(strings.TrimSpace(segment[:equals]))
			_, safeAttribute := safeSetCookieAttributes[name]
			if !isSetCookie || expectCookie || !safeAttribute {
				segment = segment[:equals+1] + "[REDACTED]"
			}
			expectCookie = false
		}
		sanitized.WriteString(segment)

		if index == len(value) {
			break
		}
		delimiter := value[index]
		sanitized.WriteByte(delimiter)
		if delimiter == ',' {
			expectCookie = true
		} else if expectCookie {
			// A comma inside Expires temporarily looks like a new cookie. The
			// following semicolon closes that attribute rather than a cookie.
			expectCookie = false
		}
		segmentStart = index + 1
	}

	return sanitized.String()
}
