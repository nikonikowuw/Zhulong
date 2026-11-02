package app

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/logsafe"
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
	// Pressure lets the storage gate shed log volume while the media filesystem
	// is in an emergency write stop.
	Pressure *logPressure
}

// builtLogger keeps the assembly result in one value so the exported constructors
// stay readable as their field count grows.
type builtLogger struct {
	logger   *zap.Logger
	level    zap.AtomicLevel
	pressure *logPressure
	sink     loggerOutput
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

// NewLogger constructs a configured logger for command modes outside the Fx host.
// Callers that use file output must prepare the returned sink before doing work.
func NewLogger(config Config) (*zap.Logger, zap.AtomicLevel, LoggerSink, error) {
	built, err := buildLogger(config)
	if err != nil {
		return nil, zap.AtomicLevel{}, nil, err
	}
	return built.logger, built.level, built.sink, nil
}

func provideLogger(config Config) (loggerProviderResult, error) {
	built, err := buildLogger(config)
	if err != nil {
		return loggerProviderResult{}, err
	}
	return loggerProviderResult{
		Logger:   built.logger,
		Level:    built.level,
		Sink:     built.sink,
		Pressure: built.pressure,
	}, nil
}

// logPolicy is the part of the logging configuration that can only be applied by
// rebuilding the encoder and the sink, i.e. by restarting.
type logPolicy struct {
	Format   string
	Output   string
	Rotation RotationConfig
	Sampling SamplingConfig
}

// resolveLogPolicy derives the values buildLogger actually uses. Startup and the
// SIGHUP reload both go through it so a comparison never pits raw TOML against a
// defaulted startup config and reports a change that is not there.
func resolveLogPolicy(config Config) logPolicy {
	logging := config.Logging

	format := logging.Format
	if format == "" {
		format = "json"
	}
	output := strings.TrimSpace(logging.Output)
	if output == "" {
		output = defaultLogOutput
	}
	return logPolicy{
		Format:   format,
		Output:   output,
		Rotation: logging.Rotation,
		Sampling: logging.Sampling,
	}
}

// diffLogPolicy names the fields that changed and that only a restart can apply.
func diffLogPolicy(current, next logPolicy) []string {
	var changed []string
	if current.Format != next.Format {
		changed = append(changed, "logging.format")
	}
	if current.Output != next.Output {
		changed = append(changed, "logging.output")
	}
	if current.Rotation != next.Rotation {
		changed = append(changed, "logging.rotation")
	}
	if current.Sampling != next.Sampling {
		changed = append(changed, "logging.sampling")
	}
	return changed
}

func buildLogger(config Config) (builtLogger, error) {
	logging := config.Logging
	levelName := logging.Level
	if levelName == "" {
		levelName = defaultLogLevel
	}
	level, err := parseLogLevel(levelName)
	if err != nil {
		return builtLogger{}, err
	}
	policy := resolveLogPolicy(config)
	format, output := policy.Format, policy.Output
	if format != "json" && format != "console" {
		return builtLogger{}, fmt.Errorf("logging.format must be json or console; got %q", logging.Format)
	}

	sink, err := openLogSink(output, logging.Rotation)
	if err != nil {
		return builtLogger{}, err
	}
	// Only the file destination can block the caller, so only it gets the
	// non-blocking queue. stderr must stay synchronous: it is the fallback the
	// queue itself degrades to, and a chain of buffers has no last resort.
	if fileSink, isFile := sink.(*boundedFileSink); isFile {
		sink = newResilientSink(fileSink, nil)
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
	encoderConfig.EncodeLevel = zapcore.LowercaseLevelEncoder
	var encoder zapcore.Encoder = zapcore.NewJSONEncoder(encoderConfig)
	if format == "console" {
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	}

	atomicLevel := zap.NewAtomicLevelAt(level)
	// The safety wrapper is always enabled and sits directly above the encoder
	// core, so every field and message is sanitized before encoding. It has no
	// production disable switch.
	//
	// Sampling, when configured, wraps the sanitizing core rather than being
	// wrapped by it: SanitizingCore.Check attaches only itself (so a Write can
	// never bypass sanitization), which means a sampler placed *inside* the
	// wrapper would never be consulted and would silently stop sampling.
	pressure := newLogPressure(atomicLevel)
	var core zapcore.Core = logsafe.NewSanitizingCore(zapcore.NewCore(encoder, sink, pressure))
	if logging.Sampling.Enabled {
		if logging.Sampling.Initial <= 0 || logging.Sampling.Thereafter <= 0 {
			return builtLogger{}, errors.New("logging.sampling.initial and logging.sampling.thereafter must be positive when sampling is enabled")
		}
		core = zapcore.NewSamplerWithOptions(core, time.Second, logging.Sampling.Initial, logging.Sampling.Thereafter)
	}

	logger := zap.New(core,
		zap.AddCaller(),
		zap.AddStacktrace(zapcore.ErrorLevel),
		zap.ErrorOutput(zapcore.Lock(os.Stderr)),
	)
	zap.ReplaceGlobals(logger)
	return builtLogger{logger: logger, level: atomicLevel, pressure: pressure, sink: sink}, nil
}
