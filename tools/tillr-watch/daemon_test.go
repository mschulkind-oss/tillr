package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestWatchDaemonWALAndSignals(t *testing.T) {
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			s, rules := fixture(t, false)
			alias := filepath.Join(t.TempDir(), "alias.db")
			if err := os.Symlink(s.Path(), alias); err != nil {
				t.Fatal(err)
			}
			count := func(query string) int {
				t.Helper()
				return daemonCount(t, s, query)
			}
			var initOut, initErr bytes.Buffer
			if code := run(t.Context(), []string{"eval", "--once", "--store", alias, "--rules", rules}, &initOut, &initErr); code != 0 || initErr.Len() != 0 {
				t.Fatalf("init eval: code=%d stdout=%q stderr=%q", code, &initOut, &initErr)
			}
			fact(t, s, `{"item_id":7}`)
			for i := 0; i < 2; i++ {
				fact(t, s, `{"item_id":"initial"}`)
			}
			child := startCLIChild(t, "daemon", "--store", alias, "--rules", rules)
			eventually(t, func() bool { return count(`SELECT COUNT(*) FROM alert_episodes WHERE state='open'`) == 1 })
			// The write to the rules file is ignored by the running daemon.
			doc := strings.Replace(testRule, "threshold=2", "threshold=4", 1)
			if err := os.WriteFile(filepath.Join(rules, "nested", "refusals.toml"), []byte(doc), 0600); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				fact(t, s, `{"item_id":"wal"}`)
			}
			eventually(t, func() bool { return count(`SELECT COUNT(*) FROM alert_episodes WHERE state='open'`) == 2 })
			fact(t, s, `{"item_id":8}`)
			// The stored override is different from both the original and changed file.
			override := strings.Replace(testRule, "threshold=2", "threshold=1", 1)
			seedStatusOverride(t, s, "refusals", override)
			eventually(t, func() bool {
				return count(`SELECT COUNT(*) FROM watch_rule_baselines WHERE rule_id='refusals' AND loaded_seq > 0`) == 1
			})
			fact(t, s, `{"item_id":"override"}`)
			eventually(t, func() bool { return count(`SELECT COUNT(*) FROM alert_episodes WHERE state='open'`) == 3 })
			if err := child.cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			if err := child.wait(t); err != nil {
				t.Fatalf("daemon signal %s: %v: %s", sig, err, &child.stderr)
			}
			want := "tillr watch: rule refusals skipped 1 events: group_by item_id missing or not a string\n" +
				"tillr watch: rule refusals skipped 2 events: group_by item_id missing or not a string\n"
			if child.out.Len() != 0 || child.stderr.String() != want {
				t.Fatalf("daemon output: stdout=%q stderr=%q", &child.out, &child.stderr)
			}
			if n := count(`SELECT COUNT(*) FROM foreground_delivery`); n != 0 {
				t.Fatalf("daemon created %d owners", n)
			}
			if _, err := os.Stat(s.Path() + ".watch-attached"); !os.IsNotExist(err) {
				t.Fatalf("daemon created attachment path: %v", err)
			}
		})
	}
}

func TestWatchDaemonTick(t *testing.T) {
	s, _ := fixture(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time, 1)
	passes := make(chan string, 10)
	e := watch.Evaluator{Store: s, Clock: func() time.Time { passes <- "evaluation"; return time.Now() }}
	done := make(chan error, 1)
	go func() { done <- daemon(ctx, e, io.Discard, ticks) }()
	awaitChunk(t, passes)
	ticks <- time.Now()
	awaitChunk(t, passes)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(hangGuard):
		t.Fatal("daemon did not stop")
	}
}

func TestWatchDaemonRejectsLiveRule(t *testing.T) {
	for _, tc := range []struct{ name, doc, want string }{
		{"invalid", "[[bad", `stored rule "refusals"`},
		{"missing-root", strings.Replace(testRule, "[rule.when]", "projections=['git_refs:refs/heads/missing']\n[rule.when]", 1), "rule refusals requires --root"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, rules := fixture(t, false)
			var initOut, initErr bytes.Buffer
			if code := run(t.Context(), []string{"eval", "--once", "--store", s.Path(), "--rules", rules}, &initOut, &initErr); code != 0 || initErr.Len() != 0 {
				t.Fatalf("init eval: code=%d stdout=%q stderr=%q", code, &initOut, &initErr)
			}
			fact(t, s, `{"item_id":"initial"}`)
			fact(t, s, `{"item_id":"initial"}`)
			child := startCLIChild(t, "daemon", "--store", s.Path(), "--rules", rules)
			eventually(t, func() bool { return daemonCount(t, s, `SELECT COUNT(*) FROM alert_episodes`) == 1 })
			seedStatusOverride(t, s, "refusals", tc.doc)
			var exit *exec.ExitError
			if err := child.wait(t); !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(child.stderr.String(), tc.want) || child.out.Len() != 0 {
				t.Fatalf("daemon accepted refused rule: %v stdout=%q stderr=%q", err, &child.out, &child.stderr)
			}
		})
	}
}

func TestWatchDaemonDiagnosticChangeAndRecovery(t *testing.T) {
	var d ruleDiagnostics
	var stderr bytes.Buffer
	a := watch.RuleResult{ID: "a", Unreadable: "missing"}
	b := watch.RuleResult{ID: "a", OutOfScope: "other"}
	for _, r := range []watch.RuleResult{a, a, b, b, {ID: "a"}, b} {
		d.report(&stderr, []watch.RuleResult{r})
	}
	want := "tillr watch: rule a not evaluated: missing\ntillr watch: rule a not evaluated: the store's charter is other\ntillr watch: rule a not evaluated: the store's charter is other\n"
	if stderr.String() != want {
		t.Fatalf("got %q, want %q", &stderr, want)
	}
}

func TestWatchWALWakeAfterRecreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	wal := path + "-wal"
	if err := os.WriteFile(wal, nil, 0600); err != nil {
		t.Fatal(err)
	}
	replacement := path + "-replacement"
	if err := os.WriteFile(replacement, nil, 0600); err != nil {
		t.Fatal(err)
	}
	wake, err := watchWAL(path, make(chan time.Time))
	if err != nil {
		t.Fatal(err)
	}
	defer wake.close()
	next := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		if reason, err := wake.next(ctx, nil); reason != walWake || err != nil {
			t.Fatalf("WAL did not wake watcher: %v, %v", reason, err)
		}
	}
	if err := os.Remove(wal); err != nil {
		t.Fatal(err)
	}
	next()
	if err := os.Rename(replacement, wal); err != nil {
		t.Fatal(err)
	}
	next()
	if err := os.WriteFile(wal, []byte("new WAL content"), 0600); err != nil {
		t.Fatal(err)
	}
	next()
}

func daemonCount(t *testing.T, s *store.Store, query string) int {
	t.Helper()
	var n int
	if err := s.ReadTx(t.Context(), func(rd *store.Reader) error { return rd.QueryRow(query).Scan(&n) }); err != nil {
		t.Fatal(err)
	}
	return n
}
