package logsafe

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// newObservedLogger returns a logger whose core is wrapped by SanitizingCore and
// an observer that records what actually reached the sink.
func newObservedLogger(t *testing.T) (*zap.Logger, *observer.ObservedLogs) {
	t.Helper()
	inner, logs := observer.New(zapcore.DebugLevel)
	return zap.New(NewSanitizingCore(inner)), logs
}

// fieldMap converts observed context fields into a map for assertions.
func fieldMap(entry observer.LoggedEntry) map[string]any { return entry.ContextMap() }

func TestSanitizingCoreWriteSanitizesMessageAndFields(t *testing.T) {
	logger, logs := newObservedLogger(t)
	logger.Info("started\r\nfake-line  token=leaked",
		zap.String("module", "camera"),
		zap.String("padded", strings.Repeat("x", MaxSafeBytes+10)),
		zap.Int("attempt", 3),
	)

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d: %+v", len(entries), entries)
	}
	entry := entries[0]
	if strings.ContainsAny(entry.Message, "\r\n") {
		t.Fatalf("message kept a line break: %q", entry.Message)
	}
	if entry.Message != "started  fake-line  token=leaked" {
		t.Fatalf("unexpected sanitized message: %q", entry.Message)
	}
	fields := fieldMap(entry)
	if fields["module"] != "camera" {
		t.Fatalf("plain string field changed: %#v", fields["module"])
	}
	padded, ok := fields["padded"].(string)
	if !ok || len(padded) > MaxSafeBytes {
		t.Fatalf("oversized field was not bounded: %d bytes", len(padded))
	}
	if !strings.HasSuffix(padded, SafeTruncatedMarker) {
		t.Fatalf("oversized field lacks the truncation marker: %q", padded)
	}
	if fields["attempt"] != int64(3) {
		t.Fatalf("scalar field changed: %#v", fields["attempt"])
	}
}

func TestSanitizingCoreSensitiveKeys(t *testing.T) {
	logger, logs := newObservedLogger(t)
	logger.Info("login",
		zap.String("password", "hunter2"),
		zap.String("passwd", "hunter2"),
		zap.String("pwd", "hunter2"),
		zap.String("token", "abc123"),
		zap.String("accessToken", "abc123"),
		zap.String("refresh_token", "abc123"),
		zap.String("secret", "abc123"),
		zap.String("clientSecret", "abc123"),
		zap.String("jwtSecret", "abc123"),
		zap.String("credentials", "abc123"),
		zap.String("authorization", "Bearer abc123"),
		zap.String("cookie", "session=abc123"),
		zap.String("setCookie", "session=abc123"),
		zap.String("apiKey", "abc123"),
		zap.String("rtspUrl", "rtsp://admin:hunter2@10.0.0.1/live"),
		zap.String("url", "https://example.com/?token=abc123"),
		zap.String("dsn", "postgres://user:hunter2@db/app"),
		zap.String("path", "/var/lib/zhulong/secrets/hunter2"),
		zap.String("filePath", "/var/lib/zhulong/secrets/hunter2"),
		zap.String("socketPath", "/run/zhulong/broker.sock"),
		zap.String("Password", "hunter2"),
		zap.Int("password_attempts", 1),
	)

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}
	fields := fieldMap(entries[0])
	for _, key := range sensitiveKeys {
		if key == "password_attempts" {
			continue
		}
		for observedKey, value := range fields {
			if normalizeKeyMatches(key, observedKey) && value != ValueMarker {
				t.Fatalf("sensitive key %q (logged as %q) was not replaced: %#v", key, observedKey, value)
			}
		}
	}
	// An int under a non-sensitive key name that merely contains "password" is
	// untouched: the core matches exact normalized names, not substrings.
	if fields["password_attempts"] != int64(1) {
		t.Fatalf("non-sensitive key was replaced: %#v", fields["password_attempts"])
	}
	encoded := fmt.Sprint(fields)
	for _, leak := range []string{"hunter2", "abc123", "10.0.0.1"} {
		if strings.Contains(encoded, leak) {
			t.Fatalf("sensitive value %q leaked: %s", leak, encoded)
		}
	}
}

