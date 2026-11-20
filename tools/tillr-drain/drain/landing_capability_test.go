package drain

import (
	"context"
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
			if err := writeForkSafe(pi, []byte("#!/bin/sh\n"+tc.script+"\n"), 0755); err != nil {
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
	if err := writeForkSafe(pi, []byte("#!/bin/sh\nsleep 30 &\nsleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	done := make(chan struct{})
	var (
		got      string
		path     string
		features []string
	)
	go func() {
		defer close(done)
		got, path, features = probePiEndingCapability(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("probe held past bound; child holding stdout was not bounded")
	}
	if got != "footer" || path != pi || features != nil {
		t.Fatalf("got %q (%q) features=%v, want footer (%q) nil", got, path, features, pi)
	}
}
