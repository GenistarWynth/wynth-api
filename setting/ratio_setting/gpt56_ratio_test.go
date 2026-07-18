package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGPT56DefaultRatios(t *testing.T) {
	models := map[string]float64{"gpt-5.6": 2.5, "gpt-5.6-sol": 2.5, "gpt-5.6-terra": 1.25, "gpt-5.6-luna": 0.5}
	for model, want := range models {
		t.Run(model, func(t *testing.T) {
			got, ok := defaultModelRatio[model]
			require.True(t, ok)
			assert.Equal(t, want, got)
			assert.Equal(t, 0.1, defaultCacheRatio[model])
			assert.Equal(t, 1.25, defaultCreateCacheRatio[model])
		})
	}
}
