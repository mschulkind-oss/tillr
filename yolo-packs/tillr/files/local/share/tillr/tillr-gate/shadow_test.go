package main_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// shadowPlan is a gate.plan payload as the shadow cases write it. It is separate from
// planIn because the shadow is the one face that reads the in-process checks' tier, and
// the run cases' anonymous struct predates that field.
type shadowPlan struct {
	PlanID         string      `json:"plan_id"`
	BaseSHA        string      `json:"base_sha"`
	TreeSHA        string      `json:"tree_sha"`
	RegistryDigest string      `json:"registry_digest"`
	Checks         []planCheck `json:"checks"`
	InProcess      []struct {
		CheckID string `json:"check_id"`
		Kind    string `json:"kind"`
		Tier    string `json:"tier"`
	} `json:"in_process"`
}

// shadowOut is the gate.shadow record the face emits, decoded.
type shadowOut struct {
	Kind    string `json:"kind"`
	Payload struct {
		ShadowID       string `json:"shadow_id"`
		PlanID         string `json:"plan_id"`
		RegistryDigest string `json:"registry_digest"`
		BaseSHA        string `json:"base_sha"`
		TreeSHA        string `json:"tree_sha"`
		Tier           string `json:"tier"`
		Result         string `json:"result"`
		Landing        struct {
			Checks    int   `json:"checks"`
			InProcess int   `json:"in_process"`
			Ran       int   `json:"ran"`
			WallMS    int64 `json:"wall_ms"`
		} `json:"landing"`
		Rolling struct {
			Checks    int   `json:"checks"`
			InProcess int   `json:"in_process"`
			Ran       int   `json:"ran"`
			WallMS    int64 `json:"wall_ms"`
		} `json:"rolling"`
		Checks []struct {
			CheckID    string `json:"check_id"`
			Kind       string `json:"kind"`
			Tier       string `json:"tier"`
			Ran        bool   `json:"ran"`
			Result     string `json:"result"`
			DurationMS int64  `json:"duration_ms"`
			LogRef     string `json:"log_ref"`
		} `json:"checks"`
		InProcess []struct {
			CheckID string `json:"check_id"`
			Kind    string `json:"kind"`
			Tier    string `json:"tier"`
		} `json:"in_process"`
	} `json:"payload"`
}

// shadowPlanRecord writes a gate.plan record for one shadow case and returns its path.
func shadowPlanRecord(t *testing.T, plan shadowPlan) string {
	t.Helper()
	return write(t, "shadow-plan.json", envelope(t, "gate.plan", plan.PlanID, plan))
}

// runShadow drives the shadow face and returns its exit code and the record it wrote.
func runShadow(t *testing.T, planPath, worktree string, extra ...string) (int, []byte) {
	t.Helper()
	out := filepath.Join(scratch(t, "shadow"), "shadow.json")
	args := append([]string{"shadow", "--input", planPath, "--worktree", worktree, "--output", out}, extra...)
	code, stdout := run(t, args...)
	raw, err := os.ReadFile(out)
	if err != nil {
		// A face this face refuses writes no record; the caller's case says whether
		// that is the expected outcome.
		return code, stdout
	}
	return code, raw
}

func decodeShadow(t *testing.T, raw []byte) shadowOut {
	t.Helper()
	var got shadowOut
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the face did not emit a shadow record: %v\n%s", err, raw)
	}
	return got
}

// gitRepo is a scratch checkout with one commit, so a case can prove a ref did not move.
func gitRepo(t *testing.T) string {
	t.Helper()
	root := scratch(t, "repo")
	git(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("writing the tracked file failed: %v", err)
	}
	git(t, root, "add", "tracked.txt")
	git(t, root, "-c", "user.name=shadow-test", "-c", "user.email=shadow@example.com",
		"-c", "commit.gpgsign=false", "commit", "-qm", "init")
	return root
}

func clearGitRepoEnv(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE",
			"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
			"GIT_IMPLICIT_WORK_TREE", "GIT_NAMESPACE", "GIT_GRAFT_FILE",
			"GIT_SHALLOW_FILE", "GIT_REPLACE_REF_BASE", "GIT_NO_REPLACE_OBJECTS",
			"GIT_PREFIX", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT":
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

func requireFixtureCommitRepo(t testing.TB, dir string, args []string) {
	t.Helper()
	commit := false
	for _, arg := range args {
		if arg == "commit" || arg == "commit-tree" {
			commit = true
			break
		}
	}
	if !commit {
		return
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("resolving fixture git dir %q: %v", dir, err)
	}
	cmd := exec.Command("git", "-C", absDir, "rev-parse", "--git-dir")
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range clearGitRepoEnv(os.Environ()) {
		if strings.HasPrefix(entry, "GIT_CEILING_DIRECTORIES=") {
			continue
		}
		env = append(env, entry)
	}
	cmd.Env = append(env, "GIT_CEILING_DIRECTORIES="+filepath.Dir(absDir))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("refusing git %v in %s: directory is not its own repository (%v: %s)", args, dir, err, strings.TrimSpace(string(out)))
	}
}

