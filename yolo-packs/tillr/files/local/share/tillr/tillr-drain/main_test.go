package main_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

var bin string

// binDir holds this module's binary and the sibling binaries the composed case builds.
var binDir string

// Scratch lives under $YOLO_DURABLE_DIR, never /tmp: a suite that writes to /tmp loses
// its evidence when the box is cleaned, and the owner refuses it.
func scratchRoot() string {
	if root := os.Getenv("YOLO_DURABLE_DIR"); root != "" {
		return root
	}
	return filepath.Join("..", "..", ".yolo", "durable")
}

func scratch(t *testing.T, prefix string) string {
	t.Helper()
	root := scratchRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("cannot create the scratch root: %v", err)
	}
	dir, err := os.MkdirTemp(root, prefix+"-")
	if err != nil {
		t.Fatalf("cannot create the scratch directory: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests returns before TestMain exits, so cleanup also runs after setup or test failure.
func runTests(m *testing.M) int {
	// This helper is deliberately killed by TestRunOutputSurvivesLoopProcessExit.
	// It inherits the parent's binaries and trace root; allocating its own would
	// leave scratch behind because a killed process cannot run deferred cleanup.
	if os.Getenv("RUN_OUTPUT_HELPER") == "1" {
		return m.Run()
	}
	root := scratchRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return 1
	}
	dir, err := os.MkdirTemp(root, "tillr-drain-bin-")
	if err != nil {
		return 1
	}
	defer os.RemoveAll(dir)
	// All default fixture commits use this deployed identity. Keep the config
	// private so identity observation never depends on the host's Git identity.
	config := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(config, []byte("[user]\n name = Fixture Landing\n email = landing@example.invalid\n"), 0600); err != nil {
		return 1
	}
	os.Setenv("GIT_CONFIG_GLOBAL", config)
	binDir = dir
	bin = filepath.Join(dir, "tillr-drain")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		return 1
	}
	// The suite's trace root is a scratch directory, not the live one: the loop runs the
	// real indexer after every tick, and one case runs the loop without naming a root.
	restoreTraceRoot, err := isolateTraceRoot()
	if err != nil {
		os.Stderr.WriteString("cannot isolate the suite trace root: " + err.Error() + "\n")
		return 1
	}
	defer restoreTraceRoot()
	return m.Run()
}

// invoke drives the binary with the state home scrubbed, so a face that derives a store
// path instead of taking --store cannot reach the owner's board. plan writes its records
// to --output and nothing to stdout, so the combined output is the diagnostic.
func invoke(t *testing.T, args ...string) (int, []byte) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "state"))
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), out
	}
	if err != nil {
		t.Fatalf("running %v failed: %v", args, err)
	}
	return 0, out
}

// envelope wraps a payload in design §5.1's envelope. The clock is the caller's, so two
// runs of the same case produce the same bytes.
func envelope(kind, id string, payload any) string {
	raw, err := json.Marshal(map[string]any{
		"record_version": 1,
		"kind":           kind,
		"id":             id,
		"producer":       map[string]string{"system": "tillr", "utility": "drain", "version": "0.1.0"},
		"created_at":     "2026-11-10T00:00:00Z",
		"payload":        payload,
	})
	if err != nil {
		// The payload is a map this file builds, so an encoding failure is a bug in the
		// test rather than an outcome to report.
		panic(err)
	}
	return string(raw)
}

// itemRecord is design §5.3's item.record payload, with the fields a decision turns on,
// and no rank: an unranked item, which is what the queue held before ranks existed.
//
// The fence is a field a decision turns on now: the planner refuses an item that names
// none and defers an item whose fence overlaps one this tick already dispatched. A
// per-item path keeps a case that is not about the fence dispatching exactly as it did
// before, because no two items in one fixture share it.
func itemRecord(itemID, state string, blockedBy ...string) string {
	return itemRecordRanked(itemID, state, "", blockedBy...)
}

// itemRecordRanked is itemRecord with the item's place in the queue stated. The rank is a
// sparse lexical token over item's RankAlphabet (rank.go), compared as a plain string, so a
// fixture's rank order can contradict its item_id order on purpose.
func itemRecordRanked(itemID, state, rank string, blockedBy ...string) string {
	return itemRecordFenced(itemID, state, rank, []string{"items/" + itemID + "/"}, blockedBy...)
}

// itemRecordFenced is itemRecord with the fence stated: a case about which fence overlaps
// which names both, and a case about a fence that fences nothing passes nil or a root
// spelling.
func itemRecordFenced(itemID, state, rank string, allowed []string, blockedBy ...string) string {
	return envelope("item.record", "item-"+itemID, map[string]any{
		"item_id":       itemID,
		"title":         "the item " + itemID,
		"state":         state,
		"blocked_by":    blockedBy,
		"rank":          rank,
		"allowed_paths": allowed,
		"criteria":      []any{},
	})
}

// itemRecordSeated is itemRecord with the seat the item asks for. The seat is the item's
// role, and the policy maps it to a backend, so a case about a seat states the seat and
// never a model id.
func itemRecordSeated(itemID, state, seat string) string {
	return envelope("item.record", "item-"+itemID, map[string]any{
		"item_id":       itemID,
		"title":         "the item " + itemID,
		"state":         state,
		"blocked_by":    []string{},
		"rank":          "",
		"allowed_paths": []string{"items/" + itemID + "/"},
		"criteria":      []any{},
		"seat":          seat,
	})
}

// itemRecordWithDoc is itemRecord with the item's design document named. The question hold
// is a fact about the item's own reference, so a case that is about it states the reference
// rather than inheriting an empty one.
func itemRecordWithDoc(itemID, state, designDoc string) string {
	return envelope("item.record", "item-"+itemID, map[string]any{
		"item_id":       itemID,
		"title":         "the item " + itemID,
		"state":         state,
		"blocked_by":    []string{},
		"rank":          "",
		"allowed_paths": []string{"items/" + itemID + "/"},
		"design_doc":    designDoc,
		"criteria":      []any{},
	})
}

// fixtureRepo writes a repository tree holding the documents named, and answers its root.
// The planner reads the tree for one fact — whether a document an item names is there — so a
// case that is about a document states the tree rather than borrowing the real one.
func fixtureRepo(t *testing.T, documents ...string) string {
	t.Helper()
	root := scratch(t, "repo")
	for _, document := range documents {
		path := filepath.Join(root, document)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating the fixture tree failed: %v", err)
		}
		if err := os.WriteFile(path, []byte("# "+document+"\n"), 0o644); err != nil {
			t.Fatalf("writing the fixture document failed: %v", err)
		}
	}
	return root
}

// indexStub writes a Vantage index document holding the questions given and answers the
// command that prints it.
//
// The planner reads the index through a program, so a case stubs the index with a program:
// `cat` of a file is the whole stub, and what the case wrote is what the planner reads. No
// shell is involved — the command line is split on whitespace and executed directly — so the
// path needs no quoting.
func indexStub(t *testing.T, documents ...map[string]any) string {
	t.Helper()
	if documents == nil {
		documents = []map[string]any{}
	}
	raw, err := json.Marshal(map[string]any{
		"tool":        "vantage-check",
		"toolVersion": "0.11.0",
		"version":     3,
		"index": map[string]any{
			"refused":   false,
			"documents": documents,
		},
	})
	if err != nil {
		t.Fatalf("building the index stub failed: %v", err)
	}
	path := filepath.Join(scratch(t, "index"), "index.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("writing the index stub failed: %v", err)
	}
	return "cat " + path
}

// fixture seeds a fixture store from the records given, in the order given: item's
// materializer refuses an edge to an item that does not exist yet, so a blocker is
// seeded before the item that names it.
func fixture(t *testing.T, records ...string) string {
	t.Helper()
	path := filepath.Join(scratch(t, "store"), "board.sqlite3")
	ctx := context.Background()
	s, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatalf("creating the fixture store failed: %v", err)
	}
	defer s.Close()
	// The bundle's records are raw JSON objects, not strings: the seed path decodes each
	// one as a record envelope.
	raw, err := json.Marshal(map[string]any{
		"kind":    "fixture.seed",
		"payload": map[string]any{"records": rawRecords(records)},
	})
	if err != nil {
		t.Fatalf("building the seed bundle failed: %v", err)
	}
	if err := s.Seed(ctx, raw); err != nil {
		t.Fatalf("seeding the fixture store failed: %v", err)
	}
	return path
}

// rawRecords answers the seed records as raw JSON, which is the shape the seed bundle
// carries.
func rawRecords(records []string) []json.RawMessage {
	raw := make([]json.RawMessage, 0, len(records))
	for _, record := range records {
		raw = append(raw, json.RawMessage(record))
	}
	return raw
}

// tick writes a drain.tick record and answers its path.
func tick(t *testing.T, now string, lanes []string, maxDispatches int, policyRef string) string {
	t.Helper()
	return tickPayload(t, map[string]any{
		"now":             now,
		"available_lanes": lanes,
		"max_dispatches":  maxDispatches,
		"policy_ref":      policyRef,
	})
}

// tickPayload writes a drain.tick from a raw payload, so a case can leave a required
// field out.
func tickPayload(t *testing.T, payload map[string]any) string {
	t.Helper()
	path := filepath.Join(scratch(t, "tick"), "tick.json")
	if err := os.WriteFile(path, []byte(envelope(drain.PassKind, "tick-1", payload)), 0o644); err != nil {
		t.Fatalf("writing the tick failed: %v", err)
	}
	return path
}

// policy writes a one-backend policy document and answers its path: the backend is named
// `fixture`, its adapter is the program the case names, and it carries one model id so a
// case that is not about the rotation draws that id.
func policy(t *testing.T, adapter string) string {
	t.Helper()
	return policyDoc(t, map[string]any{
		"default_backend": "fixture",
		"preference":      []string{"fixture"},
		"max_failovers":   1,
		"backends": map[string]any{
			"fixture": map[string]any{
				"adapter":         adapter,
				"model":           "fixture-model",
				"effort":          "low",
				"timeout_seconds": 60,
			},
		},
	})
}

// policyDoc writes a policy from a raw document, so a case can name several backends, an
// override map, or a field left out.
func policyDoc(t *testing.T, document map[string]any) string {
	t.Helper()
	path := filepath.Join(scratch(t, "policy"), "policy.json")
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("building the policy failed: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("writing the policy failed: %v", err)
	}
	return path
}

