package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/internal/storelog"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// A seat obstacle says the task exceeds the model the run was given, with the worker's own
// evidence: what it tried and what it is missing. The answer is never the same model again.
// The policy's `escalate` map lists, per seat, the backends an item climbs through; the next
// run of the item is a fresh run on the next one, with the obstacle in its brief, and every
// later run of the item stays there until it lands. Past the end of the list the design
// seat is asked for a plan or a split (obstacleDesignInstruction).
//
// The hops are backend names for now. Seats themselves are being reworked by the
// `item-plan-fans-out-by-seat` item, so this map does not define seat semantics of its own.

// SeatEscalatedKind is the event a seat obstacle writes: the item, the run, and the backend
// it moves from and to. The planner reads the latest one per item as the item's backend.
const SeatEscalatedKind = "drain.seat-escalated"

func init() {
	if err := event.Register(SeatEscalatedKind, drainmodel.Utility); err != nil {
		panic(err)
	}
}

type SeatEscalation struct {
	ItemID string `json:"item_id"`
	RunID  string `json:"run_id"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// EscalationNext answers the backend after `from` on the seat's escalation list that the
// item has not yet named a seat obstacle on, or "" when the list is spent or the seat has
// none. A backend the list does not hold starts the climb at the list's head.
func (p Policy) EscalationNext(seat, from string, tried map[string]bool) string {
	if seat == "" {
		seat = "build"
	}
	list := p.Escalate[seat]
	start := 0
	for i, name := range list {
		if name == from {
			start = i + 1
		}
	}
	for _, name := range list[start:] {
		if !tried[name] && name != from {
			return name
		}
	}
	return ""
}

// CheckEscalate refuses an escalation list that names a backend the policy does not hold or
// a seat the policy does not map.
func (p Policy) CheckEscalate() error {
	for seat, list := range p.Escalate {
		if _, ok := p.Seats[seat]; !ok {
			return fmt.Errorf("escalate names seat %q, which the policy's seats do not map", seat)
		}
		if seat == item.SeatTriage && len(list) > 0 {
			return fmt.Errorf("escalate.%s must be empty: triage never escalates to another model tier", seat)
		}
		for _, name := range list {
			if _, ok := p.Backends[name]; !ok {
				return fmt.Errorf("escalate.%s names backend %q, which the policy does not define", seat, name)
			}
		}
	}
	return nil
}

// ReadEscalations answers every item's escalations, oldest first.
func ReadEscalations(r *store.Reader) (map[string][]SeatEscalation, error) {
	rows, err := r.Query(`SELECT payload FROM events WHERE kind=? ORDER BY seq`, SeatEscalatedKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]SeatEscalation{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var e SeatEscalation
		if json.Unmarshal(raw, &e) == nil && e.ItemID != "" {
			out[e.ItemID] = append(out[e.ItemID], e)
		}
	}
	return out, rows.Err()
}

// ReadEscalationsForItems reads seat escalations for the named items via events_by_object.
// SeatEscalatedKind is recorded on object_type="run" (object_id=runID), so runIDs
// must be included alongside itemIDs.
func ReadEscalationsForItems(r *store.Reader, itemIDs, runIDs []string) (map[string][]SeatEscalation, error) {
	out := map[string][]SeatEscalation{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	itemSet := make(map[string]bool, len(itemIDs))
	objectIDs := make([]string, 0, len(itemIDs)+len(runIDs))
	for _, id := range itemIDs {
		if id != "" {
			itemSet[id] = true
			objectIDs = append(objectIDs, id)
		}
	}
	objectIDs = append(objectIDs, runIDs...)
	events, err := r.EventsForObjects(0, objectIDs, SeatEscalatedKind)
	if err != nil {
		return nil, err
	}
	for _, ev := range events {
		var e SeatEscalation
		if json.Unmarshal(ev.Payload, &e) == nil && e.ItemID != "" && itemSet[e.ItemID] {
			out[e.ItemID] = append(out[e.ItemID], e)
		}
	}
	return out, nil
}

// EscalatedBackend answers the backend an item's latest escalation moved it to, or "".
func EscalatedBackend(escalations []SeatEscalation) string {
	if len(escalations) == 0 {
		return ""
	}
	return escalations[len(escalations)-1].To
}

// EscalateSeat records a seat obstacle's move for an item from the run's backend to the
// next one, and answers it; "" means the list is spent and the design seat is asked.
func EscalateSeat(ctx context.Context, storePath string, p *Policy, now func() time.Time, itemID, runID, seat, from string) (string, error) {
	if p == nil {
		return "", nil
	}
	r, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return "", err
	}
	var prior []SeatEscalation
	err = r.ReadTx(ctx, func(tx *store.Reader) error {
		all, err := ReadEscalations(tx)
		prior = all[itemID]
		return err
	})
	r.Close()
	if err != nil {
		return "", err
	}
	tried := map[string]bool{from: true}
	for _, e := range prior {
		tried[e.From] = true
	}
	next := p.EscalationNext(seat, from, tried)
	if next == "" {
		return "", nil
	}
	return next, storelog.AppendJudgment(ctx, storePath, SeatEscalatedKind, "run", runID, SeatEscalation{ItemID: itemID, RunID: runID, From: from, To: next}, now().UTC())
}
