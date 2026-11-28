// Package faults reads item-local holds without registering writer schemas.
package faults

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	HeldKind           = "drain.item-fault"
	ClearedKind        = "drain.item-fault-cleared"
	VerifyTriageReason = "verify-triage"
	StageFailedReason  = "stage-failed"
)

// Hold is a durable refusal to advance one item, not a verdict against its code.
// Clearing permits another examination; it does not spend another model attempt.
type Hold struct {
	ItemID           string `json:"item_id"`
	RunID            string `json:"run_id,omitempty"`
	LaneID           string `json:"lane_id,omitempty"`
	HeadSHA          string `json:"head_sha,omitempty"`
	WorkerHeadSHA    string `json:"worker_head_sha,omitempty"`
	Reason           string `json:"reason"`
	Detail           string `json:"detail"`
	GateDigest       string `json:"gate_digest,omitempty"`
	RetryAck         string `json:"retry_ack,omitempty"`
	RepeatConflictID string `json:"repeat_conflict_id,omitempty"`
	AtMS             int64  `json:"-"`
	Seq              int64  `json:"-"`
}

// Read also reads historical boundary convictions. An old store needs no new
// write before a restart can hold the convicted item or show its evidence.
func Read(r *store.Reader) (map[string]Hold, error) {
	byItem := map[string]map[string]Hold{}
	if err := Fold(r, 0, byItem, nil); err != nil {
		return nil, err
	}
	return Resolve(byItem), nil
}

