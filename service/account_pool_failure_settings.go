package service

// accountPoolFailureSettings holds the configurable thresholds and durations
// for the account-pool failure-handling subsystem. All fields carry sub2api-matching
// defaults. Future slices may load overrides from DB settings; for now the struct is
// constructed as a pure literal with no external dependencies.
type accountPoolFailureSettings struct {
	// 429 rate-limit fallback cooldown (seconds) before retrying the same key.
	RateLimit429FallbackSeconds int

	// Hard cap (seconds) applied after clamping any computed 429 cooldown.
	RateLimit429CapSeconds int

	// When false, 429 responses skip the fallback cooldown entirely.
	RateLimit429FallbackEnabled bool

	// Number of HTTP 403 responses within HTTP403WindowMinutes before a key is cooled down.
	HTTP403Threshold int

	// Rolling window (minutes) for counting 403 hits.
	HTTP403WindowMinutes int

	// Cooldown duration (minutes) applied after the 403 threshold is breached.
	HTTP403CooldownMinutes int

	// Cooldown duration (minutes) applied when an OAuth key returns 401 (token expired/revoked).
	OAuth401CooldownMinutes int

	// Window (minutes) within which a re-strike 401 escalates the key to disabled.
	OAuth401RestrikeWindowMinutes int

	// Cooldown duration (minutes) for overload (503/529) responses.
	OverloadCooldownMinutes int

	// Cooldown duration (minutes) for persistent transport errors (e.g. connection refused).
	TransportPersistentMinutes int

	// Cooldown duration (seconds) for transient transport errors (e.g. timeout on first byte).
	TransportTransientSeconds int

	// Escalating cooldown tiers (seconds) for 5xx errors; successive failures advance the tier.
	Escalation5xxTiersSeconds []int

	// Maximum number of escalation tier advances before the key is hard-disabled.
	Escalation5xxHardCapCount int
}

// accountPoolFailureConfig returns the default failure-handling configuration.
// Values match sub2api's documented defaults and serve as the single source of truth
// for all failure-classifier and escalation logic in the account pool.
//
// Extension point: a future DB-settings loader can call this function, then overlay
// any administrator-configured overrides on top of the returned struct.
func accountPoolFailureConfig() accountPoolFailureSettings {
	return accountPoolFailureSettings{
		RateLimit429FallbackSeconds:   5,
		RateLimit429CapSeconds:        7200,
		RateLimit429FallbackEnabled:   true,
		HTTP403Threshold:              3,
		HTTP403WindowMinutes:          180,
		HTTP403CooldownMinutes:        10,
		OAuth401CooldownMinutes:       10,
		OAuth401RestrikeWindowMinutes: 30,
		OverloadCooldownMinutes:       10,
		TransportPersistentMinutes:    10,
		TransportTransientSeconds:     60,
		Escalation5xxTiersSeconds:     []int{60, 300, 1800},
		Escalation5xxHardCapCount:     6,
	}
}

// clampRateLimit429CooldownSeconds clamps seconds into [1, 7200].
// Values below 1 are raised to 1; values above 7200 are capped at 7200.
func clampRateLimit429CooldownSeconds(seconds int) int {
	if seconds < 1 {
		return 1
	}
	if seconds > 7200 {
		return 7200
	}
	return seconds
}
