package main_test

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestBoundaryHelp(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		t.Run(flag, func(t *testing.T) {
			cmd := exec.Command(bin, "boundary", flag)
			cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "state"))
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("boundary %s: %v; stderr: %s", flag, err, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Errorf("unexpected stderr: %s", stderr.String())
			}
			for _, face := range []struct {
				name  string
				flags []string
			}{
				{"snapshot", []string{"-repo", "-target", "-lane", "-patch-ref", "-store", "-out"}},
				{"check", []string{"-before", "-after"}},
			} {
				code, help := invoke(t, "boundary", face.name, "--help")
				if code != 0 || !strings.Contains(stdout.String(), string(help)) {
					t.Errorf("boundary help must include %s's flags (exit %d):\n%s\nfull help:\n%s", face.name, code, help, stdout.String())
				}
				for _, flag := range face.flags {
					if !strings.Contains(string(help), flag) {
						t.Errorf("%s help does not name %s: %s", face.name, flag, help)
					}
				}
			}
		})
	}
	if code, out := invoke(t, "boundary", "bogus"); code != 2 || !strings.Contains(string(out), "unknown boundary face") {
		t.Errorf("unknown boundary face: exit %d: %s", code, out)
	}
}