// ReadForItems reads faults and boundary convictions scoped to itemIDs and runIDs via events_by_object
// and historical dirty boundary.check events via events_boundary_dirty.
func ReadForItems(r *store.Reader, itemIDs, runIDs []string) (map[string]Hold, error) {
	if len(itemIDs) == 0 && len(runIDs) == 0 {
		return map[string]Hold{}, nil
	}
	objectIDs := make([]string, 0, len(itemIDs)+len(runIDs))
	objectIDs = append(objectIDs, itemIDs...)
	objectIDs = append(objectIDs, runIDs...)
	events, err := r.EventsForObjects(0, objectIDs, HeldKind, ClearedKind)
	if err != nil {
		return nil, err
	}
	dirtyRows, err := r.Query(`SELECT seq, ts, kind, COALESCE(object_type, ''), object_id, revision, payload FROM events WHERE kind = 'boundary.check' AND json_valid(payload) AND json_extract(payload, '$.clean') = 0 ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer dirtyRows.Close()
	for dirtyRows.Next() {
		var e event.Event
		var ts int64
		var objID sql.NullString
		var rev sql.NullInt64
		var payload string
		if err := dirtyRows.Scan(&e.Seq, &ts, &e.Kind, &e.ObjectType, &objID, &rev, &payload); err != nil {
			return nil, err
		}
		e.At = time.UnixMilli(ts).UTC()
		if objID.Valid {
			v := objID.String
			e.ObjectID = &v
		}
		if rev.Valid {
			v := rev.Int64
			e.Revision = &v
		}
		e.Payload = json.RawMessage(payload)
		events = append(events, e)
	}
	if err := dirtyRows.Err(); err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return map[string]Hold{}, nil
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Seq < events[j].Seq })
	byItem := map[string]map[string]Hold{}
	if err := Fold(r, 0, byItem, events); err != nil {
		return nil, err
	}
	resolved := Resolve(byItem)
	if len(itemIDs) == 0 {
		return resolved, nil
	}
	wanted := make(map[string]bool, len(itemIDs))
	for _, id := range itemIDs {
		wanted[id] = true
	}
	for id := range resolved {
		if !wanted[id] {
			delete(resolved, id)
		}
	}
	return resolved, nil
}

// Fold extends per-item, per-reason state with ordered events. With no supplied
// events, read only the relevant kinds after seq, not the whole log.
func Fold(r *store.Reader, seq int64, byItem map[string]map[string]Hold, events []event.Event) error {
	if events == nil {
		var err error
		events, err = r.EventsOfKind(seq, HeldKind, "boundary.check", ClearedKind)
		if err != nil {
			return err
		}
	}
	put := func(f Hold) {
		if byItem[f.ItemID] == nil {
			byItem[f.ItemID] = map[string]Hold{}
		}
		byItem[f.ItemID][f.Reason] = f
	}
	for _, e := range events {
		switch e.Kind {
		case HeldKind:
			var f Hold
			if err := json.Unmarshal(e.Payload, &f); err != nil {
				return err
			}
			f.AtMS = e.At.UnixMilli()
			f.Seq = e.Seq
			put(f)
		case "boundary.check":
			var c struct {
				Clean          bool   `json:"clean"`
				Runs           []Hold `json:"runs"`
				FirstViolation *struct {
					Kind   string `json:"kind"`
					Target string `json:"target"`
					Detail string `json:"detail"`
				} `json:"first_violation"`
			}
			if err := json.Unmarshal(e.Payload, &c); err != nil {
				return err
			}
			if c.Clean {
				continue
			}
			detail := "stored boundary conviction"
			if c.FirstViolation != nil {
				detail = c.FirstViolation.Kind + ": " + c.FirstViolation.Target + ": " + c.FirstViolation.Detail
			}
			for _, ref := range c.Runs {
				ref.Reason, ref.Detail, ref.AtMS, ref.Seq = "host-boundary-violation", detail, e.At.UnixMilli(), e.Seq
				put(ref)
			}
		case ClearedKind:
			var cleared Hold
			if err := json.Unmarshal(e.Payload, &cleared); err != nil {
				return err
			}
			if e.ObjectID != nil {
				delete(byItem[*e.ObjectID], cleared.Reason)
			}
		}
	}
	return nil
}

// Resolve selects the newest active hold for each item.
func Resolve(byItem map[string]map[string]Hold) map[string]Hold {
	holds := map[string]Hold{}
	for itemID, reasons := range byItem {
		for _, f := range reasons {
			if f.Seq > holds[itemID].Seq {
				holds[itemID] = f
			}
		}
	}
	return holds
}

const (
	clearFault            = "tillr drain clear-fault"
	stageRetryPlaceholder = "tillr drain retry naming the held run"
)

// CompleteMover binds a rule's trailing clear-fault instruction (and stage-failed
// retry instruction when a run id is held) to this hold, preserving the repair
// instruction and leaving other movers alone.
func (f Hold) CompleteMover(mover, storePath string) string {
	if f.Reason == StageFailedReason && strings.Contains(mover, stageRetryPlaceholder) {
		if f.RunID != "" {
			mover = strings.Replace(mover, stageRetryPlaceholder, StageRetryCommand(storePath, f.ItemID, f.RunID), 1)
		} else {
			mover = strings.Replace(mover, stageRetryPlaceholder+" or ", "", 1)
		}
	}
	if repair, ok := strings.CutSuffix(mover, clearFault); ok {
		return repair + f.ClearCommand(storePath)
	}
	return mover
}

// StageRetryCommand names the unstarted run held by a stage-failed fault.
func StageRetryCommand(storePath, itemID, runID string) string {
	return fmt.Sprintf("tillr drain retry --store %s --item %s --run %s --by <who> --why <reason> --now \"$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)\"",
		storeArgument(storePath), quote(itemID), quote(runID))
}

// ClearCommand names the store and the precise evidence identity, preventing a
// stale waiting display from silently clearing a newer conviction.
func (f Hold) ClearCommand(storePath string) string {
	if f.Reason == VerifyTriageReason {
		return "operator; " + VerifyTriageCommand(storePath, f.ItemID, f.RunID, f.RetryAck)
	}
	return fmt.Sprintf("%s --store %s --item %s --fault %s", clearFault, storeArgument(storePath), quote(f.ItemID), strconv.FormatInt(f.Seq, 10))
}

// VerifyTriageCommand names the held run, not a fault sequence. Only a
// retry authorization of that run can release the hold.
func VerifyTriageCommand(storePath, itemID, runID, ack string) string {
	cmd := fmt.Sprintf("tillr drain retry --item %s --run %s --by <who> --why <reason>", itemID, runID)
	if ack != "" {
		cmd += " --acknowledge " + ack
	}
	return cmd + fmt.Sprintf(" --store %s --now \"$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)\"", storeArgument(storePath))
}

func storeArgument(path string) string {
	if path == "" {
		return "<path>"
	}
	return quote(path)
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
