package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncForwardsOnlyOptInSQLiteDiagnosticsOnSuccess(t *testing.T) {
	for _, trace := range []string{"", "1"} {
		t.Run("trace="+trace, func(t *testing.T) {
			t.Setenv("TILLR_SQLITE_TRACE", trace)
			t.Setenv("TILLR_TRACE_SECRET_CONTROL", "environment-secret-must-not-appear")
			dir := t.TempDir()
			child := filepath.Join(dir, "item")
			body := `#!/bin/sh
printf 'private-child-stderr-must-not-appear\n' >&2
printf 'tillr-sqlite at=fixture pid=1 tx=1 operation="migrate" phase=end\n' >&2
printf 'tillr-sqlite-stack pid=1 tx=1 frame="blocked migration"\n' >&2
printf 'tillr-sqlite-lookalike private-noise\n' >&2
printf '{"kind":"item.sync","payload":{}}\n'
`
			if err := writeForkSafe(child, []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(dir, "stderr")
			capture, err := os.Create(output)
			if err != nil {
				t.Fatal(err)
			}
			original := os.Stderr
			os.Stderr = capture
			defer func() { os.Stderr = original; capture.Close() }()
			step := &ProcessSteps{ItemBin: child, Root: dir, Store: filepath.Join(dir, "fixture.db")}
			if _, err := step.Sync(context.Background()); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			log := string(raw)
			if trace == "" {
				if log != "" {
					t.Fatalf("trace-off forwarded output: %s", log)
				}
				return
			}
			for _, prefix := range []string{"tillr-sqlite ", "tillr-sqlite-stack "} {
				if !strings.Contains(log, prefix) {
					t.Errorf("missing successful child trace %q: %s", prefix, log)
				}
			}
			for _, secret := range []string{"private-child-stderr-must-not-appear", "private-noise", "environment-secret-must-not-appear"} {
				if strings.Contains(log, secret) {
					t.Errorf("forwarded private stderr: %s", log)
				}
			}
		})
	}
}
