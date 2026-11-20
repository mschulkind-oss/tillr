package drain

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// workerRecordFlood appends n run.child-session events, the worker records that were
// 1,267,132 of the 1,294,976 events in the live store on 2026-11-19 and that no fold reads.
func workerRecordFlood(t *testing.T, f refusalLoopFixture, n int) {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		id := "run-elsewhere-a1"
		for i := 0; i < n; i++ {
			body := fmt.Sprintf(`{"run_id":%q,"session_id":"%s-child-%d","turn_count":%d}`, id, id, i, i)
			if _, err := tx.Append(event.Event{Kind: "run.child-session", ObjectType: "run", ObjectID: &id, Payload: []byte(body), At: time.Unix(int64(i), 0).UTC()}); err != nil {
				return err
			}
		}
		return nil
	})
}

// On 2026-11-19 at 04:46:48Z the loop applied a pass's plans back to back, each in its own
// write transaction that held the store's write lock about half a second: every application
// re-read the pending runs, the item faults and the forks by copying and folding the whole
// event log (Events(0)), several times, and the log was 1.29 million events long. The gaps
// between the applications were a fraction of a millisecond, so tillr-land's BEGIN IMMEDIATE
// for item-request-cfae4676fc's landing waited out its 5-second busy timeout and the landing
// was held as land-harness-error.
//
// A plan's application, a settlement and an examination's release read only the kinds their
// folds read: none of them reads the whole log while it holds the write lock.
func TestAPlanApplicationReadsNoWholeLogUnderTheWriteLock(t *testing.T) {
	f, l := passFixture(t, "item-a")
	workerRecordFlood(t, f, 200)
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	before := store.WholeLogReadsUnderWrite()
	p := mustPlan(t, l)[0]
	if ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("dispatch claim: %v %v", ok, err)
	}
	if n := store.WholeLogReadsUnderWrite() - before; n != 0 {
		t.Fatalf("applying a dispatch read the whole event log %d times under the write lock", n)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		id := p.decision.RunRequest.RunID
		at := l.cfg.Now().Format("2006-01-02T15:04:05Z07:00")
		return applyTerminal(tx, run.Record{RunID: id, ItemID: p.decision.ItemID, LaneID: p.decision.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: at, FinishedAt: at, ArtifactRefs: []run.ArtifactRef{}, EndCause: run.EndNormalExit})
	})
	before = store.WholeLogReadsUnderWrite()
	settle := mustPlan(t, l)[0]
	if settle.decision.Action != ActionSettle {
		t.Fatalf("fixture: the finished run did not plan a settlement: %+v", settle.decision)
	}
	ok, work, err := l.applyPlan(context.Background(), settle, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("settle claim: %v %v", ok, err)
	}
	if err := l.markExamined(context.Background(), work.Decision); err != nil {
		t.Fatal(err)
	}
	if err := l.releaseExamination(context.Background(), settle.decision); err != nil {
		t.Fatal(err)
	}
	if err := l.holdItemFault(context.Background(), Candidate{ItemID: "item-a", RunID: settle.decision.RunRequest.RunID}, ReasonLandHarnessError, "the land program answered exit 4"); err != nil {
		t.Fatal(err)
	}
	if n := store.WholeLogReadsUnderWrite() - before; n != 0 {
		t.Fatalf("settling, examining and holding read the whole event log %d times under the write lock", n)
	}
}

