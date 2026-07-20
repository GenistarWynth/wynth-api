package operation_setting

import (
	"os"
	"strconv"

	"github.com/QuantumNous/new-api/setting/config"
)

type MonitorSetting struct {
	AutoTestChannelEnabled        bool    `json:"auto_test_channel_enabled"`
	AutoTestChannelMinutes        float64 `json:"auto_test_channel_minutes"`
	ChannelTestMode               string  `json:"channel_test_mode"`
	DeadChannelRecoveryMinMinutes int     `json:"dead_channel_recovery_min_minutes"`
	DeadChannelRecoveryMaxMinutes int     `json:"dead_channel_recovery_max_minutes"`
	DeadChannelRecoveryMaxPerTick int     `json:"dead_channel_recovery_max_per_tick"`
}

type DeadChannelRecoverySettings struct {
	MinMinutes int
	MaxMinutes int
	MaxPerTick int
}

const (
	ChannelTestModeScheduledAll          = "scheduled_all"
	ChannelTestModePassiveRecovery       = "passive_recovery"
	DefaultDeadChannelRecoveryMinMinutes = 15
	DefaultDeadChannelRecoveryMaxMinutes = 120
	DefaultDeadChannelRecoveryMaxPerTick = 5
	MaximumDeadChannelRecoveryMaxPerTick = 50
)

// 默认配置
var monitorSetting = MonitorSetting{
	AutoTestChannelEnabled:        false,
	AutoTestChannelMinutes:        10,
	ChannelTestMode:               ChannelTestModeScheduledAll,
	DeadChannelRecoveryMinMinutes: DefaultDeadChannelRecoveryMinMinutes,
	DeadChannelRecoveryMaxMinutes: DefaultDeadChannelRecoveryMaxMinutes,
	DeadChannelRecoveryMaxPerTick: DefaultDeadChannelRecoveryMaxPerTick,
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("monitor_setting", &monitorSetting)
}

func GetMonitorSetting() *MonitorSetting {
	if os.Getenv("CHANNEL_TEST_FREQUENCY") != "" {
		frequency, err := strconv.Atoi(os.Getenv("CHANNEL_TEST_FREQUENCY"))
		if err == nil && frequency > 0 {
			monitorSetting.AutoTestChannelEnabled = true
			monitorSetting.AutoTestChannelMinutes = float64(frequency)
			monitorSetting.ChannelTestMode = ChannelTestModeScheduledAll
		}
	}
	if enabled, ok := os.LookupEnv("CHANNEL_TEST_ENABLED"); ok {
		parsed, err := strconv.ParseBool(enabled)
		if err == nil {
			monitorSetting.AutoTestChannelEnabled = parsed
		}
	}
	if monitorSetting.ChannelTestMode != ChannelTestModePassiveRecovery {
		monitorSetting.ChannelTestMode = ChannelTestModeScheduledAll
	}
	recovery := NormalizeDeadChannelRecoverySettings(DeadChannelRecoverySettings{
		MinMinutes: monitorSetting.DeadChannelRecoveryMinMinutes,
		MaxMinutes: monitorSetting.DeadChannelRecoveryMaxMinutes,
		MaxPerTick: monitorSetting.DeadChannelRecoveryMaxPerTick,
	})
	monitorSetting.DeadChannelRecoveryMinMinutes = recovery.MinMinutes
	monitorSetting.DeadChannelRecoveryMaxMinutes = recovery.MaxMinutes
	monitorSetting.DeadChannelRecoveryMaxPerTick = recovery.MaxPerTick
	return &monitorSetting
}

func NormalizeDeadChannelRecoverySettings(settings DeadChannelRecoverySettings) DeadChannelRecoverySettings {
	if settings.MinMinutes < 1 {
		settings.MinMinutes = DefaultDeadChannelRecoveryMinMinutes
	}
	if settings.MaxMinutes < settings.MinMinutes {
		settings.MaxMinutes = settings.MinMinutes
	}
	if settings.MaxPerTick < 1 {
		settings.MaxPerTick = DefaultDeadChannelRecoveryMaxPerTick
	} else if settings.MaxPerTick > MaximumDeadChannelRecoveryMaxPerTick {
		settings.MaxPerTick = MaximumDeadChannelRecoveryMaxPerTick
	}
	return settings
}

func GetDeadChannelRecoverySettings() DeadChannelRecoverySettings {
	settings := GetMonitorSetting()
	return DeadChannelRecoverySettings{
		MinMinutes: settings.DeadChannelRecoveryMinMinutes,
		MaxMinutes: settings.DeadChannelRecoveryMaxMinutes,
		MaxPerTick: settings.DeadChannelRecoveryMaxPerTick,
	}
}
