package app

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func newLogger(config Config) (*zap.Logger, error) {
	loggerConfig := zap.NewProductionConfig()
	if config.Development {
		loggerConfig = zap.NewDevelopmentConfig()
		loggerConfig.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else {
		loggerConfig.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	}

	logger, err := loggerConfig.Build()
	if err != nil {
		return nil, err
	}
	zap.ReplaceGlobals(logger)
	return logger, nil
}
