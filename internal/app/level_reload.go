package app

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type logLevelReloader struct {
	path   string
	level  zap.AtomicLevel
	logger *zap.Logger
	// policy is the sink/encoder policy in force. The reload cannot apply a change
	// to it, so it reports one instead of silently ignoring the edit.
	policy logPolicy

	signals chan os.Signal
	stop    chan struct{}
	done    chan struct{}
	mu      sync.Mutex
	started bool
	stopped bool
}

func newLogLevelReloader(path string, config Config, level zap.AtomicLevel, logger *zap.Logger) *logLevelReloader {
	return &logLevelReloader{
		path:    path,
		level:   level,
		logger:  logger,
		policy:  resolveLogPolicy(config),
		signals: make(chan os.Signal, 1),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (r *logLevelReloader) Start() {
	r.mu.Lock()
	if r.started || r.stopped {
		r.mu.Unlock()
		return
	}
	r.started = true
	signal.Notify(r.signals, syscall.SIGHUP)
	r.mu.Unlock()
	go func() {
		defer close(r.done)
		for {
			select {
			case <-r.stop:
				return
			case <-r.signals:
				r.reload()
			}
		}
	}()
}

func (r *logLevelReloader) Stop() {
	r.mu.Lock()
	if r.stopped {
		started := r.started
		r.mu.Unlock()
		if started {
			<-r.done
		}
		return
	}
	r.stopped = true
	started := r.started
	if started {
		signal.Stop(r.signals)
		close(r.stop)
	} else {
		close(r.done)
	}
	r.mu.Unlock()
	if started {
		<-r.done
	}
}

func (r *logLevelReloader) reload() {
	config, level, err := loadLoggingFromFile(r.path)
	if err != nil {
		r.logger.Warn("logging level reload failed; retaining current level", zap.Error(err))
		return
	}
	r.level.SetLevel(level)
	if changed := diffLogPolicy(r.policy, resolveLogPolicy(config)); len(changed) > 0 {
		// Half-applying a sink policy would be worse than not applying it: the file
		// would claim one destination while the encoder writes to another. Say what
		// needs a restart instead of leaving the operator to discover it.
		r.logger.Warn("logging policy changed on disk; restart required to apply",
			zap.Strings("fields", changed))
	}
}

// loadLoggingFromFile reads and validates the level and the rest of the logging
// section in one step, so the applied level and the restart-required report cannot
// disagree about what the file says.
//
// Unknown keys are tolerated here on purpose: startup is the place that refuses a
// bad configuration, while a reload that rejected unrelated edits would keep an
// operator from fixing the level on a live host.
func loadLoggingFromFile(path string) (Config, zapcore.Level, error) {
	settings := viper.New()
	settings.SetConfigFile(path)
	settings.SetConfigType("toml")
	if err := settings.ReadInConfig(); err != nil {
		return Config{}, zapcore.Level(0), fmt.Errorf("read logging level config: %w", err)
	}
	if !settings.InConfig("logging.level") {
		return Config{}, zapcore.Level(0), errors.New("logging.level is not configured")
	}
	level, err := parseLogLevel(settings.GetString("logging.level"))
	if err != nil {
		return Config{}, zapcore.Level(0), fmt.Errorf("reload rejected: %w", err)
	}
	var values struct {
		Logging LoggingConfig `mapstructure:"logging"`
	}
	if err := settings.Unmarshal(&values); err != nil {
		return Config{}, zapcore.Level(0), fmt.Errorf("reload rejected: %w", err)
	}
	return Config{Logging: values.Logging}, level, nil
}
