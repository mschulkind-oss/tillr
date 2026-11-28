package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/policy"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func readInfrastructureClosed(r *store.Reader) (map[string]bool, error) {
	outcomes, err := readInfrastructureClosedFrom(r, nil)
	closed := make(map[string]bool, len(outcomes))
	for name := range outcomes {
		closed[name] = true
	}
	return closed, err
}

// readInfrastructureClosedFrom projects availability from recorded facts. A run's
// terminal event and an explicit successful probe share the store's event order;
// a clock, policy edit, restart, or quota reading cannot reopen infrastructure.
func readInfrastructureClosedFrom(r *store.Reader, byRun map[string]*pendingWork) (map[string]backendOutcome, error) {
	var maxSavedSeq int64
	if err := r.QueryRow(`SELECT COALESCE(MAX(terminal_seq), 0) FROM backend_outcomes`).Scan(&maxSavedSeq); err != nil {
		if strings.Contains(err.Error(), "no such table: backend_outcomes") {
			outcomes, err := readBackendOutcomesFrom(r, byRun)
			if err != nil {
				return nil, err
			}
			probes, err := queryBackendProbes(r, 0)
			if err != nil {
				return nil, err
			}
			return resolveInfrastructureClosed(r, outcomes, probes, nil)
		}
		return nil, err
	}

	candidates := map[string]backendOutcome{}

	// 1. Saved closing outcomes (typically 0 rows).
	rows, err := r.Query(`SELECT o.terminal_seq, o.class, o.closes,
 COALESCE(NULLIF(runs.run_id,''), NULLIF(e.object_id,''), json_extract(e.payload,'$.run_id'), ''),
 COALESCE(NULLIF(runs.backend,''), json_extract(e.payload,'$.backend'), ''),
 e.payload
 FROM backend_outcomes o
 JOIN events e ON e.seq=o.terminal_seq
 LEFT JOIN runs ON runs.run_id=e.object_id
 WHERE o.closes=1
 ORDER BY o.terminal_seq DESC`)
	if err != nil && strings.Contains(err.Error(), "no such table: runs") {
		rows, err = r.Query(`SELECT o.terminal_seq, o.class, o.closes,
 COALESCE(NULLIF(e.object_id,''), json_extract(e.payload,'$.run_id'), ''),
 COALESCE(json_extract(e.payload,'$.backend'), ''),
 e.payload
 FROM backend_outcomes o
 JOIN events e ON e.seq=o.terminal_seq
 WHERE o.closes=1
 ORDER BY o.terminal_seq DESC`)
	}
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var o backendOutcome
		var runID, backend string
		var raw []byte
		if err := rows.Scan(&o.Seq, &o.Class, &o.Closes, &runID, &backend, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		if backend == "command" {
			continue
		}
		if backend != "" {
			if _, seen := candidates[backend]; seen {
				continue
			}
		}
		o.Saved = true
		backendOutcomeUnmarshals.Add(1)
		if err := json.Unmarshal(raw, &o.Rec); err != nil {
			o.Rec = run.Record{RunID: runID, Backend: backend}
		}
		if o.Rec.Backend == "" {
			o.Rec.Backend = backend
		}
		if o.Rec.RunID == "" {
			o.Rec.RunID = runID
		}
		if o.Rec.Backend == "" || o.Rec.Backend == "command" {
			continue
		}
		if _, seen := candidates[o.Rec.Backend]; !seen {
			candidates[o.Rec.Backend] = o
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// 2. Any unsaved run.finish events after maxSavedSeq.
	unsaved, _, err := policy.QueryBackendOutcomes(r, maxSavedSeq)
	if err != nil {
		return nil, err
	}
	var latestNonCloseSeq = map[string]int64{}
	if len(unsaved) > 0 {
		offeredList, err := policy.QueryOfferedSeqs(r, maxSavedSeq)
		if err != nil {
			return nil, err
		}
		offered := make(map[int64]bool, len(offeredList))
		for _, seq := range offeredList {
			offered[seq] = true
		}
		if err := classifyUnsavedBackendOutcomes(r, unsaved, offered, byRun); err != nil {
			return nil, err
		}
		for _, o := range unsaved {
			if o.Rec.Backend == "" || o.Rec.Backend == "command" {
				continue
			}
			if o.Closes {
				if prev, seen := candidates[o.Rec.Backend]; !seen || o.Seq > prev.Seq {
					candidates[o.Rec.Backend] = o
				}
			} else if o.Class != policy.AttemptInterrupted {
				if o.Seq > latestNonCloseSeq[o.Rec.Backend] {
					latestNonCloseSeq[o.Rec.Backend] = o.Seq
				}
			}
		}
	}

	if len(candidates) == 0 {
		return map[string]backendOutcome{}, nil
	}

	// 3. Check if any candidate was superseded by a newer non-interrupted outcome, probe, or offered call.
	closed := map[string]backendOutcome{}
	for backend, cand := range candidates {
		if latestNonCloseSeq[backend] > cand.Seq {
			continue
		}
		var superseded bool
		if err := r.QueryRow(`SELECT EXISTS (
  SELECT 1 FROM events e JOIN backend_outcomes o ON o.terminal_seq=e.seq
  WHERE e.kind=? AND e.seq>? AND o.class<>'interrupted'
    AND json_extract(e.payload,'$.backend')=?
) OR EXISTS (
  SELECT 1 FROM events p
  WHERE p.kind='drain.backend-probe' AND p.object_id=? AND p.seq>?
    AND json_extract(p.payload,'$.success')=1
) OR EXISTS (
  SELECT 1 FROM events p
  WHERE p.kind='call.offered' AND json_extract(p.payload,'$.terminal_seq')=?
)`, run.FinishKind, cand.Seq, backend, backend, cand.Seq, cand.Seq).Scan(&superseded); err != nil {
			return nil, err
		}
		if !superseded {
			closed[backend] = cand
		}
	}
	return closed, nil
}

// ProbeBackend runs an operator-chosen smoke check, not a quota reading or a
// timer. Its exit is recorded even on failure; only exit zero reopens a backend.
// Output is discarded so credentials cannot enter the event log.
func ProbeBackend(ctx context.Context, path, root, backend string, argv []string) error {
	return policy.ProbeBackendWith(ctx, path, root, backend, argv, RecordBackendOutcomes)
}

// Describe the recorded evidence for the backend the decision is waiting on.
func describeBackendHold(r *store.Reader, d *Decision, p *Policy, at time.Time, shut map[string]bool) error {
	return describeBackendHoldWithClosed(r, d, p, at, shut, nil)
}

func describeBackendHoldWithClosed(r *store.Reader, d *Decision, p *Policy, at time.Time, shut map[string]bool, closed map[string]backendOutcome) error {
	if d.Action != ActionHold || d.Reason != ReasonBackendWindow || p == nil {
		return nil
	}
	if closed == nil {
		var err error
		closed, err = readInfrastructureClosedFrom(r, nil)
		if err != nil {
			return err
		}
	}
	start := p.FirstBackend(d.ItemID, d.Seat)
	if d.RunRequest != nil {
		start = d.RunRequest.Backend
	}
	if d.DeferredBy != "" {
		start = d.DeferredBy
	} else if len(d.Parallel) > 0 {
		for _, node := range parallelNodes(*d) {
			if node.RunRequest == nil || !shut[node.RunRequest.Backend] && closed[node.RunRequest.Backend].Seq == 0 {
				continue
			}
			rec, err := terminalRecord(r, node.RunRequest.RunID)
			if err != nil {
				return err
			}
			if rec == nil {
				start, d.Seat = node.RunRequest.Backend, node.Seat
				break
			}
		}
	}
	d.DeferredBy = start
	if outcome, ok := closed[start]; ok {
		d.Reason = ReasonBackendClosed
		failure, err := readRecordedInfrastructureFailure(r, outcome.Rec.ItemID, "$.run_id", outcome.Rec.RunID)
		if err != nil {
			return err
		}
		d.BackendHoldCause = failure.Detail
		if d.BackendHoldCause == "" {
			err := r.QueryRow(`SELECT COALESCE(json_extract(payload,'$.cause'),'') FROM events
 WHERE kind=? AND object_id=? AND json_extract(payload,'$.terminal_seq')=?`, backendOutcomeKind, start, outcome.Seq).Scan(&d.BackendHoldCause)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if d.BackendHoldCause == "" {
			d.BackendHoldCause = infrastructureDetail(outcome.Rec, logTail(outcome.Rec.ArtifactRefs))
		}
		d.BackendHoldMover = "operator: repair the backend and record a successful tillr-drain probe-backend"
	} else if start != "" {
		if err := describeQuotaHold(r, d, p, at); err != nil {
			return err
		}
	}
	identifyDecision(d)
	return nil
}

// Describe the same quota facts that admission reads; a missing or stale
// observation is itself a refusal to admit work, not a provider outage.
func describeQuotaHold(r *store.Reader, d *Decision, p *Policy, at time.Time) error {
	cause, mover, err := policy.DescribeQuotaHold(r, d.DeferredBy, p, at)
	if err != nil {
		return err
	}
	d.BackendHoldCause, d.BackendHoldMover = cause, mover
	return nil
}

func describeQuotaHoldFromStatuses(d *Decision, b Backend, name string, statuses []QuotaStatus, at time.Time) {
	d.BackendHoldCause, d.BackendHoldMover = policy.DescribeQuotaHoldFromStatuses(b, name, statuses, at)
}