// decision is the drain.decision payload as a case reads it.
type decision struct {
	Position        string       `json:"position"`
	Transition      string       `json:"transition"`
	DecisionID      string       `json:"decision_id"`
	ItemID          string       `json:"item_id"`
	Action          string       `json:"action"`
	Reason          string       `json:"reason"`
	LaneID          string       `json:"lane_id"`
	RunRequest      *run.Request `json:"run_request"`
	RetryID         string       `json:"retry_id"`
	SupersedesRunID string       `json:"supersedes_run_id"`
	DeferredBy      string       `json:"deferred_by"`
	QuestionID      string       `json:"question_id"`
	Seat            string       `json:"seat"`
}

// decisionRecord is one record of the output file.
type decisionRecord struct {
	Kind    string   `json:"kind"`
	ID      string   `json:"id"`
	Payload decision `json:"payload"`
}

// plan runs the face against the real repository tree, with an index that reports nothing
// open.
//
// The tree is the real one because a seed row names a document that is really there, and a
// case that is about the seed's ranks and fences must not also fail because a document was
// missing. The questions are stubbed to none for the opposite reason: whether a real
// document has an open question is the owner's business and changes between rulings, so a
// case about order that read the real index would go red on a morning nobody touched the
// code.
func plan(t *testing.T, storePath, tickPath string) []decisionRecord {
	t.Helper()
	return planFrom(t, storePath, tickPath, repoRoot(t), indexStub(t))
}

// planFrom is plan with the tree and the index command named: the root the items'
// design_doc references resolve against, and the program that answers Vantage's index.
func planFrom(t *testing.T, storePath, tickPath, root, indexCmd string) []decisionRecord {
	t.Helper()
	out := filepath.Join(scratch(t, "out"), "decisions.json")
	code, output := invoke(t, "plan", "--store", storePath, "--input", tickPath, "--output", out,
		"--root", root, "--questions-cmd", indexCmd)
	if code != 0 {
		t.Fatalf("plan exited %d: %s", code, output)
	}
	return readDecisions(t, out)
}

// readDecisions reads an output file.
func readDecisions(t *testing.T, path string) []decisionRecord {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s failed: %v", path, err)
	}
	var records []decisionRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		t.Fatalf("decoding %s failed: %v", path, err)
	}
	for _, record := range records {
		if record.Kind != drain.DecisionKind {
			t.Fatalf("the output holds a %q record, want %s", record.Kind, drain.DecisionKind)
		}
	}
	return records
}

// decisionFor answers the decision about one item, so a case whose fixture holds more than
// one item reads the one it is about rather than the first.
func decisionFor(t *testing.T, records []decisionRecord, itemID string) decision {
	t.Helper()
	for _, record := range records {
		if record.Payload.ItemID == itemID {
			return record.Payload
		}
	}
	t.Fatalf("the tick produced no decision about %q", itemID)
	return decision{}
}

// The positive decision: a queued item, a free lane and a policy that names a command
// produce a dispatch carrying a complete run.request — every field run exec reads.
func TestPlanDispatchesAQueuedItemOntoAnAvailableLane(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	records := plan(t, storePath, tickPath)
	if len(records) != 1 {
		t.Fatalf("the tick produced %d decisions, want 1", len(records))
	}
	got := records[0].Payload
	if got.Action != drain.ActionDispatch || got.Reason != drain.ReasonReady {
		t.Fatalf("the decision is %q / %q, want dispatch / ready", got.Action, got.Reason)
	}
	if got.ItemID != "item_042" || got.LaneID != "lane-1" {
		t.Fatalf("the decision names item %q on lane %q", got.ItemID, got.LaneID)
	}
	if got.DecisionID != "decision-item_042-a1-queued-running" {
		t.Fatalf("the decision id is %q", got.DecisionID)
	}
	request := got.RunRequest
	if request == nil {
		t.Fatalf("a dispatch carries no run_request")
	}
	if request.RunID != "run-item_042-a1" || request.ItemID != "item_042" || request.LaneID != "lane-1" {
		t.Fatalf("the request names run %q / item %q / lane %q", request.RunID, request.ItemID, request.LaneID)
	}
	// The command is the policy's backend composed for the adapter: the program, the
	// backend it must be told to be, the model id staging supplies, the effort, and the
	// wall the policy names.
	wantCommand := []string{"/bin/true", "--backend", "fixture", "--model", "fixture-model", "--effort", "low", "--timeout", "60"}
	if strings.Join(request.Command, " ") != strings.Join(wantCommand, " ") {
		t.Fatalf("the request's command is %v, want %v", request.Command, wantCommand)
	}
	if request.WorkingDir != drain.WorktreeDir || request.TimeoutMS != 60000 || request.EnvRefs == nil {
		t.Fatalf("the request's working_dir %q / timeout_ms %d / env_refs %v are not the defaults",
			request.WorkingDir, request.TimeoutMS, request.EnvRefs)
	}
	if request.Backend != "fixture" {
		t.Fatalf("the request names backend %q, want the policy's", request.Backend)
	}
}

// An edge is read, not assumed: the same item holds while its blocker is unfinished and
// dispatches once the blocker lands.
func TestPlanHoldsAnItemBlockedByAnUnlandedItem(t *testing.T) {
	// This tests item edges, not the invoking checkout's proved-trunk pointer.
	// A private repository with no pointer exercises the bootstrap case equally
	// in a lane and a fresh clone; lane-base tests cover proof requirements.
	root := fixtureRepo(t)
	git(t, root, "init", "-b", "main")
	git(t, root, "commit", "--allow-empty", "-qm", "base")
	policyPath := policy(t, "/bin/true")
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policyPath)

	t.Run("blocker not started", func(t *testing.T) {
		storePath := fixture(t,
			itemRecord("item_007", "not-started"),
			itemRecord("item_042", "in-queue", "item_007"),
		)
		got := decisionFor(t, planFrom(t, storePath, tickPath, root, indexStub(t)), "item_042")
		if got.Action != drain.ActionHold || got.Reason != drain.ReasonBlocked {
			t.Fatalf("the decision is %q / %q, want hold / blocked", got.Action, got.Reason)
		}
		if got.RunRequest != nil {
			t.Fatalf("a hold carries a run_request")
		}
	})

	t.Run("blocker landed", func(t *testing.T) {
		storePath := fixture(t,
			itemRecord("item_007", "landed"),
			itemRecord("item_042", "in-queue", "item_007"),
		)
		// A landed state is not proof that a shared repository's green pointer
		// contains this fixture's blocker. Supply its landing and proof in an
		// isolated repository instead of borrowing the live checkout's refs.
		root := fixtureRepo(t)
		git(t, root, "init", "-q", "-b", "main")
		git(t, root, "commit", "--allow-empty", "-qm", "proved blocker landing")
		landed := git(t, root, "rev-parse", "HEAD")
		git(t, root, "update-ref", drain.GreenRef, landed)
		s, err := store.Open(context.Background(), storePath, true)
		if err != nil {
			t.Fatal(err)
		}
		err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
			_, err := tx.Exec(`INSERT INTO landings(landing_id,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after,event_seq) VALUES('fixture-landing','item_007','fixture-run','fixture-verdict','landed','fixture-base',?,'fixture-before',?,1)`, landed, landed)
			return err
		})
		closeErr := s.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		got := decisionFor(t, planFrom(t, storePath, tickPath, root, indexStub(t)), "item_042")
		if got.Action != drain.ActionDispatch {
			t.Fatalf("the decision is %q / %q, want dispatch", got.Action, got.Reason)
		}
	})
}

// An item whose own state says blocked holds even with no edge, and an item that is not
// queued is not dispatched: the drain does not choose work the board has not queued.
func TestPlanHoldsAnItemWhoseStateIsNotQueued(t *testing.T) {
	policyPath := policy(t, "/bin/true")
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policyPath)

	for _, state := range []string{"not-started", "landed"} {
		t.Run(state, func(t *testing.T) {
			storePath := fixture(t, itemRecord("item_042", state))
			got := plan(t, storePath, tickPath)[0].Payload
			if got.Action != drain.ActionHold || got.Reason != drain.ReasonNotReady {
				t.Fatalf("the decision is %q / %q, want hold / not-ready", got.Action, got.Reason)
			}
		})
	}

	t.Run("blocked state", func(t *testing.T) {
		storePath := fixture(t, itemRecord("item_042", "blocked"))
		got := plan(t, storePath, tickPath)[0].Payload
		if got.Action != drain.ActionHold || got.Reason != drain.ReasonBlocked {
			t.Fatalf("the decision is %q / %q, want hold / blocked", got.Action, got.Reason)
		}
	})
}

// An item that has run before may be a retry, and nobody authorized the second attempt, so
// it holds with `run-link-missing` rather than being planned as a first dispatch — the
// conservative half of the retry rule. The authorization is what turns the hold into the
// retry TestPlanRetriesARunTheHarnessRefused pins; without one, the hold stands.
func TestPlanHoldsAnItemThatMayBeARetry(t *testing.T) {
	// The item has a terminal run: it was dispatched and run before. The run is the
	// evidence `runs.item_id` carries (D-UT19), not a state the item had to be left in.
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord("run-item_042-20261109T000000Z", "item_042", "lane-1", "fixture", run.StateExited, exitCode(0), ""),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonRunLinkMissing {
		t.Fatalf("the decision is %q / %q, want hold / run-link-missing", got.Action, got.Reason)
	}
	if got.RunRequest != nil {
		t.Fatalf("a hold carries a run_request")
	}
}

