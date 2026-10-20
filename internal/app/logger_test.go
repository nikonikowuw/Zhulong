package app

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestNewLoggerRejectsUnsupportedPolicy(t *testing.T) {
	restore := zap.ReplaceGlobals(zap.NewNop())
	defer restore()
	for name, config := range map[string]Config{
		"dpanic":              withLogLevel("dpanic"),
		"panic":               withLogLevel("panic"),
		"fatal":               withLogLevel("fatal"),
		"typo":                withLogLevel("infp"),
		"case":                withLogLevel("DEBUG"),
		"format":              withLogFormat("text"),
		"format case":         withLogFormat("JSON"),
		"sampling initial":    withSampling(SamplingConfig{Enabled: true, Initial: 0, Thereafter: 1}),
		"sampling thereafter": withSampling(SamplingConfig{Enabled: true, Initial: 1, Thereafter: 0}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := newLogger(config); err == nil {
				t.Fatal("expected invalid logging policy to fail")
			}
		})
	}
}

func TestNewLoggerUsesConfiguredLevelAndUTCEncoding(t *testing.T) {
	restore := zap.ReplaceGlobals(zap.NewNop())
	defer restore()

	path := filepath.Join(t.TempDir(), "service.log")
	config := fileLoggerConfig(path)
	config.Logging.Level = "debug"
	logger, level, sink, err := NewLogger(config)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("logger provider performed file I/O before lifecycle Prepare: stat err=%v", err)
	}
	if err := sink.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	logger.Named("test.service").Debug("debug-visible", zap.Duration("latency_ms", 1500*time.Millisecond))
	level.SetLevel(zapcore.ErrorLevel)
	logger.Info("filtered-info")
	logger.Warn("filtered-warn")
	logger.Error("error-visible")
	if err := logger.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log output: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"msg":"debug-visible"`) || !strings.Contains(text, `"msg":"error-visible"`) {
		t.Fatalf("configured levels did not reach output: %s", text)
	}
	if strings.Contains(text, "filtered-info") || strings.Contains(text, "filtered-warn") {
		t.Fatalf("info/warn entries were not filtered at error level: %s", text)
	}
	var fields map[string]any
	firstLine, _, _ := strings.Cut(text, "\n")
	if err := json.Unmarshal([]byte(firstLine), &fields); err != nil {
		t.Fatalf("decode first JSON record: %v", err)
	}
	if fields["level"] != "debug" || fields["logger"] != "test.service" || fields["latency_ms"] != float64(1500) {
		t.Fatalf("unexpected encoded fields: %#v", fields)
	}
	ts, ok := fields["ts"].(string)
	if !ok {
		t.Fatalf("ts is not a string: %#v", fields["ts"])
	}
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t.Fatalf("ts is not ISO8601: %q: %v", ts, err)
	}
	_, offset := parsed.Zone()
	if offset != 0 {
		t.Fatalf("timestamp is not UTC: %q", ts)
	}
}

func TestNewLoggerConsoleFormatUsesColoredUppercaseLevel(t *testing.T) {
	restore := zap.ReplaceGlobals(zap.NewNop())
	defer restore()
	path := filepath.Join(t.TempDir(), "console.log")
	config := fileLoggerConfig(path)
	config.Logging.Format = "console"
	logger, _, sink, err := NewLogger(config)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	if err := sink.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	logger.Info("console-entry")
	_ = logger.Sync()
	output, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read console output: %v", err)
	}
	if !strings.Contains(string(output), "INFO") || !strings.Contains(string(output), "\x1b[") {
		t.Fatalf("console output lacks uppercase colored level: %q", output)
	}
}

func TestLoggerSamplingIsExplicitOptIn(t *testing.T) {
	for _, test := range []struct {
		name    string
		sample  SamplingConfig
		wantAll bool
	}{
		{name: "disabled", wantAll: true},
		{name: "enabled", sample: SamplingConfig{Enabled: true, Initial: 1, Thereafter: 100}},
	} {
		t.Run(test.name, func(t *testing.T) {
			restore := zap.ReplaceGlobals(zap.NewNop())
			defer restore()
			path := filepath.Join(t.TempDir(), "sampling.log")
			config := fileLoggerConfig(path)
			config.Logging.Sampling = test.sample
			logger, _, sink, err := NewLogger(config)
			if err != nil {
				t.Fatalf("NewLogger: %v", err)
			}
			if err := sink.Prepare(); err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			t.Cleanup(func() { _ = sink.Close() })
			for i := 0; i < 1000; i++ {
				logger.Info("repeated", zap.Int("index", i))
			}
			_ = logger.Sync()
			file, err := os.Open(path)
			if err != nil {
				t.Fatalf("open sampled output: %v", err)
			}
			defer file.Close()
			count := 0
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				count++
			}
			if err := scanner.Err(); err != nil {
				t.Fatalf("scan log output: %v", err)
			}
			if test.wantAll && count != 1000 {
				t.Fatalf("sampling disabled wrote %d records, want 1000", count)
			}
			if !test.wantAll && count >= 1000 {
				t.Fatalf("sampling enabled wrote %d records, want fewer than 1000", count)
			}
		})
	}
}

func TestFileLoggerSinkRequiresLifecyclePrepare(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "service.log")
	config := fileLoggerConfig(path)
	logger, _, sink, err := NewLogger(config)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	if err, _ := sink.Health(); err != nil {
		t.Fatalf("unexpected sink health before Prepare: %v", err)
	}
	if err := logger.Core().Write(zapcore.Entry{Message: "too-early"}, nil); err == nil {
		t.Fatal("expected file sink to reject writes before Prepare")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file created before Prepare: stat err=%v", err)
	}
	if err := sink.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file was not created during Prepare: %v", err)
	}
}

func fileLoggerConfig(path string) Config {
	config := DefaultConfig()
	config.Logging.Output = path
	config.Logging.Rotation = RotationConfig{
		Enabled:        true,
		MaxSizeMB:      1,
		MaxBackups:     2,
		MaxTotalSizeMB: 3,
	}
	return config
}

func withLogLevel(level string) Config {
	config := DefaultConfig()
	config.Logging.Level = level
	return config
}

func withLogFormat(format string) Config {
	config := DefaultConfig()
	config.Logging.Format = format
	return config
}

func withSampling(sampling SamplingConfig) Config {
	config := DefaultConfig()
	config.Logging.Sampling = sampling
	return config
}
