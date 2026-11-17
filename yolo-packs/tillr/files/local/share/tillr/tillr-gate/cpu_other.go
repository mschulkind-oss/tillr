//go:build !linux

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

func startAccountedCommand(cmd *exec.Cmd) (func(int, *os.File) (watchSample, error), io.Closer, error) {
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	return clockSampler(nil, fmt.Errorf("inherited kernel CPU accounting requires Linux")), nil, nil
}