// A previously run item is planned by the outcome of its latest run, not with a generic
// `not-ready`. A fence refusal, a run that produced no commit and a red gate go back to the
// worker with their evidence (`returned-with-evidence`, the owner's ruling of 2026-11-17);
// a wall that cut a run short is held with its own reason. A run the store cannot classify
// falls back to `run-link-missing`, which is the conservative answer.
func TestAPreviouslyRunItemHoldsWithItsOwnOutcomeReason(t *testing.T) {
	const runID = "run-item_042-20261109T000000Z"
	cases := []struct {
		name  string
		state string
		exit  *int
		seed  func(t *testing.T, storePath string)
		want  string
	}{
		{
			name: "fence refused", state: run.StateExited, exit: exitCode(0),
			seed: func(t *testing.T, storePath string) { seedRefusal(t, storePath, runID, drain.RefusalFence) },
			want: drain.ReasonReturned,
		},
		{
			name: "no commit", state: run.StateExited, exit: exitCode(0),
			seed: func(t *testing.T, storePath string) { seedRefusal(t, storePath, runID, drain.RefusalNoOp) },
			want: drain.ReasonReturned,
		},
		{
			name: "timed out", state: run.StateTimedOut, exit: nil,
			want: drain.ReasonRunUnjudged,
		},
		{
			name: "gate failed", state: run.StateExited, exit: exitCode(0),
			seed: func(t *testing.T, storePath string) { seedGateVerdict(t, storePath, runID) },
			want: drain.ReasonReturned,
		},
		{
			name: "unclassifiable answer", state: run.StateExited, exit: exitCode(0),
			want: drain.ReasonRunLinkMissing,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storePath := fixture(t,
				itemRecord("item_042", "in-queue"),
				runRecord(runID, "item_042", "lane-1", "fixture", c.state, c.exit, ""),
			)
			if c.seed != nil {
				c.seed(t, storePath)
			}
			tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))
			got := plan(t, storePath, tickPath)[0].Payload
			action := drain.ActionHold
			if c.want == drain.ReasonReturned {
				action = drain.ActionDispatch
			}
			if got.Action != action || got.Reason != c.want {
				t.Fatalf("the decision is %q / %q, want %s / %q", got.Action, got.Reason, action, c.want)
			}
			if got.Reason == drain.ReasonNotReady {
				t.Fatalf("a previously run item was held as %s", drain.ReasonNotReady)
			}
		})
	}
}

// The vocabulary pin: `retry` is never emitted without an operator's authorization. The
// fixture holds one item that ran before and one that never did, so a face that guessed
// would have something to guess at — and the guess would be a retry planned as a first
// dispatch, which is the one answer the conservative rule must not produce.
func TestPlanNeverEmitsAnUnauthorizedRetry(t *testing.T) {
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord("run-item_042-20261109T000000Z", "item_042", "lane-1", "fixture", run.StateExited, exitCode(0), ""),
		itemRecord("item_043", "in-queue"),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1", "lane-2"}, 2, policy(t, "/bin/true"))

	records := plan(t, storePath, tickPath)
	for _, record := range records {
		if record.Payload.Action == drain.ActionRetry {
			t.Fatalf("the tick emitted an unauthorized retry for %q", record.Payload.ItemID)
		}
	}
	if got := decisionFor(t, records, "item_042"); got.Reason != drain.ReasonRunLinkMissing {
		t.Fatalf("the item that ran before is %q / %q, want hold / run-link-missing", got.Action, got.Reason)
	}
	if got := decisionFor(t, records, "item_043"); got.Action != drain.ActionDispatch {
		t.Fatalf("the item that never ran is %q / %q, want dispatch", got.Action, got.Reason)
	}
}

// An item whose history is not in the log at all is unknown, not never-run: the absence of
// evidence is not evidence that the item never ran, so it holds. The seed path always
// appends an item event, so the row is written through the substrate's write transaction —
// this is the one way to construct an item the log says nothing about.
func TestPlanHoldsAnItemWhoseHistoryIsUnknown(t *testing.T) {
	storePath := fixture(t)
	ctx := context.Background()
	s, err := store.Open(ctx, storePath, false)
	if err != nil {
		t.Fatalf("opening %s failed: %v", storePath, err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO items (item_id, title, state, updated_at) VALUES (?, ?, ?, ?)`,
			"item_042", "an item the log says nothing about", "in-queue", 0,
		)
		return err
	}); err != nil {
		s.Close()
		t.Fatalf("writing the item row failed: %v", err)
	}
	s.Close()

	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))
	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonRunLinkMissing {
		t.Fatalf("the decision is %q / %q, want hold / run-link-missing", got.Action, got.Reason)
	}
}

// A tick that names no policy can dispatch nothing, and it says so rather than emitting a
// run.request no runner would accept.
func TestPlanHoldsWhenTheTickNamesNoPolicy(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, "")

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonNoPolicy {
		t.Fatalf("the decision is %q / %q, want hold / no-policy", got.Action, got.Reason)
	}
}

// Capacity is the tick's own bound: the first item dispatches and the second holds, in the
// queue's order, so the same tick always spends the budget the same way.
func TestPlanHoldsWhenCapacityIsExhausted(t *testing.T) {
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		itemRecord("item_043", "in-queue"),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1", "lane-2"}, 1, policy(t, "/bin/true"))

	records := plan(t, storePath, tickPath)
	if len(records) != 2 {
		t.Fatalf("the tick produced %d decisions, want 2", len(records))
	}
	if got := records[0].Payload; got.Action != drain.ActionDispatch || got.LaneID != "lane-1" {
		t.Fatalf("the first decision is %q on lane %q, want dispatch on lane-1", got.Action, got.LaneID)
	}
	if got := records[1].Payload; got.Action != drain.ActionHold || got.Reason != drain.ReasonNoLane {
		t.Fatalf("the second decision is %q / %q, want hold / capacity", got.Action, got.Reason)
	}
}

// A tick that offers no lane dispatches nothing, and a lane it did not offer is never
// assigned.
func TestPlanHoldsWhenNoLaneIsAvailable(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonNoLane {
		t.Fatalf("the decision is %q / %q, want hold / no-lane", got.Action, got.Reason)
	}
	if got.LaneID != "" {
		t.Fatalf("a hold names lane %q", got.LaneID)
	}
}

// Two items that deliver one fork's patch branch cannot run at once, whatever their own
// fences name: `.forks/lanes/lane-1/pi` and `.forks/lanes/lane-2/pi` both advance
// `refs/heads/patch/pi`, so the second would move a branch the first is still building. It
// is the only planner-level exclusion left; two ordinary items with disjoint fences run
// together.
func TestPlanHoldsASecondItemDeliveringTheSameFork(t *testing.T) {
	storePath := fixture(t,
		itemRecordFenced("item_041", "in-queue", "", []string{".forks/lanes/lane-1/pi/"}),
		itemRecordFenced("item_042", "in-queue", "", []string{".forks/lanes/lane-2/pi/"}),
	)
	tickPath := tick(t, "2026-11-14T00:00:00Z", []string{"lane-1", "lane-2"}, 2, policy(t, "/bin/true"))

	records := plan(t, storePath, tickPath)
	if got := decisionFor(t, records, "item_041"); got.Action != drain.ActionDispatch {
		t.Fatalf("item_041 is %q / %q, want dispatch", got.Action, got.Reason)
	}
	got := decisionFor(t, records, "item_042")
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonForkHeld || got.DeferredBy != "item_041" {
		t.Fatalf("item_042 is %q / %q behind %q, want hold / %s behind item_041",
			got.Action, got.Reason, got.DeferredBy, drain.ReasonForkHeld)
	}
	if got.RunRequest != nil {
		t.Fatalf("a fork-held item carries a run_request")
	}
}

// Two ordinary items with disjoint fences run together, because each has its own worktree and
// its own land lock. A tick that serialized them would be the batch barrier back as a plan.
func TestPlanDispatchesTwoItemsWithDisjointFences(t *testing.T) {
	storePath := fixture(t,
		itemRecordFenced("item_041", "in-queue", "", []string{"tools/tillr-view/"}),
		itemRecordFenced("item_042", "in-queue", "", []string{"tools/tillr-drain/"}),
	)
	tickPath := tick(t, "2026-11-14T00:00:00Z", []string{"lane-1", "lane-2"}, 2, policy(t, "/bin/true"))

	records := plan(t, storePath, tickPath)
	for _, id := range []string{"item_041", "item_042"} {
		if got := decisionFor(t, records, id); got.Action != drain.ActionDispatch {
			t.Fatalf("%s is %q / %q, want dispatch", id, got.Action, got.Reason)
		}
	}
}

// An item that names no fence is refused, not deferred: it would fence nothing, so its
// change cannot be shown to be in scope. The alternative — treating no fence as
// overlapping everything — would serialise the whole queue behind one blanket.
func TestPlanRefusesAnItemThatNamesNoFence(t *testing.T) {
	storePath := fixture(t, itemRecordFenced("item_041", "in-queue", "", nil))
	tickPath := tick(t, "2026-11-14T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := decisionFor(t, plan(t, storePath, tickPath), "item_041")
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonNoFence {
		t.Fatalf("the decision is %q / %q, want hold / %s", got.Action, got.Reason, drain.ReasonNoFence)
	}
	if got.RunRequest != nil {
		t.Fatalf("a refused item carries a run_request")
	}
}

// A fence naming the repository root fences nothing either, and it is refused for the same
// reason. The item face refuses the spelling when an item is applied, so this rewrites the
// stored column directly: a plan reads a store rather than an apply, and a hand-edited or
// pre-column row can still carry one.
func TestPlanRefusesAFenceThatNamesTheRepositoryRoot(t *testing.T) {
	storePath := fixture(t, itemRecord("item_041", "in-queue"))
	setFence(t, storePath, "item_041", ".")
	tickPath := tick(t, "2026-11-14T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := decisionFor(t, plan(t, storePath, tickPath), "item_041")
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonNoFence {
		t.Fatalf("the decision is %q / %q, want hold / %s", got.Action, got.Reason, drain.ReasonNoFence)
	}
}

// setFence rewrites one item's stored fence, past the item face's own refusal: the planner
// must still answer a store it would not have written.
func setFence(t *testing.T, storePath, itemID string, paths ...string) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatalf("opening %s failed: %v", storePath, err)
	}
	defer s.Close()
	raw, err := json.Marshal(paths)
	if err != nil {
		t.Fatalf("encoding the fence failed: %v", err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths = ? WHERE item_id = ?`, string(raw), itemID)
		return err
	}); err != nil {
		t.Fatalf("rewriting the fence failed: %v", err)
	}
}

// The store's lane state is read: a lane a live run holds is skipped, and the next
// offered lane is taken. The lease expires after the tick's now, so this is the
// unexpired case: `plan` writes nothing, so nothing reaps it here.
func TestPlanDoesNotOfferALaneTheStoreHoldsLeased(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	seedLease(t, storePath, "lane-1", "run-1", time.Date(2026, 11, 10, 0, 10, 0, 0, time.UTC).UnixMilli())
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1", "lane-2"}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionDispatch || got.LaneID != "lane-2" {
		t.Fatalf("the decision is %q on lane %q, want dispatch on lane-2", got.Action, got.LaneID)
	}
}

