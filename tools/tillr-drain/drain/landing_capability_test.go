package drain

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
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
			got, path, _ := probePiEndingCapability(context.Background())
			if got != tc.want || path != pi {
				t.Fatalf("got %q (%q), want %q (%q)", got, path, tc.want, pi)
			}
		})
	}
}

func TestPiEndingCapabilityProbeBoundsSleeperAndChildHoldingOutput(t *testing.T) {
	oldTimeout, oldDelay := piFeaturesProbeTimeout, piFeaturesWaitDelay
	piFeaturesProbeTimeout = 100 * time.Millisecond
	piFeaturesWaitDelay = 100 * time.Millisecond
	t.Cleanup(func() {
		piFeaturesProbeTimeout, piFeaturesWaitDelay = oldTimeout, oldDelay
	})

	dir := t.TempDir()
	pi := filepath.Join(dir, "pi")
	if err := os.WriteFile(pi, []byte("#!/bin/sh\nsleep 30 &\nsleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	start := time.Now()
	got, path, features := probePiEndingCapability(context.Background())
	elapsed := time.Since(start)
	if got != "footer" || path != pi || features != nil {
		t.Fatalf("got %q (%q) features=%v, want footer (%q) nil", got, path, features, pi)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("probe took %s; child holding stdout was not bounded", elapsed)
	}
}
