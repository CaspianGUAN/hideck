package config

type ModemVoiceConfig struct {
	AutoEnableADB *bool `mapstructure:"auto_enable_adb"`
}

func (config ModemVoiceConfig) ADBEnableAllowed() bool {
	return config.AutoEnableADB == nil || *config.AutoEnableADB
}
