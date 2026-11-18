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
	if code != 2 || !strings.Contains(string(out), "absent or already landed") {
		t.Fatalf("missing item: %d %s", code, out)
	}
}

func TestWhyEmptyStoreAndMissingStore(t *testing.T) {
	path := fixture(t)
	code, out := invoke(t, "why", "--store", path)
	if code != 0 || strings.TrimSpace(string(out)) != "definition: "+definition.GitLandingName+"\nsummary: lanes 0/0 busy; reserved 0; workers 0; examining 0; running 0; could run now 0; owner-waiting 0; next [drain=0, design seat=0, operator=0]; held []" {
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
