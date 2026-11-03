//go:build !linux

package systeminfo

func newPlatformCollector() Collector {
	return NewFallbackCollector()
}
