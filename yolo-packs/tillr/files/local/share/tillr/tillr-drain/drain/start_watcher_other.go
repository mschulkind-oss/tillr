//go:build !linux

package drain

// A nil source leaves the safety ticker as the only wakeup.
func newStartWatcher(startWatchPaths) startWatcher { return nil }
