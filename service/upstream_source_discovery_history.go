package service

import "github.com/QuantumNous/new-api/model"

func buildUpstreamSourceGroupChanges(sourceID int, scanID int, previous []model.UpstreamSourceChannelMapping, discovered []model.UpstreamSourceChannelMapping, now int64) []model.UpstreamSourceGroupChange {
	previousByGroupID := make(map[string]model.UpstreamSourceChannelMapping, len(previous))
	for _, mapping := range previous {
		previousByGroupID[mapping.UpstreamGroupID] = mapping
	}
	discoveredByGroupID := make(map[string]model.UpstreamSourceChannelMapping, len(discovered))
	changes := make([]model.UpstreamSourceGroupChange, 0)
	for _, mapping := range discovered {
		discoveredByGroupID[mapping.UpstreamGroupID] = mapping
		oldMapping, existed := previousByGroupID[mapping.UpstreamGroupID]
		if !existed {
			changes = append(changes, model.UpstreamSourceGroupChange{
				SourceID:                   sourceID,
				ScanID:                     scanID,
				ChangeType:                 model.UpstreamSourceGroupChangeAdded,
				UpstreamGroupID:            mapping.UpstreamGroupID,
				UpstreamGroupName:          mapping.UpstreamGroupName,
				UpstreamGroupDescription:   mapping.UpstreamGroupDescription,
				UpstreamPlatform:           mapping.UpstreamPlatform,
				NewRateMultiplier:          mapping.UpstreamRateMultiplier,
				NewEffectiveRateMultiplier: mapping.EffectiveRateMultiplier,
				CreatedAt:                  now,
			})
			continue
		}
		if oldMapping.DiscoveryStatus == model.UpstreamMappingDiscoveryStatusStale {
			changes = append(changes, model.UpstreamSourceGroupChange{
				SourceID:                   sourceID,
				ScanID:                     scanID,
				ChangeType:                 model.UpstreamSourceGroupChangeRestored,
				UpstreamGroupID:            mapping.UpstreamGroupID,
				UpstreamGroupName:          mapping.UpstreamGroupName,
				UpstreamGroupDescription:   mapping.UpstreamGroupDescription,
				UpstreamPlatform:           mapping.UpstreamPlatform,
				OldRateMultiplier:          oldMapping.UpstreamRateMultiplier,
				NewRateMultiplier:          mapping.UpstreamRateMultiplier,
				OldEffectiveRateMultiplier: oldMapping.EffectiveRateMultiplier,
				NewEffectiveRateMultiplier: mapping.EffectiveRateMultiplier,
				CreatedAt:                  now,
			})
		}
		if !equalUpstreamSourceRate(oldMapping.UpstreamRateMultiplier, mapping.UpstreamRateMultiplier) ||
			!equalUpstreamSourceRate(oldMapping.EffectiveRateMultiplier, mapping.EffectiveRateMultiplier) {
			changes = append(changes, model.UpstreamSourceGroupChange{
				SourceID:                   sourceID,
				ScanID:                     scanID,
				ChangeType:                 model.UpstreamSourceGroupChangeRateChanged,
				UpstreamGroupID:            mapping.UpstreamGroupID,
				UpstreamGroupName:          mapping.UpstreamGroupName,
				UpstreamGroupDescription:   mapping.UpstreamGroupDescription,
				UpstreamPlatform:           mapping.UpstreamPlatform,
				OldRateMultiplier:          oldMapping.UpstreamRateMultiplier,
				NewRateMultiplier:          mapping.UpstreamRateMultiplier,
				OldEffectiveRateMultiplier: oldMapping.EffectiveRateMultiplier,
				NewEffectiveRateMultiplier: mapping.EffectiveRateMultiplier,
				CreatedAt:                  now,
			})
		}
	}
	for _, mapping := range previous {
		if mapping.DiscoveryStatus == model.UpstreamMappingDiscoveryStatusStale {
			continue
		}
		if _, discoveredNow := discoveredByGroupID[mapping.UpstreamGroupID]; discoveredNow {
			continue
		}
		changes = append(changes, model.UpstreamSourceGroupChange{
			SourceID:                   sourceID,
			ScanID:                     scanID,
			ChangeType:                 model.UpstreamSourceGroupChangeRemoved,
			UpstreamGroupID:            mapping.UpstreamGroupID,
			UpstreamGroupName:          mapping.UpstreamGroupName,
			UpstreamGroupDescription:   mapping.UpstreamGroupDescription,
			UpstreamPlatform:           mapping.UpstreamPlatform,
			OldRateMultiplier:          mapping.UpstreamRateMultiplier,
			OldEffectiveRateMultiplier: mapping.EffectiveRateMultiplier,
			CreatedAt:                  now,
		})
	}
	return changes
}

func equalUpstreamSourceRate(left *float64, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
