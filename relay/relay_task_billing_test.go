package relay

import (
	"math"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecalcQuotaFromRatiosFiltersAdjustedMultipliers(t *testing.T) {
	info := &relaycommon.RelayInfo{PriceData: types.PriceData{Quota: 100}}
	info.PriceData.AddOtherRatio("duration", 2)

	adjustedRatios := map[string]float64{
		"duration": 3,
		"zero":     0,
		"negative": -1,
		"nan":      math.NaN(),
		"pos_inf":  math.Inf(1),
		"neg_inf":  math.Inf(-1),
	}
	quota, ok := recalcQuotaFromRatios(info, adjustedRatios)

	require.True(t, ok)
	assert.Equal(t, 150, quota)
	assert.Equal(t, 100, info.PriceData.Quota, "recalculation must not mutate quota before the caller accepts it")
	assert.Equal(t, map[string]float64{"duration": 2}, info.PriceData.OtherRatios(), "recalculation must filter on a copy")

	require.True(t, info.PriceData.ReplaceOtherRatios(adjustedRatios))
	assert.Equal(t, map[string]float64{"duration": 3}, info.PriceData.OtherRatios())
}

func TestRecalcQuotaFromRatiosRejectsAllInvalidAdjustedMultipliers(t *testing.T) {
	info := &relaycommon.RelayInfo{PriceData: types.PriceData{Quota: 100}}
	info.PriceData.AddOtherRatio("duration", 2)

	quota, ok := recalcQuotaFromRatios(info, map[string]float64{
		"zero":     0,
		"negative": -1,
		"nan":      math.NaN(),
		"pos_inf":  math.Inf(1),
		"neg_inf":  math.Inf(-1),
	})

	assert.False(t, ok)
	assert.Zero(t, quota)
	assert.Equal(t, 100, info.PriceData.Quota)
	assert.Equal(t, map[string]float64{"duration": 2}, info.PriceData.OtherRatios())
}

func TestRecalcQuotaFromRatiosHandlesCancellingExtremeOriginalRatios(t *testing.T) {
	type ratioEntry struct {
		key   string
		value float64
	}
	tests := []struct {
		name   string
		first  ratioEntry
		second ratioEntry
	}{
		{
			name:   "huge then tiny",
			first:  ratioEntry{"huge", 1e308},
			second: ratioEntry{"tiny", 1e-308},
		},
		{
			name:   "tiny then huge",
			first:  ratioEntry{"tiny", 1e-308},
			second: ratioEntry{"huge", 1e308},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{PriceData: types.PriceData{Quota: 100}}
			info.PriceData.AddOtherRatio(tt.first.key, tt.first.value)
			info.PriceData.AddOtherRatio(tt.second.key, tt.second.value)
			originalRatios := info.PriceData.OtherRatios()

			quota, ok := recalcQuotaFromRatios(info, map[string]float64{"adjusted": 2})

			require.True(t, ok)
			assert.Equal(t, 200, quota)
			assert.Nil(t, info.QuotaClamp)
			assert.Equal(t, 100, info.PriceData.Quota)
			assert.Equal(t, originalRatios, info.PriceData.OtherRatios())
		})
	}
}