// A fold that reads only some kinds names them in its selection, and the selection must not
// drift from the fold: a kind the fold reads that the selection leaves out is silently never
// seen. For every function in these packages that selects events by kind, the kinds it
// compares an event's Kind against (switch cases and ==/!= on e.Kind) are exactly the kinds it
// selects.
func TestEveryFoldReadsTheKindsItSelects(t *testing.T) {
	fset := token.NewFileSet()
	var files []*ast.File
	for _, dir := range []string{".", "../faults", "../preserved", "../../tillr-run/run"} {
		pkgs, err := parser.ParseDir(fset, dir, func(info fs.FileInfo) bool { return !strings.HasSuffix(info.Name(), "_test.go") }, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, pkg := range pkgs {
			for _, file := range pkg.Files {
				files = append(files, file)
			}
		}
	}
	source := func(e ast.Expr) string {
		var b strings.Builder
		_ = printer.Fprint(&b, fset, e)
		return b.String()
	}
	lists := map[string][]ast.Expr{} // package-level []string vars
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok || len(value.Values) != 1 {
					continue
				}
				if lit, ok := value.Values[0].(*ast.CompositeLit); ok && len(value.Names) == 1 {
					lists[value.Names[0].Name] = lit.Elts
				}
			}
		}
	}
	checked := 0
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			selected, read := map[string]bool{}, map[string]bool{}
			selects := false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CallExpr:
					sel, ok := n.Fun.(*ast.SelectorExpr)
					if !ok || (sel.Sel.Name != "EventsOfKind" && sel.Sel.Name != "EventsSelected") || len(n.Args) < 2 {
						return true
					}
					selects = true
					for _, arg := range n.Args[1:] {
						if lit, ok := arg.(*ast.CompositeLit); ok {
							for _, elt := range lit.Elts {
								if kv, ok := elt.(*ast.KeyValueExpr); ok && source(kv.Key) == "Kinds" {
									for _, kind := range kv.Value.(*ast.CompositeLit).Elts {
										selected[source(kind)] = true
									}
								}
							}
							continue
						}
						if id, ok := arg.(*ast.Ident); ok && n.Ellipsis.IsValid() {
							for _, kind := range lists[id.Name] {
								selected[source(kind)] = true
							}
							continue
						}
						selected[source(arg)] = true
					}
				case *ast.SwitchStmt:
					if isEventKind(n.Tag) {
						for _, stmt := range n.Body.List {
							for _, kind := range stmt.(*ast.CaseClause).List {
								read[source(kind)] = true
							}
						}
					}
				case *ast.BinaryExpr:
					if n.Op == token.EQL || n.Op == token.NEQ {
						if isEventKind(n.X) {
							read[source(n.Y)] = true
						} else if isEventKind(n.Y) {
							read[source(n.X)] = true
						}
					}
				}
				return true
			})
			if !selects {
				continue
			}
			checked++
			for kind := range read {
				if !selected[kind] {
					t.Errorf("%s reads %s but does not select it", fn.Name.Name, kind)
				}
			}
			for kind := range selected {
				if !read[kind] {
					t.Errorf("%s selects %s but never reads it", fn.Name.Name, kind)
				}
			}
		}
	}
	if checked < 10 {
		t.Fatalf("checked %d selecting folds; the parse missed the ones this guards", checked)
	}
}

// isEventKind answers whether x is e.Kind, the kind of the event a fold's loop is reading.
func isEventKind(x ast.Expr) bool {
	sel, ok := x.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Kind" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "e"
}

// On 2026-11-19 a pass planned 342 items and computed each one's input version (inputVersion)
// by folding the pending runs and the item faults afresh: 217 ms an item against the live
// store's 1.3 million events, 74 seconds a plan, which was nearly all of the loop's
// 95-second pass. A plan reads the event log a number of times that depends on neither the
// items it plans nor the worker records the log holds, and never reads the whole log.
func TestAPlanReadsTheLogABoundedNumberOfTimes(t *testing.T) {
	plan := func(items, flood int) (reads, whole int64) {
		ids := []string{}
		for i := 0; i < items; i++ {
			ids = append(ids, fmt.Sprintf("item-%d", i))
		}
		f, l := passFixture(t, ids...)
		workerRecordFlood(t, f, flood)
		reads, whole = store.EventLogReads(), store.WholeLogReads()
		if got := len(mustPlan(t, l)); got != items {
			t.Fatalf("the plan answered %d decisions for %d items", got, items)
		}
		return store.EventLogReads() - reads, store.WholeLogReads() - whole
	}
	fewReads, fewWhole := plan(2, 10)
	manyReads, manyWhole := plan(8, 400)
	if fewWhole != 0 || manyWhole != 0 {
		t.Fatalf("a plan read the whole event log %d and %d times; it reads the kinds it folds", fewWhole, manyWhole)
	}
	if manyReads != fewReads {
		t.Fatalf("a plan of 8 items over 400 worker records read the event log %d times, one of 2 items over 10 read it %d times; the reads must not grow with the items or the records", manyReads, fewReads)
	}
}

