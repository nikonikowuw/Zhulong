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

	signals chan os.Signal
	stop    chan struct{}
	done    chan struct{}
	mu      sync.Mutex
	started bool
	stopped bool
}

func newLogLevelReloader(path string, level zap.AtomicLevel, logger *zap.Logger) *logLevelReloader {
	return &logLevelReloader{
		path:    path,
		level:   level,
		logger:  logger,
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
	level, err := loadLogLevelFromFile(r.path)
	if err != nil {
		r.logger.Warn("logging level reload failed; retaining current level", zap.Error(err))
		return
	}
	r.level.SetLevel(level)
}

// loadLogLevelFromFile reads the configured level name and returns the parsed
// level in one step, so validation and application cannot disagree about what a
// valid value is.
func loadLogLevelFromFile(path string) (zapcore.Level, error) {
	settings := viper.New()
	settings.SetConfigFile(path)
	settings.SetConfigType("toml")
	if err := settings.ReadInConfig(); err != nil {
		return zapcore.Level(0), fmt.Errorf("read logging level config: %w", err)
	}
	if !settings.InConfig("logging.level") {
		return zapcore.Level(0), errors.New("logging.level is not configured")
	}
	value := settings.GetString("logging.level")
	level, err := parseLogLevel(value)
	if err != nil {
		return zapcore.Level(0), fmt.Errorf("reload rejected: %w", err)
	}
	return level, nil
}
