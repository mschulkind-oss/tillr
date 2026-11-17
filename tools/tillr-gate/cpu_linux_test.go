//go:build linux

package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTaskClockInheritsChildrenBeforeExec(t *testing.T) {
	attr := taskClockConfig()
	if attr.Type != 1 || attr.Config != 1 || attr.Size != 64 || attr.Flags&taskClockInherit == 0 || attr.ReadFormat != 0 {
		t.Fatalf("not an inheritable CPU clock: %+v", attr)
	}
	if attr.Flags&(uint64(1)<<5|uint64(1)<<6) != 0 {
		t.Fatal("kernel CPU was excluded from accounting")
	}
}
func TestDeniedCounterKeepsOwnVerdictAndReportsImmediately(t *testing.T) {
	log, err := os.CreateTemp(scratch(t, "watchdog-denied-cpu"), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command("true")
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	sampler, closer, err := startWithTaskClock(cmd, func(int) (*os.File, error) { return nil, errors.New("counter denied") })
	if err != nil {
		t.Fatal(err)
	}
	if closer != nil {
		defer closer.Close()
	}
	sources := &watchSources{Sample: sampler, Ticks: func() (<-chan time.Time, func()) { return nil, func() {} }}
	err, state := watchCommand(cmd, log, sources)
	if err != nil || state.Unavailable == "" || state.SilentSamples != 0 {
		t.Fatalf("unavailable accounting changed verdict: %+v %v", state, err)
	}
	body, err := os.ReadFile(log.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "watchdog unavailable") {
		t.Fatal("CPU accounting failure hidden from the live log")
	}
}
