package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavyrun"
	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const BackendProbeKind = "drain.backend-probe"

func init() {
	if err := event.Register(BackendProbeKind, drainmodel.Utility); err != nil {
		panic(err)
	}
}

func (s *BackendOutcomesState) InfrastructureClosed(r *store.Reader, prev map[string]BackendOutcome) (map[string]BackendOutcome, error) {
	return ResolveInfrastructureClosed(r, s.Entries, s.Probes, prev)
}

func QueryBackendProbes(r *store.Reader, afterSeq int64) (map[string]int64, error) {
	rows, err := r.Query(`SELECT object_id,MAX(seq) FROM events WHERE kind=? AND seq>? AND json_extract(payload,'$.success')=1 GROUP BY object_id`, BackendProbeKind, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var backend string
		var seq int64
		if err := rows.Scan(&backend, &seq); err != nil {
			return nil, err
		}
		out[backend] = seq
	}
	return out, rows.Err()
}

func ResolveInfrastructureClosed(r *store.Reader, outcomes []BackendOutcome, probes map[string]int64, prev map[string]BackendOutcome) (map[string]BackendOutcome, error) {
	closed := map[string]BackendOutcome{}
	sequences := map[string]int64{}
	for _, o := range outcomes {
		if o.Class == AttemptInterrupted {
			continue
		}
		name := o.Rec.Backend
		if _, seen := sequences[name]; seen {
			continue
		}
		// The latest ending decides. A saved ending keeps its saved fact; an
		// unsaved one was classified by the read above, which saves nothing.
		sequences[name] = o.Seq
		if o.Closes && probes[name] <= o.Seq {
			closed[name] = o
		}
	}
	if err := PopulateClosedBackendRecords(r, closed, prev); err != nil {
		return nil, err
	}
	return closed, nil
}

func PopulateClosedBackendRecords(r *store.Reader, closed, prev map[string]BackendOutcome) error {
	for name, o := range closed {
		if !o.Saved {
			continue
		}
		if cached, ok := prev[name]; ok && cached.Seq == o.Seq && cached.Rec.State != "" {
			o.Rec = cached.Rec
			closed[name] = o
			continue
		}
		var raw string
		if err := r.QueryRow(`SELECT payload FROM events WHERE seq=? AND kind=?`, o.Seq, run.FinishKind).Scan(&raw); err != nil {
			return err
		}
		BackendOutcomeUnmarshals.Add(1)
		if err := json.Unmarshal([]byte(raw), &o.Rec); err != nil {
			return err
		}
		closed[name] = o
	}
	return nil
}

// ProbeBackendWith runs an operator-chosen smoke check, not a quota reading or a
// timer. Its exit is recorded even on failure; only exit zero reopens a backend.
// Output is discarded so credentials cannot enter the event log.
func ProbeBackendWith(ctx context.Context, path, root, backend string, argv []string, recordOutcomes func(context.Context, string) error) error {
	if strings.TrimSpace(backend) == "" || backend == "command" || len(argv) == 0 || argv[0] == "" {
		return drainmodel.Usagef("probe-backend needs --store, --backend and a smoke-check command after --")
	}
	if recordOutcomes != nil {
		if err := recordOutcomes(ctx, path); err != nil {
			return err
		}
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
	probeErr := heavyrun.RunCheckGroupStarted(cmd, nil)
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		at := time.Now().UTC()
		payload, err := json.Marshal(struct {
			Success bool `json:"success"`
		}{probeErr == nil})
		if err != nil {
			return err
		}
		seq, err := tx.Append(event.Event{Kind: BackendProbeKind, ObjectType: "run", ObjectID: &backend, Payload: payload, At: at})
		if err != nil || probeErr != nil {
			return err
		}
		return ReopenBackendWindow(tx, backend, seq, at)
	})
	if err != nil {
		return err
	}
	if probeErr != nil {
		return drainmodel.Externalf("backend %s probe failed: %v; backend remains closed", backend, probeErr)
	}
	return nil
}

// DescribeQuotaHold describes the same quota facts that admission reads; a missing
// or stale observation is itself a refusal to admit work, not a provider outage.
func DescribeQuotaHold(r *store.Reader, name string, p *Policy, at time.Time) (cause, mover string, err error) {
	windows, err := ReadBackendWindows(r, at)
	if err != nil {
		return "", "", err
	}
	if reset, ok := windows[name]; ok {
		var kind, runID string
		if err := r.QueryRow(`SELECT kind, run_id FROM backend_windows WHERE backend=?`, name).Scan(&kind, &runID); err != nil {
			return "", "", err
		}
		return fmt.Sprintf("%s quota refusal recorded by run %s", kind, runID),
			"provider quota resets at " + reset.Format(time.RFC3339) + "; after an early reset, record a successful probe-backend",
			nil
	}
	b := p.Backends[name]
	statuses, err := ReadQuotaStatuses(r)
	if err != nil {
		return "", "", err
	}
	cause, mover = DescribeQuotaHoldFromStatuses(b, name, statuses, at)
	return cause, mover, nil
}

func DescribeQuotaHoldFromStatuses(b Backend, name string, statuses []QuotaStatus, at time.Time) (cause, mover string) {
	cause = "no quota reading recorded"
	mover = "quota probe: record a fresh, successful reading that admits the backend"
	for _, q := range statuses {
		if q.Backend != name {
			continue
		}
		command, _ := json.Marshal(b.Quota)
		switch {
		case !QuotaFresh(q, string(command), at):
			cause = "no current quota reading (last observed " + q.ObservedAt.Format(time.RFC3339) + ")"
		case q.Error != "":
			cause = q.Error
			mover = "operator: repair the quota probe so it records a successful reading"
		default:
			var causes []string
			for _, w := range q.Windows {
				if w.Fraction >= 1-b.Reserve {
					c := fmt.Sprintf("%s quota usage %g reached the admission limit %g", w.Kind, w.Fraction, 1-b.Reserve)
					if w.ResetAt != nil {
						c += " (reset " + w.ResetAt.Format(time.RFC3339) + ")"
					}
					causes = append(causes, c)
				}
			}
			cause = strings.Join(causes, "; ")
		}
		break
	}
	return cause, mover
}
