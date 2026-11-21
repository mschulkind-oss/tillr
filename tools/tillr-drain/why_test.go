package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWhyUsesPlannerAndWritesNothing(t *testing.T) {
	path := fixture(t, itemRecordRanked("ready", "in-queue", "a"), itemRecordRanked("blocked", "in-queue", "b", "ready"), itemRecord("landed", "landed"), itemRecord("closed", "in-queue"), itemRecord("resolved", "resolved-elsewhere"))
	// An item is closed through the lifecycle face, not by applying a record that says so:
	// the store has refused that since closing gained a required reason and author.
	closeFixtureItem(t, path, "closed")
	ref := policy(t, "true")
	// The store's content, not its file: a commit stays in the write-ahead log, so the
	// database file's bytes do not move when a face writes (store.Fingerprint).
	before, err := store.Fingerprint(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	code, out := invoke(t, "why", "--store", path, "--policy", ref, "--lanes", "lane-1", "--now", "2026-11-16T00:00:00Z", "--json")
	if code != 0 {
		t.Fatalf("why: %d %s", code, out)
	}
	var answer drain.WhyAnswer
	if err := json.Unmarshal(out, &answer); err != nil {
		t.Fatal(err)
	}
	if len(answer.Rows) != 4 || answer.Rows[0].ItemID != "ready" || answer.Rows[1].Reason != drain.ReasonBlocked || answer.Rows[1].Mover != "drain; ready" {
		t.Fatalf("rows: %+v", answer.Rows)
	}
	total := answer.Summary.Running + answer.Summary.CouldRun
	for _, n := range answer.Summary.Held {
		total += n
	}
	if total != len(answer.Rows) || answer.Summary.LanesOffered != 1 || answer.Summary.LanesBusy != 0 {
		t.Fatalf("summary: %+v", answer.Summary)
	}
	s, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		p, err := readPolicyForWhyTest(ref)
		if err != nil {
			return err
		}
		lanes := []string{"lane-1"}
		envs, err := drain.Plan(r, drain.Pass{Now: "2026-11-16T00:00:00Z", AvailableLanes: &lanes}, p, nil)
		if err != nil {
			return err
		}
		for i := range answer.Rows {
			var d drain.Decision
			index := i
			if i == 3 {
				index = 4
			} // landed sorts before resolved and is omitted.
			if err := json.Unmarshal(envs[index].Payload, &d); err != nil {
				return err
			}
			d.Inputs = nil
			reported := answer.Rows[i].Decision
			reported.Inputs = nil
			if !reflect.DeepEqual(d, reported) {
				t.Errorf("why diverged from Plan: %+v != %+v", reported, d)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Change the planner's hold input, not an explanation rule: no policy now holds readiness.
	code, out = invoke(t, "why", "--store", path, "--lanes", "lane-1", "--json")
	if code != 0 {
		t.Fatalf("why without policy: %d %s", code, out)
	}
	if err := json.Unmarshal(out, &answer); err != nil {
		t.Fatal(err)
	}
	if answer.Rows[0].Reason != drain.ReasonNoPolicy {
		t.Fatalf("planner hold did not reach why: %s", out)
	}
	code, out = invoke(t, "why", "--store", path, "--policy", ref, "--lanes", "lane-1", "blocked")
	if code != 0 || !strings.Contains(string(out), "\"decision\"") || strings.Contains(string(out), "\"item_id\": \"ready\"") {
		t.Fatalf("detail: %d %s", code, out)
	}
	after, err := store.Fingerprint(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("why wrote the store")
	}
}

func readPolicyForWhyTest(path string) (*drain.Policy, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p drain.Policy
	err = json.Unmarshal(raw, &p)
	return &p, err
}

func TestWhyNamesQuestionsAndCapacity(t *testing.T) {
	root := fixtureRepo(t, "docs/design/open.md")
	path := fixture(t, itemRecordWithDoc("question", "in-queue", "docs/design/open.md"), itemRecordRanked("first", "in-queue", "a"), itemRecordRanked("second", "in-queue", "b"))
	index := indexStub(t, map[string]any{"path": "docs/design/open.md", "questions": []map[string]any{{"id": "OQ-1", "state": "open"}}})
	code, out := invoke(t, "why", "--store", path, "--root", root, "--questions-cmd", index, "--policy", policy(t, "true"), "--lanes", "lane-1", "--json")
	if code != 0 {
		t.Fatalf("why: %d %s", code, out)
	}
	var answer drain.WhyAnswer
	if err := json.Unmarshal(out, &answer); err != nil {
		t.Fatal(err)
	}
	if answer.Rows[1].Reason != drain.ReasonNoLane || answer.Rows[1].Mover != "drain; lane-1" || answer.Rows[2].Reason != drain.ReasonQuestionOpen || answer.Rows[2].Mover != "owner; answer docs/design/open.md#OQ-1" {
		t.Fatalf("references: %+v", answer.Rows)
	}
	code, out = invoke(t, "why", "missing", "--store", path)
	if code != 2 || !strings.Contains(string(out), `item "missing" is not in the drain's report`) {
		t.Fatalf("missing item: %d %s", code, out)
	}
}

func TestWhyEmptyStoreAndMissingStore(t *testing.T) {
	path := fixture(t)
	code, out := invoke(t, "why", "--store", path)
	if code != 0 || strings.TrimSpace(string(out)) != "definition: "+definition.GitLandingName+"\nlanes (store): none\nsummary: lanes 0/0 busy; reserved 0; workers 0; examining 0; running 0; could run now 0; owner-waiting 0; next [drain=0, design seat=0, operator=0]; held []\nmedian cost, last 20 landed build items: unknown" {
		t.Fatalf("empty: %d %s", code, out)
	}
	path = filepath.Join(scratch(t, "absent-why"), "absent.db")
	code, _ = invoke(t, "why", "--store", path)
	if code == 0 {
		t.Fatal("missing store succeeded")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("missing store was created: %v", err)
	}
}

func closeFixtureItem(t *testing.T, path, id string) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.Close(ctx, tx, id, "not wanted", "owner", time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC))
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// The why answer names the build that planned it. `tillr-view` runs this face as a separate
// program, and a planner of another vintage than the server reads the same store and
// answers with different rules: the server can only refuse that mismatch if the answer
// says which build it came from (tools/tillr-view/siblings.go).
func TestWhyJSONNamesThePlannersBuild(t *testing.T) {
	path := fixture(t, itemRecordRanked("ready", "in-queue", "a"))
	code, out := invoke(t, "why", "--store", path, "--json")
	if code != 0 {
		t.Fatalf("why: %d %s", code, out)
	}
	var answer struct {
		Rows  []drain.WhyRow    `json:"rows"`
		Build *drain.BuildStamp `json:"build"`
	}
	if err := json.Unmarshal(out, &answer); err != nil {
		t.Fatal(err)
	}
	if len(answer.Rows) != 1 {
		t.Fatalf("rows: %s", out)
	}
	if answer.Build == nil || answer.Build.Executable != bin || answer.Build.Digest == "" {
		t.Fatalf("the why answer must name its own build (executable %s): %s", bin, out)
	}
}

// `--questions-index` answers exactly what `--questions-cmd` would, from a document the caller
// already holds: the observatory walks the tree once and hands that walk to every planner it
// asks, so a planner must read it the same way it reads its own command's output — from a
// file or from stdin — and refuse being given both.
func TestWhyReadsAPreloadedQuestionsIndex(t *testing.T) {
	root := fixtureRepo(t, "docs/design/open.md")
	path := fixture(t, itemRecordWithDoc("question", "in-queue", "docs/design/open.md"), itemRecordRanked("first", "in-queue", "a"), itemRecordRanked("second", "in-queue", "b"))
	command := indexStub(t, map[string]any{"path": "docs/design/open.md", "questions": []map[string]any{{"id": "OQ-1", "state": "open"}}})
	file := strings.TrimPrefix(command, "cat ")
	common := []string{"--store", path, "--root", root, "--policy", policy(t, "true"), "--lanes", "lane-1", "--json"}
	_, viaCommand := invoke(t, append([]string{"why", "--questions-cmd", command}, common...)...)
	code, viaFile := invoke(t, append([]string{"why", "--questions-index", file}, common...)...)
	if code != 0 {
		t.Fatalf("why --questions-index: %d %s", code, viaFile)
	}
	rows := func(raw []byte) []drain.WhyRow {
		var answer drain.WhyAnswer
		if err := json.Unmarshal(raw, &answer); err != nil {
			t.Fatalf("%v: %s", err, raw)
		}
		return answer.Rows
	}
	if got, want := rows(viaFile), rows(viaCommand); len(got) != len(want) || got[2].Reason != drain.ReasonQuestionOpen || got[2].Mover != "owner; answer docs/design/open.md#OQ-1" {
		t.Fatalf("preloaded index rows %+v, command rows %+v", got, want)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, append([]string{"why", "--questions-index", "-"}, common...)...)
	cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "state"))
	cmd.Stdin = bytes.NewReader(raw)
	viaStdin, err := cmd.Output()
	if err != nil {
		t.Fatalf("why --questions-index -: %v %s", err, viaStdin)
	}
	if got := rows(viaStdin); got[2].Mover != "owner; answer docs/design/open.md#OQ-1" {
		t.Fatalf("stdin index rows %+v", got)
	}
	code, out := invoke(t, append([]string{"why", "--questions-index", file, "--questions-cmd", command}, common...)...)
	if code != 2 || !strings.Contains(string(out), "not both") {
		t.Fatalf("both flags: %d %s", code, out)
	}
	// A document that is not an index is the same `questions-unknown` hold a failing command
	// produces, never "no questions".
	bad := filepath.Join(scratch(t, "bad-index"), "index.json")
	if err := os.WriteFile(bad, []byte(`{"index":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out = invoke(t, append([]string{"why", "--questions-index", bad}, common...)...)
	if code != 0 {
		t.Fatalf("bad index: %d %s", code, out)
	}
	if got := rows(out); got[2].Reason == drain.ReasonQuestionOpen || got[2].Reason == "" {
		t.Fatalf("an unreadable index answered %+v", got[2])
	}
}

func TestWhyDefaultsToLiveLoopLanesAndReportsLaneSet(t *testing.T) {
	path := fixture(t,
		itemRecordRanked("first", "in-queue", "a"),
		itemRecordRanked("second", "in-queue", "b"),
		itemRecordRanked("third", "in-queue", "c"),
	)
	ref := policy(t, "true")

	// Seed the store with 4 historical lanes and a live loop session that only offers
	// lane-1 and express-1.
	ctx := context.Background()
	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal(err)
	}
	ns, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		t.Fatal(err)
	}
	statRaw, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		t.Fatal(err)
	}
	rp := strings.LastIndexByte(string(statRaw), ')')
	fields := strings.Fields(string(statRaw)[rp+1:])
	owner := drain.LoopOwner{
		PID:    os.Getpid(),
		Start:  fields[19],
		Domain: strings.TrimSpace(string(boot)) + ":" + ns,
		UID:    uint32(os.Geteuid()),
	}
	ownerRaw, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	lanesRaw, err := json.Marshal([]string{"lane-1", "express-1"})
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Minute).UnixMilli()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		for _, id := range []string{"lane-1", "lane-2", "lane-3", "express-1"} {
			if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state) VALUES (?, 'idle') ON CONFLICT(lane_id) DO NOTHING`, id); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at, loop_owner, lanes) VALUES ('live-1', 1, ?, ?, ?, ?)`,
			time.Now().UnixMilli(), future, string(ownerRaw), string(lanesRaw))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// Without --lanes, why defaults to the live loop's lane set (lane-1, express-1).
	// Ordinary items only take ordinary lanes, so second and third are no-lane behind lane-1.
	code, out := invoke(t, "why", "--store", path, "--policy", ref, "--json")
	if code != 0 {
		t.Fatalf("why json: %d %s", code, out)
	}
	var answer drain.WhyAnswer
	if err := json.Unmarshal(out, &answer); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(answer.Lanes, []string{"lane-1", "express-1"}) || answer.LanesSource != "live loop" {
		t.Fatalf("lanes=%v source=%q, want [lane-1 express-1] live loop", answer.Lanes, answer.LanesSource)
	}
	if answer.Summary.LanesOffered != 2 || answer.Rows[0].Reason != drain.ReasonReady || answer.Rows[1].Reason != drain.ReasonNoLane {
		t.Fatalf("unexpected plan with live loop lanes: summary=%+v rows=%+v", answer.Summary, answer.Rows)
	}

	// Text output also states which lane set was used.
	code, out = invoke(t, "why", "--store", path, "--policy", ref)
	if code != 0 {
		t.Fatalf("why text: %d %s", code, out)
	}
	if !strings.Contains(string(out), "lanes (live loop): lane-1, express-1\n") {
		t.Fatalf("text output did not name live loop lane set:\n%s", out)
	}

	// Explicit --lanes overrides the live loop's lane set and says so.
	code, out = invoke(t, "why", "--store", path, "--policy", ref, "--lanes", "lane-1,lane-2")
	if code != 0 {
		t.Fatalf("why --lanes: %d %s", code, out)
	}
	if !strings.Contains(string(out), "lanes (flag): lane-1, lane-2\n") {
		t.Fatalf("text output did not name flag lane set:\n%s", out)
	}

	// Once the loop session ends, why falls back to all store lanes.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE drain_sessions SET ended_at = ? WHERE session_id = 'live-1'`, time.Now().UnixMilli())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	code, out = invoke(t, "why", "--store", path, "--policy", ref, "--json")
	if code != 0 {
		t.Fatalf("why after end: %d %s", code, out)
	}
	if err := json.Unmarshal(out, &answer); err != nil {
		t.Fatal(err)
	}
	if answer.LanesSource != "store" || answer.Summary.LanesOffered != 4 {
		t.Fatalf("after loop ended: source=%q offered=%d, want store / 4", answer.LanesSource, answer.Summary.LanesOffered)
	}
}

