package drain

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/policy"
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
