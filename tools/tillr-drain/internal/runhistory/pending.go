package runhistory

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/internal/storelog"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// These events record effects, not a second stored item state: the request handed to
// a runner, the candidate a terminal run produced, and the completed examination.
// Positions are derived anew from these facts, the terminal record and the lease.
const (
	ExaminationClaimedKind  = "drain.examination-claimed"
	ExaminationReleasedKind = "drain.examination-released"
	DispatchStartedKind     = "drain.started"
	DispatchStagedKind      = "drain.staged"
	CandidateReadyKind      = "drain.candidate"
	CandidateRetirementKind = "drain.retirement"
	CandidatePreparedKind   = "drain.prepared-candidate"
	ExaminationDoneKind     = "drain.examined"
	DesignQuotaRefusedKind  = "drain.design-quota-refused"
	ExaminationOwnerKind    = "drain.examination.owner"
	LoopOrphanedKind        = "drain.orphaned"
	// LaneReleasedKind records a lane released after its run was examined, for every run,
	// not only a workflow node (whose drain.examination-released also clears its claim).
	LaneReleasedKind = "drain.lane-released"
	ActionSettle     = "settle"
	ActionJudge      = "judge"
)

func init() {
	for _, kind := range []string{
		ExaminationClaimedKind,
		ExaminationReleasedKind,
		DispatchStartedKind,
		DispatchStagedKind,
		CandidateReadyKind,
		CandidateRetirementKind,
		CandidatePreparedKind,
		ExaminationDoneKind,
		DesignQuotaRefusedKind,
		ExaminationOwnerKind,
		LoopOrphanedKind,
		LaneReleasedKind,
	} {
		if err := event.Register(kind, drainmodel.Utility); err != nil {
			panic(err)
		}
	}
}

type OrphanedRunEffect struct {
	ResumeAfterRestart bool `json:"resume_after_restart,omitempty"`
}

func AppendEffect(tx *store.Tx, kind, id string, value any, at time.Time) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{Kind: kind, ObjectType: "run", ObjectID: &id, Payload: body, At: at})
	return err
}

func AppendEffectOn(tx *store.Tx, kind, objectType, id string, value any, at time.Time) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{Kind: kind, ObjectType: objectType, ObjectID: &id, Payload: body, At: at})
	return err
}

func TerminalRecord(r *store.Reader, id string) (*run.Record, error) {
	env, found, err := run.StatusRecord(r, id)
	if err != nil || !found {
		return nil, err
	}
	var status struct {
		Run *run.Record `json:"run"`
	}
	if err := json.Unmarshal(env.Payload, &status); err != nil {
		return nil, err
	}
	// The status row has no resumed column. The terminal event retains that
	// segment fact, so a replacement Loop does not continue it a second time.
	var payload []byte
	err = r.QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ? ORDER BY seq DESC LIMIT 1`, run.FinishKind, id).Scan(&payload)
	if err == nil && status.Run != nil {
		var terminal run.Record
		if err := json.Unmarshal(payload, &terminal); err != nil {
			return nil, err
		}
		status.Run.Resumed = terminal.Resumed
		status.Run.EndingException = terminal.EndingException
	} else if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	return status.Run, nil
}

// ActiveWorkerLease reports whether the lane is currently leased to req.
// The lane is leased to the group, not to any one reader. A settled reader
// cannot borrow an active sibling's lease to appear still running.
func ActiveWorkerLease(r *store.Reader, req *run.Request, lane string, at time.Time) (bool, error) {
	var active bool
	if req.ReadOnlyGroup != "" {
		err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM lanes l JOIN read_only_runs m ON m.group_id = l.run_id AND m.lane_id = l.lane_id
		 WHERE l.lane_id = ? AND l.state = ? AND l.run_id = ? AND m.run_id = ? AND m.item_id = ? AND m.settled = 0)`,
			lane, run.LaneStateLeased, req.ReadOnlyGroup, req.RunID, req.ItemID).Scan(&active)
		return active, err
	}
	err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM lanes WHERE lane_id = ? AND run_id = ? AND state = ? AND (leased_until IS NULL OR leased_until > ?))`,
		lane, req.RunID, run.LaneStateLeased, at.UnixMilli()).Scan(&active)
	return active, err
}

// PendingHistoryHeadForKinds returns the max seq across kinds; each per-kind maximum is an indexed lookup.
func PendingHistoryHeadForKinds(r *store.Reader, kinds []string) (int64, error) {
	queries := make([]string, len(kinds))
	args := make([]any, len(kinds))
	for i, kind := range kinds {
		queries[i] = "SELECT MAX(seq) AS head FROM events WHERE kind=?"
		args[i] = kind
	}
	var head int64
	err := r.QueryRow("SELECT COALESCE(MAX(head),0) FROM ("+strings.Join(queries, " UNION ALL ")+")", args...).Scan(&head)
	return head, err
}

// PendingHistoryChangedForKinds checks only the delta since the read-phase fold. A clean
// boundary check is an observation: the fold changes state only for a failed check.
// Other folded events may change ownership, so a claim cannot proceed on stale history.
func PendingHistoryChangedForKinds(r *store.Reader, after int64, kinds []string) (bool, error) {
	args := make([]any, 0, len(kinds)+2)
	args = append(args, after)
	for _, kind := range kinds {
		args = append(args, kind)
	}
	args = append(args, storelog.BoundaryCheckKind)
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",")
	var changed bool
	err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM events WHERE seq>? AND kind IN (`+placeholders+`)
		AND CASE WHEN kind=? THEN json_type(payload, '$.clean') IS NOT 'true' ELSE 1 END)`, args...).Scan(&changed)
	return changed, err
}