// A paused history fold must not prevent another writer from committing. This
// models the 6.147s application fold seen in PID 1703174, tx8075, without a
// large database or a timing assertion about how quickly the fold runs.
func TestAnApplicationFoldsPendingHistoryWithoutBlockingAnotherWriter(t *testing.T) {
	f, l := passFixture(t, "item-a")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	p := mustPlan(t, l)[0]
	unsavedApplicationOutcome(t, f, l)
	writer, err := store.Open(context.Background(), l.cfg.Store, false)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	var contentionErr error
	foldsUnderWrite := 0
	pendingHistoryFoldHook = func() {
		if store.HoldsWrite() {
			foldsUnderWrite++
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() {
			done <- writer.WriteTx(ctx, func(tx *store.Tx) error {
				id := "run-elsewhere-a1"
				_, err := tx.Append(event.Event{Kind: "run.child-session", ObjectType: "run", ObjectID: &id, Payload: []byte(`{"run_id":"run-elsewhere-a1","session_id":"child","turn_count":1}`), At: l.cfg.Now()})
				return err
			})
		}()
		if err := <-done; err != nil {
			contentionErr = err
		}
	}
	t.Cleanup(func() { pendingHistoryFoldHook = nil })
	before := pendingFolds.Load()
	if ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("dispatch claim: %v %v", ok, err)
	}
	if contentionErr != nil || foldsUnderWrite != 0 {
		t.Fatalf("writer could not commit during the paused fold: %v; folds under write: %d", contentionErr, foldsUnderWrite)
	}
	if n := pendingFolds.Load() - before; n != 1 {
		t.Fatalf("application folded pending history %d times, want one read-phase fold", n)
	}
}

// Even an unsaved run.finish requires no second pending fold when availability
// classifies it. Keep it outside this item's history so it does not change its plan.
func unsavedApplicationOutcome(t *testing.T, f refusalLoopFixture, l *Loop) {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: "run-elsewhere-a1", ItemID: "item-elsewhere", LaneID: "lane-elsewhere", Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339)})
	})
}

func TestAnApplicationThreadsPendingHistoryToParallelAvailability(t *testing.T) {
	f, l, _ := parallelFixture(t)
	p := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	unsavedApplicationOutcome(t, f, l)
	underWrite := 0
	pendingHistoryFoldHook = func() {
		if store.HoldsWrite() {
			underWrite++
		}
	}
	t.Cleanup(func() { pendingHistoryFoldHook = nil })
	before := pendingFolds.Load()
	ok, work, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
	if err != nil || !ok || work == nil || len(work.Decision.Parallel) == 0 {
		t.Fatalf("parallel claim: %v %+v %v", ok, work, err)
	}
	if n := pendingFolds.Load() - before; n != 1 || underWrite != 0 {
		t.Fatalf("parallel claim folded history %d times, %d under write; want one outside write", n, underWrite)
	}
}

