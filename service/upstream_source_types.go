package service

import (
	"context"

	"github.com/QuantumNous/new-api/model"
)

type UpstreamSourceAdapter interface {
	DiscoverGroups(ctx context.Context, source *model.UpstreamSource) ([]UpstreamGroup, error)
	CreateKey(ctx context.Context, source *model.UpstreamSource, groupID string, name string) (UpstreamKey, error)
	UpdateKey(ctx context.Context, source *model.UpstreamSource, keyID string, groupID string, name string) (UpstreamKey, error)
	ListKeys(ctx context.Context, source *model.UpstreamSource, groupID string) ([]UpstreamKey, error)
}

type UpstreamGroup struct {
	ID                      string
	Name                    string
	Description             string
	Platform                string
	Status                  string
	RateMultiplier          *float64
	EffectiveRateMultiplier *float64
}

type UpstreamKey struct {
	ID      string
	Key     string
	Name    string
	GroupID string
}

// Collector capabilities are intentionally independent optional interfaces.
// An adapter implements only the remote datasets it actually supports.
type UpstreamBalanceCollector interface {
	CollectBalance(ctx context.Context, source *model.UpstreamSource) (UpstreamBalanceSnapshot, error)
}

type UpstreamCostCollector interface {
	CollectCost(ctx context.Context, source *model.UpstreamSource) (UpstreamCostSnapshot, error)
}

type UpstreamRateGroupCollector interface {
	CollectRateGroups(ctx context.Context, source *model.UpstreamSource) (UpstreamRateGroupSnapshot, error)
}

type UpstreamAnnouncementCollector interface {
	CollectAnnouncements(ctx context.Context, source *model.UpstreamSource) (UpstreamAnnouncementSnapshot, error)
}

type UpstreamSubscriptionUsageCollector interface {
	CollectSubscriptionUsage(ctx context.Context, source *model.UpstreamSource) (UpstreamSubscriptionUsageSnapshot, error)
}

type UpstreamBalanceSnapshot struct {
	Available   float64 `json:"available"`
	Currency    string  `json:"currency"`
	CollectedAt int64   `json:"collected_at"`
}

type UpstreamCostSnapshot struct {
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
	PeriodStart int64   `json:"period_start"`
	PeriodEnd   int64   `json:"period_end"`
	CollectedAt int64   `json:"collected_at"`
}

type UpstreamRateGroupSnapshot struct {
	Groups      []UpstreamGroup `json:"groups"`
	CollectedAt int64           `json:"collected_at"`
}

type UpstreamAnnouncement struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Content     string `json:"content"`
	URL         string `json:"url"`
	PublishedAt int64  `json:"published_at"`
}

type UpstreamAnnouncementSnapshot struct {
	Items       []UpstreamAnnouncement `json:"items"`
	CollectedAt int64                  `json:"collected_at"`
}

type UpstreamSubscriptionUsageSnapshot struct {
	Subscriptions []UpstreamSubscriptionUsage `json:"subscriptions"`
	CollectedAt   int64                       `json:"collected_at"`
}

type UpstreamSubscriptionUsage struct {
	SourceKey string                           `json:"source_key"`
	Name      string                           `json:"name"`
	ExpiresAt int64                            `json:"expires_at"`
	Daily     *UpstreamSubscriptionUsageWindow `json:"daily,omitempty"`
	Weekly    *UpstreamSubscriptionUsageWindow `json:"weekly,omitempty"`
	Monthly   *UpstreamSubscriptionUsageWindow `json:"monthly,omitempty"`
	RawData   string                           `json:"raw_data,omitempty"`
}

type UpstreamSubscriptionUsageWindow struct {
	Used             float64  `json:"used"`
	Limit            *float64 `json:"limit,omitempty"`
	Remaining        *float64 `json:"remaining,omitempty"`
	RemainingPercent *float64 `json:"remaining_percent,omitempty"`
	Unit             string   `json:"unit"`
	PeriodStart      int64    `json:"period_start"`
	PeriodEnd        int64    `json:"period_end"`
}
