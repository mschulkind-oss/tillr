package drain

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPiEndingCapabilityProbe(t *testing.T) {
	for _, tc := range []struct{ name, script, want string }{
		{"supported", "printf '[\"parked-tool-calls\"]\\n'", "call-suspends"},
		{"unsupported", "printf '[\"other\"]\\n'", "footer"},
		{"rejects", "exit 2", "footer"},
		{"malformed", "echo parked-tool-calls", "footer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			pi := filepath.Join(dir, "pi")
			if err := os.WriteFile(pi, []byte("#!/bin/sh\n"+tc.script+"\n"), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			got, path := probePiEndingCapability(context.Background())
			if got != tc.want || path != pi {
				t.Fatalf("got %q (%q), want %q (%q)", got, path, tc.want, pi)
			}
		})
	}
}