// TestSanitizingCoreSensitiveKeysCoverAllTypes proves the marker replacement
// applies regardless of the zap field type, not only to strings.
func TestSanitizingCoreSensitiveKeysCoverAllTypes(t *testing.T) {
	logger, logs := newObservedLogger(t)
	logger.Info("mixed",
		zap.Int("token", 42),
		zap.Bool("secret", true),
		zap.Duration("pwd", time.Second),
		zap.Any("credential", map[string]string{"a": "b"}),
		fieldKeyError("authorization", errors.New("Bearer abc123")),
	)
	fields := fieldMap(logs.All()[0])
	for key, value := range fields {
		if value != ValueMarker {
			t.Fatalf("sensitive key %q was not replaced: %#v", key, value)
		}
	}
}

func TestSanitizingCoreWithFields(t *testing.T) {
	logger, logs := newObservedLogger(t)
	logger.With(
		zap.String("password", "hunter2"),
		zap.String("cameraId", "cam\r\n1"),
		zap.String("clientIp", "192.168.1.77"),
	).Info("with-path")

	fields := fieldMap(logs.All()[0])
	if fields["password"] != ValueMarker {
		t.Fatalf("With did not sanitize the sensitive key: %#v", fields["password"])
	}
	if fields["cameraId"] != "cam  1" {
		t.Fatalf("With did not sanitize control characters: %#v", fields["cameraId"])
	}
	if fields["clientIp"] != "192.168.1.0/24" {
		t.Fatalf("With did not mask clientIp: %#v", fields["clientIp"])
	}
}

// TestSanitizingCoreWithReturnsNewCoreAlsoChecksParent asserts that a logger
// derived through With still routes through the wrapper.
func TestSanitizingCoreWithReturnsNewCoreAlsoChecksParent(t *testing.T) {
	logger, logs := newObservedLogger(t)
	child := logger.With(zap.String("token", "abc123"))
	if child.Core() == logger.Core() {
		t.Fatal("With returned the same core instead of a new wrapper")
	}
	logger.Info("parent", zap.String("secret", "abc123"))
	child.Info("child", zap.String("apiKey", "abc123"))

	entries := logs.All()
	if len(entries) != 2 {
		t.Fatalf("expected two entries, got %d", len(entries))
	}
	for _, entry := range entries {
		for key, value := range fieldMap(entry) {
			if value != ValueMarker {
				t.Fatalf("%s: key %q not replaced: %#v", entry.Message, key, value)
			}
		}
	}
}

func TestSanitizingCoreClientIP(t *testing.T) {
	logger, logs := newObservedLogger(t)
	logger.Info("access",
		zap.String("clientIp", "10.1.2.3"),
		zap.String("client_ip", "2001:db8:1:2::5"),
		zap.String("clientIp", "not-an-ip"),
		zap.Int("clientIp", 7),
	)
	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}
	// observer collapses duplicate keys into the last value, so assert the
	// typed behaviour directly on the sanitizer instead.
	for _, tc := range []struct {
		field zapcore.Field
		want  string
	}{
		{field: zap.String("clientIp", "10.1.2.3"), want: "10.1.2.0/24"},
		{field: zap.String("client_ip", "2001:db8:1:2::5"), want: "2001:db8:1:2::/64"},
		{field: zap.String("clientIp", "not-an-ip"), want: IPMarker},
		{field: zap.Int("clientIp", 7), want: IPMarker},
	} {
		sanitized, changed := sanitizeField(tc.field)
		if !changed || sanitized.Type != zapcore.StringType || sanitized.String != tc.want {
			t.Fatalf("clientIp %v -> %v, want %q", tc.field, sanitized, tc.want)
		}
	}
}

func TestSanitizingCoreErrorField(t *testing.T) {
	logger, logs := newObservedLogger(t)
	logger.Error("camera failed",
		zap.Error(errors.New("dial rtsp://admin:hunter2@10.0.0.1:554 password=hunter2")),
		zap.NamedError("cause", fmt.Errorf("wrapped: %w", errors.New("token=abc123"))),
		zap.Error(nil),
	)
	fields := fieldMap(logs.All()[0])
	if fields["error"] != ErrorMarker {
		t.Fatalf("error field not reduced: %#v", fields["error"])
	}
	if fields["cause"] != ErrorMarker {
		t.Fatalf("named error field not reduced: %#v", fields["cause"])
	}
	encoded := fmt.Sprint(fields)
	for _, leak := range []string{"hunter2", "10.0.0.1", "abc123", "wrapped"} {
		if strings.Contains(encoded, leak) {
			t.Fatalf("error text %q leaked: %s", leak, encoded)
		}
	}
}

