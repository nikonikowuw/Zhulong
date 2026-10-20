package app

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/fx"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	defaultLogLevel  = "info"
	defaultLogOutput = "stderr"
)

// LoggerSink is prepared from the application lifecycle, not from an Fx provider.
type LoggerSink interface {
	Prepare() error
	Health() (error, uint64)
	Close() error
}

type loggerOutput interface {
	zapcore.WriteSyncer
	LoggerSink
}

type loggerProviderResult struct {
	fx.Out

	Logger *zap.Logger
	Level  zap.AtomicLevel
	Sink   loggerOutput
}

func parseLogLevel(value string) (zapcore.Level, error) {
	switch value {
	case "debug":
		return zapcore.DebugLevel, nil
	case "info":
		return zapcore.InfoLevel, nil
	case "warn":
		return zapcore.WarnLevel, nil
	case "error":
		return zapcore.ErrorLevel, nil
	default:
		return 0, fmt.Errorf("logging.level must be one of debug, info, warn, error; got %q", value)
	}
}

func newLogger(config Config) (*zap.Logger, zap.AtomicLevel, error) {
	logger, level, _, err := buildLogger(config)
	return logger, level, err
}

// NewLogger constructs a configured logger for command modes outside the Fx host.
// Callers that use file output must prepare the returned sink before doing work.
func NewLogger(config Config) (*zap.Logger, zap.AtomicLevel, LoggerSink, error) {
	return buildLogger(config)
}

func provideLogger(config Config) (loggerProviderResult, error) {
	logger, level, sink, err := buildLogger(config)
	if err != nil {
		return loggerProviderResult{}, err
	}
	return loggerProviderResult{Logger: logger, Level: level, Sink: sink}, nil
}

func buildLogger(config Config) (*zap.Logger, zap.AtomicLevel, loggerOutput, error) {
	logging := config.Logging
	development := config.Development || logging.Development
	levelName := logging.Level
	if levelName == "" {
		levelName = defaultLogLevel
	}
	level, err := parseLogLevel(levelName)
	if err != nil {
		return nil, zap.AtomicLevel{}, nil, err
	}
	format := logging.Format
	if format == "" {
		if development {
			format = "console"
		} else {
			format = "json"
		}
	}
	if format != "json" && format != "console" {
		return nil, zap.AtomicLevel{}, nil, fmt.Errorf("logging.format must be json or console; got %q", logging.Format)
	}

	output := strings.TrimSpace(logging.Output)
	if output == "" {
		output = defaultLogOutput
	}
	sink, err := openLogSink(output, logging.Rotation)
	if err != nil {
		return nil, zap.AtomicLevel{}, nil, err
	}

	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeTime:     func(t time.Time, enc zapcore.PrimitiveArrayEncoder) { zapcore.ISO8601TimeEncoder(t.UTC(), enc) },
		EncodeDuration: zapcore.MillisDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
	if format == "console" {
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else {
		encoderConfig.EncodeLevel = zapcore.LowercaseLevelEncoder
	}

	var encoder zapcore.Encoder
	if format == "console" {
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	} else {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	}

	atomicLevel := zap.NewAtomicLevelAt(level)
	var core zapcore.Core = zapcore.NewCore(encoder, sink, atomicLevel)
	if logging.Sampling.Enabled {
		if logging.Sampling.Initial <= 0 || logging.Sampling.Thereafter <= 0 {
			return nil, zap.AtomicLevel{}, nil, errors.New("logging.sampling.initial and logging.sampling.thereafter must be positive when sampling is enabled")
		}
		core = zapcore.NewSamplerWithOptions(core, time.Second, logging.Sampling.Initial, logging.Sampling.Thereafter)
	}

	logger := zap.New(core,
		zap.AddCaller(),
		zap.AddStacktrace(zapcore.ErrorLevel),
		zap.ErrorOutput(zapcore.Lock(os.Stderr)),
	)
	zap.ReplaceGlobals(logger)
	return logger, atomicLevel, sink, nil
}
