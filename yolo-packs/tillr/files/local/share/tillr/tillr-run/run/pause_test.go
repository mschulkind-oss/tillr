package run_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestPauseAndResumeTwiceKeepsOneAttempt(t *testing.T) {
	for _, kind := range []string{run.PauseAtGap, run.PauseWhereItStands, run.PauseCannotResume} {
		t.Run(kind, func(t *testing.T) {
			dir := scratch(t)
			ctx := context.Background()
			s, err := store.InitFixture(ctx, filepath.Join(dir, "store.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			adapter := filepath.Join(dir, "adapter", "run-stub")
			os.MkdirAll(filepath.Dir(adapter), 0755)
			os.WriteFile(filepath.Join(filepath.Dir(adapter), "pause-kind"), []byte(kind+"\n"), 0644)
			// Segment three completes; the earlier two wait inside a tool call, echoing any
			// probe the test writes so the test can see the harness is still alive. The gap
			// fixture deliberately ignores its pause file so the straggler bound applies.
			script := "#!/bin/sh\nif [ -n \"${TILLR_RESUME_PROMPT_FILE:-}\" ]; then cat \"$TILLR_RESUME_PROMPT_FILE\"; fi\nn=0; [ ! -f segments ] || n=$(cat segments)\nn=$((n+1)); echo $n > segments\necho \"${TILLR_SESSION_MODE:-new}\" >> modes\nif [ $n -lt 3 ]; then trap '' TERM; echo ready; while :; do [ ! -f probe ] || cat probe > probe-echo; sleep 0.02; done; else echo completed; fi\n"
			os.WriteFile(adapter, []byte(script), 0755)
			os.WriteFile(filepath.Join(filepath.Dir(adapter), "extract-report"), []byte("#!/bin/sh\nprev=; out=\nfor arg in \"$@\"; do [ \"$prev\" != --out ] || out=$arg; prev=$arg; done\nprintf 'This run report\\n' > \"$out\"\n"), 0755)
			lane := filepath.Join(dir, "lanes", "lane")
			os.MkdirAll(filepath.Join(lane, "worktree"), 0755)
			os.WriteFile(filepath.Join(lane, "brief.md"), []byte("Continue fixture work.\n"), 0644)
			os.WriteFile(filepath.Join(lane, "session-id"), []byte("12345678-1234-1234-1234-123456789abc"), 0644)
			os.WriteFile(filepath.Join(lane, "worktree", "untouched"), []byte("uncommitted\n"), 0644)
			req := run.Request{RunID: "one", ItemID: "item", LaneID: "lane", Backend: "stub", Command: []string{adapter}, WorkingDir: "worktree"}
			opts := run.ExecOptions{LaneRoot: filepath.Join(dir, "lanes"), BackendsDir: dir, Now: time.Now()}
			os.WriteFile(filepath.Join(lane, "report.md"), []byte("Earlier report\n"), 0644)
			previousSession := "12345678-1234-1234-1234-123456789abc"
			for segment := 1; segment <= 3; segment++ {
				req.Resume = segment > 1
				// The straggler timer is injected, so the test, not the machine, decides when
				// the bound elapses. The runner reports the bound it chose; the test then
				// shows the harness still answering probes while the bound is unexpired, and
				// only then fires it. A runner that killed the harness before its bound
				// leaves the probes unanswered.
				bounds := make(chan time.Duration, 1)
				fire := make(chan time.Time)
				opts.PauseAfter = func(d time.Duration) <-chan time.Time {
					bounds <- d
					return fire
				}
				result := make(chan error, 1)
				state := make(chan string, 1)
				go func() {
					env, e := run.Exec(ctx, s, req, opts)
					if e == nil {
						state <- recordOf(t, env).State
					}
					result <- e
				}()
				if segment < 3 {
					deadline := time.Now().Add(hangGuard)
					for {
						raw, _ := os.ReadFile(filepath.Join(lane, "worktree", "segments"))
						if strings.TrimSpace(string(raw)) == string(rune('0'+segment)) {
							break
						}
						if time.Now().After(deadline) {
							t.Fatal("harness did not start")
						}
						time.Sleep(10 * time.Millisecond)
					}
					if err := run.RequestPause(ctx, s, req.RunID, 2*time.Second, time.Now()); err != nil {
						t.Fatal(err)
					}
					want := 10 * time.Second
					if kind == run.PauseAtGap {
						want = 2 * time.Second
					}
					select {
					case got := <-bounds:
						if got != want {
							t.Fatalf("straggler bound=%s want=%s", got, want)
						}
					case err := <-result:
						t.Fatalf("run ended before the straggler bound was set: %v", err)
					case <-time.After(hangGuard):
						t.Fatal("pause request was never acted on")
					}
					for round := 1; round <= 2; round++ {
						nonce := fmt.Sprintf("segment %d round %d", segment, round)
						os.WriteFile(filepath.Join(lane, "worktree", "probe"), []byte(nonce), 0644)
						deadline := time.Now().Add(hangGuard)
						for {
							raw, _ := os.ReadFile(filepath.Join(lane, "worktree", "probe-echo"))
							if string(raw) == nonce {
								break
							}
							if time.Now().After(deadline) {
								t.Fatal("killed before the pause bound")
							}
							time.Sleep(10 * time.Millisecond)
						}
					}
					close(fire)
				}
				select {
				case err := <-result:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(hangGuard):
					t.Fatal("pause did not stop the harness")
				}
				session, err := run.ReadSessionID(lane)
				if err != nil {
					t.Fatal(err)
				}
				if segment > 1 {
					if kind == run.PauseCannotResume && session == previousSession {
						t.Fatal("fresh continuation reused old session")
					}
					if kind != run.PauseCannotResume && session != previousSession {
						t.Fatal("continuation changed session")
					}
				}
				previousSession = session
				want := run.StatePaused
				if segment == 3 {
					want = run.StateExited
				}
				if got := <-state; got != want {
					t.Fatalf("state=%s want=%s", got, want)
				}
			}
			events, err := s.Events(ctx, 0)
			if err != nil {
				t.Fatal(err)
			}
			counts := map[string]int{}
			for _, e := range events {
				counts[e.Kind]++
			}
			if counts[run.AllocateKind] != 1 || counts[run.ResumeKind] != 2 {
				t.Fatalf("attempt counts: %v", counts)
			}
			for kind, n := range counts {
				if strings.Contains(kind, "verdict") || strings.Contains(kind, "refusal") || strings.Contains(kind, "retry") {
					t.Fatalf("pause wrote %s (%d)", kind, n)
				}
			}
			raw, _ := os.ReadFile(filepath.Join(lane, "worktree", "untouched"))
			if string(raw) != "uncommitted\n" {
				t.Fatal("pause touched worktree")
			}
			log, _ := os.ReadFile(filepath.Join(lane, "one.log"))
			if !strings.Contains(string(log), "interrupted by a restart") {
				t.Fatalf("no restart notice: %s", log)
			}
			if kind == run.PauseAtGap && !strings.Contains(string(log), "may be run again") {
				t.Fatal("no interrupted-command notice")
			}
			if kind == run.PauseCannotResume {
				for _, path := range []string{filepath.Join(lane, "one.log"), filepath.Join(lane, "worktree"), filepath.Join(run.DurableDir(lane, "one"), "report.md")} {
					if !strings.Contains(string(log), path) {
						t.Fatalf("fresh prompt missing %s", path)
					}
				}
			}
		})
	}
}

func TestAdapterPauseDeclarationIsRequired(t *testing.T) {
	for _, body := range []string{"", "unknown\n", "stops-at-a-gap\ncannot-resume\n"} {
		dir := scratch(t)
		path := filepath.Join(dir, "run-stub")
		os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755)
		if body != "" {
			os.WriteFile(filepath.Join(dir, "pause-kind"), []byte(body), 0644)
		}
		if _, err := run.ReadPauseKind(dir, path); err == nil {
			t.Fatalf("accepted declaration %q", body)
		}
	}
}
