package main

import (
	"fmt"
	"path/filepath"

	"github.com/nikonikowuw/Zhulong/internal/app"
	"github.com/spf13/viper"
)

type fileConfig struct {
	HTTP struct {
		Address string `mapstructure:"address"`
	} `mapstructure:"http"`
	Data struct {
		Directory string `mapstructure:"directory"`
	} `mapstructure:"data"`
	Person struct {
		FacesDirectory string `mapstructure:"faces_directory"`
	} `mapstructure:"person"`
	Logging app.LoggingConfig `mapstructure:"logging"`
	Network struct {
		CustomScript string `mapstructure:"custom_script"`
	} `mapstructure:"network"`
	Algorithm struct {
		Target string `mapstructure:"target"`
	} `mapstructure:"algorithm"`
	Auth struct {
		SessionTTL string `mapstructure:"session_ttl"`
	} `mapstructure:"auth"`
}

func loadConfig() (app.Config, error) {
	return loadConfigFile("config.toml")
}

func loadConfigFile(path string) (app.Config, error) {
	defaults := app.DefaultConfig()
	settings := viper.New()
	settings.SetConfigFile(path)
	settings.SetConfigType("toml")
	settings.SetDefault("http.address", defaults.HTTPAddress)
	settings.SetDefault("data.directory", defaults.DataDir)
	settings.SetDefault("person.faces_directory", "")
	settings.SetDefault("logging.development", defaults.Development)
	settings.SetDefault("logging.level", defaults.Logging.Level)
	settings.SetDefault("logging.format", defaults.Logging.Format)
	settings.SetDefault("logging.output", defaults.Logging.Output)
	settings.SetDefault("logging.sampling.enabled", defaults.Logging.Sampling.Enabled)
	settings.SetDefault("logging.sampling.initial", defaults.Logging.Sampling.Initial)
	settings.SetDefault("logging.sampling.thereafter", defaults.Logging.Sampling.Thereafter)
	settings.SetDefault("logging.rotation.enabled", defaults.Logging.Rotation.Enabled)
	settings.SetDefault("logging.rotation.max_size_mb", defaults.Logging.Rotation.MaxSizeMB)
	settings.SetDefault("logging.rotation.max_backups", defaults.Logging.Rotation.MaxBackups)
	settings.SetDefault("logging.rotation.max_age_days", defaults.Logging.Rotation.MaxAgeDays)
	settings.SetDefault("logging.rotation.compress", defaults.Logging.Rotation.Compress)
	settings.SetDefault("logging.rotation.max_total_size_mb", defaults.Logging.Rotation.MaxTotalSizeMB)
	settings.SetDefault("algorithm.target", defaults.AlgorithmTarget)
	settings.SetDefault("auth.session_ttl", "24h")

	if err := settings.ReadInConfig(); err != nil {
		return app.Config{}, fmt.Errorf("read configuration %q: %w", path, err)
	}

	var values fileConfig
	if err := settings.UnmarshalExact(&values); err != nil {
		return app.Config{}, fmt.Errorf("decode configuration %q: %w", path, err)
	}

	facesDir := values.Person.FacesDirectory
	if facesDir == "" {
		facesDir = filepath.Join(values.Data.Directory, "faces")
	}

	sessionTTL := defaults.SessionTTL
	if values.Auth.SessionTTL != "" {
		parsed, err := app.ParseSessionTTL(values.Auth.SessionTTL)
		if err != nil {
			return app.Config{}, fmt.Errorf("decode configuration %q: %w", path, err)
		}
		sessionTTL = parsed
	}

	configPath, err := filepath.Abs(settings.ConfigFileUsed())
	if err != nil {
		return app.Config{}, fmt.Errorf("resolve configuration path %q: %w", settings.ConfigFileUsed(), err)
	}

	return app.Config{
		HTTPAddress:     values.HTTP.Address,
		DataDir:         values.Data.Directory,
		FacesDir:        facesDir,
		AlgorithmTarget: values.Algorithm.Target,
		Development:     values.Logging.Development,
		Logging:         values.Logging,
		ConfigPath:      filepath.Clean(configPath),
		CustomScript:    values.Network.CustomScript,
		SessionTTL:      sessionTTL,
	}, nil
}
