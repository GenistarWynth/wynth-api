package codex

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
)

func TestModelListAdvertisesCurrentCodexModels(t *testing.T) {
	models := []string{
		"gpt-5.4-mini",
		"gpt-5.5",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
	}

	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			assert.Contains(t, ModelList, model)
			assert.Contains(t, ModelList, ratio_setting.WithCompactModelSuffix(model))
		})
	}
}
