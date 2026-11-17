//go:build linux

package drain

import (
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestHeavyProcessDisappearedHandlesWrappedProcErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		gone bool
	}{
		{"no error", nil, false},
		{"absent proc entry", &os.PathError{Op: "read", Path: "/proc/123/stat", Err: unix.ENOENT}, true},
		{"raw vanished process", unix.ESRCH, true},
		{"vanished process during proc read", &os.PathError{Op: "read", Path: "/proc/123/stat", Err: unix.ESRCH}, true},
		{"wrapped vanished process", fmt.Errorf("observation: %w", &os.PathError{Op: "read", Path: "/proc/123/stat", Err: unix.ESRCH}), true},
		{"permission denied", &os.PathError{Op: "read", Path: "/proc/123/stat", Err: unix.EACCES}, false},
		{"I/O failure", &os.PathError{Op: "read", Path: "/proc/123/stat", Err: unix.EIO}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := heavyProcessDisappeared(tc.err); got != tc.gone {
				t.Fatalf("heavyProcessDisappeared(%v) = %t, want %t", tc.err, got, tc.gone)
			}
		})
	}
}