// TestSanitizingCoreErrorNeverCallsErrorString proves the wrapper does not touch
// err.Error() or fmt.Stringer.
func TestSanitizingCoreErrorNeverCallsErrorString(t *testing.T) {
	logger, logs := newObservedLogger(t)
	logger.Error("boom", zap.Error(poisonError{}))
	fields := fieldMap(logs.All()[0])
	if fields["error"] != ErrorMarker {
		t.Fatalf("error field not reduced: %#v", fields["error"])
	}
}

func TestSanitizingCoreOmittedTypes(t *testing.T) {
	logger, logs := newObservedLogger(t)
	logger.Info("complex",
		zap.Object("object", poisonObject{}),
		zap.Array("array", poisonArray{}),
		zap.Any("reflect", map[string]string{"password": "hunter2"}),
		zap.Stringer("stringer", poisonStringer{}),
		zap.Binary("binary", []byte("hunter2")),
		zap.Namespace("namespace"),
		zap.String("afterNamespace", "visible"),
	)
	fields := fieldMap(logs.All()[0])
	for _, key := range []string{"object", "array", "reflect", "stringer", "binary"} {
		if fields[key] != OmittedMarker {
			t.Fatalf("%q was not omitted: %#v", key, fields[key])
		}
	}
	if _, exists := fields["afterNamespace"]; !exists {
		t.Fatalf("fields after a namespace marker must still reach the sink: %#v", fields)
	}
	if encoded := fmt.Sprint(fields); strings.Contains(encoded, "hunter2") {
		t.Fatalf("omitted value leaked: %s", encoded)
	}
}

// TestSanitizingCoreUnsupportedTypes pins that every zap field type is either
// passed through or replaced; nothing reaches a sink unclassified.
func TestSanitizingCoreUnsupportedTypes(t *testing.T) {
	for _, field := range []zapcore.Field{
		{Key: "unknown", Type: zapcore.UnknownType},
		{Key: "object", Type: zapcore.ObjectMarshalerType, Interface: poisonObject{}},
		{Key: "array", Type: zapcore.ArrayMarshalerType, Interface: poisonArray{}},
		{Key: "reflect", Type: zapcore.ReflectType, Interface: "hunter2"},
		{Key: "stringer", Type: zapcore.StringerType, Interface: poisonStringer{}},
		{Key: "binary", Type: zapcore.BinaryType, Interface: []byte("hunter2")},
		{Key: "namespace", Type: zapcore.NamespaceType},
	} {
		sanitized, changed := sanitizeField(field)
		if !changed {
			t.Fatalf("field %v was passed through unsanitized", field)
		}
		if sanitized.String != OmittedMarker {
			t.Fatalf("field %v -> %q, want %q", field, sanitized.String, OmittedMarker)
		}
	}
	for _, field := range []zapcore.Field{
		{Key: "bool", Type: zapcore.BoolType},
		{Key: "int64", Type: zapcore.Int64Type},
		{Key: "uint64", Type: zapcore.Uint64Type},
		{Key: "float64", Type: zapcore.Float64Type},
		{Key: "complex128", Type: zapcore.Complex128Type},
		{Key: "duration", Type: zapcore.DurationType},
		{Key: "time", Type: zapcore.TimeFullType},
		{Key: "skip", Type: zapcore.SkipType},
	} {
		if sanitized, changed := sanitizeField(field); changed {
			t.Fatalf("scalar field %v was replaced by %v", field, sanitized)
		}
	}
}

func TestSanitizingCoreByteString(t *testing.T) {
	sanitized, changed := sanitizeField(zapcore.Field{
		Key:       "blob",
		Type:      zapcore.ByteStringType,
		Interface: []byte("a\r\nb"),
	})
	if !changed || sanitized.Type != zapcore.StringType || sanitized.String != "a  b" {
		t.Fatalf("byte string not sanitized: %+v", sanitized)
	}
	sanitized, changed = sanitizeField(zapcore.Field{
		Key:       "blob",
		Type:      zapcore.ByteStringType,
		Interface: "not-bytes",
	})
	if !changed || sanitized.String != OmittedMarker {
		t.Fatalf("non-[]byte byte-string not omitted: %+v", sanitized)
	}
}

