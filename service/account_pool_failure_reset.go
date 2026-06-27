package service

import (
	"encoding/json"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// accountPool429ErrorBody is a minimal local struct for parsing the OpenAI error body shape.
// resets_at may be a JSON number or string, so json.RawMessage is used.
type accountPool429ErrorBody struct {
	Error struct {
		Type            string          `json:"type"`
		ResetsAt        json.RawMessage `json:"resets_at"`
		ResetsInSeconds float64         `json:"resets_in_seconds"`
	} `json:"error"`
}

// parseAccountPool429ResetAt returns the absolute unix-seconds time at which a 429 rate limit
// resets, derived from Codex response headers or the OpenAI error body JSON.
//
// Resolution order:
//  1. Codex headers (x-codex-primary/secondary-reset-after-seconds and used-percent).
//  2. OpenAI error body (error.type in {"usage_limit_reached","rate_limit_exceeded"}) with
//     resets_at (unix seconds, number or string) or resets_in_seconds (relative).
//  3. (0, false) if nothing can be derived. Malformed inputs never panic.
func parseAccountPool429ResetAt(header http.Header, body []byte, now int64) (resetAt int64, ok bool) {
	// Step 1 — Codex headers.
	primaryResetRaw := header.Get("x-codex-primary-reset-after-seconds")
	secondaryResetRaw := header.Get("x-codex-secondary-reset-after-seconds")
	primaryUsedRaw := header.Get("x-codex-primary-used-percent")
	secondaryUsedRaw := header.Get("x-codex-secondary-used-percent")

	// At least one reset-after header must be present to proceed with codex logic.
	if primaryResetRaw != "" || secondaryResetRaw != "" {
		type window struct {
			resetAfter float64
			usedPct    float64
			hasReset   bool
			hasUsed    bool
		}

		parseWindow := func(resetRaw, usedRaw string) window {
			w := window{}
			if resetRaw != "" {
				if v, err := strconv.ParseFloat(resetRaw, 64); err == nil {
					w.resetAfter = v
					w.hasReset = true
				}
			}
			if usedRaw != "" {
				if v, err := strconv.ParseFloat(usedRaw, 64); err == nil {
					w.usedPct = v
					w.hasUsed = true
				}
			}
			return w
		}

		primary := parseWindow(primaryResetRaw, primaryUsedRaw)
		secondary := parseWindow(secondaryResetRaw, secondaryUsedRaw)

		const exhaustedThreshold = 100 - 1e-9

		isExhausted := func(w window) bool {
			return w.hasUsed && w.usedPct >= exhaustedThreshold
		}

		// Collect exhausted windows that have a reset-after value, and track
		// whether ANY window is exhausted (regardless of having a reset-after).
		anyExhausted := isExhausted(primary) || isExhausted(secondary)
		var exhaustedResets []float64
		if isExhausted(primary) && primary.hasReset {
			exhaustedResets = append(exhaustedResets, primary.resetAfter)
		}
		if isExhausted(secondary) && secondary.hasReset {
			exhaustedResets = append(exhaustedResets, secondary.resetAfter)
		}

		if len(exhaustedResets) > 0 {
			// At least one exhausted window carries a reset-after: use max of those.
			chosen := exhaustedResets[0]
			for _, v := range exhaustedResets[1:] {
				if v > chosen {
					chosen = v
				}
			}
			return now + int64(chosen), true
		}

		if anyExhausted {
			// A window is exhausted but none of the exhausted windows had a reset-after
			// header.  Using a non-exhausted window's reset would under-cool the account;
			// fall through to the body-parse path instead.
			// Reset-after headers were present but unusable; fall through.
		} else {
			// No window is exhausted; pick min positive reset-after among present values.
			var positiveResets []float64
			if primary.hasReset && primary.resetAfter > 0 {
				positiveResets = append(positiveResets, primary.resetAfter)
			}
			if secondary.hasReset && secondary.resetAfter > 0 {
				positiveResets = append(positiveResets, secondary.resetAfter)
			}
			if len(positiveResets) > 0 {
				chosen := positiveResets[0]
				for _, v := range positiveResets[1:] {
					if v < chosen {
						chosen = v
					}
				}
				return now + int64(chosen), true
			}
		}

		// Reset-after headers were present but unparseable, zero, or unusable; fall through.
	}

	// Step 2 — OpenAI error body.
	if len(body) == 0 {
		return 0, false
	}

	var parsed accountPool429ErrorBody
	if err := common.UnmarshalJsonStr(string(body), &parsed); err != nil {
		return 0, false
	}

	errType := parsed.Error.Type
	if errType != "usage_limit_reached" && errType != "rate_limit_exceeded" {
		return 0, false
	}

	// Try resets_at (number or string).
	if len(parsed.Error.ResetsAt) > 0 {
		raw := parsed.Error.ResetsAt
		// Try as number first.
		var asNum json.Number
		if err := common.Unmarshal(raw, &asNum); err == nil {
			if v, err := asNum.Int64(); err == nil && v > 0 {
				return v, true
			}
			// Could be a float representation like "1777283883.0"
			if v, err := asNum.Float64(); err == nil && v > 0 {
				return int64(v), true
			}
		}
		// Try as string.
		var asStr string
		if err := common.Unmarshal(raw, &asStr); err == nil {
			if v, err := strconv.ParseInt(asStr, 10, 64); err == nil && v > 0 {
				return v, true
			}
			if v, err := strconv.ParseFloat(asStr, 64); err == nil && v > 0 {
				return int64(v), true
			}
		}
	}

	// Try resets_in_seconds.
	if parsed.Error.ResetsInSeconds > 0 {
		return now + int64(parsed.Error.ResetsInSeconds), true
	}

	return 0, false
}

// parseAnthropic429ResetAt extracts an absolute unix-seconds reset time from
// Anthropic per-window rate-limit response headers. It never panics on malformed
// inputs. Returns (0, false) when no usable reset can be derived — callers MUST
// NOT apply a cooldown in that case (Anthropic 429 with no reset is often not a
// real limit exhaustion).
//
// Window exhaustion criteria (W ∈ {5h, 7d}):
//   - anthropic-ratelimit-unified-{W}-surpassed-threshold == "true" (case-insensitive), OR
//   - anthropic-ratelimit-unified-{W}-utilization parses to >= 1.0 - 1e-9, OR
//   - for 5h only: anthropic-ratelimit-unified-5h-status == "rejected".
//
// Reset header format: unix timestamp; if value > 1e11 it is treated as ms and divided by 1000.
// Max-age guards: 5h reset ignored if > 6 h in the future; 7d reset ignored if > 8 days in the future.
//
// Selection:
//   - Both 5h and 7d exhausted (with valid resets) → 7d reset.
//   - Exactly one exhausted (with valid reset) → that window's reset.
//   - Neither exhausted but resets present → sooner of present resets.
//   - Fallback: anthropic-ratelimit-unified-reset (aggregated), then Retry-After (integer seconds).
func parseAnthropic429ResetAt(header http.Header, now int64) (resetAt int64, ok bool) {
	const (
		maxAge5hSeconds = int64(6 * 3600)  // 6 hours
		maxAge7dSeconds = int64(8 * 86400) // 8 days
		msThreshold     = int64(1e11)      // values above this are milliseconds
		exhaustedFloor  = 1.0 - 1e-9
	)

	// parseUnixHeader parses a header value as a unix timestamp (seconds or ms).
	// Returns (0, false) if the value is missing, malformed, or zero.
	parseUnixHeader := func(raw string) (int64, bool) {
		if raw == "" {
			return 0, false
		}
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			// Try float (e.g. "1777283883.0")
			f, ferr := strconv.ParseFloat(raw, 64)
			if ferr != nil || f <= 0 {
				return 0, false
			}
			v = int64(f)
		}
		if v <= 0 {
			return 0, false
		}
		if v > msThreshold {
			v = v / 1000
		}
		return v, true
	}

	// parseUtilization parses a utilization header value as a float64.
	parseUtilization := func(raw string) (float64, bool) {
		if raw == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}

	// isWindowExhausted checks the surpassed-threshold, utilization, and (5h-only) status headers.
	isWindowExhausted5h := func() bool {
		if strings.EqualFold(header.Get("Anthropic-Ratelimit-Unified-5H-Surpassed-Threshold"), "true") {
			return true
		}
		if strings.EqualFold(header.Get("Anthropic-Ratelimit-Unified-5H-Status"), "rejected") {
			return true
		}
		if util, ok2 := parseUtilization(header.Get("Anthropic-Ratelimit-Unified-5H-Utilization")); ok2 && util >= exhaustedFloor {
			return true
		}
		return false
	}
	isWindowExhausted7d := func() bool {
		if strings.EqualFold(header.Get("Anthropic-Ratelimit-Unified-7D-Surpassed-Threshold"), "true") {
			return true
		}
		if util, ok2 := parseUtilization(header.Get("Anthropic-Ratelimit-Unified-7D-Utilization")); ok2 && util >= exhaustedFloor {
			return true
		}
		return false
	}

	// Parse per-window reset headers and apply max-age guards.
	get5hReset := func() (int64, bool) {
		v, ok2 := parseUnixHeader(header.Get("Anthropic-Ratelimit-Unified-5H-Reset"))
		if !ok2 {
			return 0, false
		}
		if v > now+maxAge5hSeconds {
			return 0, false // too far in the future — ignore
		}
		return v, true
	}
	get7dReset := func() (int64, bool) {
		v, ok2 := parseUnixHeader(header.Get("Anthropic-Ratelimit-Unified-7D-Reset"))
		if !ok2 {
			return 0, false
		}
		if v > now+maxAge7dSeconds {
			return 0, false // too far in the future — ignore
		}
		return v, true
	}

	ex5h := isWindowExhausted5h()
	ex7d := isWindowExhausted7d()
	reset5h, has5h := get5hReset()
	reset7d, has7d := get7dReset()

	if ex5h && ex7d {
		// Both exhausted → must use 7d reset (longer cooldown) or fall through to outer
		// fallbacks. Falling back to the 5h reset here would under-cool the account:
		// the 7d window is still exhausted, so the next request would hit another 429.
		if has7d {
			return reset7d, true
		}
		// 7d reset absent or filtered by max-age guard — fall through to outer fallbacks
		// (aggregated header → Retry-After → (0, false)). Do NOT use the shorter 5h reset.
	} else if ex5h {
		if has5h {
			return reset5h, true
		}
		// 5h exhausted but no valid 5h reset → fall through.
	} else if ex7d {
		if has7d {
			return reset7d, true
		}
		// 7d exhausted but no valid 7d reset → fall through.
	} else {
		// Neither exhausted: pick sooner of present resets.
		switch {
		case has5h && has7d:
			if reset5h <= reset7d {
				return reset5h, true
			}
			return reset7d, true
		case has5h:
			return reset5h, true
		case has7d:
			return reset7d, true
		}
		// No per-window resets present → fall through to fallbacks.
	}

	// Fallback 1: aggregated anthropic-ratelimit-unified-reset.
	if v, ok2 := parseUnixHeader(header.Get("Anthropic-Ratelimit-Unified-Reset")); ok2 {
		return v, true
	}

	// Fallback 2: Retry-After integer seconds.
	if raw := header.Get("Retry-After"); raw != "" {
		if secs, err := strconv.ParseInt(raw, 10, 64); err == nil && secs > 0 {
			return now + secs, true
		}
	}

	return 0, false
}

