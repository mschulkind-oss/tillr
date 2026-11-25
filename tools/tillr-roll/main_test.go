package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-roll/roll"
)

func TestExecutePiExtensions(t *testing.T) {
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
	want := filepath.Join(root, "tools/tillr-run/backends/pi/build-agent-dir")
	if len(ran) != 1 || ran[0] != want || ranDir != root {
		t.Fatalf("ran=%v dir=%q want=%q", ran, ranDir, want)
	}
}

func TestMainPiExtensionsFlag(t *testing.T) {
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
	want := filepath.Join(root, "tools/tillr-run/backends/pi/build-agent-dir")
	if len(ran) != 1 || ran[0] != want || ranDir != root {
		t.Fatalf("ran=%v dir=%q want=%q", ran, ranDir, want)
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
