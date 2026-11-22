package drain_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// driveRecord separates protocol stdout from diagnostic stderr and logs the
// latter if the test fails. Only exact opt-in trace lines may be excluded
// from record decoding; unknown stderr and every stdout byte remain observable.
func driveRecord(t *testing.T, path string, args ...string) (int, []byte) {
	t.Helper()
	if os.Getenv("TILLR_SQLITE_TRACE") != "1" {
		return drive(t, path, args...)
	}
	cmd := exec.Command(path, args...)
	cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "state"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	// The diagnostic stderr is kept for a failing test's log, not left on disk.
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("face %v stderr:\n%s", args, stderr.String())
		}
	})
	output := recordFaceOutput(stdout.Bytes(), stderr.Bytes(), os.Getenv("TILLR_SQLITE_TRACE") == "1")
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), output
	}
	if err != nil {
		t.Fatalf("running %v failed: %v", args, err)
	}
	return 0, output
}

func recordFaceOutput(stdout, stderr []byte, trace bool) []byte {
	if trace {
		knownOnly := true
		for _, line := range bytes.Split(stderr, []byte{'\n'}) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			if !bytes.HasPrefix(line, []byte("tillr-sqlite ")) && !bytes.HasPrefix(line, []byte("tillr-sqlite-stack ")) && !bytes.HasPrefix(line, []byte("tillr-git ")) {
				knownOnly = false
				break
			}
		}
		if knownOnly {
			return stdout
		}
	}
	return append(append([]byte(nil), stdout...), stderr...)
}

func TestRecordFaceOutputSeparatesOnlyKnownOptInTrace(t *testing.T) {
	good := []byte(`{"kind":"fixture"}`)
	traces := []byte("tillr-sqlite pid=1\ntillr-sqlite-stack frame=blocked\ntillr-git command=rev-parse\n")
	for _, tc := range []struct {
		name           string
		stdout, stderr []byte
		trace, valid   bool
	}{
		{"trace-off-clean", good, nil, false, true},
		{"trace-off-rejects-noise", good, traces, false, false},
		{"trace-on-known", good, traces, true, true},
		{"trace-on-lookalike", good, []byte("tillr-sqlite-lookalike unknown\n"), true, false},
		{"trace-on-unknown", good, []byte("private unknown diagnostic\n"), true, false},
		{"trace-on-malformed-stdout", []byte("malformed\n"), traces, true, false},
		{"trace-on-does-not-filter-stdout", append(traces, good...), nil, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := recordFaceOutput(tc.stdout, tc.stderr, tc.trace)
			if json.Valid(got) != tc.valid {
				t.Fatalf("valid=%v want=%v output=%q", json.Valid(got), tc.valid, got)
			}
			if tc.valid && !bytes.Equal(got, good) {
				t.Fatalf("changed record bytes: %q", got)
			}
		})
	}
	if !strings.Contains(string(recordFaceOutput(good, []byte("unknown"), true)), "unknown") {
		t.Fatal("unknown stderr lost")
	}
}

func TestDriveRecordPreservesTraceOffProtocolAndTraceOnUnknownRejection(t *testing.T) {
	child := filepath.Join(scratch(t, "record-child"), "child")
	script := "#!/bin/sh\nprintf '{\"kind\":\"fixture\"}\\n'\nprintf 'tillr-sqlite pid=1\\n' >&2\n"
	if err := writeForkSafe(child, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TILLR_SQLITE_TRACE", "")
	code, output := driveRecord(t, child)
	if code != 0 || json.Valid(output) || !bytes.Contains(output, []byte("tillr-sqlite pid=1")) {
		t.Fatalf("trace-off changed combined protocol rejection: %d/%q", code, output)
	}
	t.Setenv("TILLR_SQLITE_TRACE", "1")
	code, output = driveRecord(t, child)
	if code != 0 || !json.Valid(output) {
		t.Fatalf("trace-on did not isolate record: %d/%q", code, output)
	}
	script += "printf 'unknown stderr\\n' >&2\n"
	if err := writeForkSafe(child, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	_, output = driveRecord(t, child)
	if json.Valid(output) || !bytes.Contains(output, []byte("unknown stderr")) {
		t.Fatalf("unknown stderr was silently accepted: %q", output)
	}
}