// git runs one git command and answers its trimmed output.
func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	requireFixtureCommitRepo(t, root, args)
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = clearGitRepoEnv(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// A candidate can be run through the landing tier without anything landing. The record
// names every planned check's tier, runs the landing tier's checks, defers the rolling
// tier's, and reports the wall the landing tier would have cost. Nothing in the checkout
// moves: no ref is created or advanced and the worktree stays clean.
//
// Negative controls: the rolling check writes a marker and exits nonzero, so a face that
// ran every tier fails on the marker and on the result; the landing checks write their own
// marker, so a face that ran nothing fails on its absence.
func TestShadowMeasuresTheLandingTierAndMovesNoRef(t *testing.T) {
	repo := gitRepo(t)
	markers := scratch(t, "markers")
	landingMarker := filepath.Join(markers, "landing")
	rollingMarker := filepath.Join(markers, "rolling")
	plan := shadowPlanRecord(t, shadowPlan{
		PlanID: "p-shadow", BaseSHA: "base-sha", TreeSHA: "tree-sha", RegistryDigest: "d-1",
		Checks: []planCheck{
			{CheckID: "landing-writes", Command: []string{"sh", "-c", `echo landing >> "$1"`, "sh", landingMarker}, TimeoutMS: 5000, Tier: "landing"},
			{CheckID: "rolling-writes", Command: []string{"sh", "-c", `echo rolling >> "$1"; exit 1`, "sh", rollingMarker}, TimeoutMS: 5000, Tier: "rolling"},
			{CheckID: "landing-passes", Command: []string{"sh", "-c", "exit 0"}, TimeoutMS: 5000, Tier: "landing"},
		},
		InProcess: []struct {
			CheckID string `json:"check_id"`
			Kind    string `json:"kind"`
			Tier    string `json:"tier"`
		}{{CheckID: "file-size", Kind: "file-size", Tier: "landing"}},
	})
	head := git(t, repo, "rev-parse", "HEAD")
	refs := git(t, repo, "for-each-ref", "--format=%(refname) %(objectname)")
	status := git(t, repo, "status", "--porcelain")

	code, raw := runShadow(t, plan, repo)
	if code != 0 {
		t.Fatalf("the shadow exited %d, want 0: %s", code, raw)
	}
	got := decodeShadow(t, raw)
	if got.Kind != "gate.shadow" {
		t.Fatalf("the shadow emitted kind %q", got.Kind)
	}
	if got.Payload.PlanID != "p-shadow" || got.Payload.TreeSHA != "tree-sha" ||
		got.Payload.BaseSHA != "base-sha" || got.Payload.RegistryDigest != "d-1" {
		t.Fatalf("the shadow binds %+v", got.Payload)
	}
	if got.Payload.ShadowID == "" {
		t.Fatal("the shadow carries no shadow_id")
	}
	if got.Payload.Tier != "landing" {
		t.Fatalf("the shadow measured the %q tier", got.Payload.Tier)
	}
	if len(got.Payload.Checks) != 3 {
		t.Fatalf("the record names %d checks, want 3: %+v", len(got.Payload.Checks), got.Payload.Checks)
	}
	byID := map[string]int{}
	for index, check := range got.Payload.Checks {
		byID[check.CheckID] = index
	}
	for _, id := range []string{"landing-writes", "rolling-writes", "landing-passes"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("the record does not name the check %q: %+v", id, got.Payload.Checks)
		}
	}
	first := got.Payload.Checks[byID["landing-writes"]]
	second := got.Payload.Checks[byID["landing-passes"]]
	deferred := got.Payload.Checks[byID["rolling-writes"]]
	if first.Tier != "landing" || !first.Ran || first.Result != "pass" || first.LogRef == "" {
		t.Fatalf("the landing check is recorded as %+v", first)
	}
	if second.Tier != "landing" || !second.Ran || second.Result != "pass" {
		t.Fatalf("the second landing check is recorded as %+v", second)
	}
	if deferred.Tier != "rolling" || deferred.Ran || deferred.Result != "" || deferred.LogRef != "" {
		t.Fatalf("the rolling check is recorded as %+v", deferred)
	}
	// The wall a tier would have cost is the sum of the checks it ran, because the face
	// runs them serially in the plan's order -- a structural relation, never a threshold
	// a loaded machine could move.
	if got.Payload.Landing.Ran != 2 || got.Payload.Landing.Checks != 2 {
		t.Fatalf("the landing tally is %+v", got.Payload.Landing)
	}
	if want := first.DurationMS + second.DurationMS; got.Payload.Landing.WallMS != want {
		t.Fatalf("the landing wall is %d, want the measured %d", got.Payload.Landing.WallMS, want)
	}
	if got.Payload.Rolling.Checks != 1 || got.Payload.Rolling.Ran != 0 || got.Payload.Rolling.WallMS != 0 {
		t.Fatalf("the rolling tally is %+v", got.Payload.Rolling)
	}
	// The in-process check is the gate's own code: named with its tier, never claimed as
	// measured, and counted in the landing tally so the wall is read as a floor.
	if got.Payload.Landing.InProcess != 1 {
		t.Fatalf("the landing in-process count is %d, want 1", got.Payload.Landing.InProcess)
	}
	if len(got.Payload.InProcess) != 1 || got.Payload.InProcess[0].Tier != "landing" {
		t.Fatalf("the in-process checks are recorded as %+v", got.Payload.InProcess)
	}
	if got.Payload.Result != "pass" {
		t.Fatalf("the landing tier's result is %q, want pass", got.Payload.Result)
	}
	if body, err := os.ReadFile(landingMarker); err != nil || !strings.Contains(string(body), "landing") {
		t.Fatalf("the landing check did not run: %q, %v", body, err)
	}
	if _, err := os.Stat(rollingMarker); !os.IsNotExist(err) {
		t.Fatalf("the rolling check ran in a shadow of the landing tier: %v", err)
	}
	if now := git(t, repo, "rev-parse", "HEAD"); now != head {
		t.Fatalf("the shadow moved HEAD from %s to %s", head, now)
	}
	if now := git(t, repo, "for-each-ref", "--format=%(refname) %(objectname)"); now != refs {
		t.Fatalf("the shadow changed the refs:\n%s\n%s", refs, now)
	}
	if now := git(t, repo, "status", "--porcelain"); now != status {
		t.Fatalf("the shadow changed the worktree:\n%s\n%s", status, now)
	}
}