// TestSanitizingCoreDoesNotMutateCallerFields proves Write and With leave the
// caller-owned slice untouched.
func TestSanitizingCoreDoesNotMutateCallerFields(t *testing.T) {
	inner, logs := observer.New(zapcore.DebugLevel)
	core := NewSanitizingCore(inner)
	logger := zap.New(core)

	fields := []zapcore.Field{
		zap.String("password", "hunter2"),
		zap.String("payload", "a\r\nb"),
		zap.Int("attempt", 1),
	}
	before := make([]zapcore.Field, len(fields))
	copy(before, fields)

	logger.Info("write", fields...)
	logger.With(fields...).Info("with")

	for i := range fields {
		if fields[i] != before[i] {
			t.Fatalf("field %d mutated: %+v -> %+v", i, before[i], fields[i])
		}
	}
	if len(fields) != len(before) {
		t.Fatalf("field slice length changed: %d -> %d", len(fields), len(fields))
	}

	entries := logs.All()
	if len(entries) != 2 {
		t.Fatalf("expected two entries, got %d", len(entries))
	}
	for _, entry := range entries {
		entryFields := fieldMap(entry)
		if entryFields["password"] != ValueMarker || entryFields["payload"] != "a  b" {
			t.Fatalf("%s: sink saw unsanitized fields: %#v", entry.Message, entryFields)
		}
	}
}

// TestSanitizingCoreCheckRoutesWriteThroughWrapper is the bypass regression
// test: the inner core records exactly what it receives, so an unsanitized value
// here means Check attached the inner core instead of the wrapper.
func TestSanitizingCoreCheckRoutesWriteThroughWrapper(t *testing.T) {
	inner := newRecordingCore()
	core := NewSanitizingCore(inner)

	entry := zapcore.Entry{Level: zapcore.InfoLevel, Message: "with\r\nnewline"}
	var ce *zapcore.CheckedEntry
	ce = core.Check(entry, ce)
	if ce == nil {
		t.Fatal("Check rejected an enabled entry")
	}
	ce.Write(zap.String("token", "abc123"))
	if inner.writes() != 1 {
		t.Fatalf("inner core received %d writes, want 1", inner.writes())
	}
	if strings.ContainsAny(inner.recorder.lastEntry.Message, "\r\n") {
		t.Fatalf("inner core saw an unsanitized message: %q", inner.recorder.lastEntry.Message)
	}
	if len(inner.recorder.lastFields) != 1 || inner.recorder.lastFields[0].String != ValueMarker {
		t.Fatalf("inner core saw unsanitized fields: %+v", inner.recorder.lastFields)
	}

	// A disabled core must not be attached at all.
	disabled := NewSanitizingCore(zapcore.NewNopCore())
	var disabledEntry *zapcore.CheckedEntry
	if got := disabled.Check(entry, disabledEntry); got != nil {
		t.Fatalf("Check attached a write for a disabled core: %+v", got)
	}
}

func TestSanitizingCoreMinLevelAndSync(t *testing.T) {
	inner := newRecordingCoreAt(zapcore.WarnLevel)
	core := NewSanitizingCore(inner)
	if core.Enabled(zapcore.InfoLevel) {
		t.Fatal("wrapper ignored the inner level enabler")
	}
	if err := core.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if inner.syncs() != 1 {
		t.Fatalf("Sync forwarded %d times, want 1", inner.syncs())
	}
	if got := core.Level(); got != zapcore.WarnLevel {
		t.Fatalf("Level() = %v, want %v", got, zapcore.WarnLevel)
	}
	if NewSanitizingCore(nil).Enabled(zapcore.FatalLevel) {
		t.Fatal("a nil inner core must degrade to a nop core, not panic")
	}
}

func TestSanitizingCoreWithPreservesSink(t *testing.T) {
	inner := newRecordingCore()
	logger := zap.New(NewSanitizingCore(inner))
	logger.With(zap.String("requestId", "req-1")).Info("hello", zap.String("status", "ok"))
	if inner.writes() != 1 {
		t.Fatalf("inner core received %d writes, want 1", inner.writes())
	}
	keys := make([]string, 0, len(inner.recorder.withFields)+len(inner.recorder.lastFields))
	for _, field := range inner.recorder.withFields {
		keys = append(keys, field.Key)
	}
	for _, field := range inner.recorder.lastFields {
		keys = append(keys, field.Key)
	}
	if strings.Join(keys, ",") != "requestId,status" {
		t.Fatalf("unexpected field order/keys: %v", keys)
	}
}

// recordingCore records what a wrapped core actually receives. It is a leaf
// core: it forwards nothing, so anything it sees came straight from the wrapper.
// Clones produced by With share one recorder so assertions see the writes.
type recordingCore struct {
	level    zapcore.Level
	recorder *recordedWrites
}

