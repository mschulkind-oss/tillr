package main_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWhyUsesPlannerAndWritesNothing(t *testing.T) {
	path := fixture(t, itemRecordRanked("ready", "in-queue", "a"), itemRecordRanked("blocked", "in-queue", "b", "ready"), itemRecord("landed", "landed"), itemRecord("closed", "in-queue"), itemRecord("resolved", "resolved-elsewhere"))
	// An item is closed through the lifecycle face, not by applying a record that says so:
	// the store has refused that since closing gained a required reason and author.
	closeFixtureItem(t, path, "closed")
	ref := policy(t, "true")
	before, err := os.ReadFile(path)
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
	if len(answer.Rows) != 4 || answer.Rows[0].ItemID != "ready" || answer.Rows[1].Reason != drain.ReasonBlocked || answer.Rows[1].Mover != "ready" {
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
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
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
	if answer.Rows[1].Reason != drain.ReasonNoLane || answer.Rows[1].Mover != "lane-1" || answer.Rows[2].Reason != drain.ReasonQuestionOpen || answer.Rows[2].Mover != "OQ-1" {
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
	if code != 0 || strings.TrimSpace(string(out)) != "summary: lanes 0/0 busy; running 0; could run now 0; held []" {
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