// Two shadow records over one candidate are two measurements of one plan. Everything that
// is a fact about the candidate -- the binding, the identity, each check's tier, and which
// checks the landing tier owns -- is byte-identical between them, so a reader diffs the two
// mechanically: the only fields that move are the measured ones. That is what lets the flip
// that §10 sequences second be decided from a measurement instead of from the design's own
// estimate.
func TestTwoShadowRecordsOverOneCandidateDiffMechanically(t *testing.T) {
	repo := gitRepo(t)
	plan := shadowPlanRecord(t, shadowPlan{
		PlanID: "p-twice", BaseSHA: "base-sha", TreeSHA: "tree-sha", RegistryDigest: "d-1",
		Checks: []planCheck{
			{CheckID: "landing", Command: []string{"sh", "-c", "exit 0"}, TimeoutMS: 5000, Tier: "landing"},
			{CheckID: "rolling", Command: []string{"sh", "-c", "exit 0"}, TimeoutMS: 5000, Tier: "rolling"},
		},
	})
	firstCode, firstRaw := runShadow(t, plan, repo)
	secondCode, secondRaw := runShadow(t, plan, repo)
	if firstCode != 0 || secondCode != 0 {
		t.Fatalf("the shadows exited %d then %d", firstCode, secondCode)
	}
	first := decodeShadow(t, firstRaw)
	second := decodeShadow(t, secondRaw)
	// The identity is a function of the plan and the tree, so two runs of one plan are
	// one identity: a shadow id that counted runs would make every diff a new record.
	if first.Payload.ShadowID != second.Payload.ShadowID {
		t.Fatalf("two runs of one plan produced shadow ids %q and %q",
			first.Payload.ShadowID, second.Payload.ShadowID)
	}
	if first.Payload.Landing.Ran != 1 || first.Payload.Landing.Checks != 1 ||
		first.Payload.Rolling.Checks != 1 || second.Payload.Landing.Ran != 1 {
		t.Fatalf("the two records do not agree on the tier split: %+v / %+v",
			first.Payload, second.Payload)
	}
	if !bytes.Equal(withoutMeasurements(t, firstRaw), withoutMeasurements(t, secondRaw)) {
		t.Fatalf("two shadow records over one candidate differ in more than the measurement:\n%s\n%s",
			withoutMeasurements(t, firstRaw), withoutMeasurements(t, secondRaw))
	}
}

