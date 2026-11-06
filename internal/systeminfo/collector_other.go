//go:build !linux

package systeminfo

func newPlatformCollector() collector {
	return newFallbackCollector()
}
