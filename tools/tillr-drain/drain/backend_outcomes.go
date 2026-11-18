package drain

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// Terminal classifications are captured once, keyed to the immutable run.finish
// event, never the mutable runs.event_seq (usage corrections advance that field).
// class is what ClassifyAttempt answered, which routing reads; closes is the
// availability fact: an infrastructure ending that no other rule answers.
const backendOutcomesDDL = `CREATE TABLE IF NOT EXISTS backend_outcomes (
 terminal_seq INTEGER PRIMARY KEY REFERENCES events(seq),
 class TEXT NOT NULL,
 closes INTEGER NOT NULL DEFAULT 0
);`

const backendOutcomeKind = "drain.backend-outcome"

type backendOutcome struct {
	seq    int64
	rec    run.Record
	class  AttemptClass
	closes bool
	// saved says the class and closes were read from backend_outcomes. An unsaved
	// ending is classified in memory by whoever reads it; only the loop saves.
	saved bool
}

// readBackendOutcomes answers every agent run ending, newest first. A saved
// ending keeps its saved facts; an unsaved one is classified from the record and
// the log as they are now. It writes nothing, so a read-only face may call it.
func readBackendOutcomes(r *store.Reader) ([]backendOutcome, error) {
	rows, err := r.Query(`SELECT e.seq,e.payload,COALESCE(o.class,''),COALESCE(o.closes,0) FROM events e
 LEFT JOIN backend_outcomes o ON o.terminal_seq=e.seq WHERE e.kind=? ORDER BY e.seq DESC`, run.FinishKind)
	if err != nil && strings.Contains(err.Error(), "no such table: backend_outcomes") {
		rows, err = r.Query(`SELECT seq,payload,'',0 FROM events WHERE kind=? ORDER BY seq DESC`, run.FinishKind)
	}
	if err != nil {
		return nil, err
	}
	out := []backendOutcome{}
	unsaved := false
	for rows.Next() {
		var o backendOutcome
		var raw string
		if err := rows.Scan(&o.seq, &raw, &o.class, &o.closes); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &o.rec); err != nil {
			rows.Close()
			return nil, err
		}
		if o.rec.Backend == "command" {
			continue
		}
		o.saved = o.class != ""
		unsaved = unsaved || !o.saved
		out = append(out, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || !unsaved {
		return out, err
	}
	interrupted, _, err := readInterruptedRuns(r)
	if err != nil {
		return nil, err
	}
	_, byRun, err := readPendingHistory(r)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].saved {
			continue
		}
		rec := out[i].rec
		out[i].class = ClassifyAttempt(rec, logTail(rec.ArtifactRefs))
		out[i].closes = out[i].class == AttemptInfrastructure && !answeredByAnotherRule(rec, interrupted, byRun)
	}
	return out, nil
}

// answeredByAnotherRule says an infrastructure-class ending is not evidence that
// its backend is broken: the loop interrupted the run itself, the provider
// refused it with a stated reset time (the quota window closes and reopens that
// backend), or the session it resumed is gone (a fresh session replaces it).
func answeredByAnotherRule(rec run.Record, interrupted map[string]bool, byRun map[string]*pendingWork) bool {
	if interrupted[rec.RunID] {
		return true
	}
	if _, _, reset, ok := refusedWindow(rec); ok && !reset.IsZero() {
		return true
	}
	work := byRun[rec.RunID]
	if work == nil {
		return false
	}
	decision := work.Decision
	for _, node := range parallelNodes(work.Decision) {
		if node.RunRequest != nil && node.RunRequest.RunID == rec.RunID {
			decision = node
			break
		}
	}
	gone, err := failedDesignerResume(decision, rec)
	return err == nil && gone
}

// readInterruptedRuns answers the runs whose latest segment the loop ended
// itself. An interruption describes the segment it ended. A later continuation
// of the same run (run.resume) is that run's new segment, and its outcome is judged.
func readInterruptedRuns(r *store.Reader) (interrupted, restaged map[string]bool, err error) {
	interrupted, restaged = map[string]bool{}, map[string]bool{}
	events, err := r.Events(0)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range events {
		if e.ObjectID == nil {
			continue
		}
		switch e.Kind {
		case loopOrphanedKind:
			interrupted[*e.ObjectID] = true
		case staleBaseKind:
			// A stale base is the staging's failure, not the item's: like a loop stop,
			// it ends the attempt without an outcome to hold the item on.
			interrupted[*e.ObjectID], restaged[*e.ObjectID] = true, true
		case run.ResumeKind:
			delete(interrupted, *e.ObjectID)
		}
	}
	return interrupted, restaged, nil
}

// RecordBackendOutcomes saves the class and the availability fact of every
// ending not saved yet, including standalone and judge runs. The loop calls it
// where it settles a run and when it starts; a saved ending survives a later
// edit or loss of its report.
func RecordBackendOutcomes(ctx context.Context, path string) error {
	reader, err := store.OpenRead(ctx, path)
	if err != nil {
		return err
	}
	var outcomes []backendOutcome
	err = reader.ReadTx(ctx, func(r *store.Reader) error { var err error; outcomes, err = readBackendOutcomes(r); return err })
	reader.Close()
	if err != nil {
		return err
	}
	pending := []backendOutcome{}
	for _, o := range outcomes {
		if !o.saved {
			pending = append(pending, o)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	s, err := store.Open(ctx, path, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		for _, o := range pending {
			inserted, err := tx.Exec(`INSERT OR IGNORE INTO backend_outcomes(terminal_seq,class,closes) VALUES (?,?,?)`, o.seq, o.class, o.closes)
			if err != nil {
				return err
			}
			count, err := inserted.RowsAffected()
			if err != nil {
				return err
			}
			if count > 0 {
				if err := appendEffect(tx, backendOutcomeKind, o.rec.Backend, struct {
					TerminalSeq int64        `json:"terminal_seq"`
					Class       AttemptClass `json:"class"`
					Closes      bool         `json:"closes"`
				}{o.seq, o.class, o.closes}, time.Now().UTC()); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
