package presence

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestRunProcessEnvironmentReadErrors(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{"access denied", syscall.EACCES, nil},
		{"operation not permitted", syscall.EPERM, nil},
		{"I/O error", syscall.EIO, syscall.EIO},
		{"process disappeared", syscall.ENOENT, syscall.ENOENT},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := readRunProcessEnvironment(dir, uint32(os.Geteuid()), func(path string) ([]byte, error) {
				if path != filepath.Join(dir, "environ") {
					t.Fatalf("read %q, want environ", path)
				}
				return nil, &os.PathError{Op: "open", Path: path, Err: tc.err}
			})
			if env != nil || !errors.Is(err, tc.want) {
				t.Fatalf("environment = %q, error = %v; want nil environment and error %v", env, err, tc.want)
			}
		})
	}
}
