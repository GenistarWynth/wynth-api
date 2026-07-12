package common

import (
	"errors"
	"math"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 2000 quota per call * n=18446744073686646784 overflows int64; the constant
// below reproduces that oversized product for the saturation checks.
const overflowingProduct = 2000 * 1.8446744073686647e19

// TestQuotaFromFloat guards the billing invariant that oversized quota
// products (e.g. price multiplied by a huge user-supplied count) saturate
// instead of wrapping into a negative charge (credit). QuotaFromFloat
// truncates toward zero.
func TestQuotaFromFloat(t *testing.T) {
	assert.Equal(t, 42, QuotaFromFloat(42.4))
	assert.Equal(t, 42, QuotaFromFloat(42.9))
	assert.Equal(t, -42, QuotaFromFloat(-42.9))
	assert.Equal(t, MaxQuota, QuotaFromFloat(overflowingProduct))
	assert.Equal(t, MinQuota, QuotaFromFloat(-overflowingProduct))
	assert.Equal(t, MaxQuota, QuotaFromFloat(math.Inf(1)))
	assert.Equal(t, MinQuota, QuotaFromFloat(math.Inf(-1)))
	assert.Equal(t, 0, QuotaFromFloat(math.NaN()))
}

// TestQuotaRound checks half-away-from-zero rounding with the same
// saturation policy.
func TestQuotaRound(t *testing.T) {
	assert.Equal(t, 42, QuotaRound(41.5))
	assert.Equal(t, 43, QuotaRound(42.5))
	assert.Equal(t, -43, QuotaRound(-42.5))
	assert.Equal(t, MaxQuota, QuotaRound(overflowingProduct))
	assert.Equal(t, MinQuota, QuotaRound(-overflowingProduct))
	assert.Equal(t, 0, QuotaRound(math.NaN()))
}

// TestQuotaFromDecimal checks the decimal entry point rounds and saturates
// consistently with the float variants.
func TestQuotaFromDecimal(t *testing.T) {
	assert.Equal(t, 43, QuotaFromDecimal(decimal.NewFromFloat(42.5)))
	assert.Equal(t, 42, QuotaFromDecimal(decimal.NewFromFloat(41.7)))
	assert.Equal(t, MaxQuota, QuotaFromDecimal(decimal.NewFromInt(2000).Mul(decimal.NewFromFloat(1.8446744073686647e19))))
	assert.Equal(t, MinQuota, QuotaFromDecimal(decimal.NewFromInt(-2000).Mul(decimal.NewFromFloat(1.8446744073686647e19))))
}

// TestQuotaFromFloatChecked verifies the clamp descriptor is nil in range and
// carries the correct kind/clamped value on saturation, so billing callers can
// audit the event.
func TestQuotaFromFloatChecked(t *testing.T) {
	quota, clamp := QuotaFromFloatChecked(42.9)
	assert.Equal(t, 42, quota)
	assert.Nil(t, clamp)

	quota, clamp = QuotaFromFloatChecked(overflowingProduct)
	assert.Equal(t, MaxQuota, quota)
	if assert.NotNil(t, clamp) {
		assert.Equal(t, "QuotaFromFloat", clamp.Op)
		assert.Equal(t, QuotaClampOverflow, clamp.Kind)
		assert.Equal(t, MaxQuota, clamp.Clamped)
	}

	quota, clamp = QuotaFromFloatChecked(-overflowingProduct)
	assert.Equal(t, MinQuota, quota)
	if assert.NotNil(t, clamp) {
		assert.Equal(t, QuotaClampUnderflow, clamp.Kind)
		assert.Equal(t, MinQuota, clamp.Clamped)
	}

	quota, clamp = QuotaFromFloatChecked(math.NaN())
	assert.Equal(t, 0, quota)
	if assert.NotNil(t, clamp) {
		assert.Equal(t, QuotaClampNaN, clamp.Kind)
		assert.Equal(t, 0, clamp.Clamped)
	}
}

// TestQuotaRoundChecked verifies the rounding entry point reports clamps the
// same way.
func TestQuotaRoundChecked(t *testing.T) {
	quota, clamp := QuotaRoundChecked(42.5)
	assert.Equal(t, 43, quota)
	assert.Nil(t, clamp)

	quota, clamp = QuotaRoundChecked(overflowingProduct)
	assert.Equal(t, MaxQuota, quota)
	if assert.NotNil(t, clamp) {
		assert.Equal(t, "QuotaRound", clamp.Op)
		assert.Equal(t, QuotaClampOverflow, clamp.Kind)
	}
}

// TestQuotaFromDecimalChecked verifies the decimal entry point reports clamps.
func TestQuotaFromDecimalChecked(t *testing.T) {
	quota, clamp := QuotaFromDecimalChecked(decimal.NewFromFloat(41.7))
	assert.Equal(t, 42, quota)
	assert.Nil(t, clamp)

	quota, clamp = QuotaFromDecimalChecked(decimal.NewFromInt(2000).Mul(decimal.NewFromFloat(1.8446744073686647e19)))
	assert.Equal(t, MaxQuota, quota)
	if assert.NotNil(t, clamp) {
		assert.Equal(t, "QuotaFromDecimal", clamp.Op)
		assert.Equal(t, QuotaClampOverflow, clamp.Kind)
	}
}

func TestStrictQuotaConversionsReturnTypedClampErrors(t *testing.T) {
	tests := []struct {
		name string
		call func() (int, error)
		kind QuotaClampKind
		op   string
	}{
		{name: "float overflow", call: func() (int, error) { return QuotaFromFloatStrict(overflowingProduct) }, kind: QuotaClampOverflow, op: "QuotaFromFloat"},
		{name: "float underflow", call: func() (int, error) { return QuotaFromFloatStrict(-overflowingProduct) }, kind: QuotaClampUnderflow, op: "QuotaFromFloat"},
		{name: "float NaN", call: func() (int, error) { return QuotaFromFloatStrict(math.NaN()) }, kind: QuotaClampNaN, op: "QuotaFromFloat"},
		{name: "round overflow", call: func() (int, error) { return QuotaRoundStrict(overflowingProduct) }, kind: QuotaClampOverflow, op: "QuotaRound"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quota, err := tt.call()
			require.Error(t, err)
			assert.Zero(t, quota)

			var clamp *QuotaClamp
			require.True(t, errors.As(err, &clamp))
			assert.Equal(t, tt.kind, clamp.Kind)
			assert.Equal(t, tt.op, clamp.Op)
		})
	}

	quota, err := QuotaRoundStrict(42.5)
	require.NoError(t, err)
	assert.Equal(t, 43, quota)
}

func TestStrictQuotaConversionsAcceptExactInt32Bounds(t *testing.T) {
	for _, tt := range []struct {
		name string
		call func(float64) (int, error)
	}{
		{name: "float", call: QuotaFromFloatStrict},
		{name: "round", call: QuotaRoundStrict},
	} {
		t.Run(tt.name, func(t *testing.T) {
			quota, err := tt.call(float64(MaxQuota))
			require.NoError(t, err)
			assert.Equal(t, MaxQuota, quota)

			quota, err = tt.call(float64(MinQuota))
			require.NoError(t, err)
			assert.Equal(t, MinQuota, quota)

			_, err = tt.call(float64(MaxQuota) + 1)
			var clamp *QuotaClamp
			require.ErrorAs(t, err, &clamp)
			assert.Equal(t, QuotaClampOverflow, clamp.Kind)

			_, err = tt.call(float64(MinQuota) - 1)
			require.ErrorAs(t, err, &clamp)
			assert.Equal(t, QuotaClampUnderflow, clamp.Kind)
		})
	}
}
