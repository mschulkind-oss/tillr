package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// backendAvailabilityChanged is an indexed check of just the tail. Changes to
// dispatch history (including resumes and orphans) are checked separately.
func backendAvailabilityChanged(r *store.Reader, after int64) (bool, error) {
	var changed bool
	err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind IN (?, ?, ?, ?, ?, ?) AND seq > ?)`,
		run.FinishKind, backendOutcomeKind, backendProbeKind, callOffered, staleBaseKind, rollPendingKind, after).Scan(&changed)
	return changed, err
}

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
	seq int64
	// For a saved outcome, readBackendOutcomesFrom populates only RunID and
	// Backend on rec; readInfrastructureClosedFrom loads the full record for
	// any backend that remains closed.
	rec    run.Record
	class  AttemptClass
	closes bool
	// saved says the class and closes were read from backend_outcomes. An unsaved
	// ending is classified in memory by whoever reads it; only the loop saves.
	saved bool
}

// These counters let claim and refresh tests detect typed history scans and payload unmarshals.
var backendOutcomesReads, backendOutcomesUnderWrite, backendOutcomeUnmarshals atomic.Int64

// backendOutcomesState caches folded run.finish dispositions, call.offered
// overrides, and successful drain.backend-probe sequences up to seq so
// startSnapshot refreshes only inspect new events and unsaved endings.
type backendOutcomesState struct {
	seq     int64
	entries []backendOutcome
	unsaved int
	offered map[int64]bool
	probes  map[string]int64
}

// readBackendOutcomes answers every agent run ending, newest first. A saved
// ending keeps its saved facts; an unsaved one is classified from the record and
// the log as they are now. It writes nothing, so a read-only face may call it.
func readBackendOutcomes(r *store.Reader) ([]backendOutcome, error) {
	return readBackendOutcomesFrom(r, nil)
}

// readBackendOutcomesFrom reuses a supplied history from this transaction or a
// snapshot validated against pendingHistoryHead. Nil asks it to fold for itself.
func readBackendOutcomesFrom(r *store.Reader, byRun map[string]*pendingWork) ([]backendOutcome, error) {
	out, _, _, err := readBackendOutcomesFull(r, byRun)
	return out, err
}

func readBackendOutcomesFull(r *store.Reader, byRun map[string]*pendingWork) ([]backendOutcome, int, map[int64]bool, error) {
	backendOutcomesReads.Add(1)
	if store.HoldsWrite() {
		backendOutcomesUnderWrite.Add(1)
	}
	out, unsaved, err := queryBackendOutcomes(r, 0)
	if err != nil {
		return nil, 0, nil, err
	}
	offeredList, err := queryOfferedSeqs(r, 0)
	if err != nil {
		return nil, 0, nil, err
	}
	offered := make(map[int64]bool, len(offeredList))
	for _, seq := range offeredList {
		offered[seq] = true
	}
	if err := classifyUnsavedBackendOutcomes(r, out, offered, byRun); err != nil {
		return nil, 0, nil, err
	}
	return out, unsaved, offered, nil
}

// queryBackendOutcomes projects RunID and Backend for saved rows so historical
// endings already recorded in backend_outcomes neither read overflow payload
// pages nor run json.Unmarshal; only unsaved rows decode their full payload.
func queryBackendOutcomes(r *store.Reader, afterSeq int64) ([]backendOutcome, int, error) {
	rows, err := r.Query(`SELECT e.seq, COALESCE(o.class,''), COALESCE(o.closes,0),
 CASE WHEN o.class IS NOT NULL AND o.class <> '' THEN COALESCE(NULLIF(runs.run_id,''), NULLIF(e.object_id,''), json_extract(e.payload,'$.run_id'), '') ELSE '' END,
 CASE WHEN o.class IS NOT NULL AND o.class <> '' THEN COALESCE(NULLIF(runs.backend,''), json_extract(e.payload,'$.backend'), '') ELSE '' END,
 CASE WHEN o.class IS NULL OR o.class = '' THEN e.payload ELSE '' END
 FROM events e
 LEFT JOIN backend_outcomes o ON o.terminal_seq=e.seq
 LEFT JOIN runs ON o.class IS NOT NULL AND o.class <> '' AND runs.run_id=e.object_id
 WHERE e.kind=? AND e.seq>? ORDER BY e.seq DESC`, run.FinishKind, afterSeq)
	if err != nil && strings.Contains(err.Error(), "no such table: backend_outcomes") {
		rows, err = r.Query(`SELECT seq, '', 0, '', '', payload FROM events WHERE kind=? AND seq>? ORDER BY seq DESC`, run.FinishKind, afterSeq)
	} else if err != nil && strings.Contains(err.Error(), "no such table: runs") {
		rows, err = r.Query(`SELECT e.seq, COALESCE(o.class,''), COALESCE(o.closes,0),
 CASE WHEN o.class IS NOT NULL AND o.class <> '' THEN COALESCE(NULLIF(e.object_id,''), json_extract(e.payload,'$.run_id'), '') ELSE '' END,
 CASE WHEN o.class IS NOT NULL AND o.class <> '' THEN COALESCE(json_extract(e.payload,'$.backend'), '') ELSE '' END,
 CASE WHEN o.class IS NULL OR o.class = '' THEN e.payload ELSE '' END
 FROM events e
 LEFT JOIN backend_outcomes o ON o.terminal_seq=e.seq
 WHERE e.kind=? AND e.seq>? ORDER BY e.seq DESC`, run.FinishKind, afterSeq)
	}
	if err != nil {
		return nil, 0, err
	}
	out := []backendOutcome{}
	unsaved := 0
	for rows.Next() {
		var o backendOutcome
		var runID, backend, raw string
		if err := rows.Scan(&o.seq, &o.class, &o.closes, &runID, &backend, &raw); err != nil {
			rows.Close()
			return nil, 0, err
		}
		o.saved = o.class != ""
		if o.saved {
			o.rec = run.Record{RunID: runID, Backend: backend}
		} else {
			backendOutcomeUnmarshals.Add(1)
			if err := json.Unmarshal([]byte(raw), &o.rec); err != nil {
				rows.Close()
				return nil, 0, err
			}
			unsaved++
		}
		if o.rec.Backend == "command" {
			if !o.saved {
				unsaved--
			}
			continue
		}
		out = append(out, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	return out, unsaved, nil
}

func queryOfferedSeqs(r *store.Reader, afterSeq int64) ([]int64, error) {
	offerRows, err := r.Query(`SELECT json_extract(payload,'$.terminal_seq') FROM events WHERE kind=? AND seq>?`, callOffered, afterSeq)
	if err != nil {
		return nil, err
	}
	var out []int64
	for offerRows.Next() {
		var seq int64
		if err := offerRows.Scan(&seq); err != nil {
			offerRows.Close()
			return nil, err
		}
		out = append(out, seq)
	}
	err = offerRows.Err()
	offerRows.Close()
	return out, err
}

func classifyUnsavedBackendOutcomes(r *store.Reader, out []backendOutcome, offered map[int64]bool, byRun map[string]*pendingWork) error {
	needClassify := false
	for i := range out {
		if offered[out[i].seq] {
			out[i].class, out[i].closes = AttemptAnswered, false
			continue
		}
		if !out[i].saved {
			needClassify = true
		}
	}
	if !needClassify {
		return nil
	}
	interrupted, _, err := readInterruptedRuns(r)
	if err != nil {
		return err
	}
	if byRun == nil {
		_, byRun, err = readPendingHistory(r)
		if err != nil {
			return err
		}
	}
	for i := range out {
		if out[i].saved || offered[out[i].seq] {
			continue
		}
		rec := out[i].rec
		log := logTail(rec.ArtifactRefs)
		out[i].class = classifyWorkAttempt(byRun[rec.RunID], rec, log)
		closes := out[i].class == AttemptInfrastructure && !answeredByAnotherRule(rec, interrupted, byRun)
		if closes && isUnknownFaceFailure(rec, log) {
			pending, err := rollPendingAt(r, out[i].seq)
			if err != nil {
				return err
			}
			if pending {
				closes = false
			}
		}
		out[i].closes = closes
	}
	return nil
}

// isUnknownFaceFailure reports whether a run exited because an adapter called a
// tillr CLI face that the installed binary does not yet provide.
func isUnknownFaceFailure(rec run.Record, log string) bool {
	if rec.State != run.StateExited {
		return false
	}
	if rec.ExitCode != nil && *rec.ExitCode == 0 {
		return false
	}
	return hasUnknownFaceText(rec.StderrTail) || hasUnknownFaceText(log)
}

func hasUnknownFaceText(s string) bool {
	idx := strings.Index(s, `unknown face "`)
	if idx < 0 {
		return false
	}
	rest := s[idx+len(`unknown face "`):]
	return strings.IndexByte(rest, '"') > 0
}

