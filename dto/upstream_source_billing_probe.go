package dto

type UpstreamSourceBillingProbeUpdateRequest struct {
	Enabled                bool   `json:"enabled"`
	IntervalMinutes        int    `json:"interval_minutes"`
	AutoPriorityCostSource string `json:"auto_priority_cost_source"`
}

type UpstreamSourceBillingProbeResponse struct {
	SourceID               int    `json:"source_id"`
	MappingID              int    `json:"mapping_id"`
	Enabled                bool   `json:"enabled"`
	IntervalMinutes        int    `json:"interval_minutes"`
	Status                 string `json:"status"`
	ErrorCode              string `json:"-"`
	Unsupported            bool   `json:"unsupported"`
	AutoPriorityCostSource string `json:"auto_priority_cost_source"`
	EmpiricalStatus        string `json:"empirical_status"`

	AdvertisedEffectiveRateMultiplier *float64 `json:"advertised_effective_rate_multiplier,omitempty"`
	EmpiricalNominalRateMultiplier    *float64 `json:"empirical_nominal_rate_multiplier,omitempty"`
	GroupRateMultiplier               *float64 `json:"group_rate_multiplier,omitempty"`
	UserRateMultiplier                *float64 `json:"user_rate_multiplier,omitempty"`
	ResolvedRateMultiplier            *float64 `json:"resolved_rate_multiplier,omitempty"`
	PeakRateEnabled                   *bool    `json:"peak_rate_enabled,omitempty"`
	PeakStart                         string   `json:"peak_start,omitempty"`
	PeakEnd                           string   `json:"peak_end,omitempty"`
	PeakRateMultiplier                *float64 `json:"peak_rate_multiplier,omitempty"`
	AppliedPeakMultiplier             *float64 `json:"applied_peak_multiplier,omitempty"`
	EffectiveRateMultiplier           *float64 `json:"effective_rate_multiplier,omitempty"`
	Timezone                          string   `json:"timezone,omitempty"`
	ObservedAt                        string   `json:"observed_at,omitempty"`
	LastAttemptAt                     int64    `json:"last_attempt_at,omitempty"`
	ReceivedAt                        int64    `json:"received_at,omitempty"`
	FreshUntil                        int64    `json:"fresh_until,omitempty"`
	HasLastGood                       bool     `json:"has_last_good"`
	Fresh                             bool     `json:"fresh"`
	Stale                             bool     `json:"stale"`
}
