package policy

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

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/internal/runhistory"
	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	callOfferedKind = "call.offered"
	staleBaseKind   = "drain.stale-base"
	rollPendingKind = "drain.roll-pending"
	postLandingKind = "drain.post-landing-action"
)

// BackendAvailabilityChanged is an indexed check of just the tail. Changes to
// dispatch history (including resumes and orphans) are checked separately.
func BackendAvailabilityChanged(r *store.Reader, after int64) (bool, error) {
	var changed bool
	err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind IN (?, ?, ?, ?, ?, ?) AND seq > ?)`,
		run.FinishKind, BackendOutcomeKind, BackendProbeKind, callOfferedKind, staleBaseKind, rollPendingKind, after).Scan(&changed)
	return changed, err
}

const BackendOutcomeKind = "drain.backend-outcome"

func init() {
	if err := event.Register(BackendOutcomeKind, drainmodel.Utility); err != nil {
		panic(err)
	}
}

type BackendOutcome struct {
	Seq int64
	// For a saved outcome, ReadBackendOutcomesWith populates only RunID and
	// Backend on Rec; ResolveInfrastructureClosed loads the full record for
	// any backend that remains closed.
	Rec    run.Record
	Class  AttemptClass
	Closes bool
	// Saved says the Class and Closes were read from backend_outcomes. An unsaved
	// ending is classified in memory by whoever reads it; only the loop saves.
	Saved bool
}

// These counters let claim and refresh tests detect typed history scans and payload unmarshals.
var BackendOutcomesReads, BackendOutcomesUnderWrite, BackendOutcomeUnmarshals atomic.Int64

// BackendOutcomesState caches folded run.finish dispositions, call.offered
// overrides, and successful drain.backend-probe sequences up to Seq so
// startSnapshot refreshes only inspect new events and unsaved endings.
type BackendOutcomesState struct {
	Seq     int64
	Entries []BackendOutcome
	Unsaved int
	Offered map[int64]bool
	Probes  map[string]int64
}

func ReadBackendOutcomesWith(r *store.Reader, classify func(*store.Reader, []BackendOutcome, map[int64]bool) error) ([]BackendOutcome, int, map[int64]bool, error) {
	BackendOutcomesReads.Add(1)
	if store.HoldsWrite() {
		BackendOutcomesUnderWrite.Add(1)
	}
	out, unsaved, err := QueryBackendOutcomes(r, 0)
	if err != nil {
		return nil, 0, nil, err
	}
	offeredList, err := QueryOfferedSeqs(r, 0)
	if err != nil {
		return nil, 0, nil, err
	}
	offered := make(map[int64]bool, len(offeredList))
	for _, seq := range offeredList {
		offered[seq] = true
	}
	if classify != nil {
		if err := classify(r, out, offered); err != nil {
			return nil, 0, nil, err
		}
	}
	return out, unsaved, offered, nil
}

// QueryBackendOutcomes projects RunID and Backend for saved rows so historical
// endings already recorded in backend_outcomes neither read overflow payload
// pages nor run json.Unmarshal; only unsaved rows decode their full payload.
func QueryBackendOutcomes(r *store.Reader, afterSeq int64) ([]BackendOutcome, int, error) {
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
	out := []BackendOutcome{}
	unsaved := 0
	for rows.Next() {
		var o BackendOutcome
		var runID, backend, raw string
		if err := rows.Scan(&o.Seq, &o.Class, &o.Closes, &runID, &backend, &raw); err != nil {
			rows.Close()
			return nil, 0, err
		}
		o.Saved = o.Class != ""
		if o.Saved {
			o.Rec = run.Record{RunID: runID, Backend: backend}
		} else {
			BackendOutcomeUnmarshals.Add(1)
			if err := json.Unmarshal([]byte(raw), &o.Rec); err != nil {
				rows.Close()
				return nil, 0, err
			}
			unsaved++
		}
		if o.Rec.Backend == "command" {
			if !o.Saved {
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

// QueryBackendOutcomesForRuns reads backend outcomes scoped to runIDs via events_by_object.
func QueryBackendOutcomesForRuns(r *store.Reader, runIDs []string) ([]BackendOutcome, int, error) {
	if len(runIDs) == 0 {
		return nil, 0, nil
	}
	var out []BackendOutcome
	unsaved := 0
	const chunk = 400
	for start := 0; start < len(runIDs); start += chunk {
		end := start + chunk
		if end > len(runIDs) {
			end = len(runIDs)
		}
		batch := runIDs[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, len(batch)+1)
		for _, id := range batch {
			args = append(args, id)
		}
		args = append(args, run.FinishKind)
		rows, err := r.Query(`SELECT e.seq, COALESCE(o.class,''), COALESCE(o.closes,0),
 CASE WHEN o.class IS NOT NULL AND o.class <> '' THEN COALESCE(NULLIF(runs.run_id,''), NULLIF(e.object_id,''), json_extract(e.payload,'$.run_id'), '') ELSE '' END,
 CASE WHEN o.class IS NOT NULL AND o.class <> '' THEN COALESCE(NULLIF(runs.backend,''), json_extract(e.payload,'$.backend'), '') ELSE '' END,
 CASE WHEN o.class IS NULL OR o.class = '' THEN e.payload ELSE '' END
 FROM events e
 LEFT JOIN backend_outcomes o ON o.terminal_seq=e.seq
 LEFT JOIN runs ON o.class IS NOT NULL AND o.class <> '' AND runs.run_id=e.object_id
 WHERE e.object_id IN (`+placeholders+`) AND e.kind=? ORDER BY e.seq DESC`, args...)
		if err != nil && strings.Contains(err.Error(), "no such table: backend_outcomes") {
			rows, err = r.Query(`SELECT seq, '', 0, '', '', payload FROM events WHERE object_id IN (`+placeholders+`) AND kind=? ORDER BY seq DESC`, args...)
		} else if err != nil && strings.Contains(err.Error(), "no such table: runs") {
			rows, err = r.Query(`SELECT e.seq, COALESCE(o.class,''), COALESCE(o.closes,0),
 CASE WHEN o.class IS NOT NULL AND o.class <> '' THEN COALESCE(NULLIF(e.object_id,''), json_extract(e.payload,'$.run_id'), '') ELSE '' END,
 CASE WHEN o.class IS NOT NULL AND o.class <> '' THEN COALESCE(json_extract(e.payload,'$.backend'), '') ELSE '' END,
 CASE WHEN o.class IS NULL OR o.class = '' THEN e.payload ELSE '' END
 FROM events e
 LEFT JOIN backend_outcomes o ON o.terminal_seq=e.seq
 WHERE e.object_id IN (`+placeholders+`) AND e.kind=? ORDER BY e.seq DESC`, args...)
		}
		if err != nil {
			return nil, 0, err
		}
		for rows.Next() {
			var o BackendOutcome
			var runID, backend, raw string
			if err := rows.Scan(&o.Seq, &o.Class, &o.Closes, &runID, &backend, &raw); err != nil {
				rows.Close()
				return nil, 0, err
			}
			o.Saved = o.Class != ""
			if o.Saved {
				o.Rec = run.Record{RunID: runID, Backend: backend}
			} else {
				BackendOutcomeUnmarshals.Add(1)
				if err := json.Unmarshal([]byte(raw), &o.Rec); err != nil {
					rows.Close()
					return nil, 0, err
				}
				unsaved++
			}
			if o.Rec.Backend == "command" {
				if !o.Saved {
					unsaved--
				}
				continue
			}
			out = append(out, o)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, 0, err
		}
		rows.Close()
	}
	return out, unsaved, nil
}

func QueryOfferedSeqs(r *store.Reader, afterSeq int64) ([]int64, error) {
	offerRows, err := r.Query(`SELECT json_extract(payload,'$.terminal_seq') FROM events WHERE kind=? AND seq>?`, callOfferedKind, afterSeq)
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

// IsUnknownFaceFailure reports whether a run exited because an adapter called a
// tillr CLI face that the installed binary does not yet provide.
func IsUnknownFaceFailure(rec run.Record, log string) bool {
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

// RollPendingAt reports whether a binary roll for a newer commit was pending or
// running at event sequence atSeq (or at the current tail when atSeq <= 0).
func RollPendingAt(r *store.Reader, atSeq int64) (bool, error) {
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

func SeedBackendOutcomesState(r *store.Reader, head int64, classify func(*store.Reader, []BackendOutcome, map[int64]bool) error) (*BackendOutcomesState, error) {
	entries, unsaved, offered, err := ReadBackendOutcomesWith(r, classify)
	if err != nil {
		return nil, err
	}
	probes, err := QueryBackendProbes(r, 0)
	if err != nil {
		return nil, err
	}
	return &BackendOutcomesState{
		Seq:     head,
		Entries: entries,
		Unsaved: unsaved,
		Offered: offered,
		Probes:  probes,
	}, nil
}

func (s *BackendOutcomesState) Advance(r *store.Reader, checkEvents bool, classify func(*store.Reader, []BackendOutcome, map[int64]bool) error) (*BackendOutcomesState, bool, error) {
	head, err := r.EventHeadSeq()
	if err != nil {
		return nil, false, err
	}
	if s == nil || head < s.Seq {
		next, err := SeedBackendOutcomesState(r, head, classify)
		return next, true, err
	}
	var newEntries []BackendOutcome
	var newOffered []int64
	var newProbes map[string]int64
	if checkEvents && head > s.Seq {
		changed, err := BackendAvailabilityChanged(r, s.Seq)
		if err != nil {
			return nil, false, err
		}
		if changed {
			newEntries, _, err = QueryBackendOutcomes(r, s.Seq)
			if err != nil {
				return nil, false, err
			}
			newOffered, err = QueryOfferedSeqs(r, s.Seq)
			if err != nil {
				return nil, false, err
			}
			newProbes, err = QueryBackendProbes(r, s.Seq)
			if err != nil {
				return nil, false, err
			}
		}
	}
	if len(newEntries) == 0 && len(newOffered) == 0 && len(newProbes) == 0 && s.Unsaved == 0 {
		if !checkEvents || head == s.Seq {
			return s, false, nil
		}
		return &BackendOutcomesState{
			Seq:     head,
			Entries: s.Entries,
			Unsaved: 0,
			Offered: s.Offered,
			Probes:  s.Probes,
		}, false, nil
	}

	offered := s.Offered
	if len(newOffered) > 0 {
		offered = maps.Clone(s.Offered)
		for _, seq := range newOffered {
			offered[seq] = true
		}
	}
	probes := s.Probes
	if len(newProbes) > 0 {
		probes = maps.Clone(s.Probes)
		for backend, seq := range newProbes {
			if seq > probes[backend] {
				probes[backend] = seq
			}
		}
	}
	var entries []BackendOutcome
	if len(newEntries) > 0 {
		entries = make([]BackendOutcome, 0, len(newEntries)+len(s.Entries))
		entries = append(entries, newEntries...)
		entries = append(entries, s.Entries...)
	} else if s.Unsaved > 0 || len(newOffered) > 0 {
		entries = slices.Clone(s.Entries)
	} else {
		entries = s.Entries
	}

	if s.Unsaved > 0 {
		for i := len(newEntries); i < len(entries); i++ {
			if entries[i].Saved {
				continue
			}
			var class AttemptClass
			var closes bool
			err := r.QueryRow(`SELECT class, closes FROM backend_outcomes WHERE terminal_seq=?`, entries[i].Seq).Scan(&class, &closes)
			if err == nil && class != "" {
				entries[i].Saved = true
				entries[i].Class, entries[i].Closes = class, closes
				entries[i].Rec = run.Record{RunID: entries[i].Rec.RunID, Backend: entries[i].Rec.Backend}
			} else if err != nil && !errors.Is(err, sql.ErrNoRows) && !strings.Contains(err.Error(), "no such table: backend_outcomes") {
				return nil, false, err
			}
		}
	}
	if classify != nil {
		if err := classify(r, entries, offered); err != nil {
			return nil, false, err
		}
	}
	unsaved := 0
	for i := range entries {
		if !entries[i].Saved {
			unsaved++
		}
	}

	if len(newEntries) == 0 && len(newOffered) == 0 && len(newProbes) == 0 && unsaved == s.Unsaved {
		unchanged := true
		for i := range entries {
			if entries[i].Saved != s.Entries[i].Saved || entries[i].Class != s.Entries[i].Class || entries[i].Closes != s.Entries[i].Closes {
				unchanged = false
				break
			}
		}
		if unchanged {
			if !checkEvents || head == s.Seq {
				return s, false, nil
			}
			return &BackendOutcomesState{
				Seq:     head,
				Entries: s.Entries,
				Unsaved: s.Unsaved,
				Offered: s.Offered,
				Probes:  s.Probes,
			}, false, nil
		}
	}

	nextSeq := s.Seq
	if checkEvents && head > nextSeq {
		nextSeq = head
	}
	return &BackendOutcomesState{
		Seq:     nextSeq,
		Entries: entries,
		Unsaved: unsaved,
		Offered: offered,
		Probes:  probes,
	}, true, nil
}

// ReadInterruptedRuns answers the runs whose latest segment the loop ended
// itself. An interruption describes the segment it ended. A later continuation
// of the same run (run.resume) is that run's new segment, and its outcome is judged.
func ReadInterruptedRuns(r *store.Reader) (interrupted, restaged map[string]bool, err error) {
	interrupted, restaged = map[string]bool{}, map[string]bool{}
	events, err := r.EventsOfKind(0, runhistory.LoopOrphanedKind, staleBaseKind, run.ResumeKind)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range events {
		if e.ObjectID == nil {
			continue
		}
		switch e.Kind {
		case runhistory.LoopOrphanedKind:
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

// ReadInterruptedRunsFor reads interrupted and restaged flags for the named run IDs via events_by_object.
func ReadInterruptedRunsFor(r *store.Reader, runIDs []string) (interrupted, restaged map[string]bool, err error) {
	interrupted, restaged = map[string]bool{}, map[string]bool{}
	if len(runIDs) == 0 {
		return interrupted, restaged, nil
	}
	events, err := r.EventsForObjects(0, runIDs, runhistory.LoopOrphanedKind, staleBaseKind, run.ResumeKind)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range events {
		if e.ObjectID == nil {
			continue
		}
		switch e.Kind {
		case runhistory.LoopOrphanedKind:
			interrupted[*e.ObjectID] = true
		case staleBaseKind:
			interrupted[*e.ObjectID], restaged[*e.ObjectID] = true, true
		case run.ResumeKind:
			delete(interrupted, *e.ObjectID)
		}
	}
	return interrupted, restaged, nil
}

// RecordBackendOutcomesWith saves the class and the availability fact of every
// ending not saved yet, including standalone and judge runs.
func RecordBackendOutcomesWith(
	ctx context.Context,
	path string,
	readOutcomes func(*store.Reader) ([]BackendOutcome, error),
	infraDetail func(run.Record, string) string,
) error {
	reader, err := store.OpenRead(ctx, path)
	if err != nil {
		return err
	}
	var outcomes []BackendOutcome
	err = reader.ReadTx(ctx, func(r *store.Reader) error { var err error; outcomes, err = readOutcomes(r); return err })
	reader.Close()
	if err != nil {
		return err
	}
	pending := []BackendOutcome{}
	causes := map[int64]string{}
	for _, o := range outcomes {
		if !o.Saved {
			pending = append(pending, o)
			if o.Closes && infraDetail != nil {
				causes[o.Seq] = infraDetail(o.Rec, LogTail(o.Rec.ArtifactRefs))
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
			inserted, err := tx.Exec(`INSERT OR IGNORE INTO backend_outcomes(terminal_seq,class,closes) VALUES (?,?,?)`, o.Seq, o.Class, o.Closes)
			if err != nil {
				return err
			}
			count, err := inserted.RowsAffected()
			if err != nil {
				return err
			}
			if count > 0 {
				if err := runhistory.AppendEffect(tx, BackendOutcomeKind, o.Rec.Backend, struct {
					TerminalSeq int64        `json:"terminal_seq"`
					Class       AttemptClass `json:"class"`
					Closes      bool         `json:"closes"`
					Cause       string       `json:"cause,omitempty"`
				}{o.Seq, o.Class, o.Closes, causes[o.Seq]}, time.Now().UTC()); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
