package run_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestCooperativeGapAndSIGTERMExitPausePromptly(t *testing.T) {
	for _, kind := range []string{run.PauseAtGap, run.PauseWhereItStands} {
		t.Run(kind, func(t *testing.T) {
			dir := scratch(t)
			adapter := filepath.Join(dir, "run-stub")
			script := "#!/bin/sh\ntrap 'echo TERM > term-seen; exit 143' TERM\necho ready\n"
			if kind == run.PauseAtGap {
				script += "while [ ! -f \"$TILLR_PAUSE_REQUEST\" ]; do sleep .01; done\nexit 0\n"
			} else {
				script += "sleep 60\n"
			}
			if err := os.WriteFile(adapter, []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "pause-kind"), []byte(kind+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			s, err := store.InitFixture(context.Background(), filepath.Join(dir, "store.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			root := filepath.Join(dir, "lanes")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				env, err := run.Exec(ctx, s, run.Request{RunID: "run", LaneID: "lane", Backend: "stub", Command: []string{adapter}, WorkingDir: "."}, run.ExecOptions{LaneRoot: root, BackendsDir: dir, Now: time.Now()})
				if err == nil && recordOf(t, env).State != run.StatePaused {
					err = run.Internalf("not paused")
				}
				done <- err
			}()
			for {
				log, _ := os.ReadFile(filepath.Join(root, "lane", "run.log"))
				if strings.Contains(string(log), "ready") {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("harness not ready")
				default:
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := run.RequestPause(ctx, s, "run", 2*time.Second, time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			_, err = os.Stat(filepath.Join(root, "lane", "term-seen"))
			if kind == run.PauseAtGap && !os.IsNotExist(err) {
				t.Fatal("gap adapter received TERM")
			}
			if kind == run.PauseWhereItStands && err != nil {
				t.Fatalf("TERM not sent: %v", err)
			}
		})
	}
}