// geminiRetryInMsgRe matches "retry in <number>s" (case-insensitive) in a Gemini error message.
var geminiRetryInMsgRe = regexp.MustCompile(`(?i)retry\s+in\s+([0-9]+(?:\.[0-9]+)?)s`)

// geminiDailyQuotaRe matches messages that indicate a daily quota cap.
var geminiDailyQuotaRe = regexp.MustCompile(`(?i)(per\s*day|perday|requests\s+per\s+day|(quota\b.*\bday\b|\bday\b.*\bquota\b))`)

// nextGeminiDailyResetUnix returns the next 00:00 America/Los_Angeles (Pacific time) after now
// expressed as a Unix timestamp in seconds. If the timezone cannot be loaded, falls back to now+24h.
func nextGeminiDailyResetUnix(now int64) int64 {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return now + int64(24*3600)
	}
	t := time.Unix(now, 0).In(loc)
	// Build the next midnight in Pacific time.
	midnight := time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
	return midnight.Unix()
}

// geminiDetail is a minimal struct for parsing one element of error.details[].
type geminiDetail struct {
	RetryDelay string `json:"retryDelay"`
	Metadata   struct {
		QuotaResetDelay string `json:"quotaResetDelay"`
	} `json:"metadata"`
}

// geminiErrorBody is a minimal struct for parsing the Gemini/Google error JSON shape.
type geminiErrorBody struct {
	Error struct {
		Message string         `json:"message"`
		Details []geminiDetail `json:"details"`
	} `json:"error"`
}

