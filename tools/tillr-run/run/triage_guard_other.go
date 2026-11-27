//go:build !linux

package run

import "os/exec"

// CanUseReadOnlyMountNS always returns false on non-Linux hosts.
func CanUseReadOnlyMountNS() bool {
	return false
}

// PrepareReadOnlyMountCmd is a no-op on non-Linux hosts, returning (false, nil)
// so callers fall back to userspace confinement.
func PrepareReadOnlyMountCmd(_ *exec.Cmd, _ ReadOnlyMountConfig) (bool, error) {
	return false, nil
}

func prepareTriageExecCmd(_ *exec.Cmd, _ string, _ []string, root, _, workDir, _, _, _ string) (bool, error) {
	if err := prepareTriageSymlinkView(root, workDir); err != nil {
		return false, err
	}
	return false, nil
}
