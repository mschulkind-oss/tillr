package run_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// scratch answers a directory under $YOLO_DURABLE_DIR, never /tmp: a suite that writes
// to /tmp loses its evidence when the box is cleaned, and the owner refuses it.
func scratch(t *testing.T) string {
	t.Helper()
	root := os.Getenv("YOLO_DURABLE_DIR")
	if root == "" {
		root = filepath.Join("..", "..", "..", ".yolo", "durable")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("cannot create the scratch root: %v", err)
	}
	dir, err := os.MkdirTemp(root, "run-conformance-")
	if err != nil {
		t.Fatalf("cannot create the scratch directory: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// The run utility's conformance case: the two things a second consumer of this package
// would break by linking it — the run kinds registered with their owner, and the run
// tables present in the composed migration list.
//
// It lives in the utility's own package so the conformance suite grows one case per
// landed face, and no central file is edited to add one.
func TestTheRunUtilityRegistersItsOwnContract(t *testing.T) {
	for _, kind := range []string{run.RecordKind, run.AllocateKind, run.FinishKind} {
		registered, err := event.Lookup(kind)
		if err != nil {
			t.Fatalf("the run kind %q is not registered: %v", kind, err)
		}
		if registered.Owner != "run" {
			t.Fatalf("%s is owned by %q, want run", kind, registered.Owner)
		}
	}
	s, err := store.InitFixture(context.Background(), filepath.Join(scratch(t), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	counts, err := s.TableCounts()
	if err != nil {
		t.Fatalf("reading the table counts failed: %v", err)
	}
	for _, table := range []string{"lanes", "runs"} {
		if _, ok := counts[table]; !ok {
			t.Fatalf("the run table %q is not in the composed migration list: %v", table, counts)
		}
	}
}

// The backend field is part of the contract rather than a convenience: a request that
// names none is refused before anything is created, and a request that names one carries
// it onto the record and onto the runs row, so a reader answers "which backend ran this"
// from the record instead of guessing it from the command's argv.
func TestTheBackendFieldTravelsFromRequestToRecordAndRow(t *testing.T) {
	ctx := context.Background()
	dir := scratch(t)
	s, err := store.InitFixture(ctx, filepath.Join(dir, "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	laneRoot := filepath.Join(dir, "lanes")
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)

	t.Run("a request that names no backend is refused, and writes nothing", func(t *testing.T) {
		stored, err := run.Exec(ctx, s, run.Request{
			RunID: "run_no_backend", Command: []string{"/bin/true"}, WorkingDir: ".", TimeoutMS: 60000,
		}, run.ExecOptions{LaneRoot: laneRoot, Now: now})
		if err == nil {
			t.Fatalf("a request with no backend produced a record: %+v", stored)
		}
		if code := run.ExitCode(err); code != record.ExitUsage {
			t.Fatalf("the refusal exits %d, want %d (a malformed request): %v", code, record.ExitUsage, err)
		}
		if stored.Kind != "" {
			t.Fatalf("a refused request emitted a %q record", stored.Kind)
		}
		if _, found := storedRun(t, s, "run_no_backend"); found {
			t.Fatalf("a refused request left a run row")
		}
	})

	t.Run("a request that names one carries it onto the record and the row", func(t *testing.T) {
		stored, err := run.Exec(ctx, s, run.Request{
			RunID: "run_muse", Backend: "muse",
			Command: []string{"/bin/true"}, WorkingDir: ".", TimeoutMS: 60000,
		}, run.ExecOptions{LaneRoot: laneRoot, Now: now})
		if err != nil {
			t.Fatalf("executing the request failed: %v", err)
		}
		if got := recordOf(t, stored).Backend; got != "muse" {
			t.Fatalf("the record names backend %q, want the request's %q", got, "muse")
		}
		// The row is what a read face answers from, so the field has to be there too:
		// a record carrying a backend over a row that drops it is the coupling this
		// case exists to hold.
		row, found := storedRun(t, s, "run_muse")
		if !found {
			t.Fatalf("the run is not in the store after it executed")
		}
		if row.Backend != "muse" {
			t.Fatalf("the runs row names backend %q, want %q", row.Backend, "muse")
		}
	})
}

// The item field is D-UT19's ruling: a run names the item it was dispatched for, copied
// from the request onto the record and materialized on the runs row, so a run is
// traceable to the work behind it. A run dispatched for no item still runs, and nothing
// pretends it named one: the record carries no item_id key at all rather than an empty
// string, and the row holds NULL for the same answer.
func TestTheItemIDTravelsFromRequestToRecordAndRow(t *testing.T) {
	ctx := context.Background()
	dir := scratch(t)
	s, err := store.InitFixture(ctx, filepath.Join(dir, "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	laneRoot := filepath.Join(dir, "lanes")
	now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)

	t.Run("a request that names an item carries it onto the record and the row", func(t *testing.T) {
		stored, err := run.Exec(ctx, s, run.Request{
			RunID: "run_item", ItemID: "item_042", Backend: "muse",
			Command: []string{"/bin/true"}, WorkingDir: ".", TimeoutMS: 60000,
		}, run.ExecOptions{LaneRoot: laneRoot, Now: now})
		if err != nil {
			t.Fatalf("executing the request failed: %v", err)
		}
		if got := recordOf(t, stored).ItemID; got != "item_042" {
			t.Fatalf("the record names item %q, want the request's %q", got, "item_042")
		}
		// The row is what a read face answers from, so the id has to be there too: a
		// record naming an item over a row that drops it is the coupling this case
		// exists to hold.
		row, found := storedRun(t, s, "run_item")
		if !found {
			t.Fatalf("the run is not in the store after it executed")
		}
		if row.ItemID != "item_042" {
			t.Fatalf("the runs row names item %q, want %q", row.ItemID, "item_042")
		}
	})

	t.Run("a request that names no item runs, and the record names none", func(t *testing.T) {
		stored, err := run.Exec(ctx, s, run.Request{
			RunID: "run_no_item", Backend: "muse",
			Command: []string{"/bin/true"}, WorkingDir: ".", TimeoutMS: 60000,
		}, run.ExecOptions{LaneRoot: laneRoot, Now: now})
		if err != nil {
			t.Fatalf("a request naming no item did not run: %v", err)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(stored.Payload, &body); err != nil {
			t.Fatalf("reading the run record payload failed: %v", err)
		}
		if raw, carried := body["item_id"]; carried {
			t.Fatalf("a run dispatched for no item carries item_id %s", raw)
		}
		row, found := storedRun(t, s, "run_no_item")
		if !found {
			t.Fatalf("a run dispatched for no item left no row")
		}
		if row.ItemID != "" {
			t.Fatalf("the runs row names item %q for a run dispatched for none", row.ItemID)
		}
	})
}

// recordOf decodes a run.record envelope's payload as the record the package defines.
func recordOf(t *testing.T, env record.Envelope) run.Record {
	t.Helper()
	var payload run.Record
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatalf("reading the run record payload failed: %v", err)
	}
	return payload
}

// storedRun answers one run as the store holds it, through the read face inside one read
// transaction: a case reads the row through the substrate rather than by opening the
// database itself.
func storedRun(t *testing.T, s *store.Store, runID string) (run.Record, bool) {
	t.Helper()
	var (
		stored run.Record
		found  bool
	)
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		env, ok, err := run.StatusRecord(r, runID)
		if err != nil {
			return err
		}
		found = ok
		if !ok {
			return nil
		}
		var body struct {
			Run *run.Record `json:"run"`
		}
		if err := json.Unmarshal(env.Payload, &body); err != nil {
			return err
		}
		if body.Run == nil {
			return fmt.Errorf("a run.status for %q reports found with no run", runID)
		}
		stored = *body.Run
		return nil
	}); err != nil {
		t.Fatalf("reading run %q through the read face failed: %v", runID, err)
	}
	return stored, found
}

// The route is the model id the adapter was handed, and the record names it: the record
// and the runs row carry the argv element staging put there, so a reader answers "which
// model route served this run" without re-deriving the draw.
//
// The request here is built by a backend whose draw is one of three ids, and then the
// argv's `--model` element is changed to an id that draw never produced. The record must
// name the id the process would have received — the argv element — so an implementation
// that records the draw (or re-draws it) instead of copying the argv fails this case.
func TestTheRouteTravelsFromTheAdaptersArgvToTheRecordAndRow(t *testing.T) {
	ctx := context.Background()
	dir := scratch(t)
	s, err := store.InitFixture(ctx, filepath.Join(dir, "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	laneRoot := filepath.Join(dir, "lanes")
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)

	t.Run("the argv's model id is the record's and the row's route", func(t *testing.T) {
		backend := run.Backend{
			Adapter: "/bin/true", Models: []string{"route-a", "route-b", "route-c"}, TimeoutSeconds: 60,
		}
		req := backend.Request("", "lane_route", "run_route", "pi")
		handed := "route-handed-not-drawn"
		for i, element := range req.Command {
			if element == "--model" {
				req.Command[i+1] = handed
			}
		}
		stored, err := run.Exec(ctx, s, req, run.ExecOptions{LaneRoot: laneRoot, Now: now})
		if err != nil {
			t.Fatalf("executing the request failed: %v", err)
		}
		if got := recordOf(t, stored).Route; got != handed {
			t.Fatalf("the record names route %q, want the argv's %q", got, handed)
		}
		// The row is what a read face answers from, so the route has to be there too: a
		// record naming a route over a row that drops it is the coupling this case
		// exists to hold.
		row, found := storedRun(t, s, "run_route")
		if !found {
			t.Fatalf("the run is not in the store after it executed")
		}
		if row.Route != handed {
			t.Fatalf("the runs row names route %q, want the argv's %q", row.Route, handed)
		}
	})

	t.Run("a request whose argv names no model runs, and the record names no route", func(t *testing.T) {
		stored, err := run.Exec(ctx, s, run.Request{
			RunID: "run_no_route", Backend: "pi",
			Command: []string{"/bin/true"}, WorkingDir: ".", TimeoutMS: 60000,
		}, run.ExecOptions{LaneRoot: laneRoot, Now: now})
		if err != nil {
			t.Fatalf("a request naming no route did not run: %v", err)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(stored.Payload, &body); err != nil {
			t.Fatalf("reading the run record payload failed: %v", err)
		}
		if raw, carried := body["route"]; carried {
			t.Fatalf("a run whose argv named no model carries route %s", raw)
		}
		row, found := storedRun(t, s, "run_no_route")
		if !found {
			t.Fatalf("a run naming no route left no row")
		}
		if row.Route != "" {
			t.Fatalf("the runs row names route %q for a run whose argv named no model", row.Route)
		}
	})
}
