package identity

import (
	"net/http"
	"strings"
)

const (
	CodexCLIOriginator = "codex_cli_rs"
	CodexCLIUserAgent  = "codex_cli_rs/0.145.0"
)

var officialOriginators = map[string]string{
	CodexCLIOriginator: CodexCLIOriginator,
	"codex-tui":        "codex-tui",
	"codex_vscode":     "codex_vscode",
}

// NormalizeIdentityHeaders pairs the final Codex User-Agent and originator.
// Requests without an originator are intentionally left untouched.
func NormalizeIdentityHeaders(header http.Header) {
	if header == nil || header.Get("originator") == "" {
		return
	}
	ua := strings.TrimSpace(header.Get("User-Agent"))
	slash := strings.IndexByte(ua, '/')
	if slash > 0 {
		name := strings.TrimSpace(ua[:slash])
		if saneIdentity(name) {
			if canonical, ok := officialOriginators[strings.ToLower(name)]; ok {
				header.Set("originator", canonical)
				header.Set("User-Agent", canonical+ua[slash:])
				return
			}
			if strings.HasPrefix(name, "Codex ") {
				header.Set("originator", name)
				header.Set("User-Agent", name+ua[slash:])
				return
			}
		}
	}
	header.Set("originator", CodexCLIOriginator)
	header.Set("User-Agent", CodexCLIUserAgent)
}

func saneIdentity(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for i := range len(name) {
		if name[i] < 0x20 || name[i] > 0x7e {
			return false
		}
	}
	return true
}