// The encoder omits a zero duration_ms, so a sub-millisecond check has no key
// while a slower run (including one delayed by CPU load) has a key. Both are
// measurements, not different candidate facts. Keep the other fields strict.
func TestWithoutMeasurementsIgnoresOmittedAndPresentMeasurements(t *testing.T) {
	baseline := []byte(`{"payload":{"checks":[{"check_id":"landing","tier":"landing","ran":true,"result":"pass"}],"landing":{"wall_ms":0}}}`)
	want := withoutMeasurements(t, baseline)
	for _, raw := range []string{
		`{"payload":{"checks":[{"check_id":"landing","tier":"landing","ran":true,"result":"pass","duration_ms":0,"log_ref":""}],"landing":{"wall_ms":0}}}`,
		`{"payload":{"checks":[{"check_id":"landing","tier":"landing","ran":true,"result":"pass","duration_ms":17,"log_ref":"run/check.log"}],"landing":{"wall_ms":17}}}`,
	} {
		if got := withoutMeasurements(t, []byte(raw)); !bytes.Equal(got, want) {
			t.Fatalf("measurement presence changed the candidate facts:\n%s\n%s", want, got)
		}
	}
	for _, change := range []struct{ old, new string }{
		{`"check_id":"landing"`, `"check_id":"different"`},
		{`"tier":"landing"`, `"tier":"rolling"`},
		{`"ran":true`, `"ran":false`},
		{`"result":"pass"`, `"result":"fail"`},
	} {
		raw := strings.Replace(string(baseline), change.old, change.new, 1)
		if bytes.Equal(withoutMeasurements(t, []byte(raw)), want) {
			t.Errorf("normalization hid a candidate fact change: %s -> %s", change.old, change.new)
		}
	}
}

// withoutMeasurements is the mechanical diff this face promises: every field a run
// measures -- a check's duration and log, and the wall its tier cost -- is removed,
// whether present or omitted by the encoder. What remains is the record's account
// of the candidate. Two records that agree here agree about what the landing tier
// would have run; only the measurements differ.
func withoutMeasurements(t *testing.T, raw []byte) []byte {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("decoding a shadow record failed: %v", err)
	}
	normalizeMeasurements(record)
	normalized, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("encoding the normalized shadow record failed: %v", err)
	}
	return normalized
}

func normalizeMeasurements(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			switch key {
			case "duration_ms", "wall_ms", "log_ref":
				delete(typed, key)
			default:
				normalizeMeasurements(item)
			}
		}
	case []any:
		for _, item := range typed {
			normalizeMeasurements(item)
		}
	}
}

// A check that cannot start is a hole in the measurement, not a red candidate: the record
// says `error`, the tier's result says `error`, and the face still answers 0 because it
// wrote the record. A shadow that read its exit as a verdict would be the one thing this
// face exists to avoid.
//
// Negative control: the pass case above is the same face with a command that starts, so a
// face that answered `error` for every plan fails there.
func TestShadowRecordsACheckThatCannotStartAndStillAnswersZero(t *testing.T) {
	repo := gitRepo(t)
	plan := shadowPlanRecord(t, shadowPlan{
		PlanID: "p-hole", BaseSHA: "base-sha", TreeSHA: "tree-sha", RegistryDigest: "d-1",
		Checks: []planCheck{
			{CheckID: "absent", Command: []string{"/nonexistent/checker"}, TimeoutMS: 5000, Tier: "landing"},
		},
	})
	code, raw := runShadow(t, plan, repo)
	if code != 0 {
		t.Fatalf("a shadow whose check could not start exited %d, want 0: %s", code, raw)
	}
	got := decodeShadow(t, raw)
	if len(got.Payload.Checks) != 1 || got.Payload.Checks[0].Result != "error" {
		t.Fatalf("the record is %+v, want one check recorded as error", got.Payload.Checks)
	}
	if got.Payload.Result != "error" {
		t.Fatalf("the landing tier's result is %q, want error", got.Payload.Result)
	}
	if got.Payload.Landing.Ran != 1 {
		t.Fatalf("the landing tally is %+v", got.Payload.Landing)
	}
}

// The face refuses a plan that names no tree, exactly as the run face does: a shadow bound
// to nothing is a number nobody can tie back to the tree it measured, and a record of such
// a run would be worse than no record.
//
// Negative control: the shaded cases above carry a tree, so a face that refused every plan
// fails there.
func TestShadowRefusesAPlanThatNamesNoTree(t *testing.T) {
	plan := shadowPlanRecord(t, shadowPlan{
		PlanID: "p-unbound", BaseSHA: "base-sha", RegistryDigest: "d-1",
		Checks: []planCheck{{CheckID: "landing", Command: []string{"sh", "-c", "exit 0"}, TimeoutMS: 5000, Tier: "landing"}},
	})
	code, _ := runShadow(t, plan, tree(t))
	if code != 2 {
		t.Fatalf("a plan naming no tree exited %d, want 2", code)
	}
}
