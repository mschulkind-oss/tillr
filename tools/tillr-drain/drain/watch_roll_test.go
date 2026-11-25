package drain

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/tillr-roll/roll"
)

func TestNamedRollWatchInstallation(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	writePackFixture(t, root, "revision", "pre-watch\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "pre-watch")
	old := runGit(t, root, "rev-parse", "HEAD")
	writePackFixture(t, root, "tools/tillr-watch/go.mod", "module fixture\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "with watch")
	current := runGit(t, root, "rev-parse", "HEAD")

	t.Setenv("TILLR_ROLL_VERIFICATION", "after-merge")

	live := filepath.Join(root, "bin")
	rolled := filepath.Join(root, roll.Rolled)
	marker := filepath.Join(root, roll.WatchRetire)
	watch := filepath.Join(live, "tillr-watch")

	var mu sync.Mutex
	deferred := false
	failBuild := false
	refuseRestart := false
	facts := map[string]roll.BuildFact{}
	seq := int64(0)

	activate := func(stage, commit string) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(stage, "roll.json"))
		if err != nil {
			t.Fatal(err)
		}
		var meta struct {
			Commit string `json:"commit"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil {
			t.Fatal(err)
		}
		if meta.Commit != commit {
			t.Fatalf("stage commit = %q, want %q", meta.Commit, commit)
		}
		entries, err := os.ReadDir(stage)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.Name() == "roll.json" {
				continue
			}
			if err := os.Rename(filepath.Join(stage, entry.Name()), filepath.Join(live, entry.Name())); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(rolled, []byte(commit+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(stage, "roll.json")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(stage); err != nil {
			t.Fatal(err)
		}
	}

	var removeHook func(path string) error

	deps := &roll.Deps{
		Run: func(args []string, opts roll.RunOpts) (roll.CmdResult, error) {
			if len(args) >= 2 && args[0] == "go" && args[1] == "build" {
				target := ""
				for i := 0; i+1 < len(args); i++ {
					if args[i] == "-o" {
						target = args[i+1]
						break
					}
				}
				if target == "" {
					t.Fatalf("missing -o in go build args: %v", args)
				}
				mu.Lock()
				shouldFail := failBuild
				mu.Unlock()
				if shouldFail {
					return roll.CmdResult{}, errors.New("fixture build failed")
				}
				repoDir := filepath.Dir(filepath.Dir(opts.Dir))
				rev, err := roll.Git(repoDir, nil, "rev-parse", "HEAD")
				if err != nil {
					return roll.CmdResult{}, err
				}
				if err := os.WriteFile(target, []byte(rev), 0o755); err != nil {
					return roll.CmdResult{}, err
				}
				return roll.CmdResult{}, nil
			}
			if len(args) >= 3 && args[1] == "loop" && args[2] == "--check" {
				mu.Lock()
				isDeferred := deferred
				mu.Unlock()
				if isDeferred {
					return roll.CmdResult{Stdout: roll.SchemaCheck + "\n"}, nil
				}
				return roll.CmdResult{}, nil
			}
			if len(args) == 2 && args[1] == "--help" {
				info, err := os.Stat(args[0])
				if err != nil || !info.Mode().IsRegular() {
					t.Fatalf("smoke target %q is not a regular file: %v", args[0], err)
				}
				return roll.CmdResult{}, nil
			}
			return roll.Run(args, opts, nil)
		},
		Builds: func(store string) (map[string]roll.BuildFact, error) {
			mu.Lock()
			defer mu.Unlock()
			out := make(map[string]roll.BuildFact, len(facts))
			for k, v := range facts {
				out[k] = v
			}
			return out, nil
		},
		RecordFailure: func(store, commit, step, detail string) {},
		TestCommit: func(root, source, work, tip, store string, old map[string]roll.BuildFact, buildEnv []string) error {
			return nil
		},
		Restart: func(liveDir, rootDir, role string) string {
			mu.Lock()
			defer mu.Unlock()
			exe := filepath.Join(liveDir, "tillr-"+role)
			if deferred && role == "drain" {
				d, err := roll.Digest(exe)
				if err != nil {
					t.Fatal(err)
				}
				content, err := os.ReadFile(exe)
				if err != nil {
					t.Fatal(err)
				}
				stage := filepath.Join(liveDir, roll.SchemaStage+d)
				activate(stage, string(content))
			}
			if refuseRestart && (!deferred || role == "view") {
				return "refused"
			}
			content, err := os.ReadFile(exe)
			if err != nil {
				t.Fatal(err)
			}
			d, err := roll.Digest(exe)
			if err != nil {
				t.Fatal(err)
			}
			seq++
			facts[role] = roll.BuildFact{
				Seq:      seq,
				Role:     role,
				Revision: string(content),
				Digest:   d,
			}
			return "restarted"
		},
		Remove: func(path string) error {
			if removeHook != nil {
				return removeHook(path)
			}
			return os.Remove(path)
		},
	}

	install := func(commit string, noRequest bool) {
		t.Helper()
		rc, err := roll.Roll(root, live, noRequest, false, commit, deps)
		if err != nil || rc != 0 {
			t.Fatalf("roll(%s, noRequest=%v) = (%d, %v), want (0, nil)", commit, noRequest, rc, err)
		}
	}

	expectBadRevision := func() {
		t.Helper()
		_, err := roll.Roll(root, live, true, false, "bad-requested-revision", deps)
		if err == nil {
			t.Fatal("roll unexpectedly resolved a bad revision")
		}
		var exitErr *roll.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("expected *roll.ExitError for bad revision, got %T: %v", err, err)
		}
	}

	expectFailedBuild := func() {
		t.Helper()
		_, err := roll.Roll(root, live, true, false, old, deps)
		if err == nil {
			t.Fatal("build unexpectedly succeeded")
		}
		var rf *roll.RollFailedError
		if !errors.As(err, &rf) || !strings.Contains(err.Error(), "fixture build failed") {
			t.Fatalf("expected RollFailedError with fixture build failed, got %T: %v", err, err)
		}
	}

	mustRead := func(path string) string {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	exists := func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	}

	install(current, true)
	if got := mustRead(watch); got != current {
		t.Fatalf("watch = %q, want %q", got, current)
	}

	failBuild = true
	expectFailedBuild()
	if got := mustRead(watch); got != current {
		t.Fatalf("watch after failed build = %q, want %q", got, current)
	}
	failBuild = false

	if err := os.Remove(rolled); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(rolled, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := roll.Roll(root, live, true, false, old, deps); err == nil {
		t.Fatal("roll unexpectedly stamped a directory")
	} else {
		var pathErr *os.PathError
		if !errors.As(err, &pathErr) || pathErr.Path != rolled || !errors.Is(err, syscall.EISDIR) {
			t.Fatalf("expected EISDIR on %s, got %T: %v", rolled, err, err)
		}
	}
	if got := mustRead(watch); got != current {
		t.Fatalf("watch after EISDIR = %q, want %q", got, current)
	}
	if err := os.Remove(rolled); err != nil {
		t.Fatal(err)
	}

	refuseRestart = true
	if rc, err := roll.Roll(root, live, false, false, old, deps); err != nil || rc != 1 {
		t.Fatalf("roll with refused restart = (%d, %v), want (1, nil)", rc, err)
	}
	if got := mustRead(watch); got != current {
		t.Fatalf("watch after refused restart = %q, want %q", got, current)
	}
	refuseRestart = false

	install(old, true)
	if exists(watch) {
		t.Fatalf("watch %s still exists after install(old)", watch)
	}
	if got := mustRead(filepath.Join(live, "tillr")); got != old {
		t.Fatalf("live/tillr = %q, want %q", got, old)
	}

	// A successful requested handoff must retire watch itself, not settle a
	// marker from an earlier --no-request roll.
	install(current, true)
	deferred = true
	if got := mustRead(watch); got != current || exists(marker) {
		t.Fatalf("unexpected state before deferred handoff: watch=%q marker=%v", got, exists(marker))
	}
	install(old, false)
	if got := strings.TrimSpace(mustRead(rolled)); got != old {
		t.Fatalf("rolled = %q, want %q", got, old)
	}
	if exists(watch) || exists(marker) {
		t.Fatalf("expected watch and marker removed after deferred handoff: watch=%v marker=%v", exists(watch), exists(marker))
	}
	install(current, false)
	if got := mustRead(watch); got != current {
		t.Fatalf("watch = %q, want %q", got, current)
	}

	refuseRestart = true
	if rc, err := roll.Roll(root, live, false, false, old, deps); err != nil || rc != 1 {
		t.Fatalf("roll with refused restart = (%d, %v), want (1, nil)", rc, err)
	}
	expectBadRevision()
	if got := mustRead(watch); got != current || exists(marker) {
		t.Fatalf("unexpected state after refused+badrev: watch=%q marker=%v", got, exists(marker))
	}
	refuseRestart = false

	// Cleanup failure during settlement of an earlier successful --no-request
	// roll must keep the binary and leave the marker eligible for a retry.
	install(old, true)
	var pending struct {
		Stage  string `json:"stage"`
		Commit string `json:"commit"`
	}
	if err := json.Unmarshal([]byte(mustRead(marker)), &pending); err != nil {
		t.Fatal(err)
	}
	activate(filepath.Join(live, pending.Stage), old)

	expectFailedCleanup := func(target func(string) bool, errMsg string) {
		t.Helper()
		removeHook = func(path string) error {
			if target(path) {
				return errors.New(errMsg)
			}
			return os.Remove(path)
		}
		_, err := roll.Roll(root, live, false, false, old, deps)
		removeHook = nil
		if err == nil || err.Error() != errMsg {
			t.Fatalf("expected cleanup error %q, got %v", errMsg, err)
		}
		if got := mustRead(watch); got != current || !exists(marker) {
			t.Fatalf("expected watch=%q and marker present after failed cleanup, got watch=%q marker=%v", current, got, exists(marker))
		}
	}

	expectFailedCleanup(func(path string) bool { return path == marker }, "fixture marker cleanup failed")
	// The next roll tries to settle the previous installation first. Failure to
	// delete the recovery copy must restore both the binary and the retirement intent.
	expectFailedCleanup(func(path string) bool { return strings.HasPrefix(filepath.Base(path), ".tillr-watch-") }, "fixture backup cleanup failed")

	install(old, false)
	if exists(watch) || exists(marker) || strings.TrimSpace(mustRead(rolled)) != old {
		t.Fatalf("expected clean retirement after retry: watch=%v marker=%v rolled=%q", exists(watch), exists(marker), mustRead(rolled))
	}

	for _, schemaRoll := range []bool{false, true} {
		deferred = false
		install(current, true)
		deferred = schemaRoll
		fullFile, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
		if err != nil {
			t.Fatalf("open /dev/full: %v", err)
		}
		// Neither report line fills this buffer. Only flushing delivers the error.
		deps.Stdout = bufio.NewWriter(fullFile)
		_, rollErr := roll.Roll(root, live, !deferred, false, old, deps)
		deps.Stdout = nil
		_ = fullFile.Close()
		if !errors.Is(rollErr, syscall.ENOSPC) {
			t.Fatalf("roll with unflushable stdout (deferred=%v): got %v, want ENOSPC", schemaRoll, rollErr)
		}
		if got := strings.TrimSpace(mustRead(rolled)); got != old {
			t.Fatalf("rolled = %q, want %q", got, old)
		}
		if got := mustRead(watch); got != current {
			t.Fatalf("watch = %q, want %q", got, current)
		}
		expectBadRevision()
		if got := mustRead(watch); got != current || exists(marker) {
			t.Fatalf("unexpected state after ENOSPC+badrev: watch=%q marker=%v", got, exists(marker))
		}
	}

	deferred = false
	install(current, true)
	deferred = true
	install(old, true)
	if got := mustRead(watch); got != current {
		t.Fatalf("watch = %q, want %q", got, current)
	}
	if err := json.Unmarshal([]byte(mustRead(marker)), &pending); err != nil {
		t.Fatal(err)
	}
	if pending.Commit != old || !strings.HasPrefix(pending.Stage, roll.SchemaStage) {
		t.Fatalf("unexpected pending marker: %+v", pending)
	}
	stage := filepath.Join(live, pending.Stage)
	failBuild = true
	// Even a matching commit stamp is not enough while staging still exists.
	if err := os.WriteFile(rolled, []byte(old+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expectFailedBuild()
	if got := mustRead(watch); got != current || !exists(marker) || !exists(stage) {
		t.Fatalf("expected watch=%q marker=true stage=true, got watch=%q marker=%v stage=%v", current, got, exists(marker), exists(stage))
	}
	activate(stage, old)
	expectFailedBuild()
	if exists(watch) || exists(marker) {
		t.Fatalf("expected watch and marker settled after stage activation: watch=%v marker=%v", exists(watch), exists(marker))
	}
}
