package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatUpstreamSourceRateMultiplierKeepsBaselineThreeDecimalLabel(t *testing.T) {
	tests := []struct {
		name  string
		value float64
		want  string
	}{
		{name: "zero", value: 0, want: "0.000"},
		{name: "one", value: 1, want: "1.000"},
		{name: "stable three decimal label", value: 0.05, want: "0.050"},
		{name: "fourth decimal rounds", value: 0.0625, want: "0.062"},
		{name: "tiny advertised rate remains fixed", value: 0.00009999, want: "0.000"},
		{name: "ordinary rounding carry", value: 0.99996, want: "1.000"},
		{name: "larger ordinary value", value: 12.34567, want: "12.346"},
		{name: "negative value", value: -0.0625, want: "-0.062"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			want := test.want
			if want != "-" {
				want += "x"
			}
			got := formatUpstreamSourceRateMultiplier(test.value)
			assert.Equal(t, want, got)
			assert.NotContains(t, got, "e")
			assert.NotContains(t, got, "E")
		})
	}
}

func TestGeneratedChannelNameAndRemarkKeepBaselineRateLabel(t *testing.T) {
	rate := 0.00009999
	source := model.UpstreamSource{Name: "canonical"}
	mapping := model.UpstreamSourceChannelMapping{EffectiveRateMultiplier: &rate}

	assert.Equal(t, "canonical / 0.000x", upstreamSourceGeneratedChannelName(&source, &mapping))
	remark := upstreamSourceGeneratedChannelRemark(&mapping)
	require.NotNil(t, remark)
	assert.Contains(t, *remark, "Rate: 0.000x")
}