// parseGemini429ResetAt extracts an absolute unix-seconds reset time from a Gemini
// 429 response body. Resolution order:
//  1. Scan error.details[] for a positive Go-duration in retryDelay or metadata.quotaResetDelay.
//  2. Regex error.message for "retry in <N>s".
//  3. If error.message indicates a daily quota cap → nextGeminiDailyResetUnix(now).
//  4. (0, false) otherwise. Never panics on malformed input.
func parseGemini429ResetAt(body []byte, now int64) (resetAt int64, ok bool) {
	if len(body) == 0 {
		return 0, false
	}

	var parsed geminiErrorBody
	if err := common.UnmarshalJsonStr(string(body), &parsed); err != nil {
		return 0, false
	}

	// Step 1: scan details for retryDelay or metadata.quotaResetDelay.
	for _, d := range parsed.Error.Details {
		if d.RetryDelay != "" {
			if dur, err := time.ParseDuration(d.RetryDelay); err == nil && dur > 0 {
				secs := int64(math.Ceil(dur.Seconds()))
				return now + secs, true
			}
		}
		if d.Metadata.QuotaResetDelay != "" {
			if dur, err := time.ParseDuration(d.Metadata.QuotaResetDelay); err == nil && dur > 0 {
				secs := int64(math.Ceil(dur.Seconds()))
				return now + secs, true
			}
		}
	}

	// Step 2: regex the message for "retry in Ns".
	if m := geminiRetryInMsgRe.FindStringSubmatch(parsed.Error.Message); len(m) >= 2 {
		if f, err := strconv.ParseFloat(m[1], 64); err == nil && f > 0 {
			secs := int64(math.Ceil(f))
			return now + secs, true
		}
	}

	// Step 3: daily quota message → next Pacific midnight.
	msgLower := strings.ToLower(parsed.Error.Message)
	if geminiDailyQuotaRe.MatchString(msgLower) {
		return nextGeminiDailyResetUnix(now), true
	}

	return 0, false
}
