package app

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/logsafe"
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

// TestNewLoggerAppliesSanitizingCore proves the assembled logger routes Write,
// With and Check through logsafe.SanitizingCore. It writes to a real file so the
// assertion covers the actual production assembly (encoder + sink), not a
// substitute core.
func TestNewLoggerAppliesSanitizingCore(t *testing.T) {
	restore := zap.ReplaceGlobals(zap.NewNop())
	defer restore()

	path := filepath.Join(t.TempDir(), "sanitized.log")
	logger, _, sink, err := NewLogger(fileLoggerConfig(path))
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	if err := sink.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	// Write path: sensitive key, control characters and an error value.
	logger.Info("camera started\r\ninjected",
		zap.String("password", "hunter2"),
		zap.String("rtspUrl", "rtsp://admin:hunter2@10.0.0.1/live/main"),
		zap.String("clientIp", "192.168.1.77"),
		zap.Error(errors.New("dial rtsp://admin:hunter2@10.0.0.1:554 failed")),
		zap.Any("payload", map[string]string{"password": "hunter2"}),
	)
	// With path: fields attached before Write must be sanitized too.
	logger.With(zap.String("token", "abc123")).Warn("session renewed")
	// Check path: writing through the CheckedEntry must still be sanitized,
	// which only holds if Check attached the wrapper rather than the inner core.
	ce := logger.Core().Check(zapcore.Entry{Level: zapcore.InfoLevel, Message: "via check"}, nil)
	if ce == nil {
		t.Fatal("Check rejected an enabled entry")
	}
	ce.Write(zap.String("secret", "abc123"))
	if err := logger.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log output: %v", err)
	}
	text := string(data)
	for _, leak := range []string{
		"hunter2",
		"abc123",
		"10.0.0.1",
		"192.168.1.77",
		"/live/main",
		"admin:",
	} {
		if strings.Contains(text, leak) {
			t.Fatalf("assembled logger leaked %q:\n%s", leak, text)
		}
	}
	// The injected CRLF must be flattened into the single "msg" value, so the
	// JSON line for the Write path still ends right after the last field.
	if !strings.Contains(text, `"msg":"camera started  injected"`) {
		t.Fatalf("message control characters were not normalized:\n%s", text)
	}
	if !strings.Contains(text, logsafe.ValueMarker) {
		t.Fatalf("sensitive keys were not replaced with the marker:\n%s", text)
	}
	if !strings.Contains(text, logsafe.OmittedMarker) {
		t.Fatalf("reflect field was not omitted:\n%s", text)
	}
	if !strings.Contains(text, "192.168.1.0/24") {
		t.Fatalf("clientIp was not reduced to a masked network summary:\n%s", text)
	}
}

// TestNewLoggerSanitizingCoreIsAlwaysEnabled pins that no configuration value
// can unwrap the safety core.
func TestNewLoggerSanitizingCoreIsAlwaysEnabled(t *testing.T) {
	restore := zap.ReplaceGlobals(zap.NewNop())
	defer restore()
	for _, format := range []string{"json", "console"} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), format+".log")
			config := fileLoggerConfig(path)
			config.Logging.Format = format
			config.Logging.Sampling = SamplingConfig{Enabled: true, Initial: 100, Thereafter: 100}
			logger, _, sink, err := NewLogger(config)
			if err != nil {
				t.Fatalf("NewLogger: %v", err)
			}
			if err := sink.Prepare(); err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			t.Cleanup(func() { _ = sink.Close() })

			// With sampling enabled the sampler must be the outer core and the
			// wrapper must sit directly beneath it. If the wrapper were outer,
			// Check would attach only itself and the sampler would never run.
			if got := typeName(logger.Core()); !strings.Contains(got, "sampler") {
				t.Fatalf("sampling enabled but the outer core is %s", got)
			}
			logger.Error("boom", zap.String("password", "hunter2"))
			if err := logger.Sync(); err != nil {
				t.Fatalf("Sync: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read log output: %v", err)
			}
			if strings.Contains(string(data), "hunter2") {
				t.Fatalf("sampled assembly leaked a sensitive value:\n%s", data)
			}
		})
	}
}

// TestNewLoggerSamplingStillApplies proves the documented ordering: the wrapper
// sits under the sampler, so enabling sampling still reduces record count.
func TestNewLoggerSamplingStillApplies(t *testing.T) {
	restore := zap.ReplaceGlobals(zap.NewNop())
	defer restore()
	path := filepath.Join(t.TempDir(), "sampled-sanitized.log")
	config := fileLoggerConfig(path)
	config.Logging.Sampling = SamplingConfig{Enabled: true, Initial: 1, Thereafter: 100}
	logger, _, sink, err := NewLogger(config)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	if err := sink.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	for i := 0; i < 1000; i++ {
		logger.Info("repeated", zap.String("password", "hunter2"))
	}
	if err := logger.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log output: %v", err)
	}
	records := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			records++
		}
	}
	if records >= 1000 {
		t.Fatalf("sampling did not apply to the sanitizing core: %d records", records)
	}
	if strings.Contains(string(data), "hunter2") {
		t.Fatalf("sampled sanitizing core leaked a sensitive value")
	}
}

// typeName renders the dynamic type name of a core for diagnostic assertions.
func typeName(core zapcore.Core) string {
	return fmt.Sprintf("%T", core)
}

// TestNewLoggerWithoutSamplingWrapsDirectly proves the unsampled assembly puts
// the sanitizing wrapper at the top of the core chain.
func TestNewLoggerWithoutSamplingWrapsDirectly(t *testing.T) {
	restore := zap.ReplaceGlobals(zap.NewNop())
	defer restore()
	path := filepath.Join(t.TempDir(), "nosample.log")
	logger, _, sink, err := NewLogger(fileLoggerConfig(path))
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	if err := sink.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	if got := typeName(logger.Core()); !strings.Contains(got, "SanitizingCore") {
		t.Fatalf("unsampled assembly core = %s, want a SanitizingCore at the top", got)
	}
}
