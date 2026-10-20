package app

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/spf13/viper"
	"go.uber.org/zap"
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
	value, err := validLogLevelFromFile(r.path)
	if err != nil {
		r.logger.Warn("logging level reload failed; retaining current level", zap.Error(err))
		return
	}
	level, _ := parseLogLevel(value)
	r.level.SetLevel(level)
}

func validLogLevelFromFile(path string) (string, error) {
	settings := viper.New()
	settings.SetConfigFile(path)
	settings.SetConfigType("toml")
	if err := settings.ReadInConfig(); err != nil {
		return "", fmt.Errorf("read logging level config: %w", err)
	}
	if !settings.InConfig("logging.level") {
		return "", fmt.Errorf("logging.level is not configured")
	}
	value := settings.GetString("logging.level")
	if _, err := parseLogLevel(value); err != nil {
		return "", err
	}
	return value, nil
}