// A lease at or before the tick's now has expired: the lane is a lane the loop may
// reuse, and `plan` offers it. `plan` writes nothing, so this is the pure half of the
// same rule `once` reaps by, and the boundary is the lease's own instant — "at or
// before", not "before".
func TestPlanTreatsAnExpiredLeaseAsAvailable(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	seedLease(t, storePath, "lane-1", "run-1", now.UnixMilli())
	tickPath := tick(t, now.Format(time.RFC3339), []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionDispatch || got.LaneID != "lane-1" {
		t.Fatalf("the decision is %q on lane %q, want dispatch on the lane whose lease expired", got.Action, got.LaneID)
	}
}

// The face's whole claim: it writes nothing. The store's event head and table counts are
// the same before and after, and the store is opened read-only so a write is impossible
// rather than merely absent.
func TestPlanWritesNothingToTheStore(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	before := describe(t, storePath)
	plan(t, storePath, tickPath)
	after := describe(t, storePath)

	if before.EventHeadSeq != after.EventHeadSeq {
		t.Fatalf("the event head moved from %d to %d", before.EventHeadSeq, after.EventHeadSeq)
	}
	if len(before.TableCounts) != len(after.TableCounts) {
		t.Fatalf("the table set changed: %v then %v", before.TableCounts, after.TableCounts)
	}
	for table, count := range before.TableCounts {
		if after.TableCounts[table] != count {
			t.Fatalf("table %q went from %d rows to %d", table, count, after.TableCounts[table])
		}
	}
}

// seedLease writes one lane row held by a run until leasedUntil, through the substrate's
// write transaction: the claim that writes a lease is `run exec`'s, and no record seeds
// one, so a case that needs a held lane says so here.
func seedLease(t *testing.T, path, laneID, runID string, leasedUntil int64) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatalf("opening %s failed: %v", path, err)
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO lanes (lane_id, state, run_id, leased_until) VALUES (?, 'leased', ?, ?)`,
			laneID, runID, leasedUntil,
		)
		return err
	}); err != nil {
		t.Fatalf("seeding lane %q failed: %v", laneID, err)
	}
}

// describe reads the store's description.
func describe(t *testing.T, path string) store.Snapshot {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		t.Fatalf("opening %s failed: %v", path, err)
	}
	defer s.Close()
	snapshot, err := s.Describe(ctx)
	if err != nil {
		t.Fatalf("describing %s failed: %v", path, err)
	}
	return snapshot
}

// The decision order is the queue's order, which is the rank's: an item whose rank sorts
// before another's is decided first however the two ids compare. The fixture states ranks
// that reverse the id order, so a planner still answering in item_id order fails here —
// that is this case's negative control, and it is the behaviour the face had before rank.
//
// Ranks are strings over the capacity queue's sparse alphabet (`a`, `am`, `b`), compared as
// plain strings, so `item_042`'s `a` is ahead of `item_043`'s `am` and both are ahead of
// `item_041`'s `b`.
func TestPlanAnswersItemsInRankOrder(t *testing.T) {
	storePath := fixture(t,
		itemRecordRanked("item_041", "in-queue", "b"),
		itemRecordRanked("item_042", "in-queue", "a"),
		itemRecordRanked("item_043", "in-queue", "am"),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1", "lane-2", "lane-3"}, 3, policy(t, "/bin/true"))

	records := plan(t, storePath, tickPath)
	want := []string{"item_042", "item_043", "item_041"}
	if len(records) != len(want) {
		t.Fatalf("the tick produced %d decisions, want %d", len(records), len(want))
	}
	for i, id := range want {
		if records[i].Payload.ItemID != id {
			t.Fatalf("decision %d is about %q, want %q", i, records[i].Payload.ItemID, id)
		}
	}
}

// An unranked item sorts after every ranked one and keeps item_id order among the unranked,
// which is the half of the rule a store that has never been ranked relies on: two ticks over
// one store still produce comparable files, and the queue is the id order it always was.
//
// The negative control is the two ranked items' ids: they are the *highest* ids in the
// fixture, so a planner that read the column and then ignored it, or that sorted the whole
// store by id, would put them last instead of first.
func TestPlanAnswersUnrankedItemsAfterEveryRankedOneInItemIDOrder(t *testing.T) {
	storePath := fixture(t,
		itemRecord("item_040", "in-queue"),
		itemRecordRanked("item_042", "in-queue", "z"),
		itemRecord("item_041", "in-queue"),
		itemRecordRanked("item_043", "in-queue", "a"),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z",
		[]string{"lane-1", "lane-2", "lane-3", "lane-4"}, 4, policy(t, "/bin/true"))

	records := plan(t, storePath, tickPath)
	want := []string{"item_043", "item_042", "item_040", "item_041"}
	if len(records) != len(want) {
		t.Fatalf("the tick produced %d decisions, want %d", len(records), len(want))
	}
	for i, id := range want {
		if records[i].Payload.ItemID != id {
			t.Fatalf("decision %d is about %q, want %q: the order is %v",
				i, records[i].Payload.ItemID, id, decisionIDs(records))
		}
	}
}

// decisionIDs answers the items a tick decided about, in the order it decided them, so a
// failure prints the order the face produced rather than one item of it.
func decisionIDs(records []decisionRecord) []string {
	ids := make([]string, len(records))
	for i, record := range records {
		ids[i] = record.Payload.ItemID
	}
	return ids
}

// The same store and the same tick produce the same bytes, which is what makes the face a
// capacity simulator rather than a report on a moment.
func TestPlanIsDeterministic(t *testing.T) {
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		itemRecord("item_043", "in-queue"),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1", "lane-2"}, 2, policy(t, "/bin/true"))

	first := filepath.Join(scratch(t, "first"), "decisions.json")
	second := filepath.Join(scratch(t, "second"), "decisions.json")
	for _, out := range []string{first, second} {
		if code, output := invoke(t, "plan", "--store", storePath, "--input", tickPath, "--output", out); code != 0 {
			t.Fatalf("plan exited %d: %s", code, output)
		}
	}
	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("reading %s failed: %v", first, err)
	}
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatalf("reading %s failed: %v", second, err)
	}
	if string(a) != string(b) {
		t.Fatalf("two runs of one tick differ:\n%s\n%s", a, b)
	}
	// The record time is injected, and identities name the attempt. Re-running the
	// same plan therefore answers the same bytes without reading the process clock.
	for _, record := range readDecisions(t, first) {
		if want := "decision-" + record.Payload.ItemID + "-a1-queued-running"; record.Payload.DecisionID != want {
			t.Fatalf("the decision id is %q, want %q", record.Payload.DecisionID, want)
		}
		if want := "run-" + record.Payload.ItemID + "-a1"; record.Payload.RunRequest.RunID != want {
			t.Fatalf("the run id is %q, want %q", record.Payload.RunRequest.RunID, want)
		}
	}
}

// A tick the face cannot read is malformed input: exit 2, and no output file, because a
// plan that failed half way must not leave a file a caller would read as an answer.
func TestPlanRefusesAMalformedTick(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))

	cases := map[string]map[string]any{
		"unparsable now": {"now": "yesterday", "available_lanes": []string{"lane-1"}, "max_dispatches": 1},
		"negative max":   {"now": "2026-11-10T00:00:00Z", "available_lanes": []string{"lane-1"}, "max_dispatches": -1},
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(scratch(t, "refused"), "decisions.json")
			code, output := invoke(t, "plan", "--store", storePath, "--input", tickPayload(t, payload), "--output", out)
			if code != 2 {
				t.Fatalf("plan exited %d, want 2: %s", code, output)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatalf("a refused tick left an output file at %s", out)
			}
		})
	}

	t.Run("wrong record kind", func(t *testing.T) {
		path := filepath.Join(scratch(t, "wrong-kind"), "tick.json")
		raw := envelope("drain.tock", "tick-1", map[string]any{
			"now": "2026-11-10T00:00:00Z", "available_lanes": []string{"lane-1"}, "max_dispatches": 1,
		})
		if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
			t.Fatalf("writing the tick failed: %v", err)
		}
		out := filepath.Join(scratch(t, "wrong-kind"), "decisions.json")
		if code, output := invoke(t, "plan", "--store", storePath, "--input", path, "--output", out); code != 2 {
			t.Fatalf("plan exited %d, want 2: %s", code, output)
		}
	})

	t.Run("policy names no backends", func(t *testing.T) {
		empty := policyDoc(t, map[string]any{"backends": map[string]any{}})
		out := filepath.Join(scratch(t, "empty-policy"), "decisions.json")
		code, output := invoke(t, "plan", "--store", storePath,
			"--input", tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, empty), "--output", out)
		if code != 2 {
			t.Fatalf("plan exited %d, want 2: %s", code, output)
		}
	})

	t.Run("policy that is not there", func(t *testing.T) {
		out := filepath.Join(scratch(t, "missing-policy"), "decisions.json")
		code, output := invoke(t, "plan", "--store", storePath,
			"--input", tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, filepath.Join(scratch(t, "missing-policy"), "nope.json")),
			"--output", out)
		if code != 2 {
			t.Fatalf("plan exited %d, want 2: %s", code, output)
		}
	})
}

// twoBackendPolicy writes the policy a failover case needs: `first` (a two-route
// rotation) and `second`, the preference first -> second, and the budget the case names.
func twoBackendPolicy(t *testing.T, maxFailovers int, overrides map[string]string) string {
	t.Helper()
	document := map[string]any{
		"default_backend": "first",
		"preference":      []string{"first", "second"},
		"max_failovers":   maxFailovers,
		"backends": map[string]any{
			"first": map[string]any{
				"adapter":         "/bin/true",
				"models":          []string{"route-a", "route-b"},
				"effort":          "high",
				"timeout_seconds": 60,
			},
			"second": map[string]any{
				"adapter":         "/bin/true",
				"model":           "route-z",
				"timeout_seconds": 60,
			},
		},
	}
	if overrides != nil {
		document["overrides"] = overrides
	}
	return policyDoc(t, document)
}

// runRecord seeds one terminal run.record for an item: the run history a failover decision
// reads. A log the case writes is named as the record's artifact, which is how the
// classification reaches the footer a harness leaves.
func runRecord(runID, itemID, laneID, backend, state string, exitCode *int, logPath string) string {
	payload := map[string]any{
		"run_id":        runID,
		"item_id":       itemID,
		"lane_id":       laneID,
		"backend":       backend,
		"state":         state,
		"started_at":    "2026-11-09T00:00:00Z",
		"finished_at":   "2026-11-09T00:05:00Z",
		"artifact_refs": []any{},
	}
	if exitCode != nil {
		payload["exit_code"] = *exitCode
	}
	if logPath != "" {
		payload["artifact_refs"] = []any{map[string]any{"kind": "log", "ref": logPath}}
	}
	return envelope("run.record", "run-record-"+runID, payload)
}

// exitCode answers an exit code pointer for a seeded record.
func exitCode(code int) *int { return &code }

// modelOf answers the model id a request's argv names, which is where the design puts a
// model id and therefore the only place a reader has for the route that was drawn.
func modelOf(t *testing.T, request *run.Request) string {
	t.Helper()
	for i, element := range request.Command {
		if element == "--model" && i+1 < len(request.Command) {
			return request.Command[i+1]
		}
	}
	t.Fatalf("the request's argv %v names no model", request.Command)
	return ""
}

// Per-item selection: the override chooses the item's first backend and the default serves
// the rest, and each request carries the chosen name and a route drawn from that backend's
// own rotation — the record, the report header and the retry policy then read one datum.
func TestPlanSendsEachItemToTheBackendThePolicyNamesForIt(t *testing.T) {
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		itemRecord("item_043", "in-queue"),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1", "lane-2"}, 2,
		twoBackendPolicy(t, 1, map[string]string{"item_043": "second"}))

	records := plan(t, storePath, tickPath)
	defaulted := decisionFor(t, records, "item_042")
	overridden := decisionFor(t, records, "item_043")

	if defaulted.RunRequest == nil || defaulted.RunRequest.Backend != "first" {
		t.Fatalf("the item with no override was dispatched to %+v, want backend first", defaulted.RunRequest)
	}
	if overridden.RunRequest == nil || overridden.RunRequest.Backend != "second" {
		t.Fatalf("the overridden item was dispatched to %+v, want backend second", overridden.RunRequest)
	}
	if defaulted.Reason != drain.ReasonReady || overridden.Reason != drain.ReasonReady {
		t.Fatalf("the decisions are %q / %q, want ready / ready", defaulted.Reason, overridden.Reason)
	}
	// The route is one of the backend's own models, and it is in the request before the run
	// starts: the wire id collapses the rotation to one id, so this is the attribution.
	route := modelOf(t, defaulted.RunRequest)
	if route != "route-a" && route != "route-b" {
		t.Fatalf("the drawn route %q is not one of the backend's models", route)
	}
	if got := modelOf(t, overridden.RunRequest); got != "route-z" {
		t.Fatalf("the single-model backend drew %q, want its model", got)
	}
}

// DRAIN-2B's positive direction: a run that failed for the machinery's reasons — here the
// adapter's own preflight refusal, exit 2 — is re-staged on the next backend in the
// preference, under a new run id, and the decision says it was a failover.
func TestPlanFailsOverAnInfrastructureFailureOnceToTheNextBackend(t *testing.T) {
	const failedRun = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(failedRun, "item_042", "lane-9", "first", run.StateExited, exitCode(2), ""),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, twoBackendPolicy(t, 1, nil))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionDispatch || got.Reason != drain.ReasonFailover {
		t.Fatalf("the decision is %q / %q, want dispatch / failover", got.Action, got.Reason)
	}
	if got.RunRequest == nil || got.RunRequest.Backend != "second" {
		t.Fatalf("the failover names %+v, want the next backend in the preference", got.RunRequest)
	}
	if got.RunRequest.RunID == failedRun {
		t.Fatalf("the failover reused the failed run's id %q: a re-dispatch is a new run", failedRun)
	}
}

// DRAIN-2B's negative direction, both halves. A run that answered — cleanly, or with a
// report footer that says the model failed — is judged work, and judged work never fails
// over: spending a second backend on it would pay twice to learn what the gate already
// said once.
func TestPlanDoesNotFailOverACandidateFailure(t *testing.T) {
	cases := map[string]struct {
		state string
		exit  *int
		log   string
	}{
		"the harness answered cleanly": {state: run.StateExited, exit: exitCode(0)},
		"the model reported a failed answer": {
			state: run.StateExited, exit: exitCode(1), log: "verification: go test ./... -> FAIL\n\nRESULT: failed\n",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			logPath := ""
			if c.log != "" {
				logPath = filepath.Join(scratch(t, "log"), "run.log")
				if err := os.WriteFile(logPath, []byte(c.log), 0o644); err != nil {
					t.Fatalf("writing the run log failed: %v", err)
				}
			}
			storePath := fixture(t,
				itemRecord("item_042", "in-queue"),
				runRecord("run-item_042-20261109T000000Z", "item_042", "lane-9", "first", c.state, c.exit, logPath),
			)
			tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, twoBackendPolicy(t, 1, nil))

			got := plan(t, storePath, tickPath)[0].Payload
			if got.Action != drain.ActionHold {
				t.Fatalf("the decision is %q on %+v, want a hold: judged work never fails over", got.Action, got.RunRequest)
			}
			if got.RunRequest != nil {
				t.Fatalf("a hold carries a run_request to %q", got.RunRequest.Backend)
			}
		})
	}
}

// The budget: the policy says one failover, and a chain longer than that refuses rather
// than walking. Two infrastructure failures in the item's history are one failover already
// spent, so the item holds with the reason day-2's vocabulary names.
func TestPlanHoldsWhenTheFailoverBudgetIsSpent(t *testing.T) {
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord("run-item_042-20261109T000000Z", "item_042", "lane-8", "first", run.StateFailedToStart, nil, ""),
		runRecord("run-item_042-20261109T010000Z", "item_042", "lane-9", "second", run.StateExited, exitCode(2), ""),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, twoBackendPolicy(t, 1, nil))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonInfraExhausted {
		t.Fatalf("the decision is %q / %q, want hold / %s", got.Action, got.Reason, drain.ReasonInfraExhausted)
	}
	if got.RunRequest != nil {
		t.Fatalf("a spent budget still dispatched to %q", got.RunRequest.Backend)
	}
}

// The same chain with a budget of zero: the first infrastructure failure already exhausts
// it, so the policy's number is what decides and not the code's.
func TestPlanWalksNoFailoverWhenThePolicyBudgetIsZero(t *testing.T) {
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord("run-item_042-20261109T000000Z", "item_042", "lane-9", "first", run.StateFailedToStart, nil, ""),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, twoBackendPolicy(t, 0, nil))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonInfraExhausted {
		t.Fatalf("the decision is %q / %q, want hold / %s", got.Action, got.Reason, drain.ReasonInfraExhausted)
	}
}

// A backend whose timeout_seconds is negative refuses the plan by name instead of emitting
// a request run would refuse at claim. Zero is not refused: it is an unbounded run whose
// lane lease carries no expiry (design D-RW1, docs/design/run-wall-ruling.md).
func TestPlanRefusesAPolicyWhoseBackendHasANegativeTimeout(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	policyPath := policyDoc(t, map[string]any{
		"default_backend": "first",
		"preference":      []string{"first"},
		"backends": map[string]any{
			"first": map[string]any{"adapter": "/bin/true", "model": "route-a", "timeout_seconds": -1},
		},
	})
	out := filepath.Join(scratch(t, "negative-timeout"), "decisions.json")
	code, output := invoke(t, "plan", "--store", storePath,
		"--input", tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policyPath), "--output", out)
	if code != 2 {
		t.Fatalf("plan exited %d, want 2: %s", code, output)
	}
	if !strings.Contains(string(output), "timeout_seconds") {
		t.Fatalf("the refusal %q does not name the field", output)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("a refused policy left an output file at %s", out)
	}
}

// A backend with no wall plans an unbounded request: timeout_ms 0 and no `--timeout` on the
// adapter's argv.
func TestPlanDispatchesAnUnboundedRequestWhenThePolicyNamesNoWall(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	policyPath := policyDoc(t, map[string]any{
		"default_backend": "first",
		"preference":      []string{"first"},
		"backends": map[string]any{
			"first": map[string]any{"adapter": "/bin/true", "model": "route-a"},
		},
	})
	out := filepath.Join(scratch(t, "unbounded"), "decisions.json")
	code, output := invoke(t, "plan", "--store", storePath,
		"--input", tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policyPath), "--output", out)
	if code != 0 {
		t.Fatalf("plan exited %d: %s", code, output)
	}
	request := readDecisions(t, out)[0].Payload.RunRequest
	if request == nil {
		t.Fatalf("the decision carries no run_request")
	}
	if request.TimeoutMS != 0 {
		t.Fatalf("the unbounded request carries timeout_ms %d, want 0", request.TimeoutMS)
	}
	for _, element := range request.Command {
		if element == "--timeout" {
			t.Fatalf("the unbounded request passes a wall to the adapter: %v", request.Command)
		}
	}
}

// An override naming an item the store does not hold is refused rather than ignored: the
// override map names items, so a stale entry is exactly the rot design §2 refuses
// mechanically instead of letting it fall through a silent default.
func TestPlanRefusesAnOverrideNamingAnUnknownItem(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	policyPath := twoBackendPolicy(t, 1, map[string]string{"item_999": "second"})
	out := filepath.Join(scratch(t, "stale-override"), "decisions.json")
	code, output := invoke(t, "plan", "--store", storePath,
		"--input", tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policyPath), "--output", out)
	if code != 2 {
		t.Fatalf("plan exited %d, want 2: %s", code, output)
	}
	if !strings.Contains(string(output), "item_999") {
		t.Fatalf("the refusal %q does not name the stale override", output)
	}
}

// seedGateVerdict appends the gate's judgment for one run: the store's record that a
// candidate was judged. A retry claim naming that run must be refused, whatever the run's
// own exit status says.
func seedGateVerdict(t *testing.T, storePath, runID string) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatalf("opening %s failed: %v", storePath, err)
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       drain.GateVerdictKind,
			ObjectType: "run",
			ObjectID:   &runID,
			Payload:    json.RawMessage(`{"result":"fail"}`),
			At:         time.Date(2026, 11, 9, 0, 5, 0, 0, time.UTC),
		})
		return err
	}); err != nil {
		t.Fatalf("appending the gate verdict for %q failed: %v", runID, err)
	}
}

// legacyItemState rewrites one item's row to a state the write face no longer accepts,
// which is how a case constructs a row written before the freeze. The seed path refuses it
// on purpose: the freeze is the rule, and the legacy row is the exception a reader must
// still be able to read and move out.
func legacyItemState(t *testing.T, storePath, itemID, state string) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatalf("opening %s failed: %v", storePath, err)
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = ?`, state, itemID)
		return err
	}); err != nil {
		t.Fatalf("writing item %q as legacy %q failed: %v", itemID, state, err)
	}
}

