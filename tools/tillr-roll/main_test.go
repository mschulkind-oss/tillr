package main

import (
	"os"
	"testing"

	"github.com/mschulkind-oss/tillr-roll/roll"
)

func TestExecutePiExtensions(t *testing.T) {
	t.Setenv("TILLR_RUN_BIN", "")
	root := t.TempDir()
	var ran []string
	var ranDir string
	deps := &roll.Deps{
		Git: func(dir string, args ...string) (string, error) {
			return root, nil
		},
		Run: func(args []string, opts roll.RunOpts) (roll.CmdResult, error) {
			ran = args
			ranDir = opts.Dir
			return roll.CmdResult{}, nil
		},
	}
	status, err := execute(false, "", false, true, deps)
	if err != nil || status != 0 {
		t.Fatalf("status=%d err=%v", status, err)
	}
	if len(ran) != 2 || ran[0] != "tillr-run" || ran[1] != "pi-build-agent-dir" || ranDir != root {
		t.Fatalf("ran=%v dir=%q want=[tillr-run pi-build-agent-dir]", ran, ranDir)
	}
}

func TestMainPiExtensionsFlag(t *testing.T) {
	t.Setenv("TILLR_RUN_BIN", "")
	root := t.TempDir()
	var ran []string
	var ranDir string
	deps := &roll.Deps{
		Git: func(dir string, args ...string) (string, error) {
			return root, nil
		},
		Run: func(args []string, opts roll.RunOpts) (roll.CmdResult, error) {
			ran = args
			ranDir = opts.Dir
			return roll.CmdResult{}, nil
		},
	}
	status := runMain([]string{"--pi-extensions"}, deps)
	if status != 0 {
		t.Fatalf("expected status 0, got %d", status)
	}
	if len(ran) != 2 || ran[0] != "tillr-run" || ran[1] != "pi-build-agent-dir" || ranDir != root {
		t.Fatalf("ran=%v dir=%q want=[tillr-run pi-build-agent-dir]", ran, ranDir)
	}
}

func TestExecuteRequiresDurableDir(t *testing.T) {
	t.Setenv("YOLO_DURABLE_DIR", "")
	_ = os.Unsetenv("YOLO_DURABLE_DIR")
	deps := &roll.Deps{
		Git: func(dir string, args ...string) (string, error) {
			return t.TempDir(), nil
		},
	}
	status, err := execute(false, "", false, false, deps)
	if err == nil || status != 1 {
		t.Fatalf("expected error for missing YOLO_DURABLE_DIR, got status=%d err=%v", status, err)
	}
}
