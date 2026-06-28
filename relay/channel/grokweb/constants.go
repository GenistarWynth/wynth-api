// Package grokweb implements a best-effort reverse-proxy adaptor for the
// grok.com *web* chat API (the browser-facing internal endpoint), as opposed
// to the official api.x.ai REST API served by relay/channel/xai.
//
// WARNING: THIS IS A FRAGILE, REVERSE-ENGINEERED, BEST-EFFORT WEB PROXY.
// grok.com is an undocumented, private web endpoint. Its request shape,
// headers (notably x-statsig-id), Cloudflare protection and SSE frame format
// can change without notice. Live grok.com calls are NOT verifiable in-repo;
// all tests run against an httptest mock that reproduces the observed shapes.
// Field shapes are mirrored from the grok2api Python reference
// (.codex/external/grok2api-src), which is read-only reference material.
package grokweb

// ChannelName is the human-facing adaptor name.
const ChannelName = "grok-web"

// defaultBaseURL is the grok.com host. It is a package var (not const) so tests
// can point the adaptor at an httptest mock server.
var defaultBaseURL = "https://grok.com"

// chatPath is the new-conversation SSE endpoint.
// Mirror of endpoint_table.py: CHAT = f"{BASE}/rest/app-chat/conversations/new".
const chatPath = "/rest/app-chat/conversations/new"

// staticStatsigID is the static x-statsig-id default copied verbatim from
// grok2api headers.py _statsig_id() (the non-dynamic branch). It is a base64
// payload spoofing a JS TypeError; grok.com accepts it in place of a real
// Statsig SDK signature. This value may stop working if grok.com tightens
// validation — UNVERIFIABLE longevity.
const staticStatsigID = "ZTpUeXBlRXJyb3I6IENhbm5vdCByZWFkIHByb3BlcnRpZXMgb2YgdW5kZWZpbmVkIChyZWFkaW5nICdjaGlsZE5vZGVzJyk="

// defaultUserAgent mirrors the browser UA used by grok2api's default proxy
// profile (a recent desktop Chrome on macOS).
const defaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) " +
	"Chrome/136.0.0.0 Safari/537.36"

// baggageHeader mirrors the static Sentry baggage header from
// grok2api headers.py build_http_headers.
const baggageHeader = "sentry-environment=production," +
	"sentry-release=d6add6fb0460641fd482d767a335ef72b9b6abb8," +
	"sentry-public_key=b311e0f2690c81f25e2c4cf6d4f7ce1c"

// defaultModeID is the fallback grok web "modeId" when a model is not in the
// mode map. "auto" lets grok choose; it is the safest broad default.
const defaultModeID = "auto"

// modelModeMap maps the OpenAI model id (as requested by the client) to the
// grok.com web "modeId" string.
//
// grok.com expects a coarse mode rather than a precise model name. The valid
// modes observed in the grok2api reference (xai_usage.py _MODE_NAMES and
// registry.py ModeId) are: "auto", "fast", "expert", "heavy" and the
// computer-use special "grok-420-computer-use-sa" (used for grok-4.3).
// We expose a small, stable set of model ids that map onto those modes.
var modelModeMap = map[string]string{
	// grok-4.3 family routes to the computer-use mode used by grok2api.
	"grok-4.3":        "grok-420-computer-use-sa",
	"grok-4.3-beta":   "grok-420-computer-use-sa",
	"grok-4.3-expert": "grok-420-computer-use-sa",
	// Coarse modes exposed directly.
	"grok-auto":   "auto",
	"grok-fast":   "fast",
	"grok-expert": "expert",
	"grok-heavy":  "heavy",
	// grok-4 generic ids.
	"grok-4":      "auto",
	"grok-4-fast": "fast",
}

// ModelList is the set of model ids this adaptor advertises.
var ModelList = []string{
	"grok-4.3",
	"grok-4.3-beta",
	"grok-4.3-expert",
	"grok-4",
	"grok-4-fast",
	"grok-auto",
	"grok-fast",
	"grok-expert",
	"grok-heavy",
}

// modelToModeID resolves a requested model id to a grok web modeId, falling
// back to defaultModeID for unknown models so the request still has a chance
// to succeed rather than being rejected locally.
func modelToModeID(model string) string {
	if mode, ok := modelModeMap[model]; ok {
		return mode
	}
	return defaultModeID
}