// rollPendingAt reports whether a binary roll for a newer commit was pending or
// running at event sequence atSeq (or at the current tail when atSeq <= 0).
func rollPendingAt(r *store.Reader, atSeq int64) (bool, error) {
	var pendingSeq int64
	var tip string
	var err error
	if atSeq > 0 {
		err = r.QueryRow(`SELECT seq, COALESCE(object_id,'') FROM events WHERE kind=? AND seq<? ORDER BY seq DESC LIMIT 1`, rollPendingKind, atSeq).Scan(&pendingSeq, &tip)
	} else {
		err = r.QueryRow(`SELECT seq, COALESCE(object_id,'') FROM events WHERE kind=? ORDER BY seq DESC LIMIT 1`, rollPendingKind).Scan(&pendingSeq, &tip)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var completed bool
	if atSeq > 0 {
		err = r.QueryRow(`SELECT EXISTS(
			SELECT 1 FROM events
			WHERE ((kind=? AND json_extract(payload,'$.action')='roll' AND COALESCE(json_extract(payload,'$.what'),'')='' AND object_id=?)
			    OR (kind=? AND object_id=?))
			  AND seq>? AND seq<?
		)`, postLandingKind, tip, restart.BuildKind, restart.RoleDrain, pendingSeq, atSeq).Scan(&completed)
	} else {
		err = r.QueryRow(`SELECT EXISTS(
			SELECT 1 FROM events
			WHERE ((kind=? AND json_extract(payload,'$.action')='roll' AND COALESCE(json_extract(payload,'$.what'),'')='' AND object_id=?)
			    OR (kind=? AND object_id=?))
			  AND seq>?
		)`, postLandingKind, tip, restart.BuildKind, restart.RoleDrain, pendingSeq).Scan(&completed)
	}
	if err != nil {
		return false, err
	}
	return !completed, nil
}

func seedBackendOutcomesState(r *store.Reader, head int64, byRun map[string]*pendingWork) (*backendOutcomesState, error) {
	entries, unsaved, offered, err := readBackendOutcomesFull(r, byRun)
	if err != nil {
		return nil, err
	}
	probes, err := queryBackendProbes(r, 0)
	if err != nil {
		return nil, err
	}
	return &backendOutcomesState{
		seq:     head,
		entries: entries,
		unsaved: unsaved,
		offered: offered,
		probes:  probes,
	}, nil
}

func (s *backendOutcomesState) advance(r *store.Reader, checkEvents bool, byRun map[string]*pendingWork) (*backendOutcomesState, bool, error) {
	head, err := r.EventHeadSeq()
	if err != nil {
		return nil, false, err
	}
	if s == nil || head < s.seq {
		next, err := seedBackendOutcomesState(r, head, byRun)
		return next, true, err
	}
	var newEntries []backendOutcome
	var newOffered []int64
	var newProbes map[string]int64
	if checkEvents && head > s.seq {
		changed, err := backendAvailabilityChanged(r, s.seq)
		if err != nil {
			return nil, false, err
		}
		if changed {
			newEntries, _, err = queryBackendOutcomes(r, s.seq)
			if err != nil {
				return nil, false, err
			}
			newOffered, err = queryOfferedSeqs(r, s.seq)
			if err != nil {
				return nil, false, err
			}
			newProbes, err = queryBackendProbes(r, s.seq)
			if err != nil {
				return nil, false, err
			}
		}
	}
	if len(newEntries) == 0 && len(newOffered) == 0 && len(newProbes) == 0 && s.unsaved == 0 {
		if !checkEvents || head == s.seq {
			return s, false, nil
		}
		return &backendOutcomesState{
			seq:     head,
			entries: s.entries,
			unsaved: 0,
			offered: s.offered,
			probes:  s.probes,
		}, false, nil
	}

	offered := s.offered
	if len(newOffered) > 0 {
		offered = maps.Clone(s.offered)
		for _, seq := range newOffered {
			offered[seq] = true
		}
	}
	probes := s.probes
	if len(newProbes) > 0 {
		probes = maps.Clone(s.probes)
		for backend, seq := range newProbes {
			if seq > probes[backend] {
				probes[backend] = seq
			}
		}
	}
	var entries []backendOutcome
	if len(newEntries) > 0 {
		entries = make([]backendOutcome, 0, len(newEntries)+len(s.entries))
		entries = append(entries, newEntries...)
		entries = append(entries, s.entries...)
	} else if s.unsaved > 0 || len(newOffered) > 0 {
		entries = slices.Clone(s.entries)
	} else {
		entries = s.entries
	}

	if s.unsaved > 0 {
		for i := len(newEntries); i < len(entries); i++ {
			if entries[i].saved {
				continue
			}
			var class AttemptClass
			var closes bool
			err := r.QueryRow(`SELECT class, closes FROM backend_outcomes WHERE terminal_seq=?`, entries[i].seq).Scan(&class, &closes)
			if err == nil && class != "" {
				entries[i].saved = true
				entries[i].class, entries[i].closes = class, closes
				entries[i].rec = run.Record{RunID: entries[i].rec.RunID, Backend: entries[i].rec.Backend}
			} else if err != nil && !errors.Is(err, sql.ErrNoRows) && !strings.Contains(err.Error(), "no such table: backend_outcomes") {
				return nil, false, err
			}
		}
	}
	if err := classifyUnsavedBackendOutcomes(r, entries, offered, byRun); err != nil {
		return nil, false, err
	}
	unsaved := 0
	for i := range entries {
		if !entries[i].saved {
			unsaved++
		}
	}

	if len(newEntries) == 0 && len(newOffered) == 0 && len(newProbes) == 0 && unsaved == s.unsaved {
		unchanged := true
		for i := range entries {
			if entries[i].saved != s.entries[i].saved || entries[i].class != s.entries[i].class || entries[i].closes != s.entries[i].closes {
				unchanged = false
				break
			}
		}
		if unchanged {
			if !checkEvents || head == s.seq {
				return s, false, nil
			}
			return &backendOutcomesState{
				seq:     head,
				entries: s.entries,
				unsaved: s.unsaved,
				offered: s.offered,
				probes:  s.probes,
			}, false, nil
		}
	}

	nextSeq := s.seq
	if checkEvents && head > nextSeq {
		nextSeq = head
	}
	return &backendOutcomesState{
		seq:     nextSeq,
		entries: entries,
		unsaved: unsaved,
		offered: offered,
		probes:  probes,
	}, true, nil
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
	// An exited workflow evaluator is judged from its verdict file, not its
	// process footer. Its exit alone is not evidence that the backend is closed.
	if decision.Workflow != nil && decision.NodeIndex >= 0 && decision.NodeIndex < len(decision.Workflow.Stages) && evaluatorNode(decision) && rec.State == run.StateExited {
		return true
	}
	gone, err := failedDesignerResume(decision, rec)
	return err == nil && gone
}

// readInterruptedRuns answers the runs whose latest segment the loop ended
// itself. An interruption describes the segment it ended. A later continuation
// of the same run (run.resume) is that run's new segment, and its outcome is judged.
func readInterruptedRuns(r *store.Reader) (interrupted, restaged map[string]bool, err error) {
	interrupted, restaged = map[string]bool{}, map[string]bool{}
	events, err := r.EventsOfKind(0, loopOrphanedKind, staleBaseKind, run.ResumeKind)
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
	causes := map[int64]string{}
	for _, o := range outcomes {
		if !o.saved {
			pending = append(pending, o)
			if o.closes {
				causes[o.seq] = infrastructureDetail(o.rec, logTail(o.rec.ArtifactRefs))
			}
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
					Cause       string       `json:"cause,omitempty"`
				}{o.seq, o.class, o.closes, causes[o.seq]}, time.Now().UTC()); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
