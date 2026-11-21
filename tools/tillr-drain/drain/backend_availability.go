package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
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

// A mixed quota/infrastructure hold names the closed backends on this route and
// their mover. The walk uses the same seat boundary as dispatch.
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
	names := []string{}
	seen := map[string]bool{}
	for name := start; name != "" && !seen[name]; {
		seen[name] = true
		if _, isClosed := closed[name]; isClosed {
			names = append(names, name)
		}
		next, ok := policy.nextBackend(name, d.Seat)
		if !ok {
			break
		}
		name = next
	}
	// A detached first-node retry waits for its recorded backend even if
	// the seat's route also has other backends.
	pinnedRetry := d.LaneID == "" && d.RunRequest != nil && d.WorkflowRestart && d.NodeInfraFailures == 0
	if policy.singleBackendSeat(d.Seat) || pinnedRetry {
		d.DeferredBy = start
		if outcome, ok := closed[start]; ok {
			d.Reason = ReasonBackendClosed
			d.BackendHoldCause = fmt.Sprintf("infrastructure failure: run %s (%s)", outcome.rec.RunID, outcome.rec.State)
			if outcome.rec.ExitCode != nil {
				d.BackendHoldCause += fmt.Sprintf(", exit %d", *outcome.rec.ExitCode)
			}
			d.BackendHoldMover = "operator: repair the backend and record a successful probe-backend"
		} else if err := describeQuotaHold(r, d, policy, at); err != nil {
			return err
		}
		identifyDecision(d)
		return nil
	}
	d.DeferredBy = ""
	if len(names) > 0 {
		d.Reason = ReasonBackendClosed
		d.DeferredBy = strings.Join(names, ", ")
		identifyDecision(d)
	}
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

// Retain node identity, timeout, and parallel-group provenance while changing
// only the backend's execution fields.
func rerouteNodeRequest(policy *Policy, d Decision, name string) *run.Request {
	request := *policy.request(d.ItemID, d.LaneID, d.RunRequest.RunID, name)
	request.TimeoutMS = d.RunRequest.TimeoutMS
	request.ReadOnlyGroup = d.RunRequest.ReadOnlyGroup
	request.NodeID = d.RunRequest.NodeID
	request.Seat = d.RunRequest.Seat
	request.Workflow = d.RunRequest.Workflow
	request.Node = d.RunRequest.Node
	request.CorrelationID = d.RunRequest.CorrelationID
	stampDecisionRequest(d, &request)
	return &request
}
