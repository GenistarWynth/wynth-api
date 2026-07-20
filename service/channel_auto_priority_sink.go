package service

import (
	"context"

	"github.com/QuantumNous/new-api/model"

	"gorm.io/gorm"
)

func sinkManuallyDisabledChannels(
	ctx context.Context,
	tx *gorm.DB,
	channelIDs []int,
	sinkPrioritiesByGroup map[string]int64,
) ([]model.ManuallyDisabledChannelSinkResult, error) {
	if tx != nil {
		return model.SinkManuallyDisabledChannels(tx.WithContext(ctx), channelIDs, sinkPrioritiesByGroup)
	}
	var results []model.ManuallyDisabledChannelSinkResult
	err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		results, err = model.SinkManuallyDisabledChannels(tx, channelIDs, sinkPrioritiesByGroup)
		return err
	})
	return results, err
}
