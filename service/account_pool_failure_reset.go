package service

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
)

// accountPool429ErrorBody is a minimal local struct for parsing the OpenAI error body shape.
// resets_at may be a JSON number or string, so json.RawMessage is used.
type accountPool429ErrorBody struct {
	Error struct {
		Type           string          `json:"type"`
		ResetsAt       json.RawMessage `json:"resets_at"`
		ResetsInSeconds float64        `json:"resets_in_seconds"`
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
