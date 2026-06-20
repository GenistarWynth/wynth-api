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