type recordedWrites struct {
	writes     int
	syncs      int
	lastEntry  zapcore.Entry
	lastFields []zapcore.Field
	withFields []zapcore.Field
}

func newRecordingCore() *recordingCore {
	return &recordingCore{level: zapcore.DebugLevel, recorder: &recordedWrites{}}
}

func newRecordingCoreAt(level zapcore.Level) *recordingCore {
	return &recordingCore{level: level, recorder: &recordedWrites{}}
}

func (r *recordingCore) writes() int { return r.recorder.writes }

func (r *recordingCore) syncs() int { return r.recorder.syncs }

func (r *recordingCore) Enabled(level zapcore.Level) bool { return level >= r.level }

func (r *recordingCore) With(fields []zapcore.Field) zapcore.Core {
	r.recorder.withFields = append(r.recorder.withFields, fields...)
	return &recordingCore{level: r.level, recorder: r.recorder}
}

func (r *recordingCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if r.Enabled(ent.Level) {
		return ce.AddCore(ent, r)
	}
	return ce
}

func (r *recordingCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	r.recorder.writes++
	r.recorder.lastEntry = ent
	r.recorder.lastFields = append([]zapcore.Field(nil), fields...)
	return nil
}

func (r *recordingCore) Sync() error {
	r.recorder.syncs++
	return nil
}

// fieldKeyError builds an ErrorType field under an arbitrary key.
func fieldKeyError(key string, err error) zapcore.Field {
	return zapcore.Field{Key: key, Type: zapcore.ErrorType, Interface: err}
}

type poisonObject struct{}

func (poisonObject) MarshalLogObject(zapcore.ObjectEncoder) error {
	panic("MarshalLogObject must never be called")
}

type poisonArray struct{}

func (poisonArray) MarshalLogArray(zapcore.ArrayEncoder) error {
	panic("MarshalLogArray must never be called")
}

type poisonStringer struct{}

func (poisonStringer) String() string { panic("String() must never be called") }

type poisonError struct{}

func (poisonError) Error() string { panic("Error() must never be called") }

// TestSensitiveKeyTableCoversSwitch keeps sensitiveKeys (the documented list)
// and sensitiveKeyNormalized (the hot-path matcher) from drifting apart.
func TestSensitiveKeyTableCoversSwitch(t *testing.T) {
	for _, key := range sensitiveKeys {
		if !sensitiveKeyNormalized([]byte(key)) {
			t.Fatalf("documented sensitive key %q is not matched by sensitiveKeyNormalized", key)
		}
	}
	// The documented list is what the spec records, so it must be complete too.
	for _, key := range []string{
		"password", "passwd", "pwd", "token", "accesstoken", "refreshtoken",
		"secret", "clientsecret", "jwtsecret", "credential", "credentials",
		"authorization", "cookie", "setcookie", "apikey",
		"rtspurl", "url", "dsn", "path", "filepath", "socketpath",
	} {
		found := false
		for _, documented := range sensitiveKeys {
			if documented == key {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("sensitive key %q is missing from sensitiveKeys", key)
		}
	}
}

func TestNormalizeKey(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{in: "password", want: "password"},
		{in: "Password", want: "password"},
		{in: "accessToken", want: "accesstoken"},
		{in: "refresh_token", want: "refreshtoken"},
		{in: "client-secret", want: "clientsecret"},
		{in: "setCookie", want: "setcookie"},
		{in: "clientIp", want: "clientip"},
		{in: "client.ip", want: "clientip"},
		{in: "api key", want: "apikey"},
	} {
		if got := string(normalizeKey(tc.in)); got != tc.want {
			t.Fatalf("normalizeKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := normalizeKey(""); got != nil {
		t.Fatalf("normalizeKey(\"\") = %q, want nil", got)
	}
	if got := normalizeKey("пароль"); got != nil {
		t.Fatalf("normalizeKey(non-ASCII) = %q, want nil", got)
	}
	if got := normalizeKey(strings.Repeat("a", maxNormalizedKeyLen+1)); got != nil {
		t.Fatalf("normalizeKey(oversized) = %q, want nil", got)
	}
	if got := normalizeKey("__--__"); got != nil {
		t.Fatalf("normalizeKey(separators only) = %q, want nil", got)
	}
}

// normalizeKeyMatches is a test-only helper mirroring the production matcher for
// assertions against the documented key list.
func normalizeKeyMatches(want, key string) bool {
	return string(normalizeKey(want)) == string(normalizeKey(key))
}
