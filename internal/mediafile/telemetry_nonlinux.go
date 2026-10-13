//go:build !linux

package mediafile

// deviceTelemetryIsReal reports whether storage's path inspector returns real
// kernel filesystem telemetry.
//
// It is false here because storage only implements real telemetry on Linux; the
// non-Linux build provides inspector_fallback.go with fixed synthetic values.
// Fallthrough detection is therefore skipped on these hosts instead of
// comparing a synthetic device number against the real one os.Root.Stat
// reports, which would refuse every read.
const deviceTelemetryIsReal = false
