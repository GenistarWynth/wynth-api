package operation_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetMonitorSetting_ChannelTestEnabledEnvOverridesEnabledConfig(t *testing.T) {
	orig := monitorSetting
	t.Cleanup(func() { monitorSetting = orig })

	t.Setenv("CHANNEL_TEST_ENABLED", "false")
	t.Setenv("CHANNEL_TEST_FREQUENCY", "5")
	monitorSetting = MonitorSetting{
		AutoTestChannelEnabled: true,
		AutoTestChannelMinutes: 20,
	}

	setting := GetMonitorSetting()

	require.NotNil(t, setting)
	assert.False(t, setting.AutoTestChannelEnabled)
	assert.Equal(t, float64(5), setting.AutoTestChannelMinutes)
}

func TestGetMonitorSetting_ChannelTestEnabledEnvCanEnableDisabledConfig(t *testing.T) {
	orig := monitorSetting
	t.Cleanup(func() { monitorSetting = orig })

	t.Setenv("CHANNEL_TEST_ENABLED", "true")
	monitorSetting = MonitorSetting{
		AutoTestChannelEnabled: false,
		AutoTestChannelMinutes: 12,
	}

	setting := GetMonitorSetting()

	require.NotNil(t, setting)
	assert.True(t, setting.AutoTestChannelEnabled)
	assert.Equal(t, float64(12), setting.AutoTestChannelMinutes)
}

func TestNormalizeDeadChannelRecoverySettings(t *testing.T) {
	tests := []struct {
		name  string
		input DeadChannelRecoverySettings
		want  DeadChannelRecoverySettings
	}{
		{
			name: "valid values stay unchanged",
			input: DeadChannelRecoverySettings{
				MinMinutes: 20,
				MaxMinutes: 90,
				MaxPerTick: 8,
			},
			want: DeadChannelRecoverySettings{
				MinMinutes: 20,
				MaxMinutes: 90,
				MaxPerTick: 8,
			},
		},
		{
			name: "invalid minimum and per tick use defaults",
			input: DeadChannelRecoverySettings{
				MinMinutes: 0,
				MaxMinutes: 120,
				MaxPerTick: 0,
			},
			want: DeadChannelRecoverySettings{
				MinMinutes: DefaultDeadChannelRecoveryMinMinutes,
				MaxMinutes: 120,
				MaxPerTick: DefaultDeadChannelRecoveryMaxPerTick,
			},
		},
		{
			name: "maximum cannot be below normalized minimum",
			input: DeadChannelRecoverySettings{
				MinMinutes: 30,
				MaxMinutes: 10,
				MaxPerTick: 3,
			},
			want: DeadChannelRecoverySettings{
				MinMinutes: 30,
				MaxMinutes: 30,
				MaxPerTick: 3,
			},
		},
		{
			name: "per tick is capped",
			input: DeadChannelRecoverySettings{
				MinMinutes: 15,
				MaxMinutes: 120,
				MaxPerTick: MaximumDeadChannelRecoveryMaxPerTick + 1,
			},
			want: DeadChannelRecoverySettings{
				MinMinutes: 15,
				MaxMinutes: 120,
				MaxPerTick: MaximumDeadChannelRecoveryMaxPerTick,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := NormalizeDeadChannelRecoverySettings(test.input)

			assert.Equal(t, test.want, actual)
		})
	}
}

func TestGetDeadChannelRecoverySettingsUsesMonitorDefaults(t *testing.T) {
	orig := monitorSetting
	t.Cleanup(func() { monitorSetting = orig })

	monitorSetting = MonitorSetting{
		DeadChannelRecoveryMinMinutes: DefaultDeadChannelRecoveryMinMinutes,
		DeadChannelRecoveryMaxMinutes: DefaultDeadChannelRecoveryMaxMinutes,
		DeadChannelRecoveryMaxPerTick: DefaultDeadChannelRecoveryMaxPerTick,
	}

	assert.Equal(t, DeadChannelRecoverySettings{
		MinMinutes: DefaultDeadChannelRecoveryMinMinutes,
		MaxMinutes: DefaultDeadChannelRecoveryMaxMinutes,
		MaxPerTick: DefaultDeadChannelRecoveryMaxPerTick,
	}, GetDeadChannelRecoverySettings())
}