// seedRefusal appends the `drain.refusal` event the loop writes when a candidate is
// refused before the gate judged it — the fence refused it, or its run committed nothing.
// The run's own record cannot say which, so the planner reads this event.
func seedRefusal(t *testing.T, storePath, runID, step string) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatalf("opening %s failed: %v", storePath, err)
	}
	defer s.Close()
	payload, err := json.Marshal(map[string]string{"step": step})
	if err != nil {
		t.Fatalf("encoding the refusal payload failed: %v", err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       drain.RefusalKind,
			ObjectType: "run",
			ObjectID:   &runID,
			Payload:    payload,
			At:         time.Date(2026, 11, 9, 0, 5, 0, 0, time.UTC),
		})
		return err
	}); err != nil {
		t.Fatalf("appending the refusal for %q failed: %v", runID, err)
	}
}

// seedRetry writes one authorization row directly, bypassing the face. It is how a case
// reaches the plan's refusal rules for a claim the face would refuse to record: plan is the
// authority, and a race — the authorization written before the gate's verdict — is exactly a
// case the plan must still refuse.
func seedRetry(t *testing.T, storePath, itemID, runID, by, why, at string) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatalf("opening %s failed: %v", storePath, err)
	}
	defer s.Close()
	when, err := time.Parse(time.RFC3339, at)
	if err != nil {
		t.Fatalf("the case's own clock %q is not RFC 3339: %v", at, err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO retries (retry_id, item_id, supersedes_run_id, requested_by, reason, requested_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			"retry-"+itemID+"-"+runID, itemID, runID, by, why, when.UnixMilli(),
		)
		return err
	}); err != nil {
		t.Fatalf("seeding the retry for %q failed: %v", itemID, err)
	}
}

