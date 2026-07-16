package types

import (
	"math"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPriceDataAddOtherRatioFiltersInvalidValues(t *testing.T) {
	var priceData PriceData
	for key, ratio := range map[string]float64{
		"zero":     0,
		"negative": -1,
		"nan":      math.NaN(),
		"pos_inf":  math.Inf(1),
		"neg_inf":  math.Inf(-1),
	} {
		priceData.AddOtherRatio(key, ratio)
	}
	require.Nil(t, priceData.OtherRatios(), "invalid additions must not create an empty ratio map")

	priceData.AddOtherRatio("duration", 2)
	priceData.AddOtherRatio("identity", 1)
	priceData.AddOtherRatio("duration", math.NaN())
	priceData.AddOtherRatio("duration", 0)

	assert.True(t, priceData.HasOtherRatio("duration"))
	assert.True(t, priceData.HasOtherRatio("identity"))
	assert.False(t, priceData.HasOtherRatio("zero"))
	assert.False(t, priceData.HasOtherRatio("missing"))
	assert.Equal(t, map[string]float64{"duration": 2, "identity": 1}, priceData.OtherRatios())
	assert.Equal(t, 2.0, priceData.OtherRatioMultiplier(), "identity ratios are retained but do not change the product")
}

func TestPriceDataOtherRatiosReturnsDefensiveCopy(t *testing.T) {
	var priceData PriceData
	priceData.AddOtherRatio("duration", 2)

	snapshot := priceData.OtherRatios()
	snapshot["duration"] = 99
	snapshot["injected"] = 3

	assert.Equal(t, map[string]float64{"duration": 2}, priceData.OtherRatios())
}

func TestPriceDataReplaceOtherRatiosFiltersAndReplaces(t *testing.T) {
	var priceData PriceData
	priceData.AddOtherRatio("original", 2)

	ok := priceData.ReplaceOtherRatios(map[string]float64{
		"duration": 3,
		"identity": 1,
		"zero":     0,
		"negative": -2,
		"nan":      math.NaN(),
		"pos_inf":  math.Inf(1),
		"neg_inf":  math.Inf(-1),
	})
	require.True(t, ok)
	assert.Equal(t, map[string]float64{"duration": 3, "identity": 1}, priceData.OtherRatios())
	assert.False(t, priceData.HasOtherRatio("original"))

	ok = priceData.ReplaceOtherRatios(map[string]float64{
		"zero": 0,
		"nan":  math.NaN(),
	})
	assert.False(t, ok)
	assert.Nil(t, priceData.OtherRatios())
}

func TestPriceDataAppliesAndRemovesOtherRatios(t *testing.T) {
	var priceData PriceData
	priceData.AddOtherRatio("duration", 2)
	priceData.AddOtherRatio("quality", 3)
	priceData.AddOtherRatio("identity", 1)

	assert.Equal(t, 60.0, priceData.ApplyOtherRatiosToFloat(10))
	assert.InDelta(t, 10.0, priceData.RemoveOtherRatiosFromFloat(60), 1e-12)
	assert.True(t, decimal.NewFromInt(60).Equal(priceData.ApplyOtherRatiosToDecimal(decimal.NewFromInt(10))))
}

func TestPriceDataCancellingExtremeRatiosAreOrderIndependent(t *testing.T) {
	type ratioEntry struct {
		key   string
		value float64
	}
	tests := []struct {
		name   string
		ratios []ratioEntry
	}{
		{name: "huge tiny double", ratios: []ratioEntry{{"huge", 1e308}, {"tiny", 1e-308}, {"double", 2}}},
		{name: "huge double tiny", ratios: []ratioEntry{{"huge", 1e308}, {"double", 2}, {"tiny", 1e-308}}},
		{name: "tiny huge double", ratios: []ratioEntry{{"tiny", 1e-308}, {"huge", 1e308}, {"double", 2}}},
		{name: "tiny double huge", ratios: []ratioEntry{{"tiny", 1e-308}, {"double", 2}, {"huge", 1e308}}},
		{name: "double huge tiny", ratios: []ratioEntry{{"double", 2}, {"huge", 1e308}, {"tiny", 1e-308}}},
		{name: "double tiny huge", ratios: []ratioEntry{{"double", 2}, {"tiny", 1e-308}, {"huge", 1e308}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var priceData PriceData
			for _, ratio := range tt.ratios {
				priceData.AddOtherRatio(ratio.key, ratio.value)
			}

			assert.InDelta(t, 2.0, priceData.OtherRatioMultiplier(), 1e-12)
			assert.InDelta(t, 200.0, priceData.ApplyOtherRatiosToFloat(100), 1e-9)
			assert.InDelta(t, 50.0, priceData.RemoveOtherRatiosFromFloat(100), 1e-9)
			assert.True(t, decimal.NewFromInt(200).Equal(priceData.ApplyOtherRatiosToDecimal(decimal.NewFromInt(100))))
		})
	}
}

func TestPriceDataFloatOperationsPreserveNonFiniteBase(t *testing.T) {
	var priceData PriceData
	priceData.AddOtherRatio("huge", 1e308)
	priceData.AddOtherRatio("tiny", 1e-308)

	assert.True(t, math.IsNaN(priceData.ApplyOtherRatiosToFloat(math.NaN())))
	assert.True(t, math.IsNaN(priceData.RemoveOtherRatiosFromFloat(math.NaN())))
	assert.Equal(t, math.Inf(1), priceData.ApplyOtherRatiosToFloat(math.Inf(1)))
	assert.Equal(t, math.Inf(-1), priceData.RemoveOtherRatiosFromFloat(math.Inf(-1)))
}