func TestWhyReusesFreshSavedQuestionsIndex(t *testing.T) {
	root := fixtureRepo(t, "docs/design/open.md")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.com")
	git("add", ".")
	git("commit", "-m", "initial")
	sha1 := git("rev-parse", "main")

	path := fixture(t, itemRecordWithDoc("question", "in-queue", "docs/design/open.md"))
	ref := policy(t, "true")

	// Save a valid question index for sha1, as the loop does.
	rawIndex := []byte(`{"index":{"documents":[{"path":"docs/design/open.md","questions":[{"id":"OQ-1","state":"open"}]}]}}`)
	if err := drain.SaveQuestionsIndex(root, sha1, nil, rawIndex); err != nil {
		t.Fatalf("SaveQuestionsIndex: %v", err)
	}

	// Without --questions-cmd or --questions-index, why reuses the saved index when main == sha1.
	code, out := invoke(t, "why", "--store", path, "--root", root, "--policy", ref, "--lanes", "lane-1", "--json")
	if code != 0 {
		t.Fatalf("why with fresh saved index: %d %s", code, out)
	}
	var answer drain.WhyAnswer
	if err := json.Unmarshal(out, &answer); err != nil {
		t.Fatal(err)
	}
	if len(answer.Rows) != 1 || answer.Rows[0].Reason != drain.ReasonQuestionOpen || answer.Rows[0].Mover != "owner; answer docs/design/open.md#OQ-1" {
		t.Fatalf("did not reuse fresh saved index: %+v", answer.Rows)
	}

	// Explicit --questions-cmd overrides the saved index.
	emptyCmd := indexStub(t)
	code, out = invoke(t, "why", "--store", path, "--root", root, "--policy", ref, "--lanes", "lane-1", "--questions-cmd", emptyCmd, "--json")
	if code != 0 {
		t.Fatalf("why with explicit --questions-cmd: %d %s", code, out)
	}
	if err := json.Unmarshal(out, &answer); err != nil {
		t.Fatal(err)
	}
	if answer.Rows[0].Reason != drain.ReasonReady {
		t.Fatalf("explicit --questions-cmd did not override saved index: %+v", answer.Rows)
	}

	// Advancing main makes the sha1 saved index stale, so LoadSavedQuestionsIndex rejects it.
	if err := os.WriteFile(filepath.Join(root, "docs/design/open.md"), []byte("# updated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "advance trunk")
	sha2 := git("rev-parse", "main")
	if _, ok := drain.LoadSavedQuestionsIndex(root, sha2, nil, false); ok {
		t.Fatal("stale saved index was accepted for advanced trunk")
	}
}

func TestWhyCLIPrintsRunAndRetryCommandOnRunLinkMissing(t *testing.T) {
	runEnv := envelope("run.record", "run-rec-1", map[string]any{
		"run_id":        "run-item_041-a1-n1",
		"item_id":       "item_041",
		"lane_id":       "lane-1",
		"backend":       "fixture",
		"state":         "exited",
		"exit_code":     0,
		"end_cause":     "normal-exit",
		"started_at":    "2026-11-16T00:00:00Z",
		"finished_at":   "2026-11-16T00:01:00Z",
		"artifact_refs": []any{},
	})
	path := fixture(t, itemRecord("item_041", "in-queue"), runEnv)
	ref := policy(t, "true")
	wantCmd := "tillr drain retry --item item_041 --run run-item_041-a1-n1 --by <who> --why <reason>"

	code, out := invoke(t, "why", "--store", path, "--policy", ref, "--lanes", "lane-1", "--now", "2026-11-16T01:00:00Z")
	if code != 0 {
		t.Fatalf("why table: %d %s", code, out)
	}
	table := string(out)
	if strings.Contains(table, "cause not recorded") || !strings.Contains(table, "run-item_041-a1-n1") || !strings.Contains(table, wantCmd) {
		t.Fatalf("why table output missing run or command:\n%s", table)
	}

	code, out = invoke(t, "why", "--store", path, "--policy", ref, "--lanes", "lane-1", "--now", "2026-11-16T01:00:00Z", "item_041")
	if code != 0 {
		t.Fatalf("why item detail: %d %s", code, out)
	}
	detail := string(out)
	if strings.Contains(detail, "cause not recorded") || !strings.Contains(detail, "run-item_041-a1-n1") || !strings.Contains(detail, wantCmd) {
		t.Fatalf("why item detail missing run or command:\n%s", detail)
	}
}