// authorizeRetry drives the retry face and answers its exit code and output.
func authorizeRetry(t *testing.T, storePath, itemID, runID, by, why, now string) (int, []byte) {
	t.Helper()
	return invoke(t, "retry", "--store", storePath, "--item", itemID, "--run", runID,
		"--by", by, "--why", why, "--now", now)
}

// authorizeRetryWith is authorizeRetry with the acknowledgment the face may carry. An
// empty acknowledgment is the bare retry, so the two spellings differ only in the flag the
// case is about.
func authorizeRetryWith(t *testing.T, storePath, itemID, runID, by, why, now, acknowledge string) (int, []byte) {
	t.Helper()
	args := []string{"retry", "--store", storePath, "--item", itemID, "--run", runID,
		"--by", by, "--why", why, "--now", now}
	if acknowledge != "" {
		args = append(args, "--acknowledge", acknowledge)
	}
	return invoke(t, args...)
}

// retryRows answers the store's authorizations in retry_id order, through the substrate's
// reader rather than by opening the database.
func retryRows(t *testing.T, storePath string) []drain.Retry {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		t.Fatalf("opening %s failed: %v", storePath, err)
	}
	defer s.Close()
	rows := []drain.Retry{}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		got, err := r.Query(
			`SELECT retry_id, item_id, supersedes_run_id, requested_by, reason, requested_at
			   FROM retries ORDER BY retry_id`)
		if err != nil {
			return err
		}
		defer got.Close()
		for got.Next() {
			var (
				row    drain.Retry
				millis int64
			)
			if err := got.Scan(&row.RetryID, &row.ItemID, &row.SupersedesRunID, &row.RequestedBy, &row.Reason, &millis); err != nil {
				return err
			}
			row.RequestedAt = time.UnixMilli(millis).UTC()
			rows = append(rows, row)
		}
		return got.Err()
	}); err != nil {
		t.Fatalf("reading the retries rows failed: %v", err)
	}
	return rows
}

// The item-authorized retry: a run the harness refused — the adapter's own preflight exit 2,
// the class a mandatory fence or a stale binary also leaves — is re-dispatched because a
// person authorized it, and the decision names the run it supersedes.
//
// This is A-NORETRY's closing test: the same fixture without the authorization holds with
// `run-link-missing`, and the only difference the retry turns on is the recorded claim.
func TestPlanRetriesARunTheHarnessRefused(t *testing.T) {
	const failedRun = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(failedRun, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
	)
	if code, output := authorizeRetry(t, storePath, "item_042", failedRun,
		"the owner", "the harness refused before the work started", "2026-11-10T00:00:00Z"); code != 0 {
		t.Fatalf("retry exited %d: %s", code, output)
	}
	tickPath := tick(t, "2026-11-10T00:01:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionRetry || got.Reason != drain.ReasonRetry {
		t.Fatalf("the decision is %q / %q, want retry / %s", got.Action, got.Reason, drain.ReasonRetry)
	}
	if got.SupersedesRunID != failedRun {
		t.Fatalf("the decision supersedes %q, want %q", got.SupersedesRunID, failedRun)
	}
	if got.RetryID != "retry-item_042-"+failedRun {
		t.Fatalf("the decision names authorization %q", got.RetryID)
	}
	if got.RunRequest == nil {
		t.Fatalf("a retry carries no run_request")
	}
	if got.RunRequest.RunID == failedRun {
		t.Fatalf("the retry reused the failed run's id %q: a re-dispatch is a new run", failedRun)
	}
	if got.RunRequest.ItemID != "item_042" || got.RunRequest.LaneID != "lane-1" {
		t.Fatalf("the retry request names item %q on lane %q", got.RunRequest.ItemID, got.RunRequest.LaneID)
	}
}

// The same fixture without an authorization is still a parking space by design: the link
// exists, but nobody asked for the second attempt, so the item holds. This is the control
// the retry test above is read against.
func TestPlanHoldsARefusedRunWithoutAnAuthorization(t *testing.T) {
	const failedRun = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(failedRun, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
	)
	tickPath := tick(t, "2026-11-10T00:01:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionHold {
		t.Fatalf("the decision is %q, want a hold without an authorization", got.Action)
	}
	if got.SupersedesRunID != "" || got.RetryID != "" {
		t.Fatalf("a hold names the authorization it does not have: retry %q supersedes %q", got.RetryID, got.SupersedesRunID)
	}
}

// The distinction criterion 3 is about: the gate judged this candidate, so the claim is
// refused rather than guessed at. The run exited 0 — a fence refusal looks exactly like an
// answered run by exit status alone — and only the gate's own `gate.verdict` event tells
// the two apart.
func TestPlanHoldsARetryWhoseRunTheGateJudged(t *testing.T) {
	const judgedRun = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(judgedRun, "item_042", "lane-9", "fixture", run.StateExited, exitCode(0), ""),
	)
	seedGateVerdict(t, storePath, judgedRun)
	// The face refuses a judged run, so the row is seeded directly: this is the race the
	// plan's own check exists for, not a claim the CLI would have recorded.
	seedRetry(t, storePath, "item_042", judgedRun, "the owner", "a mistaken claim", "2026-11-10T00:00:00Z")
	tickPath := tick(t, "2026-11-10T00:01:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonRetryJudged {
		t.Fatalf("the decision is %q / %q, want hold / %s", got.Action, got.Reason, drain.ReasonRetryJudged)
	}
	if got.SupersedesRunID != judgedRun {
		t.Fatalf("the hold supersedes %q, want the run the claim names", got.SupersedesRunID)
	}
	if got.RunRequest != nil {
		t.Fatalf("a refused retry still carried a run_request to %q", got.RunRequest.RunID)
	}
}

// A run a wall cut short is not the harness refusing: it is unjudged work a person inspects,
// so the claim is refused with its own reason rather than re-dispatched.
func TestPlanHoldsARetryWhoseRunAWallCutShort(t *testing.T) {
	const cutShort = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(cutShort, "item_042", "lane-9", "fixture", run.StateTimedOut, nil, ""),
	)
	seedRetry(t, storePath, "item_042", cutShort, "the owner", "a mistaken claim", "2026-11-10T00:00:00Z")
	tickPath := tick(t, "2026-11-10T00:01:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonRetryUnjudged {
		t.Fatalf("the decision is %q / %q, want hold / %s", got.Action, got.Reason, drain.ReasonRetryUnjudged)
	}
}

// A run a wall cut short can also have committed, and a committed candidate is judged
// before its wall status (design D-RW2). Once the gate has spoken, the item goes back to its
// worker with the gate's evidence (`returned-with-evidence`), even though the run is also
// `AttemptUnjudged`: a judged candidate's next act is the gate's.
func TestPlanHoldsAWalledRunByTheGatesVerdict(t *testing.T) {
	const cutShort = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(cutShort, "item_042", "lane-9", "fixture", run.StateTimedOut, nil, ""),
	)
	seedGateVerdict(t, storePath, cutShort)
	tickPath := tick(t, "2026-11-10T00:01:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionDispatch || got.Reason != drain.ReasonReturned {
		t.Fatalf("the decision is %q / %q, want dispatch / %s: a judged candidate's next act is the gate's",
			got.Action, got.Reason, drain.ReasonReturned)
	}
}

// ...and the acknowledgment such a run accepts is the gate's. Demanding `unjudged` as well
// would make every acknowledgment unsatisfiable, which is the deadlock the guard exists to
// prevent.
func TestPlanRetriesAWalledRunWithTheGatesAcknowledgment(t *testing.T) {
	const cutShort = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(cutShort, "item_042", "lane-9", "fixture", run.StateTimedOut, nil, ""),
	)
	seedGateVerdict(t, storePath, cutShort)
	if code, output := authorizeRetryWith(t, storePath, "item_042", cutShort,
		"the owner", "the gate failed", "2026-11-10T00:00:00Z", drain.RetryAckGateFailed); code != 0 {
		t.Fatalf("retry exited %d: %s", code, output)
	}
	tickPath := tick(t, "2026-11-10T00:01:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionRetry {
		t.Fatalf("the decision is %q / %q, want a retry naming the walled run", got.Action, got.Reason)
	}
	if got.SupersedesRunID != cutShort {
		t.Fatalf("the retry supersedes %q, want %q", got.SupersedesRunID, cutShort)
	}
}