func TestPendingHistoryHeadTracksOnlyFoldedKinds(t *testing.T) {
	f, l := passFixture(t, "item-a")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, kind := range pendingHistoryKinds {
			id := "run-history-head"
			seq, err := tx.Append(event.Event{Kind: kind, ObjectType: "run", ObjectID: &id, Payload: []byte(`{}`), At: l.cfg.Now()})
			if err != nil {
				return err
			}
			head, err := pendingHistoryHead(tx.Reader())
			if err != nil {
				return err
			}
			if head != seq {
				t.Fatalf("%s did not advance pending history: head=%d seq=%d", kind, head, seq)
			}
		}
		head, err := pendingHistoryHead(tx.Reader())
		if err != nil {
			return err
		}
		id := "run-history-head"
		if _, err := tx.Append(event.Event{Kind: "run.child-session", ObjectType: "run", ObjectID: &id, Payload: []byte(`{}`), At: l.cfg.Now()}); err != nil {
			return err
		}
		after, err := pendingHistoryHead(tx.Reader())
		if after != head {
			t.Errorf("worker record advanced pending history: %d -> %d", head, after)
		}
		return err
	})
}

// An append between the snapshot and the writer invalidates it, including when
// it arrives between gathered attempts. It must not claim the competing lane,
// refresh history under the lock, or allocate a pass generation for a skipped plan.
func TestAnApplicationSkipsStalePendingHistory(t *testing.T) {
	for _, phase := range []string{"fold", "gathered-fetch"} {
		t.Run(phase, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			p := mustPlan(t, l)[0]
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			changed := false
			compete := func() {
				if changed {
					return
				}
				changed = true
				fixtureWrite(t, f, func(tx *store.Tx) error {
					d := p.decision
					d.ItemID = "item-other"
					r := *d.RunRequest
					r.RunID, r.ItemID = "run-other-a1", d.ItemID
					d.RunRequest = &r
					return appendEffect(tx, dispatchStartedKind, r.RunID, pendingWork{Decision: d, BaseSHA: base}, l.cfg.Now())
				})
			}
			if phase == "fold" {
				pendingHistoryFoldHook = compete
			} else {
				writeGatheredHooks.Fetch = func(string) { compete() }
			}
			t.Cleanup(func() { pendingHistoryFoldHook = nil; writeGatheredHooks.Fetch = nil })
			before := pendingFolds.Load()
			generation := ""
			ok, work, err := l.applyPlanRecorded(context.Background(), p, &base, l.cfg.Now(), &generation)
			if err != nil || ok || work != nil || generation != "" || !changed {
				t.Fatalf("stale claim: applied=%v work=%+v generation=%q changed=%v err=%v", ok, work, generation, changed, err)
			}
			if n := pendingFolds.Load() - before; n != 1 {
				t.Fatalf("stale application refolded history %d times", n)
			}
			pendingHistoryFoldHook, writeGatheredHooks.Fetch = nil, nil
			fixtureWrite(t, f, func(tx *store.Tx) error {
				var claimed int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id=?`, dispatchStartedKind, p.decision.RunRequest.RunID).Scan(&claimed); err != nil {
					return err
				}
				if claimed != 0 {
					t.Error("stale dispatch was recorded")
				}
				leases, err := activeLeases(tx.Reader(), l.cfg.Now(), []string{p.decision.LaneID})
				if len(leases) != 0 {
					t.Error("stale dispatch leased the lane")
				}
				return err
			})
		})
	}
}

// A query that correlates the events table with each run row (`e.object_id=runs.run_id`)
// visits every event whose object is that run, and a run's events are mostly its worker
// records: on 2026-11-19 readPriorRuns's two such subqueries cost about 2 seconds a plan
// against the live store, a plan the loop makes at least once a pass. The planner reads the
// kinds it correlates by kind (EventsOfKind) and joins them to the runs in memory. The loop's
// end-of-run spend snapshot keeps its correlation: it runs once per loop, not per pass.
func TestNoPassQueryCorrelatesTheEventsWithEachRun(t *testing.T) {
	correlated := regexp.MustCompile(`object_id\s*=\s*runs\.run_id`)
	allowed := map[string]string{"spend.go": "snapshotSpend runs once, when the loop ends"}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || allowed[name] != "" {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if correlated.MatchString(line) {
				t.Errorf("%s line %d correlates the events with each run: %s", name, i+1, strings.TrimSpace(line))
			}
		}
	}
}
