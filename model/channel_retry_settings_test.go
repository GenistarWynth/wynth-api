package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelValidateSettingsAcceptsAutoRetryTimesOverride(t *testing.T) {
	channel := &Channel{OtherSettings: `{"auto_retry_times":0}`}

	require.NoError(t, channel.ValidateSettings())
}

func TestChannelValidateSettingsRejectsInvalidAutoRetryTimesOverride(t *testing.T) {
	tests := []struct {
		name     string
		settings string
	}{
		{name: "negative", settings: `{"auto_retry_times":-1}`},
		{name: "too high", settings: `{"auto_retry_times":11}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &Channel{OtherSettings: tt.settings}

			err := channel.ValidateSettings()

			require.Error(t, err)
			assert.Contains(t, err.Error(), "auto_retry_times")
		})
	}
}
