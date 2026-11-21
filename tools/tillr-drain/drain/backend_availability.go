package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const backendProbeKind = "drain.backend-probe"

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
	outcomes, err := readBackendOutcomesFrom(r, byRun)
	if err != nil {
		return nil, err
	}
	closed := map[string]backendOutcome{}
	sequences := map[string]int64{}
	for _, o := range outcomes {
		if o.class == AttemptInterrupted {
			continue
		}
		name := o.rec.Backend
		if _, seen := sequences[name]; seen {
			continue
		}
		// The latest ending decides. A saved ending keeps its saved fact; an
		// unsaved one was classified by the read above, which saves nothing.
		sequences[name] = o.seq
		if o.closes {
			closed[name] = o
		}
	}
	probes, err := r.Query(`SELECT object_id,MAX(seq) FROM events WHERE kind=? AND json_extract(payload,'$.success')=1 GROUP BY object_id`, backendProbeKind)
	if err != nil {
		return nil, err
	}
	defer probes.Close()
	for probes.Next() {
		var backend string
		var seq int64
		if err := probes.Scan(&backend, &seq); err != nil {
			return nil, err
		}
		if seq > sequences[backend] {
			delete(closed, backend)
		}
	}
	return closed, probes.Err()
}

// ProbeBackend runs an operator-chosen smoke check, not a quota reading or a
// timer. Its exit is recorded even on failure; only exit zero reopens a backend.
// Output is discarded so credentials cannot enter the event log.
func ProbeBackend(ctx context.Context, path, root, backend string, argv []string) error {
	if strings.TrimSpace(backend) == "" || backend == "command" || len(argv) == 0 || argv[0] == "" {
		return Usagef("probe-backend needs --store, --backend and a smoke-check command after --")
	}
	if err := RecordBackendOutcomes(ctx, path); err != nil {
		return err
	}
	s, err := store.Open(ctx, path, true)
	if err != nil {
		return err
	}
	defer s.Close()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = root
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	probeErr := runCheckGroup(cmd)
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		at := time.Now().UTC()
		payload, err := json.Marshal(struct {
			Success bool `json:"success"`
		}{probeErr == nil})
		if err != nil {
			return err
		}
		seq, err := tx.Append(event.Event{Kind: backendProbeKind, ObjectType: "run", ObjectID: &backend, Payload: payload, At: at})
		if err != nil || probeErr != nil {
			return err
		}
		return reopenBackendWindow(tx, backend, seq, at)
	})
	if err != nil {
		return err
	}
	if probeErr != nil {
		return Externalf("backend %s probe failed: %v; backend remains closed", backend, probeErr)
	}
	return nil
}

// Describe the recorded evidence for the backend the decision is waiting on.
func describeBackendHold(r *store.Reader, d *Decision, policy *Policy, at time.Time, shut map[string]bool) error {
	if d.Action != ActionHold || d.Reason != ReasonBackendWindow || policy == nil {
		return nil
	}
	closed, err := readInfrastructureClosedFrom(r, nil)
	if err != nil {
		return err
	}
	start := policy.firstBackend(itemRow{ItemID: d.ItemID, Seat: d.Seat})
	if d.RunRequest != nil {
		start = d.RunRequest.Backend
	}
	if d.DeferredBy != "" {
		start = d.DeferredBy
	} else if len(d.Parallel) > 0 {
		for _, node := range parallelNodes(*d) {
			if node.RunRequest == nil || !shut[node.RunRequest.Backend] && closed[node.RunRequest.Backend].seq == 0 {
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
		failure, err := readRecordedInfrastructureFailure(r, outcome.rec.ItemID, "$.run_id", outcome.rec.RunID)
		if err != nil {
			return err
		}
		d.BackendHoldCause = failure.Detail
		if d.BackendHoldCause == "" {
			err := r.QueryRow(`SELECT COALESCE(json_extract(payload,'$.cause'),'') FROM events
 WHERE kind=? AND object_id=? AND json_extract(payload,'$.terminal_seq')=?`, backendOutcomeKind, start, outcome.seq).Scan(&d.BackendHoldCause)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if d.BackendHoldCause == "" {
				d.BackendHoldCause = infrastructureDetail(outcome.rec, logTail(outcome.rec.ArtifactRefs))
			}
		}
		d.BackendHoldMover = "operator: repair the backend and record a successful tillr-drain probe-backend"
	} else if start != "" {
		if err := describeQuotaHold(r, d, policy, at); err != nil {
			return err
		}
	}
	identifyDecision(d)
	return nil
}

// Describe the same quota facts that admission reads; a missing or stale
// observation is itself a refusal to admit work, not a provider outage.
func describeQuotaHold(r *store.Reader, d *Decision, policy *Policy, at time.Time) error {
	name := d.DeferredBy
	windows, err := readBackendWindows(r, at)
	if err != nil {
		return err
	}
	if reset, ok := windows[name]; ok {
		var kind, runID string
		if err := r.QueryRow(`SELECT kind, run_id FROM backend_windows WHERE backend=?`, name).Scan(&kind, &runID); err != nil {
			return err
		}
		d.BackendHoldCause = fmt.Sprintf("%s quota refusal recorded by run %s", kind, runID)
		d.BackendHoldMover = "provider quota resets at " + reset.Format(time.RFC3339) + "; after an early reset, record a successful probe-backend"
		return nil
	}
	b := policy.Backends[name]
	statuses, err := readQuotaStatuses(r)
	if err != nil {
		return err
	}
	d.BackendHoldCause = "no quota reading recorded"
	d.BackendHoldMover = "quota probe: record a fresh, successful reading that admits the backend"
	for _, q := range statuses {
		if q.Backend != name {
			continue
		}
		command, _ := json.Marshal(b.Quota)
		switch {
		case !quotaFresh(q, string(command), at):
			d.BackendHoldCause = "no current quota reading (last observed " + q.ObservedAt.Format(time.RFC3339) + ")"
		case q.Error != "":
			d.BackendHoldCause = q.Error
			d.BackendHoldMover = "operator: repair the quota probe so it records a successful reading"
		default:
			var causes []string
			for _, w := range q.Windows {
				if w.Fraction >= 1-b.Reserve {
					cause := fmt.Sprintf("%s quota usage %g reached the admission limit %g", w.Kind, w.Fraction, 1-b.Reserve)
					if w.ResetAt != nil {
						cause += " (reset " + w.ResetAt.Format(time.RFC3339) + ")"
					}
					causes = append(causes, cause)
				}
			}
			d.BackendHoldCause = strings.Join(causes, "; ")
		}
		break
	}
	return nil
}
