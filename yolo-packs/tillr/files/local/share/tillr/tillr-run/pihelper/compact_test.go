package pihelper

import (
	"bytes"
	"strings"
	"testing"
)

func TestCompactHelpAndFlagValidation(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		t.Run("help "+flag, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			if code := RunCompact([]string{flag}, &out, &errBuf); code != 0 {
				t.Fatalf("expected exit 0 for %s, got %d", flag, code)
			}
			for _, want := range []string{"--backend", "--model", "--session-id", "--effort", "--timeout", "--pi-dir"} {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("help output missing %q: %q", want, out.String())
				}
			}
		})
	}

	invalid := []struct {
		name string
		args []string
	}{
		{name: "no args", args: nil},
		{name: "missing model", args: []string{"--backend", "pi", "--session-id", "s"}},
		{name: "missing session-id", args: []string{"--backend", "pi", "--model", "m"}},
		{name: "missing backend", args: []string{"--model", "m", "--session-id", "s"}},
		{name: "non-pi backend", args: []string{"--backend", "claude", "--model", "m", "--session-id", "s"}},
		{name: "negative timeout", args: []string{"--backend", "pi", "--model", "m", "--session-id", "s", "--timeout", "-1"}},
		{name: "unexpected positional arg", args: []string{"--backend", "pi", "--model", "m", "--session-id", "s", "extra"}},
		{name: "unknown flag", args: []string{"--bogus"}},
	}

	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			if code := RunCompact(tc.args, &out, &errBuf); code != 2 {
				t.Fatalf("expected exit 2, got %d (stdout=%q stderr=%q)", code, out.String(), errBuf.String())
			}
		})
	}
}
