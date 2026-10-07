package main

import (
	"fmt"

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
	Logging struct {
		Development bool `mapstructure:"development"`
	} `mapstructure:"logging"`
	Network struct {
		CustomScript string `mapstructure:"custom_script"`
	} `mapstructure:"network"`
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
	settings.SetDefault("logging.development", defaults.Development)

	if err := settings.ReadInConfig(); err != nil {
		return app.Config{}, fmt.Errorf("read configuration %q: %w", path, err)
	}

	var values fileConfig
	if err := settings.UnmarshalExact(&values); err != nil {
		return app.Config{}, fmt.Errorf("decode configuration %q: %w", path, err)
	}
	return app.Config{
		HTTPAddress:  values.HTTP.Address,
		DataDir:      values.Data.Directory,
		Development:  values.Logging.Development,
		CustomScript: values.Network.CustomScript,
	}, nil
}
