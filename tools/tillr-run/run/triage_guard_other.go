//go:build !linux

package run

import "os/exec"

func prepareTriageExecCmd(_ *exec.Cmd, _ string, _ []string, root, _, workDir, _, _, _ string) (bool, error) {
	if err := prepareTriageSymlinkView(root, workDir); err != nil {
		return false, err
	}
	return false, nil
}
