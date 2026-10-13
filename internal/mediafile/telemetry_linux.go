//go:build linux

package mediafile

// deviceTelemetryIsReal reports whether storage's path inspector returns real
// kernel filesystem telemetry.
//
// Only Linux reads st_dev, st_dev of / and statfs through golang.org/x/sys/unix.
// Everywhere else storage ships inspector_fallback.go, which returns fixed
// synthetic values ("simulated" filesystem, device 1). Mixing a synthetic device
// number with the real st_dev that os.Root.Stat reports makes the fallthrough
// comparison meaningless: on a developer host it would compare the real device
// against the stub's constant and refuse every read.
const deviceTelemetryIsReal = true
