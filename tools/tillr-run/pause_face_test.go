package main_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestPauseFaceRecordsRequestAndExecAnswersPaused(t *testing.T) {
	dir := scratch(t, "pause-face")
	adapter := filepath.Join(dir, "run-stub")
	writeExecutable(t, adapter, "#!/bin/sh\ntrap 'exit 143' TERM\necho ready\nsleep 60\n")
	os.WriteFile(filepath.Join(dir, "pause-kind"), []byte("stops-where-it-stands\n"), 0644)
	db := filepath.Join(dir, "store.db")
	s, err := store.Open(context.Background(), db, true)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	root := filepath.Join(dir, "lanes")
	req := base("pause-fixture", adapter)
	req.LaneID = "lane"
	cmd := exec.Command(bin, "exec", "--store", db, "--lane-root", root, "--input", input(t, envelope(t, "run.request", "request", req)))
	done := make(chan struct{})
	var out []byte
	var runErr error
	go func() { out, runErr = cmd.Output(); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		log, _ := os.ReadFile(filepath.Join(root, "lane", "pause-fixture.log"))
		if strings.Contains(string(log), "ready") {
			break
		}
		if time.Now().After(deadline) {
			cmd.Process.Kill()
			t.Fatal("harness did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	code, output := run(t, "pause", "--store", db, "--run", "pause-fixture", "--pause-timeout-seconds", "2")
	if code != 0 || !strings.Contains(string(output), "pause requested") {
		t.Fatalf("pause exit=%d %s", code, output)
	}
	select {
	case <-done:
	case <-time.After(12 * time.Second):
		cmd.Process.Kill()
		t.Fatal("pause did not finish")
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
	if got := decode(t, out).Payload.State; got != "paused" {
		t.Fatalf("state=%s", got)
	}
	if eventsOfKind(t, db, "run.pause-request") != 1 {
		t.Fatal("pause request not recorded")
	}
}

func TestExecFaceRefusesMissingOrInvalidPauseDeclaration(t *testing.T) {
	for _, kind := range []string{"", "bad\n"} {
		dir := scratch(t, "undeclared-adapter")
		adapter := filepath.Join(dir, "run-stub")
		writeExecutable(t, adapter, "#!/bin/sh\necho ran\n")
		if kind != "" {
			os.WriteFile(filepath.Join(dir, "pause-kind"), []byte(kind), 0644)
		}
		db := filepath.Join(dir, "store.db")
		s, err := store.Open(context.Background(), db, true)
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
		reqPath := filepath.Join(dir, "request.json")
		os.WriteFile(reqPath, []byte(envelope(t, "run.request", "request", base("undeclared", adapter))), 0644)
		cmd := exec.Command(bin, "exec", "--store", db, "--lane-root", filepath.Join(dir, "lanes"), "--input", reqPath)
		output, err := cmd.CombinedOutput()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 2 || !strings.Contains(string(output), "pause-kind") {
			t.Fatalf("exit=%v output=%s", err, output)
		}
		if eventsOfKind(t, db, "run.allocate") != 0 {
			t.Fatal("undeclared adapter was started")
		}
	}
}
