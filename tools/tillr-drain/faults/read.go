// Package faults reads item-local holds without registering writer schemas.
package faults

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

const (
	HeldKind    = "drain.item-fault"
	ClearedKind = "drain.item-fault-cleared"
)

// Hold is a durable refusal to advance one item, not a verdict against its code.
// Clearing permits another examination; it does not spend another model attempt.
type Hold struct {
	ItemID string `json:"item_id"`
	RunID  string `json:"run_id,omitempty"`
	LaneID string `json:"lane_id,omitempty"`
	Reason string `json:"reason"`
	Detail string `json:"detail"`
	AtMS   int64  `json:"-"`
	Seq    int64  `json:"-"`
}

// Read also reads historical boundary convictions. An old store needs no new
// write before a restart can hold the convicted item or show its evidence.
func Read(r *store.Reader) (map[string]Hold, error) {
	// Only the kinds the fold reads: it runs inside write transactions, where a read of the
	// whole log holds the store's write lock for the log's length.
	events, err := r.EventsOfKind(0, HeldKind, "boundary.check", ClearedKind)
	if err != nil {
		return nil, err
	}
	byItem := map[string]map[string]Hold{}
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
				return nil, err
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
				return nil, err
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
				return nil, err
			}
			if e.ObjectID != nil {
				delete(byItem[*e.ObjectID], cleared.Reason)
			}
		}
	}
	holds := map[string]Hold{}
	for itemID, reasons := range byItem {
		for _, f := range reasons {
			if f.Seq > holds[itemID].Seq {
				holds[itemID] = f
			}
		}
	}
	return holds, nil
}

// ClearCommand names the store and the precise evidence identity, preventing a
// stale waiting display from silently clearing a newer conviction.
func (f Hold) ClearCommand(storePath string) string {
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	return fmt.Sprintf("tillr drain clear-fault --store %s --item %s --fault %s", quote(storePath), quote(f.ItemID), strconv.FormatInt(f.Seq, 10))
}
