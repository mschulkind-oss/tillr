package drain

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

const backendProbeKind = "drain.backend-probe"

// readInfrastructureClosed projects availability from recorded facts. A run's
// terminal event and an explicit successful probe share the store's event order;
// a clock, policy edit, restart, or quota reading cannot reopen infrastructure.
func readInfrastructureClosed(r *store.Reader) (map[string]bool, error) {
	outcomes, err := readBackendOutcomes(r)
	if err != nil {
		return nil, err
	}
	closed := map[string]bool{}
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
		closed[name] = o.closes
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
		return appendEffect(tx, backendProbeKind, backend, struct {
			Success bool `json:"success"`
		}{probeErr == nil}, time.Now().UTC())
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
func describeBackendHold(r *store.Reader, d *Decision, policy *Policy) error {
	if d.Action != ActionHold || d.Reason != ReasonBackendWindow || policy == nil {
		return nil
	}
	closed, err := readInfrastructureClosed(r)
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
			if node.RunRequest == nil || !closed[node.RunRequest.Backend] {
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
		if closed[name] {
			names = append(names, name)
		}
		next, _, ok := policy.nextBackend(name, d.Seat)
		if !ok {
			break
		}
		name = next
	}
	d.DeferredBy = ""
	if len(names) > 0 {
		d.Reason = ReasonBackendClosed
		d.DeferredBy = strings.Join(names, ", ")
		identifyDecision(d)
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
	request.CorrelationID = d.RunRequest.CorrelationID
	return &request
}
