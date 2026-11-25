package roll

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

var watchStageRe = regexp.MustCompile(`^` + regexp.QuoteMeta(SchemaStage) + `[a-f0-9]{64}$`)

// Builds reads the latest drain.build-started fact per role from the store.
func Builds(store string) (map[string]BuildFact, error) {
	absStore, err := filepath.Abs(store)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+absStore+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query("SELECT seq, payload FROM events WHERE kind = 'drain.build-started' ORDER BY seq DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	facts := make(map[string]BuildFact)
	for rows.Next() {
		var seq int64
		var payload string
		if err := rows.Scan(&seq, &payload); err != nil {
			return nil, err
		}
		var fact BuildFact
		if err := json.Unmarshal([]byte(payload), &fact); err != nil {
			return nil, err
		}
		fact.Seq = seq
		if _, exists := facts[fact.Role]; !exists {
			facts[fact.Role] = fact
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return facts, nil
}

// Digest returns the lowercase hex SHA-256 digest of the file at path.
func Digest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Report prints per-role restart status and flushes stdout before returning.
// Returns 1 if any role had a digest mismatch or answered refused/error.
func Report(old, newFacts map[string]BuildFact, answers map[string]string, roles []string, live string, stdout io.Writer) (int, error) {
	if stdout == nil {
		stdout = os.Stdout
	}
	failed := false
	for _, role := range roles {
		answer, ok := answers[role]
		if !ok {
			continue
		}
		before := old[role].Revision
		if before == "" {
			before = "unknown"
		}
		after := newFacts[role].Revision
		if after == "" {
			after = "unknown"
		}
		mismatch := false
		if answer != "refused" && answer != "not-requested" && answer != "error" {
			d, err := Digest(filepath.Join(live, "tillr-"+role))
			if err != nil {
				return 1, err
			}
			mismatch = newFacts[role].Digest != d
		}
		detail := ""
		if mismatch {
			detail = "; part does not run from the live directory"
		}
		if _, err := fmt.Fprintf(stdout, "%s: old=%s new=%s answer=%s%s\n", role, before, after, answer, detail); err != nil {
			return 1, err
		}
		if mismatch || answer == "refused" || answer == "error" {
			failed = true
		}
	}
	if err := flushWriter(stdout); err != nil {
		return 1, err
	}
	if failed {
		return 1, nil
	}
	return 0, nil
}

// Evidence returns nil when refs/tillr/green^{commit} is an ancestor of tip (all modules proved),
// or an empty map otherwise.
func Evidence(root, tip string, deps *Deps) map[string]bool {
	green, err := deps.git(root, "rev-parse", "--verify", "--quiet", "refs/tillr/green^{commit}")
	if err == nil && green != "" {
		if _, err := deps.run([]string{"git", "merge-base", "--is-ancestor", tip, green}, RunOpts{Dir: root, CaptureOutput: true}); err == nil {
			return nil
		}
	}
	return map[string]bool{}
}

// GatedModules returns the set of module names under tools/ that tip changed and
// that were gated at the exact landed tree.
func GatedModules(root, tip, store string, deps *Deps) map[string]bool {
	absStore, err := filepath.Abs(store)
	if err != nil {
		return map[string]bool{}
	}
	db, err := sql.Open("sqlite", "file:"+absStore+"?mode=ro")
	if err != nil {
		return map[string]bool{}
	}
	defer db.Close()

	var gatedTree, landedTree sql.NullString
	err = db.QueryRow(
		"SELECT gated_tree, landed_tree FROM landings WHERE trunk_after = ? AND result = 'landed'",
		tip,
	).Scan(&gatedTree, &landedTree)
	if err != nil || !gatedTree.Valid || gatedTree.String == "" || gatedTree.String != landedTree.String {
		return map[string]bool{}
	}
	tipTree, err := deps.git(root, "rev-parse", tip+"^{tree}")
	if err != nil || landedTree.String != tipTree {
		return map[string]bool{}
	}
	changed, err := deps.git(root, "diff", "--name-only", "-z", "--no-renames", tip+"^", tip, "--", "tools")
	if err != nil {
		return map[string]bool{}
	}
	modules := make(map[string]bool)
	for _, p := range strings.Split(changed, "\x00") {
		if strings.HasPrefix(p, "tools/") {
			parts := strings.SplitN(p, "/", 3)
			if len(parts) >= 2 && parts[1] != "" {
				modules[parts[1]] = true
			}
		}
	}
	return modules
}

// CleanCheckout clones root into dest at tip and copies untracked web/dist if present.
func CleanCheckout(root, tip, dest, shared string, deps *Deps) error {
	if _, err := deps.run([]string{"git", "clone", "-q", "--shared", "--no-checkout", root, dest}, RunOpts{}); err != nil {
		return err
	}
	if _, err := deps.run([]string{"git", "-c", "advice.detachedHead=false", "checkout", "-q", "--detach", tip}, RunOpts{Dir: dest}); err != nil {
		return err
	}
	dist := filepath.Join(shared, "tools/tillr-view/web/dist")
	destDist := filepath.Join(dest, "tools/tillr-view/web/dist")
	if isDir(dist) && !pathExists(destDist) {
		if err := copyDir(dist, destDist); err != nil {
			return err
		}
	}
	return nil
}

// TestCommit runs tests for modules changed since the running drain/view revisions
// unless already proved by green or gated_tree.
func TestCommit(root, source, work, tip, store string, old map[string]BuildFact, buildEnv []string, deps *Deps) error {
	changed := make(map[string]bool)
	changedAll := false
	for _, role := range []string{"drain", "view"} {
		fact, ok := old[role]
		if !ok || fact.Revision == "" || fact.Modified {
			changedAll = true
			break
		}
		out, err := deps.git(root, "diff", "--name-only", "-z", "--no-renames", fact.Revision, tip, "--", "tools")
		if err != nil {
			changedAll = true
			break
		}
		for _, p := range strings.Split(out, "\x00") {
			if p != "" {
				changed[p] = true
			}
		}
	}

	var proved map[string]bool
	if err := deps.stepped("evidence", func() error {
		proved = Evidence(root, tip, deps)
		return nil
	}); err != nil {
		return err
	}
	if proved == nil {
		return nil
	}

	var gated map[string]bool
	if err := deps.stepped("evidence", func() error {
		gated = GatedModules(root, tip, store, deps)
		return nil
	}); err != nil {
		return err
	}
	accepted := make(map[string]bool, len(proved)+len(gated))
	for k, v := range proved {
		if v {
			accepted[k] = true
		}
	}
	for k, v := range gated {
		if v {
			accepted[k] = true
		}
	}

	entries, err := filepath.Glob(filepath.Join(source, "tools", "*"))
	if err != nil {
		return err
	}
	sort.Strings(entries)
	for _, module := range entries {
		if !isFile(filepath.Join(module, "go.mod")) {
			continue
		}
		name := filepath.Base(module)
		if accepted[name] {
			continue
		}
		prefix := "tools/" + name + "/"
		moduleChanged := changedAll
		if !moduleChanged {
			for p := range changed {
				if strings.HasPrefix(p, prefix) {
					moduleChanged = true
					break
				}
			}
		}
		if !moduleChanged {
			continue
		}
		relative := "tools/" + name
		if isSharded(relative) {
			if err := deps.stepped("test "+name, func() error {
				_, err := deps.run([]string{
					"python3", filepath.Join(source, "tools/go-test-shards.py"),
					relative, "-k", "6", "--timeout", "0", "--scratch", work,
				}, RunOpts{Dir: source, Env: buildEnv})
				return err
			}); err != nil {
				return err
			}
		} else {
			if err := deps.stepped("test "+name, func() error {
				_, err := deps.run([]string{"go", "test", "-timeout", "0", "./..."}, RunOpts{Dir: module, Env: buildEnv})
				return err
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// StageSchemaRoll stages sibling binaries in live/.tillr-schema-roll-<digest>
// and atomically replaces live/tillr-drain.
func StageSchemaRoll(stage, live, tip, store string) (string, error) {
	drain := filepath.Join(stage, "tillr-drain")
	d, err := Digest(drain)
	if err != nil {
		return "", err
	}
	pending := filepath.Join(live, SchemaStage+d)
	absStore, err := resolvePath(store)
	if err != nil {
		return "", err
	}
	rollMeta, err := json.Marshal(struct {
		Commit string `json:"commit"`
		Store  string `json:"store"`
	}{
		Commit: tip,
		Store:  absStore,
	})
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(stage, "roll.json"), rollMeta, 0o644); err != nil {
		return "", err
	}
	if !pathExists(pending) {
		if err := os.Rename(stage, pending); err != nil {
			return "", err
		}
		if err := os.Rename(filepath.Join(pending, "tillr-drain"), filepath.Join(live, "tillr-drain")); err != nil {
			return "", err
		}
	} else {
		if err := os.Rename(drain, filepath.Join(live, "tillr-drain")); err != nil {
			return "", err
		}
	}
	return pending, nil
}

// WatchRetirement hides the installed tillr-watch binary while keeping it recoverable until Commit.
type WatchRetirement struct {
	watch  string
	backup string
	remove func(string) error
}

// NewWatchRetirement creates a WatchRetirement for the given live bin directory.
func NewWatchRetirement(live string, deps *Deps) *WatchRetirement {
	remove := os.Remove
	if deps != nil && deps.Remove != nil {
		remove = deps.Remove
	}
	return &WatchRetirement{
		watch:  filepath.Join(live, "tillr-watch"),
		remove: remove,
	}
}

// MoveAside moves live/tillr-watch to a temporary backup file if present.
func (w *WatchRetirement) MoveAside() error {
	_, err := os.Lstat(w.watch)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(w.watch), ".tillr-watch-")
	if err != nil {
		return err
	}
	backup := tmp.Name()
	_ = tmp.Close()
	if err := os.Rename(w.watch, backup); err != nil {
		_ = w.remove(backup)
		return err
	}
	w.backup = backup
	return nil
}

// Commit removes the backup, finalizing retirement of tillr-watch.
func (w *WatchRetirement) Commit() error {
	if w.backup != "" {
		if err := w.remove(w.backup); err != nil {
			return err
		}
		w.backup = ""
	}
	return nil
}

// Close restores the backup if Commit was not called.
func (w *WatchRetirement) Close() {
	if w.backup != "" {
		_ = os.Rename(w.backup, w.watch)
		w.backup = ""
	}
}

// StampedCommit reads the commit SHA from tillr-roll.commit beside live, or "" if absent.
func StampedCommit(live string) string {
	b, err := os.ReadFile(filepath.Join(filepath.Dir(live), Rolled))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// PublishWatchRetirement atomically writes the watch retirement marker.
func PublishWatchRetirement(marker, tip, stage string) error {
	tmp, err := os.CreateTemp(filepath.Dir(marker), "."+filepath.Base(marker)+"-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	payload, err := json.Marshal(struct {
		Commit string `json:"commit"`
		Stage  string `json:"stage"`
	}{
		Commit: tip,
		Stage:  filepath.Base(stage),
	})
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, marker); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// SettleWatchRetirement completes or discards any pending tillr-watch.retire marker.
func SettleWatchRetirement(live string, deps *Deps) error {
	marker := filepath.Join(filepath.Dir(live), WatchRetire)
	record, err := os.ReadFile(marker)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var pending struct {
		Stage  *string `json:"stage"`
		Commit *string `json:"commit"`
	}
	if err := json.Unmarshal(record, &pending); err == nil &&
		pending.Stage != nil && watchStageRe.MatchString(*pending.Stage) &&
		pending.Commit != nil {
		if pathExists(filepath.Join(live, *pending.Stage)) {
			return nil
		}
		if StampedCommit(live) == *pending.Commit {
			retirement := NewWatchRetirement(live, deps)
			defer retirement.Close()
			if err := retirement.MoveAside(); err != nil {
				return err
			}
			if err := removeIgnoringNotExist(deps, marker); err != nil {
				return err
			}
			if err := retirement.Commit(); err != nil {
				_ = os.WriteFile(marker, record, 0o644)
				return err
			}
			return nil
		}
	}
	return removeIgnoringNotExist(deps, marker)
}

// RollCommit builds, smokes, and installs tip from a clean checkout.
func RollCommit(root, live, tip string, noRequest bool, store string, deps *Deps) (int, error) {
	retirement := NewWatchRetirement(live, deps)
	defer retirement.Close()
	status, err := InstallCommit(root, live, tip, noRequest, store, retirement, deps)
	if err != nil {
		return 1, err
	}
	if status == 0 {
		if err := deps.stepped("install", retirement.Commit); err != nil {
			return 1, err
		}
	}
	return status, nil
}

// InstallCommit performs the checkout, test, build, smoke, loop check, install, and restart sequence.
func InstallCommit(root, live, tip string, noRequest bool, store string, retirement *WatchRetirement, deps *Deps) (int, error) {
	var old map[string]BuildFact
	if err := deps.stepped("store check", func() error {
		var err error
		old, err = deps.builds(store)
		return err
	}); err != nil {
		return 1, err
	}

	afterMerge := noRequest && os.Getenv("TILLR_ROLL_VERIFICATION") == "after-merge"
	baseEnv := filteredEnv(os.Environ(), "TILLR_SQLITE_TRACE", "TILLR_ROLL_VERIFICATION")
	buildEnv := append(append([]string(nil), baseEnv...), "CGO_ENABLED=0")

	if err := os.MkdirAll(filepath.Dir(live), 0o755); err != nil {
		return 1, err
	}
	marker := filepath.Join(filepath.Dir(live), WatchRetire)
	retireAfter := ""

	work, err := os.MkdirTemp(filepath.Dir(live), "tillr-roll-")
	if err != nil {
		return 1, err
	}
	defer os.RemoveAll(work)

	source := filepath.Join(work, "source")
	if err := deps.stepped("checkout", func() error {
		return deps.cleanCheckout(root, tip, source, root)
	}); err != nil {
		return 1, err
	}

	if !afterMerge {
		if err := deps.testCommit(root, source, work, tip, store, old, buildEnv); err != nil {
			return 1, err
		}
	}

	stage := filepath.Join(work, "stage")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return 1, err
	}

	var programs []string
	hasWatch := isDir(filepath.Join(source, "tools/tillr-watch"))
	for _, module := range Modules {
		if module == "watch" && !hasWatch {
			continue
		}
		name := "tillr-" + module
		if module == "cli" {
			name = "tillr"
		}
		target := filepath.Join(stage, name)
		moduleDir := filepath.Join(source, "tools", "tillr-"+module)
		buildArgs := []string{"go", "build", "-buildvcs=true"}
		if module == "view" && isFile(filepath.Join(moduleDir, "web/dist/index.html")) {
			buildArgs = append(buildArgs, "-tags", "observatory")
		}
		buildArgs = append(buildArgs, "-o", target, ".")
		if err := deps.stepped("build "+name, func() error {
			_, err := deps.run(buildArgs, RunOpts{Dir: moduleDir, Env: buildEnv})
			return err
		}); err != nil {
			return 1, err
		}
		programs = append(programs, target)
	}

	for _, program := range programs {
		prog := program
		if err := deps.stepped("smoke "+filepath.Base(prog), func() error {
			_, err := deps.run([]string{prog, "--help"}, RunOpts{DiscardStdout: true})
			return err
		}); err != nil {
			return 1, err
		}
	}

	checkArgs := []string{filepath.Join(stage, "tillr-drain"), "loop", "--check", "--root", source, "--store", store}
	for _, face := range []string{"gate", "land", "verdict"} {
		checkArgs = append(checkArgs, "--"+face, "tillr-loop "+face)
	}
	checkEnv := withEnvVar(baseEnv, "PATH", stage+string(os.PathListSeparator)+os.Getenv("PATH"))
	var checkedRes CmdResult
	if err := deps.stepped("loop check", func() error {
		var err error
		checkedRes, err = deps.run(checkArgs, RunOpts{Dir: source, Env: checkEnv})
		return err
	}); err != nil {
		return 1, err
	}
	schemaRoll := containsLine(checkedRes.Stdout, SchemaCheck)

	if err := deps.stepped("store check", func() error {
		var err error
		old, err = deps.builds(store)
		return err
	}); err != nil {
		return 1, err
	}

	if err := os.MkdirAll(live, 0o755); err != nil {
		return 1, err
	}
	if schemaRoll {
		var pending string
		if err := deps.stepped("stage schema roll", func() error {
			var err error
			pending, err = StageSchemaRoll(stage, live, tip, store)
			return err
		}); err != nil {
			return 1, err
		}
		if err := removeIgnoringNotExist(deps, marker); err != nil {
			return 1, err
		}
		if !hasWatch {
			retireAfter = pending
		}
	} else {
		for _, program := range programs {
			prog := program
			if err := deps.stepped("install", func() error {
				return os.Rename(prog, filepath.Join(live, filepath.Base(prog)))
			}); err != nil {
				return 1, err
			}
		}
		if !hasWatch {
			if err := deps.stepped("install", retirement.MoveAside); err != nil {
				return 1, err
			}
		}
		if err := removeIgnoringNotExist(deps, marker); err != nil {
			return 1, err
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(live), Rolled), []byte(tip+"\n"), 0o644); err != nil {
			return 1, err
		}
	}

	roles := []string{"drain", "view"}
	if noRequest {
		answers := map[string]string{
			"drain": "not-requested",
			"view":  "not-requested",
		}
		status, err := Report(old, old, answers, roles, live, deps.stdout())
		if err != nil {
			return 1, err
		}
		if status == 0 && retireAfter != "" {
			if err := PublishWatchRetirement(marker, tip, retireAfter); err != nil {
				return 1, err
			}
		}
		return status, nil
	}

	answers := make(map[string]string, 2)
	if schemaRoll {
		drainAnswer := deps.restart(live, root, "drain")
		viewAnswer := "not-requested"
		if drainAnswer == "restarted" {
			viewAnswer = deps.restart(live, root, "view")
		}
		answers["drain"] = drainAnswer
		answers["view"] = viewAnswer
	} else {
		var wg sync.WaitGroup
		var mu sync.Mutex
		for _, role := range roles {
			r := role
			wg.Add(1)
			go func() {
				defer wg.Done()
				ans := deps.restart(live, root, r)
				mu.Lock()
				answers[r] = ans
				mu.Unlock()
			}()
		}
		wg.Wait()
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		newFacts, err := deps.builds(store)
		if err != nil {
			return 1, err
		}
		ready := true
		for _, role := range roles {
			if answers[role] == "restarted" && newFacts[role].Seq <= old[role].Seq {
				ready = false
				break
			}
		}
		if ready || !time.Now().Before(deadline) {
			status, err := Report(old, newFacts, answers, roles, live, deps.stdout())
			if err != nil {
				return 1, err
			}
			if status == 0 && retireAfter != "" {
				if pathExists(retireAfter) {
					if err := PublishWatchRetirement(marker, tip, retireAfter); err != nil {
						return 1, err
					}
				} else if StampedCommit(live) == tip {
					if err := deps.stepped("install", retirement.MoveAside); err != nil {
						return 1, err
					}
				}
			}
			return status, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Roll rolls one commit: the named commit, or HEAD at the moment the lock is acquired.
func Roll(root, live string, noRequest, buildPi bool, commit string, deps *Deps) (int, error) {
	if buildPi {
		status, err := deps.git(root, "status", "--porcelain")
		if err != nil {
			return 1, err
		}
		if status != "" {
			return 1, errors.New("refusing a dirty checkout")
		}
		if _, err := deps.run([]string{filepath.Join(root, "bin/pi-forks"), "build-pi"}, RunOpts{Dir: root}); err != nil {
			return 1, err
		}
		return 0, nil
	}

	store := filepath.Join(root, ".tillr/state.db")
	if err := os.MkdirAll(filepath.Dir(live), 0o755); err != nil {
		return 1, err
	}
	lockPath := filepath.Join(filepath.Dir(live), "tillr-roll.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 1, err
	}
	defer lockFile.Close()

	waited := false
	err = unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		waited = true
		deps.onWait()
		err = unix.Flock(int(lockFile.Fd()), unix.LOCK_EX)
	}
	if err != nil {
		return 1, err
	}
	defer func() { _ = unix.Flock(int(lockFile.Fd()), unix.LOCK_UN) }()

	if err := SettleWatchRetirement(live, deps); err != nil {
		return 1, err
	}
	rev := commit
	if rev == "" {
		rev = "HEAD"
	}
	tip, err := deps.git(root, "rev-parse", "--verify", rev+"^{commit}")
	if err != nil {
		return 1, err
	}
	rolled := filepath.Join(filepath.Dir(live), Rolled)
	if waited && commit == "" {
		if b, err := os.ReadFile(rolled); err == nil && strings.TrimSpace(string(b)) == tip {
			short := tip
			if len(short) > 12 {
				short = short[:12]
			}
			_, _ = fmt.Fprintf(deps.stdout(), "tillr-roll: %s was rolled while this request waited\n", short)
			_ = flushWriter(deps.stdout())
			return 0, nil
		}
	}

	status, err := deps.rollCommit(root, live, tip, noRequest, store)
	if err != nil {
		var rf *RollFailedError
		if errors.As(err, &rf) {
			deps.recordFailure(store, tip, rf.Step, rf.Detail)
		}
		return 1, err
	}
	return status, nil
}

// RollPiExtensions rebuilds the drain pi extensions agent directory without restarting drain/view.
func RollPiExtensions(root string, deps *Deps) (int, error) {
	builder := filepath.Join(root, "tools/tillr-run/backends/pi/build-agent-dir")
	if _, err := deps.run([]string{builder}, RunOpts{Dir: root}); err != nil {
		return 1, err
	}
	return 0, nil
}

func defaultRestart(live, root, role string) string {
	cmd := exec.Command(filepath.Join(live, "tillr-"+role), "restart", "--root", root, "--wait")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	lines := splitLines(string(out))
	if len(lines) == 2 && strings.HasPrefix(lines[1], lines[0]+": ") {
		fields := strings.Fields(lines[1])
		if len(fields) >= 2 {
			answer := fields[1]
			if err == nil || answer == "refused" {
				return answer
			}
		}
	}
	return "error"
}

func isSharded(rel string) bool {
	for _, s := range Sharded {
		if s == rel {
			return true
		}
	}
	return false
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

func resolvePath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return abs, nil
}

func removeIgnoringNotExist(deps *Deps, path string) error {
	err := deps.remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func filteredEnv(env []string, drop ...string) []string {
	dropSet := make(map[string]struct{}, len(drop))
	for _, k := range drop {
		dropSet[k] = struct{}{}
	}
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if _, skip := dropSet[key]; !skip {
			out = append(out, entry)
		}
	}
	return out
}

func withEnvVar(env []string, key, val string) []string {
	out := filteredEnv(env, key)
	return append(out, key+"="+val)
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	raw := strings.Split(s, "\n")
	for i := range raw {
		raw[i] = strings.TrimSuffix(raw[i], "\r")
	}
	return raw
}

func containsLine(s, target string) bool {
	for _, line := range splitLines(s) {
		if line == target {
			return true
		}
	}
	return false
}

func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '@' || c == '%' || c == '+' || c == '=' || c == ':' || c == ',' ||
			c == '.' || c == '/' || c == '-') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
}
