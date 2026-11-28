package policy

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/internal/runhistory"
	_ "github.com/mschulkind-oss/tillr-drain/internal/storelog"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func init() {
	for _, kind := range []string{staleBaseKind, "drain.judge.request"} {
		_ = event.Register(kind, drainmodel.Utility)
	}
}

func openPolicyTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestQueryBackendOutcomesForRuns(t *testing.T) {
	ctx := context.Background()
	s := openPolicyTestStore(t)
	at := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	appendFinish := func(tx *store.Tx, rec run.Record) int64 {
		body, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		id := rec.RunID
		seq, err := tx.Append(event.Event{
			Kind:       run.FinishKind,
			ObjectType: "run",
			ObjectID:   &id,
			Payload:    body,
			At:         at,
		})
		if err != nil {
			t.Fatal(err)
		}
		return seq
	}

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		seqSaved := appendFinish(tx, run.Record{RunID: "run-open-saved", ItemID: "item-open", Backend: "codex", State: run.StateExited, ExitCode: code(0)})
		if _, err := tx.Exec(`INSERT INTO backend_outcomes(terminal_seq, class, closes) VALUES (?, ?, ?)`, seqSaved, string(AttemptAnswered), 0); err != nil {
			return err
		}
		appendFinish(tx, run.Record{RunID: "run-open-unsaved", ItemID: "item-open", Backend: "claude", State: run.StateExited, ExitCode: code(1)})
		appendFinish(tx, run.Record{RunID: "run-open-cmd", ItemID: "item-open", Backend: "command", State: run.StateExited, ExitCode: code(0)})
		appendFinish(tx, run.Record{RunID: "run-closed", ItemID: "item-closed", Backend: "codex", State: run.StateExited, ExitCode: code(0)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		empty, unsaved, err := QueryBackendOutcomesForRuns(r, nil)
		if err != nil {
			return err
		}
		if len(empty) != 0 || unsaved != 0 {
			t.Fatalf("empty runIDs = (%+v, %d), want empty", empty, unsaved)
		}

		got, unsaved, err := QueryBackendOutcomesForRuns(r, []string{"run-open-saved", "run-open-unsaved", "run-open-cmd"})
		if err != nil {
			return err
		}
		if unsaved != 1 {
			t.Fatalf("unsaved = %d, want 1", unsaved)
		}
		if len(got) != 2 {
			t.Fatalf("len(got) = %d, want 2 (%+v)", len(got), got)
		}
		byID := map[string]BackendOutcome{}
		for _, o := range got {
			byID[o.Rec.RunID] = o
		}
		if o, ok := byID["run-open-saved"]; !ok || !o.Saved || o.Class != AttemptAnswered {
			t.Errorf("run-open-saved = %+v, want saved answered", o)
		}
		if o, ok := byID["run-open-unsaved"]; !ok || o.Saved || o.Rec.Backend != "claude" {
			t.Errorf("run-open-unsaved = %+v, want unsaved claude", o)
		}
		if _, ok := byID["run-closed"]; ok {
			t.Errorf("unexpected run-closed in scoped result")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReadInterruptedRunsFor(t *testing.T) {
	ctx := context.Background()
	s := openPolicyTestStore(t)
	at := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	appendRunEv := func(tx *store.Tx, runID, kind string) error {
		id := runID
		_, err := tx.Append(event.Event{
			Kind:       kind,
			ObjectType: "run",
			ObjectID:   &id,
			Payload:    []byte(`{}`),
			At:         at,
		})
		return err
	}

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := appendRunEv(tx, "run-orphaned", runhistory.LoopOrphanedKind); err != nil {
			return err
		}
		if err := appendRunEv(tx, "run-restaged", staleBaseKind); err != nil {
			return err
		}
		if err := appendRunEv(tx, "run-restaged", run.ResumeKind); err != nil {
			return err
		}
		return appendRunEv(tx, "run-other", runhistory.LoopOrphanedKind)
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		inter, rest, err := ReadInterruptedRunsFor(r, nil)
		if err != nil {
			return err
		}
		if len(inter) != 0 || len(rest) != 0 {
			t.Fatalf("empty runIDs = (%v, %v), want empty", inter, rest)
		}

		inter, rest, err = ReadInterruptedRunsFor(r, []string{"run-orphaned", "run-restaged"})
		if err != nil {
			return err
		}
		if !inter["run-orphaned"] || inter["run-restaged"] || inter["run-other"] {
			t.Errorf("interrupted = %v, want only run-orphaned", inter)
		}
		if !rest["run-restaged"] || rest["run-orphaned"] {
			t.Errorf("restaged = %v, want only run-restaged", rest)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReadDispatchDeclinesAndEscalationsForItems(t *testing.T) {
	ctx := context.Background()
	s := openPolicyTestStore(t)
	at := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	appendItemEv := func(tx *store.Tx, itemID, kind string, payload any) error {
		body, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		id := itemID
		_, err = tx.Append(event.Event{
			Kind:       kind,
			ObjectType: "item",
			ObjectID:   &id,
			Payload:    body,
			At:         at,
		})
		return err
	}

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := appendItemEv(tx, "item-open", DispatchDeclinedKind, DispatchDecline{DecisionID: "d1", RunID: "r1", WaitingOn: "lane"}); err != nil {
			return err
		}
		if err := appendItemEv(tx, "item-open", DispatchDeclinedKind, DispatchDecline{DecisionID: "d2", RunID: "r2", WaitingOn: "seat"}); err != nil {
			return err
		}
		if err := appendItemEv(tx, "item-open", DispatchDeclinedKind, DispatchDecline{DecisionID: "d3", RunID: "r3", WaitingOn: ""}); err != nil {
			return err
		}
		if err := appendItemEv(tx, "item-closed", DispatchDeclinedKind, DispatchDecline{DecisionID: "d4", RunID: "r4", WaitingOn: "lane"}); err != nil {
			return err
		}
		if err := appendItemEv(tx, "item-open", SeatEscalatedKind, SeatEscalation{ItemID: "item-open", From: "codex", To: "claude"}); err != nil {
			return err
		}
		return appendItemEv(tx, "item-closed", SeatEscalatedKind, SeatEscalation{ItemID: "item-closed", From: "codex", To: "claude"})
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		emptyDecl, err := ReadDispatchDeclinesForItems(r, nil)
		if err != nil || len(emptyDecl) != 0 {
			t.Fatalf("empty declines = %v, %v", emptyDecl, err)
		}
		decl, err := ReadDispatchDeclinesForItems(r, []string{"item-open"})
		if err != nil {
			return err
		}
		if len(decl) != 1 || decl["item-open"].DecisionID != "d2" || decl["item-open"].WaitingOn != "seat" || decl["item-open"].Seq == 0 {
			t.Errorf("declines = %+v, want latest non-empty WaitingOn for item-open", decl)
		}

		emptyEsc, err := ReadEscalationsForItems(r, nil)
		if err != nil || len(emptyEsc) != 0 {
			t.Fatalf("empty escalations = %v, %v", emptyEsc, err)
		}
		esc, err := ReadEscalationsForItems(r, []string{"item-open"})
		if err != nil {
			return err
		}
		if len(esc) != 1 || EscalatedBackend(esc["item-open"]) != "claude" {
			t.Errorf("escalations = %+v, want item-open -> claude", esc)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReadPriorRunsAndJudgmentsForItems(t *testing.T) {
	ctx := context.Background()
	s := openPolicyTestStore(t)
	at := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		insertRun := func(itemID, runID, backend, state string, exitCode int) int64 {
			rec := run.Record{RunID: runID, ItemID: itemID, Backend: backend, State: state, ExitCode: code(exitCode)}
			body, err := json.Marshal(rec)
			if err != nil {
				t.Fatal(err)
			}
			id := runID
			seq, err := tx.Append(event.Event{Kind: run.FinishKind, ObjectType: "run", ObjectID: &id, Payload: body, At: at})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(
				`INSERT INTO runs(run_id, item_id, lane_id, backend, state, exit_code, artifact_refs, started_at, finished_at, event_seq) VALUES (?, ?, ?, ?, ?, ?, '[]', ?, ?, ?)`,
				runID, itemID, "lane-1", backend, state, exitCode, at.UnixMilli(), at.UnixMilli(), seq,
			); err != nil {
				t.Fatal(err)
			}
			return seq
		}
		insertRun("item-open", "run-open-worker", "codex", run.StateExited, 0)
		insertRun("item-open", "run-open-judge", "codex", run.StateExited, 0)
		insertRun("item-closed", "run-closed-1", "codex", run.StateExited, 0)
		id := "run-open-judge"
		if _, err := tx.Append(event.Event{Kind: "drain.judge.request", ObjectType: "run", ObjectID: &id, Payload: []byte(`{}`), At: at}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		judged, endings, payloads, err := ReadRunJudgmentsAndEndingsFor(r, nil)
		if err != nil || len(judged) != 0 || len(endings) != 0 || len(payloads) != 0 {
			t.Fatalf("empty ReadRunJudgmentsAndEndingsFor = %v %v %v %v", judged, endings, payloads, err)
		}
		judged, endings, payloads, err = ReadRunJudgmentsAndEndingsFor(r, []string{"run-open-worker", "run-open-judge"})
		if err != nil {
			return err
		}
		if !judged["run-open-judge"] || judged["run-open-worker"] || endings["run-open-worker"] == 0 || len(payloads["run-open-worker"]) == 0 {
			t.Errorf("scoped judgments/endings/payloads = %v / %v / %d", judged, endings, len(payloads["run-open-worker"]))
		}
		if endings["run-closed-1"] != 0 {
			t.Errorf("unexpected run-closed-1 in scoped endings: %v", endings)
		}

		emptyPrior, err := ReadPriorRunsForItems(r, nil, nil, nil, nil, nil)
		if err != nil || len(emptyPrior) != 0 {
			t.Fatalf("empty ReadPriorRunsForItems = %v, %v", emptyPrior, err)
		}
		outcomes, _, err := QueryBackendOutcomesForRuns(r, []string{"run-open-worker", "run-open-judge"})
		if err != nil {
			return err
		}
		scoped, err := ReadPriorRunsForItems(r, []string{"item-open"}, []string{"run-open-worker", "run-open-judge"}, outcomes, nil, nil)
		if err != nil {
			return err
		}
		if len(scoped) != 1 || len(scoped["item-open"]) != 1 || scoped["item-open"][0].RunID != "run-open-worker" || scoped["item-open"][0].Class != AttemptAnswered {
			t.Fatalf("scoped prior runs = %+v, want 1 answered worker run for item-open", scoped)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