// The negative control: the same judged-and-walled run does not accept the wall's own
// acknowledgment, because the gate's word is the one that governs it.
func TestRetryFaceRefusesTheWallsAcknowledgmentOnAJudgedRun(t *testing.T) {
	const cutShort = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(cutShort, "item_042", "lane-9", "fixture", run.StateTimedOut, nil, ""),
	)
	seedGateVerdict(t, storePath, cutShort)
	code, output := authorizeRetryWith(t, storePath, "item_042", cutShort,
		"the owner", "the gate failed", "2026-11-10T00:00:00Z", drain.RetryAckUnjudged)
	if code != 2 {
		t.Fatalf("retry exited %d for a judged run acknowledged as unjudged, want 2: %s", code, output)
	}
}

// An authorization naming a run that is no longer the item's latest terminal run cannot be
// the retry's link, and it no longer speaks: the item routes by its latest run, as with no
// authorization. It is not held as `retry-stale` (the owner's ruling of 2026-11-17).
func TestPlanIgnoresAStaleRetryAuthorization(t *testing.T) {
	const (
		older = "run-item_042-20261109T000000Z"
		newer = "run-item_042-20261109T010000Z"
	)
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(older, "item_042", "lane-8", "fixture", run.StateExited, exitCode(2), ""),
		runRecord(newer, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
	)
	seedRetry(t, storePath, "item_042", older, "the owner", "the older run", "2026-11-10T00:00:00Z")
	tickPath := tick(t, "2026-11-10T00:01:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action == drain.ActionRetry || got.Reason == "retry-stale" || got.RetryID != "" || got.SupersedesRunID != "" {
		t.Fatalf("a stale authorization still spoke: %+v", got)
	}
}

// One authorization is one retry. Once the retry's own run is the item's latest terminal
// run, the authorization that named the previous run no longer applies — the retry run's row
// is what retires it — and the item holds until a person authorizes the new run.
func TestPlanRetriesOncePerAuthorization(t *testing.T) {
	const (
		failedRun = "run-item_042-20261109T000000Z"
		retryRun  = "run-item_042-20261109T010000Z"
	)
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(failedRun, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
	)
	if code, output := authorizeRetry(t, storePath, "item_042", failedRun,
		"the owner", "the harness refused", "2026-11-10T00:00:00Z"); code != 0 {
		t.Fatalf("retry exited %d: %s", code, output)
	}
	first := plan(t, storePath, tick(t, "2026-11-10T00:01:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true")))[0].Payload
	if first.Action != drain.ActionRetry {
		t.Fatalf("the first plan is %q / %q, want a retry", first.Action, first.Reason)
	}

	seedRunRecord(t, storePath, runRecord(retryRun, "item_042", "lane-1", "fixture", run.StateExited, exitCode(2), ""))
	second := plan(t, storePath, tick(t, "2026-11-10T00:02:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true")))[0].Payload
	if second.Action != drain.ActionHold {
		t.Fatalf("the second plan is %q / %q, want a hold: one authorization is one retry", second.Action, second.Reason)
	}
}

// seedRunRecord adds one run.record to an existing fixture store, which is how a case after
// an authorization models the retry's own run arriving.
func seedRunRecord(t *testing.T, storePath, record string) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatalf("opening %s failed: %v", storePath, err)
	}
	defer s.Close()
	raw, err := json.Marshal(map[string]any{
		"kind":    "fixture.seed",
		"payload": map[string]any{"records": []json.RawMessage{json.RawMessage(record)}},
	})
	if err != nil {
		t.Fatalf("building the seed bundle failed: %v", err)
	}
	if err := s.Seed(ctx, raw); err != nil {
		t.Fatalf("seeding the run failed: %v", err)
	}
}

// The face's own write: the authorization is recorded with who asked, why and which run it
// supersedes, a `drain.retry` record is echoed, and a legacy `in-progress` item — a row
// written before the state was frozen — goes back in the queue.
func TestRetryFaceRecordsTheAuthorizationAndRequeuesTheItem(t *testing.T) {
	const failedRun = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(failedRun, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
	)
	legacyItemState(t, storePath, "item_042", "in-progress")
	code, output := driveRecord(t, bin, "retry", "--store", storePath, "--item", "item_042", "--run", failedRun,
		"--by", "the owner", "--why", "the harness refused before the work started", "--now", "2026-11-10T00:00:00Z")
	if code != 0 {
		t.Fatalf("retry exited %d: %s", code, output)
	}
	var echoed struct {
		Kind    string      `json:"kind"`
		ID      string      `json:"id"`
		Payload drain.Retry `json:"payload"`
	}
	if err := json.Unmarshal(output, &echoed); err != nil {
		t.Fatalf("the face's output is not a record: %v\n%s", err, output)
	}
	if echoed.Kind != drain.RetryKind || echoed.ID != "retry-item_042-"+failedRun {
		t.Fatalf("the output is %q / %q, want %s / retry-item_042-%s", echoed.Kind, echoed.ID, drain.RetryKind, failedRun)
	}
	if echoed.Payload.RequestedBy != "the owner" || echoed.Payload.Reason != "the harness refused before the work started" {
		t.Fatalf("the authorization names %q / %q", echoed.Payload.RequestedBy, echoed.Payload.Reason)
	}

	rows := retryRows(t, storePath)
	if len(rows) != 1 {
		t.Fatalf("the store holds %d authorizations, want 1", len(rows))
	}
	if rows[0].ItemID != "item_042" || rows[0].SupersedesRunID != failedRun {
		t.Fatalf("the row names item %q / run %q", rows[0].ItemID, rows[0].SupersedesRunID)
	}
	if got := itemStateOf(t, storePath, "item_042"); got != "in-queue" {
		t.Fatalf("the failed item is %q after the retry, want in-queue", got)
	}
}

// The negative direction the face owns: a run the gate judged is refused at the face, so an
// operator learns immediately instead of leaving a claim the plan can never honour.
func TestRetryFaceRefusesAJudgedRun(t *testing.T) {
	const judgedRun = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(judgedRun, "item_042", "lane-9", "fixture", run.StateExited, exitCode(0), ""),
	)
	seedGateVerdict(t, storePath, judgedRun)

	code, output := authorizeRetry(t, storePath, "item_042", judgedRun,
		"the owner", "a mistaken claim", "2026-11-10T00:00:00Z")
	if code != 2 {
		t.Fatalf("retry exited %d, want 2: %s", code, output)
	}
	if !strings.Contains(string(output), "judged by the gate") {
		t.Fatalf("the refusal %q does not name the gate's judgment", output)
	}
	if rows := retryRows(t, storePath); len(rows) != 0 {
		t.Fatalf("a refused retry left %d authorization(s) in the store", len(rows))
	}
}

// A wall cut a run short, and that is not the harness refusing: the face refuses it by name
// so the operator inspects the tree instead of buying the same wall twice.
func TestRetryFaceRefusesAnUnjudgedRun(t *testing.T) {
	const cutShort = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(cutShort, "item_042", "lane-9", "fixture", run.StateTimedOut, nil, ""),
	)

	code, output := authorizeRetry(t, storePath, "item_042", cutShort,
		"the owner", "a mistake", "2026-11-10T00:00:00Z")
	if code != 2 {
		t.Fatalf("retry exited %d, want 2: %s", code, output)
	}
	if !strings.Contains(string(output), "cut short by a wall") {
		t.Fatalf("the refusal %q does not name the wall", output)
	}
	if rows := retryRows(t, storePath); len(rows) != 0 {
		t.Fatalf("a refused retry left %d authorization(s) in the store", len(rows))
	}
}

// The authorization survives: a second retry of the same run is refused rather than
// overwriting the first record, which is the defect the criterion names.
func TestRetryFaceRefusesADuplicateAuthorization(t *testing.T) {
	const failedRun = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(failedRun, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
	)
	if code, output := authorizeRetry(t, storePath, "item_042", failedRun,
		"the owner", "the harness refused", "2026-11-10T00:00:00Z"); code != 0 {
		t.Fatalf("the first retry exited %d: %s", code, output)
	}
	code, output := authorizeRetry(t, storePath, "item_042", failedRun,
		"someone else", "a second claim", "2026-11-10T00:05:00Z")
	if code != 2 {
		t.Fatalf("the duplicate retry exited %d, want 2: %s", code, output)
	}
	if !strings.Contains(string(output), "already authorized") {
		t.Fatalf("the refusal %q does not say the run is already authorized", output)
	}
	rows := retryRows(t, storePath)
	if len(rows) != 1 || rows[0].RequestedBy != "the owner" {
		t.Fatalf("the duplicate overwrote the first record: %+v", rows)
	}
}

