package drain_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

var (
	drainBinOnce sync.Once
	drainBinDirV string
	drainBinPath string
	drainBinErr  error

	fixtureStoreTplOnce sync.Once
	fixtureStoreTplPath string
	fixtureStoreTplErr  error
)

func scratchRoot() string {
	root := os.Getenv("YOLO_DURABLE_DIR")
	if root == "" {
		root = filepath.Join("..", "..", "..", ".yolo", "durable")
	}
	if !filepath.IsAbs(root) {
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
	}
	return root
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

// processEnvironment is the environment this test process started with, read before any
// case replaces PATH.
var processEnvironment = os.Environ()

// buildDrainBinary builds the program the command-line cases run. It builds in the
// environment the process started with, so a case's stubbed tools never take part.
func buildDrainBinary(target string) ([]byte, error) {
	build := exec.Command("go", "build", "-o", target, "..")
	build.Env = processEnvironment
	return build.CombinedOutput()
}

func ensureDrainBinDir(t *testing.T) string {
	t.Helper()
	drainBinOnce.Do(func() {
		root := scratchRoot()
		if err := os.MkdirAll(root, 0o755); err != nil {
			drainBinErr = err
			return
		}
		dir, err := os.MkdirTemp(root, "tillr-drain-bin-")
		if err != nil {
			drainBinErr = err
			return
		}
		drain.RegisterTestCleanup(func() { _ = os.RemoveAll(dir) })
		drainBinDirV = dir
		drainBinPath = filepath.Join(dir, "tillr-drain")
		if out, err := buildDrainBinary(drainBinPath); err != nil {
			drainBinErr = os.ErrInvalid
			t.Logf("building tillr-drain failed: %v\n%s", err, out)
		}
	})
	if drainBinErr != nil {
		t.Fatalf("preparing tillr-drain binary failed: %v", drainBinErr)
	}
	return drainBinDirV
}

func drainBinDir(t *testing.T) string {
	t.Helper()
	return ensureDrainBinDir(t)
}

func drainBin(t *testing.T) string {
	t.Helper()
	ensureDrainBinDir(t)
	return drainBinPath
}

func invoke(t *testing.T, args ...string) (int, []byte) {
	t.Helper()
	cmd := exec.Command(drainBin(t), args...)
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

func getFixtureStoreTemplate(t *testing.T) string {
	t.Helper()
	fixtureStoreTplOnce.Do(func() {
		root := scratchRoot()
		if err := os.MkdirAll(root, 0o755); err != nil {
			fixtureStoreTplErr = err
			return
		}
		dir, err := os.MkdirTemp(root, "tillr-drain-store-tpl-")
		if err != nil {
			fixtureStoreTplErr = err
			return
		}
		drain.RegisterTestCleanup(func() { _ = os.RemoveAll(dir) })
		tplDB := filepath.Join(dir, "board.sqlite3")
		s, err := store.InitFixture(context.Background(), tplDB)
		if err != nil {
			fixtureStoreTplErr = err
			return
		}
		if err := s.Close(); err != nil {
			fixtureStoreTplErr = err
			return
		}
		db, err := sql.Open("sqlite", "file:"+tplDB)
		if err != nil {
			fixtureStoreTplErr = err
			return
		}
		_, ckptErr := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		closeErr := db.Close()
		if ckptErr != nil {
			fixtureStoreTplErr = ckptErr
			return
		}
		if closeErr != nil {
			fixtureStoreTplErr = closeErr
			return
		}
		fixtureStoreTplPath = tplDB
	})
	if fixtureStoreTplErr != nil {
		t.Fatalf("initializing fixture store template: %v", fixtureStoreTplErr)
	}
	return fixtureStoreTplPath
}

func cloneFixtureStoreTemplate(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(getFixtureStoreTemplate(t))
	if err != nil {
		t.Fatalf("reading fixture store template failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating fixture store directory failed: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("copying fixture store template failed: %v", err)
	}
}

// fixture seeds a fixture store from the records given, in the order given: item's
// materializer refuses an edge to an item that does not exist yet, so a blocker is
// seeded before the item that names it.
func fixture(t *testing.T, records ...string) string {
	t.Helper()
	path := filepath.Join(scratch(t, "store"), "board.sqlite3")
	cloneFixtureStoreTemplate(t, path)
	if len(records) == 0 {
		return path
	}
	ctx := context.Background()
	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatalf("opening the fixture store failed: %v", err)
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
	Position         string       `json:"position"`
	Transition       string       `json:"transition"`
	DecisionID       string       `json:"decision_id"`
	ItemID           string       `json:"item_id"`
	Action           string       `json:"action"`
	Reason           string       `json:"reason"`
	LaneID           string       `json:"lane_id"`
	RunRequest       *run.Request `json:"run_request"`
	RetryID          string       `json:"retry_id"`
	SupersedesRunID  string       `json:"supersedes_run_id"`
	DeferredBy       string       `json:"deferred_by"`
	BackendHoldCause string       `json:"backend_hold_cause"`
	QuestionID       string       `json:"question_id"`
	QuestionRefs     []string     `json:"question_refs"`
	Seat             string       `json:"seat"`
}

// decisionRecord is one record of the output file.
type decisionRecord struct {
	Kind    string   `json:"kind"`
	ID      string   `json:"id"`
	Payload decision `json:"payload"`
}

// plan runs the face with no fork catalog and an index that reports nothing
// open. Tests of repository-dependent decisions name their tree in planFrom;
// the live checkout's fork catalog and documents are not fixture inputs.
func plan(t *testing.T, storePath, tickPath string) []decisionRecord {
	t.Helper()
	return planFrom(t, storePath, tickPath, fixtureRepo(t), indexStub(t))
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

// decisionIDs answers the items a tick decided about, in the order it decided them, so a
// failure prints the order the face produced rather than one item of it.
func decisionIDs(records []decisionRecord) []string {
	ids := make([]string, len(records))
	for i, record := range records {
		ids[i] = record.Payload.ItemID
	}
	return ids
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

// reasonIs reports whether a recorded reason is want or want with a detail after "<want>: ".
func reasonIs(got, want string) bool {
	return got == want || strings.HasPrefix(got, want+": ")
}
