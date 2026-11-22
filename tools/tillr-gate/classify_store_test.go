package main_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/record"
)

func storeGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func storeWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func storeWriteJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	storeWriteFile(t, path, string(raw)+"\n")
}

func checkHashHex(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

func runGateCLI(t *testing.T, dir, stdin string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(gateBin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+dir)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("exec %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

func setupDeclaredRepo(t *testing.T) (root, storeDir string) {
	t.Helper()
	root = t.TempDir()
	storeGit(t, root, "init", "-q")
	storeWriteFile(t, filepath.Join(root, "watched", "input.txt"), "v1\n")
	storeWriteJSON(t, filepath.Join(root, "tools", "gate", "checks", "fast-sample.json"), map[string]any{
		"id":        "fast-sample",
		"label":     "Check Fast Sample!",
		"command":   []string{"sh", "-c", "echo fast-sample-output && test ! -f fail-toggle"},
		"scope":     "tree",
		"tier":      "fast",
		"budget_ms": 500,
		"reads":     []string{"watched/"},
	})
	storeWriteJSON(t, filepath.Join(root, "tools", "gate", "checks", "heavy-sample.json"), map[string]any{
		"id":         "heavy-sample",
		"label":      "Heavy Suite Sample",
		"command":    []string{"sh", "-c", "echo heavy-ok"},
		"scope":      "tree",
		"tier":       "heavy",
		"timeout_ms": 5000,
		"reads":      []string{"watched/"},
	})
	storeWriteJSON(t, filepath.Join(root, "tools", "gate", "checks", "documents.json"), map[string]any{
		"id":        "documents",
		"label":     "Render changed documents",
		"command":   []string{"sh", "-c", "exit 0"},
		"scope":     "change",
		"tier":      "fast",
		"budget_ms": 500,
		"remember":  false,
	})
	storeGit(t, root, "add", ".")
	storeGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "initial declarations")
	common := storeGit(t, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	storeDir = filepath.Join(common, "tillr-gate", "results")
	return root, storeDir
}

func TestStoredRecordLogRefAndLegacyRecordCompatibility(t *testing.T) {
	root, storeDir := setupDeclaredRepo(t)
	passLogs := filepath.Join(t.TempDir(), "pass-logs")
	stdout, stderr, code := runGateCLI(t, root, "",
		"checks", "--fast-tree", "--base", "HEAD", "--head", "HEAD",
		"--worktree", root, "--logs", passLogs,
	)
	if code != 0 {
		t.Fatalf("checks --fast-tree exited %d: %s\n%s", code, stderr, stdout)
	}
	entries, err := os.ReadDir(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	var passFile string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".pass.json") {
			passFile = filepath.Join(storeDir, entry.Name())
		}
	}
	if passFile == "" {
		t.Fatalf("expected a stored .pass.json in %s", storeDir)
	}
	var stored map[string]any
	raw, err := os.ReadFile(passFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	logRef, _ := stored["log_ref"].(string)
	if logRef == "" {
		t.Fatalf("expected non-empty log_ref on stored record, got %v", stored)
	}
	logBytes, err := os.ReadFile(logRef)
	if err != nil || !strings.Contains(string(logBytes), "fast-sample-output") {
		t.Fatalf("stored log_ref %q unreadable or missing check output: %v (%q)", logRef, err, logBytes)
	}

	// Older records written without log_ref remain valid and classify with "" log location.
	fp := strings.TrimSuffix(filepath.Base(passFile), ".pass.json")
	delete(stored, "log_ref")
	storeWriteJSON(t, passFile, stored)
	obsJSON := `[{"check":"fast-sample","observed_at":"2026-11-22T12:00:00Z","source":"check-command","exit_code":1}]`
	clsOut, clsErr, clsCode := runGateCLI(t, root, obsJSON, "classify", "--repo", root)
	if clsCode != 0 {
		t.Fatalf("classify exited %d: %s", clsCode, clsErr)
	}
	var results []checkresult.Result
	if err := json.Unmarshal([]byte(clsOut), &results); err != nil || len(results) != 1 {
		t.Fatalf("unexpected classify output %q: %v", clsOut, err)
	}
	if results[0].Outcome != checkresult.Disabled || results[0].Fingerprint != fp || results[0].PassLogRef != "" || results[0].FailLogRef != "" {
		t.Fatalf("expected disabled with empty unrecorded log_refs, got %+v", results[0])
	}
}

func TestClassifyStoreContradictionWritesMarkerAndCopiesLogsBeforeCleanup(t *testing.T) {
	// Direction 1: stored pass (from `tillr-gate checks`) + fresh fail observation.
	root, storeDir := setupDeclaredRepo(t)
	passLogs := filepath.Join(t.TempDir(), "ephemeral-pass-logs")
	_, stderr, code := runGateCLI(t, root, "",
		"checks", "--fast-tree", "--base", "HEAD", "--head", "HEAD",
		"--worktree", root, "--logs", passLogs,
	)
	if code != 0 {
		t.Fatalf("checks exited %d: %s", code, stderr)
	}

	failLogs := filepath.Join(t.TempDir(), "ephemeral-fail-logs")
	freshFailLog := filepath.Join(failLogs, "fresh-fail.log")
	storeWriteFile(t, freshFailLog, "fresh failure transcript\n")

	inputBytes, err := json.Marshal([]map[string]any{{
		"check":       "fast-sample",
		"observed_at": "2026-11-22T15:00:00Z",
		"log_ref":     freshFailLog,
		"source":      "check-command",
		"exit_code":   1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runGateCLI(t, root, string(inputBytes), "classify", "--repo", root)
	if code != 0 {
		t.Fatalf("classify --repo failed (%d): %s", code, stderr)
	}

	// Simulate post-run temporary directory removal / failed cleanup: marker and copied logs survive.
	if err := os.RemoveAll(passLogs); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(failLogs); err != nil {
		t.Fatal(err)
	}

	var got []checkresult.Result
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || len(got) != 1 {
		t.Fatalf("unmarshal %q: %v", stdout, err)
	}
	res := got[0]
	if res.Outcome != checkresult.Disabled || res.CheckID != "fast-sample" || res.Episode == "" {
		t.Fatalf("expected disabled fast-sample with episode, got %+v", res)
	}
	if _, _, _, err := checkresult.ParseEpisode(res.Episode); err != nil {
		t.Fatalf("invalid returned episode %q: %v", res.Episode, err)
	}
	passCopy, err := os.ReadFile(res.PassLogRef)
	if err != nil || !strings.Contains(string(passCopy), "fast-sample-output") {
		t.Fatalf("copied pass log %q unreadable after temp cleanup: %v (%q)", res.PassLogRef, err, passCopy)
	}
	failCopy, err := os.ReadFile(res.FailLogRef)
	if err != nil || string(failCopy) != "fresh failure transcript\n" {
		t.Fatalf("copied fail log %q unreadable after temp cleanup: %v (%q)", res.FailLogRef, err, failCopy)
	}

	markerPath := filepath.Join(storeDir, "disabled", checkHashHex("fast-sample")+".json")
	markerRaw, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("expected marker at %s: %v", markerPath, err)
	}
	var marker checkresult.DisabledMarker
	if err := json.Unmarshal(markerRaw, &marker); err != nil {
		t.Fatal(err)
	}
	if marker.CheckID != "fast-sample" || marker.Episode != res.Episode || marker.PassLogRef != res.PassLogRef || marker.FailLogRef != res.FailLogRef {
		t.Fatalf("marker mismatch: %+v vs %+v", marker, res)
	}

	// Direction 2: stored fail + fresh pass observation in an isolated --store.
	altStore := filepath.Join(t.TempDir(), "custom-store")
	storedFailLog := filepath.Join(t.TempDir(), "stored-fail.log")
	freshPassLog := filepath.Join(t.TempDir(), "fresh-pass.log")
	storeWriteFile(t, storedFailLog, "stored fail log\n")
	storeWriteFile(t, freshPassLog, "fresh pass log\n")
	storeWriteJSON(t, filepath.Join(altStore, res.Fingerprint+".fail.json"), map[string]any{
		"check_id":    "fast-sample",
		"result":      "fail",
		"duration_ms": 12,
		"time":        "2026-11-22T15:01:00Z",
		"tree":        storeGit(t, root, "rev-parse", "HEAD^{tree}"),
		"log_ref":     storedFailLog,
	})
	reverseIn, err := json.Marshal([]map[string]any{{
		"check":       "fast-sample:Check Fast Sample!",
		"observed_at": "2026-11-22T15:02:00Z",
		"log_ref":     freshPassLog,
		"source":      "check-command",
		"exit_code":   0,
	}})
	if err != nil {
		t.Fatal(err)
	}
	revOut, revErr, revCode := runGateCLI(t, root, string(reverseIn), "classify", "--repo", root, "--store", altStore)
	if revCode != 0 {
		t.Fatalf("reverse classify failed (%d): %s", revCode, revErr)
	}
	var revGot []checkresult.Result
	if err := json.Unmarshal([]byte(revOut), &revGot); err != nil || len(revGot) != 1 {
		t.Fatalf("decode %q: %v", revOut, err)
	}
	if revGot[0].Outcome != checkresult.Disabled || revGot[0].CheckID != "fast-sample" ||
		revGot[0].PassAt != "2026-11-22T15:02:00Z" || revGot[0].FailAt != "2026-11-22T15:01:00Z" {
		t.Fatalf("unexpected reverse contradiction result: %+v", revGot[0])
	}
}

func TestClassifyStoreMarkerDisablesNewTreeAndProveDisabledRunsRule4(t *testing.T) {
	root, storeDir := setupDeclaredRepo(t)
	marker := checkresult.DisabledMarker{
		CheckID:     "fast-sample",
		Episode:     "fp0|2026-11-22T10:00:00Z|2026-11-22T10:01:00Z",
		Fingerprint: "fp0",
		PassAt:      "2026-11-22T10:00:00Z",
		PassLogRef:  "",
		FailAt:      "2026-11-22T10:01:00Z",
		FailLogRef:  "",
		RepairItem:  "flaky-fast-sample",
	}
	storeWriteJSON(t, filepath.Join(storeDir, "disabled", checkHashHex("fast-sample")+".json"), marker)

	// Advance the repository to a new tree so fast-sample has a different fingerprint.
	storeWriteFile(t, filepath.Join(root, "watched", "input.txt"), "v2-new-tree\n")
	storeGit(t, root, "add", ".")
	storeGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "advance watched file")

	// Pre-run query and raw pass on the new tree are both disabled under Rule 2, and aggregate passes.
	batch := `[
		{"check":"fast-sample"},
		{"check":"fast-sample/check-fast-sample","source":"check-command","exit_code":0}
	]`
	stdout, stderr, code := runGateCLI(t, root, batch, "classify", "--repo", root, "--aggregate", "runner")
	if code != 0 {
		t.Fatalf("classify --aggregate exited %d: %s", code, stderr)
	}
	var agg struct {
		Checks []checkresult.Result `json:"checks"`
		Run    struct {
			Outcome  checkresult.Outcome `json:"outcome"`
			Detail   string              `json:"detail"`
			ExitCode int                 `json:"exit_code"`
		} `json:"run"`
	}
	if err := json.Unmarshal([]byte(stdout), &agg); err != nil {
		t.Fatalf("decode %q: %v", stdout, err)
	}
	if len(agg.Checks) != 2 || agg.Checks[0].Outcome != checkresult.Disabled || agg.Checks[1].Outcome != checkresult.Disabled ||
		agg.Checks[0].RepairItem != "flaky-fast-sample" || agg.Run.Outcome != checkresult.Pass || agg.Run.ExitCode != 0 {
		t.Fatalf("expected both disabled and passing run aggregate, got %+v", agg)
	}

	// --prove-disabled skips Rules 2 and 3 and evaluates Rule 4 directly.
	proveBatch := `[
		{"check":"fast-sample","source":"check-command","exit_code":1},
		{"check":"fast-sample:Check Fast Sample!","source":"check-command","exit_code":0},
		{"check":"fast-sample"}
	]`
	pOut, pErr, pCode := runGateCLI(t, root, proveBatch, "classify", "--repo", root, "--prove-disabled", "fast-sample")
	if pCode != 0 {
		t.Fatalf("prove-disabled classify failed (%d): %s", pCode, pErr)
	}
	var proved []checkresult.Result
	if err := json.Unmarshal([]byte(pOut), &proved); err != nil || len(proved) != 3 {
		t.Fatalf("decode %q: %v", pOut, err)
	}
	if proved[0].Outcome != checkresult.Fail || proved[1].Outcome != checkresult.Pass ||
		proved[2].Outcome != checkresult.Unknown || proved[2].Detail != checkresult.DetailUnobserved {
		t.Fatalf("unexpected --prove-disabled answers: %+v", proved)
	}
}

func TestClassifySelectorResolutionCommitWorktreeAndPerUnitFingerprint(t *testing.T) {
	root, storeDir := setupDeclaredRepo(t)
	firstCommit := storeGit(t, root, "rev-parse", "HEAD")

	// Exact `<id>`, `<id>:<label>`, and `<id>/<slug(label)>` on a fast check resolve to `fast-sample`.
	// `<id>/<slug(label)>` on a heavy check and bare `<id>/anything` do NOT resolve to declarations.
	selectorBatch := `[
		{"check":"fast-sample"},
		{"check":"fast-sample:Check Fast Sample!","source":"check-command","exit_code":0},
		{"check":"fast-sample/check-fast-sample","source":"result-word","word":"pass"},
		{"check":"heavy-sample:Heavy Suite Sample","source":"result-word","word":"pass"},
		{"check":"heavy-sample/heavy-suite-sample","source":"result-word","word":"pass"},
		{"check":"fast-sample/other-suffix","source":"result-word","word":"pass"},
		{"check":"series:pi","source":"result-word","word":"pass"}
	]`
	stdout, stderr, code := runGateCLI(t, root, selectorBatch, "classify", "--repo", root)
	if code != 0 {
		t.Fatalf("selector classify exited %d: %s", code, stderr)
	}
	var got []checkresult.Result
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || len(got) != 7 {
		t.Fatalf("decode %q: %v", stdout, err)
	}
	if got[0].CheckID != "fast-sample" || got[0].Outcome != checkresult.Unknown || got[0].Detail != checkresult.DetailUnobserved {
		t.Fatalf("pre-run fast-sample: %+v", got[0])
	}
	if got[1].CheckID != "fast-sample" || got[1].Outcome != checkresult.Pass {
		t.Fatalf("<id>:<label> fast-sample: %+v", got[1])
	}
	if got[2].CheckID != "fast-sample" || got[2].Outcome != checkresult.Pass {
		t.Fatalf("<id>/<slug> fast-sample: %+v", got[2])
	}
	if got[3].CheckID != "heavy-sample" || got[3].Outcome != checkresult.Pass {
		t.Fatalf("<id>:<label> heavy-sample: %+v", got[3])
	}
	for _, idx := range []int{4, 5, 6} {
		if got[idx].CheckID != "" {
			t.Fatalf("selector index %d unexpectedly resolved to declaration %q: %+v", idx, got[idx].CheckID, got[idx])
		}
	}

	// Per-unit explicit fingerprint on `documents` (P5) disables `documents` whole-check on contradiction.
	storeWriteJSON(t, filepath.Join(storeDir, "doc-unit-key-1.pass.json"), map[string]any{
		"check_id":    "documents",
		"result":      "pass",
		"duration_ms": 5,
		"time":        "2026-11-22T11:00:00Z",
		"tree":        "doc-tree",
	})
	docBatch := `[
		{"check":"documents","fingerprint":"doc-unit-key-1","observed_at":"2026-11-22T11:02:00Z","source":"document-render","exit_code":1,"report_read":true,"has_finding":true},
		{"check":"documents","fingerprint":"doc-unit-key-2"}
	]`
	docOut, docErr, docCode := runGateCLI(t, root, docBatch, "classify", "--repo", root)
	if docCode != 0 {
		t.Fatalf("documents classify failed (%d): %s", docCode, docErr)
	}
	var docGot []checkresult.Result
	if err := json.Unmarshal([]byte(docOut), &docGot); err != nil || len(docGot) != 2 {
		t.Fatalf("decode %q: %v", docOut, err)
	}
	if docGot[0].Outcome != checkresult.Disabled || docGot[0].CheckID != "documents" ||
		docGot[1].Outcome != checkresult.Disabled || docGot[1].CheckID != "documents" {
		t.Fatalf("expected documents whole-check disabled across fingerprints, got %+v", docGot)
	}

	// Remove fast-sample in a new commit, add uncommitted worktree-only declaration, and verify commit vs worktree:true.
	if err := os.Remove(filepath.Join(root, "tools", "gate", "checks", "fast-sample.json")); err != nil {
		t.Fatal(err)
	}
	storeGit(t, root, "add", "-A")
	storeGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "drop fast-sample")
	storeWriteJSON(t, filepath.Join(root, "tools", "gate", "checks", "wt-only.json"), map[string]any{
		"id":        "wt-only",
		"label":     "Worktree Only Check",
		"command":   []string{"sh", "-c", "exit 0"},
		"scope":     "tree",
		"tier":      "fast",
		"budget_ms": 100,
		"reads":     []string{"watched/"},
	})
	cwBatch, err := json.Marshal([]map[string]any{
		{"check": "fast-sample:Check Fast Sample!", "commit": firstCommit, "source": "result-word", "word": "pass"},
		{"check": "fast-sample:Check Fast Sample!", "source": "result-word", "word": "pass"},
		{"check": "wt-only:Worktree Only Check", "source": "result-word", "word": "pass"},
		{"check": "wt-only:Worktree Only Check", "worktree": true, "source": "result-word", "word": "pass"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cwOut, cwErr, cwCode := runGateCLI(t, root, string(cwBatch), "classify", "--repo", root)
	if cwCode != 0 {
		t.Fatalf("commit/worktree classify failed (%d): %s", cwCode, cwErr)
	}
	var cwGot []checkresult.Result
	if err := json.Unmarshal([]byte(cwOut), &cwGot); err != nil || len(cwGot) != 4 {
		t.Fatalf("decode %q: %v", cwOut, err)
	}
	if cwGot[0].CheckID != "fast-sample" || cwGot[1].CheckID != "" || cwGot[2].CheckID != "" || cwGot[3].CheckID != "wt-only" {
		t.Fatalf("commit vs worktree resolution mismatch: %+v", cwGot)
	}

	// Ambiguous match: declaration id `ambig/one` collides with fast declaration `id: ambig, label: One`.
	storeWriteJSON(t, filepath.Join(root, "tools", "gate", "checks", "ambig-a.json"), map[string]any{
		"id": "ambig/one", "label": "Direct ID", "command": []string{"true"},
		"scope": "tree", "tier": "fast", "budget_ms": 100, "reads": []string{"watched/"},
	})
	storeWriteJSON(t, filepath.Join(root, "tools", "gate", "checks", "ambig-b.json"), map[string]any{
		"id": "ambig", "label": "One", "command": []string{"true"},
		"scope": "tree", "tier": "fast", "budget_ms": 100, "reads": []string{"watched/"},
	})
	ambOut, ambErr, ambCode := runGateCLI(t, root, `[{"check":"ambig/one","worktree":true}]`, "classify", "--repo", root)
	if ambCode == 0 || ambOut != "" || !strings.Contains(ambErr, "ambiguous check selector") {
		t.Fatalf("expected ambiguous selector failure with empty stdout, got code=%d stdout=%q stderr=%q", ambCode, ambOut, ambErr)
	}
}

func TestEnableArchivesEpisodeRecordsFencePreservesNewerMarkerAndIsIdempotent(t *testing.T) {
	root, storeDir := setupDeclaredRepo(t)
	logDir := t.TempDir()
	passLog := filepath.Join(logDir, "pass.log")
	failLog := filepath.Join(logDir, "fail.log")
	storeWriteFile(t, passLog, "pass-1\n")
	storeWriteFile(t, failLog, "fail-1\n")

	// Determine fast-sample's canonical fingerprint by running checks once, then seed fail record too.
	_, stderr, code := runGateCLI(t, root, "",
		"checks", "--fast-tree", "--base", "HEAD", "--head", "HEAD",
		"--worktree", root, "--logs", filepath.Join(logDir, "check-logs"),
	)
	if code != 0 {
		t.Fatalf("checks failed (%d): %s", code, stderr)
	}
	clsOut, clsErr, clsCode := runGateCLI(t, root,
		`[{"check":"fast-sample","observed_at":"2026-11-22T16:00:00Z","log_ref":"`+failLog+`","source":"check-command","exit_code":1}]`,
		"classify", "--repo", root,
	)
	if clsCode != 0 {
		t.Fatalf("classify failed (%d): %s", clsCode, clsErr)
	}
	var first []checkresult.Result
	if err := json.Unmarshal([]byte(clsOut), &first); err != nil || len(first) != 1 {
		t.Fatalf("decode %q: %v", clsOut, err)
	}
	ep1 := first[0].Episode
	fp := first[0].Fingerprint
	tree := storeGit(t, root, "rev-parse", "HEAD^{tree}")
	// Also write the matching fail record as a runner would have published before repair.
	storeWriteJSON(t, filepath.Join(storeDir, fp+".fail.json"), map[string]any{
		"check_id":    "fast-sample",
		"result":      "fail",
		"duration_ms": 10,
		"time":        "2026-11-22T16:00:00Z",
		"tree":        tree,
		"log_ref":     failLog,
	})

	// Invalid enable invocations exit ExitUsage (2) and leave the marker intact.
	for _, args := range [][]string{
		{"enable"},
		{"enable", "--check", "fast-sample"},
		{"enable", "--check", "fast-sample", "--episode", "bad-episode"},
		{"enable", "--check", "fast-sample", "--episode", fp + "|2026-11-22T11:00:00-05:00|2026-11-22T16:00:00Z"},
	} {
		_, _, badCode := runGateCLI(t, root, "", args...)
		if badCode != record.ExitUsage {
			t.Fatalf("%v exited %d, want %d", args, badCode, record.ExitUsage)
		}
	}

	// Enable episode 1: marker removed, pass/fail records and copied logs archived in resolved/, enabled/ written.
	enOut, enErr, enCode := runGateCLI(t, root, "", "enable", "--check", "fast-sample", "--episode", ep1, "--worktree", root)
	if enCode != 0 {
		t.Fatalf("enable exited %d: %s (%s)", enCode, enErr, enOut)
	}
	markerPath := filepath.Join(storeDir, "disabled", checkHashHex("fast-sample")+".json")
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("expected marker %s to be removed, stat err=%v", markerPath, err)
	}
	for _, suffix := range []string{".pass.json", ".fail.json"} {
		if _, err := os.Stat(filepath.Join(storeDir, fp+suffix)); !os.IsNotExist(err) {
			t.Fatalf("expected %s%s moved out of lookup dir", fp, suffix)
		}
	}
	ep1Sum := sha256.Sum256([]byte("fast-sample|" + ep1))
	archiveDir := filepath.Join(storeDir, "resolved", hex.EncodeToString(ep1Sum[:]))
	for _, wantName := range []string{fp + ".pass.json", fp + ".fail.json"} {
		if _, err := os.Stat(filepath.Join(archiveDir, wantName)); err != nil {
			t.Fatalf("expected archived %s in %s: %v", wantName, archiveDir, err)
		}
	}
	archivedEntries, err := os.ReadDir(archiveDir)
	if err != nil || len(archivedEntries) < 4 {
		t.Fatalf("expected both records and both copied logs in %s, got %d entries (%v)", archiveDir, len(archivedEntries), err)
	}

	// Idempotent second enable on already-enabled episode 1.
	_, enErr2, enCode2 := runGateCLI(t, root, "", "enable", "--check", "fast-sample", "--episode", ep1, "--worktree", root)
	if enCode2 != 0 {
		t.Fatalf("idempotent enable exited %d: %s", enCode2, enErr2)
	}

	// Pre-run query now answers unobserved; a post-repair fail + stale pre-repair cached pass does NOT re-disable.
	storeWriteJSON(t, filepath.Join(storeDir, fp+".fail.json"), map[string]any{
		"check_id":    "fast-sample",
		"result":      "fail",
		"duration_ms": 9,
		"time":        "2029-01-01T00:00:00Z",
		"tree":        tree,
	})
	postOut, postErr, postCode := runGateCLI(t, root, `[
		{"check":"fast-sample"},
		{"check":"fast-sample","source":"cached-record","word":"pass"}
	]`, "classify", "--repo", root)
	if postCode != 0 {
		t.Fatalf("post-enable classify failed (%d): %s", postCode, postErr)
	}
	var postGot []checkresult.Result
	if err := json.Unmarshal([]byte(postOut), &postGot); err != nil || len(postGot) != 2 {
		t.Fatalf("decode %q: %v", postOut, err)
	}
	if postGot[0].Outcome != checkresult.Unknown || postGot[0].Detail != checkresult.DetailUnobserved ||
		postGot[1].Outcome != checkresult.Pass {
		t.Fatalf("stale cached pass must not re-disable after enable: %+v", postGot)
	}

	// A genuine fresh post-repair pass contradicts the post-repair fail and creates episode 2.
	ep2Out, ep2Err, ep2Code := runGateCLI(t, root,
		`[{"check":"fast-sample","observed_at":"2029-01-01T00:05:00Z","source":"check-command","exit_code":0}]`,
		"classify", "--repo", root,
	)
	if ep2Code != 0 {
		t.Fatalf("ep2 classify failed (%d): %s", ep2Code, ep2Err)
	}
	var ep2Got []checkresult.Result
	if err := json.Unmarshal([]byte(ep2Out), &ep2Got); err != nil || len(ep2Got) != 1 {
		t.Fatalf("decode %q: %v", ep2Out, err)
	}
	if ep2Got[0].Outcome != checkresult.Disabled || ep2Got[0].Episode == ep1 {
		t.Fatalf("expected new episode 2 disabled result, got %+v", ep2Got[0])
	}

	// Late enable of episode 1 leaves episode 2's marker and post-repair fail record untouched.
	_, lateErr, lateCode := runGateCLI(t, root, "", "enable", "--check", "fast-sample", "--episode", ep1, "--worktree", root)
	if lateCode != 0 {
		t.Fatalf("late enable exited %d: %s", lateCode, lateErr)
	}
	rawMarker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("newer marker must remain at %s: %v", markerPath, err)
	}
	var active checkresult.DisabledMarker
	if err := json.Unmarshal(rawMarker, &active); err != nil || active.Episode != ep2Got[0].Episode {
		t.Fatalf("newer marker altered by late enable: %+v (%v)", active, err)
	}
	if _, err := os.Stat(filepath.Join(storeDir, fp+".fail.json")); err != nil {
		t.Fatalf("newer episode fail record should remain in lookup dir: %v", err)
	}
}

func TestPlainClassifyWithoutRepoOrStoreStaysPure(t *testing.T) {
	root, storeDir := setupDeclaredRepo(t)
	storeWriteJSON(t, filepath.Join(storeDir, "disabled", checkHashHex("fast-sample")+".json"), checkresult.DisabledMarker{
		CheckID:     "fast-sample",
		Episode:     "fp|2026-11-22T10:00:00Z|2026-11-22T10:01:00Z",
		Fingerprint: "fp",
		PassAt:      "2026-11-22T10:00:00Z",
		FailAt:      "2026-11-22T10:01:00Z",
	})
	stdout, stderr, code := runGateCLI(t, root,
		`[{"check":"fast-sample","source":"check-command","exit_code":0}]`,
		"classify",
	)
	if code != 0 {
		t.Fatalf("plain classify exited %d: %s", code, stderr)
	}
	if strings.TrimSpace(stdout) != `[{"outcome":"pass","detail":"passed"}]` {
		t.Fatalf("plain classify leaked store state: %s", stdout)
	}
}