// The structural refusals: an item the store does not hold, a run it does not hold, a run
// that belongs to another item, and a terminal item a retry must not re-open.
func TestRetryFaceRefusesWhatItCannotLink(t *testing.T) {
	const failedRun = "run-item_042-20261109T000000Z"
	cases := map[string]struct {
		item string
		run  string
		want string
	}{
		"the item is not in the store": {item: "item_999", run: failedRun, want: "no item"},
		"the run is not in the store":  {item: "item_042", run: "run-item_042-20260101T000000Z", want: "no run"},
		"the run is another item's":    {item: "item_043", run: failedRun, want: "belongs to item"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			storePath := fixture(t,
				itemRecord("item_042", "in-queue"),
				itemRecord("item_043", "in-queue"),
				runRecord(failedRun, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
			)
			code, output := authorizeRetry(t, storePath, c.item, c.run,
				"the owner", "a reason", "2026-11-10T00:00:00Z")
			if code != 2 {
				t.Fatalf("retry exited %d, want 2: %s", code, output)
			}
			if !strings.Contains(string(output), c.want) {
				t.Fatalf("the refusal %q does not name %q", output, c.want)
			}
		})
	}

	t.Run("the item is terminal", func(t *testing.T) {
		storePath := fixture(t,
			itemRecord("item_042", "landed"),
			runRecord(failedRun, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
		)
		code, output := authorizeRetry(t, storePath, "item_042", failedRun,
			"the owner", "a reason", "2026-11-10T00:00:00Z")
		if code != 2 {
			t.Fatalf("retry exited %d, want 2: %s", code, output)
		}
		if !strings.Contains(string(output), "terminal item") {
			t.Fatalf("the refusal %q does not name the terminal item", output)
		}
	})
}

// On 2026-11-17 an operator facing eight owner holds could not learn from any retry
// refusal which spelling the face would accept. Every refusal names the exact command it
// would accept, or says that none exists and who moves (docs/design/landing-principles.md,
// section 13).
func TestEveryRetryRefusalNamesTheAcceptedCommandOrTheMover(t *testing.T) {
	const r = "run-item_042-20261109T000000Z"
	accepted := func(item, ack string) string {
		cmd := "tillr drain retry --item " + item + " --run " + r + " --by <who> --why <reason>"
		if ack != "" {
			cmd += " --acknowledge " + ack
		}
		return cmd
	}
	cases := map[string]struct {
		item, state, runState, ack string
		code                       *int
		judged                     bool
		want                       string
	}{
		"a judged run":                     {item: "item_042", state: "in-queue", runState: run.StateExited, code: exitCode(0), judged: true, want: accepted("item_042", drain.RetryAckGateFailed)},
		"a run a wall cut short":           {item: "item_042", state: "in-queue", runState: run.StateTimedOut, want: accepted("item_042", drain.RetryAckUnjudged)},
		"the wall's ack on a judged run":   {item: "item_042", state: "in-queue", runState: run.StateTimedOut, judged: true, ack: drain.RetryAckUnjudged, want: accepted("item_042", drain.RetryAckGateFailed)},
		"the gate's ack on a harness fail": {item: "item_042", state: "in-queue", runState: run.StateExited, code: exitCode(2), ack: drain.RetryAckGateFailed, want: accepted("item_042", "") + "\n"},
		"an unknown acknowledgment":        {item: "item_042", state: "in-queue", runState: run.StateTimedOut, ack: "maybe", want: accepted("item_042", drain.RetryAckUnjudged)},
		"another item's run":               {item: "item_043", state: "in-queue", runState: run.StateExited, code: exitCode(2), want: accepted("item_042", "")},
		"a paused run":                     {item: "item_042", state: "in-queue", runState: run.StatePaused, want: "No retry command is accepted: the loop moves"},
		"a terminal item":                  {item: "item_042", state: "landed", runState: run.StateExited, code: exitCode(2), want: "No retry command is accepted: the owner moves: tillr item reopen"},
		"an item the store does not hold":  {item: "item_999", state: "in-queue", runState: run.StateExited, code: exitCode(2), want: "No retry command is accepted: the owner moves: tillr item apply"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			state42 := "in-queue"
			if c.item == "item_042" {
				state42 = c.state
			}
			storePath := fixture(t,
				itemRecord("item_042", state42),
				itemRecord("item_043", "in-queue"),
				runRecord(r, "item_042", "lane-9", "fixture", c.runState, c.code, ""),
			)
			if c.judged {
				seedGateVerdict(t, storePath, r)
			}
			code, output := authorizeRetryWith(t, storePath, c.item, r, "the owner", "a reason", "2026-11-10T00:00:00Z", c.ack)
			if code != 2 {
				t.Fatalf("retry exited %d, want 2: %s", code, output)
			}
			if !strings.Contains(string(output), c.want) {
				t.Fatalf("the refusal does not name %q:\n%s", c.want, output)
			}
		})
	}

	t.Run("a run the store does not hold", func(t *testing.T) {
		storePath := fixture(t,
			itemRecord("item_042", "in-queue"),
			runRecord(r, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
		)
		code, output := authorizeRetry(t, storePath, "item_042", "run-item_042-20260101T000000Z", "the owner", "a reason", "2026-11-10T00:00:00Z")
		if code != 2 || !strings.Contains(string(output), "latest finished run is "+r) || !strings.Contains(string(output), accepted("item_042", "")) {
			t.Fatalf("the refusal does not name the item's latest finished run (exit %d):\n%s", code, output)
		}
	})

	t.Run("a duplicate authorization", func(t *testing.T) {
		storePath := fixture(t,
			itemRecord("item_042", "in-queue"),
			runRecord(r, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
		)
		if code, output := authorizeRetry(t, storePath, "item_042", r, "the owner", "a reason", "2026-11-10T00:00:00Z"); code != 0 {
			t.Fatalf("the first retry exited %d: %s", code, output)
		}
		code, output := authorizeRetry(t, storePath, "item_042", r, "the owner", "again", "2026-11-10T00:01:00Z")
		if code != 2 || !strings.Contains(string(output), "No retry command is accepted: the loop moves") {
			t.Fatalf("the duplicate refusal does not name the loop (exit %d):\n%s", code, output)
		}
	})
}

// The retry face's help names both forms — a build retry and a design run's re-raise — and
// that every refusal names the command it would accept.
func TestRetryHelpNamesTheDesignRunForm(t *testing.T) {
	code, out := invoke(t, "retry", "--help")
	if code != 0 {
		t.Fatalf("retry --help exited %d: %s", code, out)
	}
	for _, want := range []string{"design run", "raises the item's conflict again", "names the exact command it would accept"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("retry --help does not say %q:\n%s", want, out)
		}
	}
}

// TestHelpIsTheFaceSurface pins the layer-1 surface (docs/design/tillr-distribution.md
// Section 3): `--help` names the faces and a face's `--help` names that face's flags, both
// on stdout and both exit 0.
func TestHelpIsTheFaceSurface(t *testing.T) {
	code, out := invoke(t, "--help")
	if code != 0 {
		t.Fatalf("--help exited %d, want 0: %s", code, out)
	}
	for _, face := range []string{"plan", "once", "retry", "boundary", "loop"} {
		if !strings.Contains(string(out), face) {
			t.Errorf("--help does not name the face %q: %s", face, out)
		}
	}
	code, out = invoke(t, "plan", "--help")
	if code != 0 {
		t.Fatalf("plan --help exited %d, want 0: %s", code, out)
	}
	if !strings.Contains(string(out), "Usage of tillr-drain plan") {
		t.Errorf("plan --help does not name the face's flags: %s", out)
	}
	if code, out = invoke(t, "bogus", "--help"); code != 2 {
		t.Errorf("a face that does not exist exited %d, want 2: %s", code, out)
	}
}

// The question hold, from the index: an item whose design document carries a question
// Vantage's index reports as unanswered holds with reason `question-open` and names the
// question, and the same item dispatches the moment the index reports the question settled.
// Nothing about the item changed between the two plans — the answer did — which is the whole
// point of reading the index rather than a copy of its state.
func TestPlanHoldsAnItemWhoseDesignDocumentCarriesAnOpenQuestion(t *testing.T) {
	root := fixtureRepo(t, "docs/design/open.md")
	storePath := fixture(t, itemRecordWithDoc("item_042", "in-queue", "docs/design/open.md"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	open := indexStub(t, map[string]any{
		"path": "docs/design/open.md",
		"questions": []map[string]any{
			{"id": "OQ-1", "state": "open", "title": "OQ-1: which way?", "line": 12},
		},
	})
	got := decisionFor(t, planFrom(t, storePath, tickPath, root, open), "item_042")
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonQuestionOpen {
		t.Fatalf("the decision is %q / %q, want hold / %s", got.Action, got.Reason, drain.ReasonQuestionOpen)
	}
	if got.QuestionID != "OQ-1" {
		t.Fatalf("the decision names question %q, want OQ-1", got.QuestionID)
	}
	if got.RunRequest != nil {
		t.Fatalf("a question-open hold carries a run_request: %+v", got.RunRequest)
	}

	settled := indexStub(t, map[string]any{
		"path": "docs/design/open.md",
		"questions": []map[string]any{
			{"id": "OQ-1", "state": drain.QuestionAnswered, "title": "OQ-1: which way?", "line": 12},
		},
	})
	got = decisionFor(t, planFrom(t, storePath, tickPath, root, settled), "item_042")
	if got.Action != drain.ActionDispatch || got.Reason != drain.ReasonReady {
		t.Fatalf("the settled decision is %q / %q, want dispatch / %s", got.Action, got.Reason, drain.ReasonReady)
	}
	if got.QuestionID != "" {
		t.Fatalf("a dispatch names question %q: only a question-open hold names one", got.QuestionID)
	}
}

// An item whose design document is not in the tree holds as `design-doc-missing` — and it
// stays that answer even when the index answers, because the tree is what says the file is
// not there.
func TestPlanHoldsAnItemWhoseDesignDocumentIsGone(t *testing.T) {
	root := fixtureRepo(t)
	storePath := fixture(t, itemRecordWithDoc("item_042", "in-queue", "docs/design/gone.md"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := decisionFor(t, planFrom(t, storePath, tickPath, root, indexStub(t)), "item_042")
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonDesignDocMissing {
		t.Fatalf("the decision is %q / %q, want hold / %s", got.Action, got.Reason, drain.ReasonDesignDocMissing)
	}
	if got.QuestionID != "" {
		t.Fatalf("a design-doc-missing hold names question %q: there is no document to read one from", got.QuestionID)
	}
}

// An index that cannot be read is `questions-unknown`, never "no questions": a tool that is
// absent, that fails, or that answers something which is not an index leaves the item held
// rather than dispatching it on a guess.
func TestPlanHoldsAnItemWhenTheQuestionIndexCannotBeRead(t *testing.T) {
	root := fixtureRepo(t, "docs/design/open.md")
	storePath := fixture(t, itemRecordWithDoc("item_042", "in-queue", "docs/design/open.md"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	cases := map[string]string{
		"the tool is not there":     "definitely-not-a-vantage-tool-7f3a index --format json",
		"the tool fails":            "false",
		"the tool answers no index": `echo {"index":{}}`,
		"the tool refuses the tree": `echo {"index":{"refused":true,"documents":[]}}`,
	}
	for name, command := range cases {
		t.Run(name, func(t *testing.T) {
			got := decisionFor(t, planFrom(t, storePath, tickPath, root, command), "item_042")
			if got.Action != drain.ActionHold || got.Reason != drain.ReasonQuestionsUnknown {
				t.Fatalf("the decision is %q / %q, want hold / %s", got.Action, got.Reason, drain.ReasonQuestionsUnknown)
			}
			if got.RunRequest != nil {
				t.Fatalf("a questions-unknown hold carries a run_request: %+v", got.RunRequest)
			}
		})
	}
}
