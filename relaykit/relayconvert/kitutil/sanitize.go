package kitutil

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

type secretSanitizerRule struct {
	pattern     *regexp.Regexp
	replacement string
}

const secretSanitizerInputLimit = 64 * 1024

var cookieHeaderPattern = regexp.MustCompile(`(?im)\b(?:set[-_ ]?cookie|cookie)\s*[:=]\s*[^\r\n]*`)

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
		pattern: regexp.MustCompile(
			`(?i)("(?:` +
				`(?:proxy[-_ ]?)?authorization|` +
				`set[-_ ]?cookie|cookie|credentials?|` +
				`(?:access|refresh|auth|id)[-_ ]?tokens?|` +
				`api[-_ ]?keys?|x[-_ ]?(?:api[-_ ]?key|auth[-_ ]?token)|x[-_ ]?goog[-_ ]?api[-_ ]?key|` +
				`client[-_ ]?secrets?|provider[-_ ]?secrets?|webhook[-_ ]?secrets?|` +
				`pass[-_ ]?words?|secret|cf[-_ ]?clearance` +
				`)"\s*:\s*")(?:\\.|[^"\\])*(")`),
		replacement: `${1}[REDACTED]${2}`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/@\s]+@`),
		replacement: `${1}[REDACTED]@`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)([?&](?:access[-_]?token|refresh[-_]?token|auth[-_]?token|api[-_]?key|x[-_]?api[-_]?key|token|pass[-_]?word|client[-_]?secret|provider[-_]?secret|webhook[-_]?secret)=)[^&#\s]+`),
		replacement: `${1}[REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)((?:proxy[-_ ]?)?authorization\s*[:=]\s*)(?:(?:bearer|basic)\s+)?[^,\s;&}]+`),
		replacement: `${1}[REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)\bbearer\s+[^,\s;&]+`),
		replacement: `Bearer [REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)\bbasic\s+[A-Za-z0-9+/_=-]{8,}`),
		replacement: `Basic [REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)((?:x[-_ ]?(?:api[-_ ]?key|auth[-_ ]?token)|x[-_ ]?goog[-_ ]?api[-_ ]?key)\s*[:=]\s*)[^,\s;&}]+`),
		replacement: `${1}[REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)((?:access[-_ ]?token|refresh[-_ ]?token|auth[-_ ]?token|api[-_ ]?key|pass[-_ ]?word|client[-_ ]?secret|provider[-_ ]?secret|webhook[-_ ]?secret|token)\s*[=:]\s*)[^,\s;&}]+`),
		replacement: `${1}[REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)\b(credentials?|client[-_ ]?secret|provider[-_ ]?secret|webhook[-_ ]?secret)(?:\s*[:=]\s*|\s+)[^\s,;]+`),
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
	{
		pattern:     regexp.MustCompile(`\bAIza[A-Za-z0-9_-]{20,}\b`),
		replacement: `[REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
		replacement: `[REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[baprs]-[A-Za-z0-9-]{10,})\b`),
		replacement: `[REDACTED]`,
	},
}

// SanitizeSecrets removes credentials while preserving ordinary provider
// diagnostics, status codes, and non-sensitive context. Work is bounded before
// applying the regular expressions so hostile upstream responses cannot amplify
// logging or error-path latency.
func SanitizeSecrets(text string) string {
	originalLength := len(text)
	truncated := originalLength > secretSanitizerInputLimit
	if truncated {
		end := secretSanitizerInputLimit
		for end > 0 && !utf8.RuneStart(text[end]) {
			end--
		}
		text = text[:end]
	}

	text = cookieHeaderPattern.ReplaceAllStringFunc(text, sanitizeCookieHeader)
	for _, rule := range secretSanitizerRules {
		text = rule.pattern.ReplaceAllString(text, rule.replacement)
	}
	if len(text) > secretSanitizerInputLimit {
		end := secretSanitizerInputLimit
		for end > 0 && !utf8.RuneStart(text[end]) {
			end--
		}
		text = text[:end]
		truncated = true
	}
	if truncated {
		text += fmt.Sprintf("... [truncated, original_length=%d, limit=%d]", originalLength, secretSanitizerInputLimit)
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
			expectCookie = false
		}
		segmentStart = index + 1
	}

	return sanitized.String()
}
