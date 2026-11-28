package policy

import (
	"context"
	"slices"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/internal/runhistory"
	"github.com/mschulkind-oss/tillr-drain/internal/storelog"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// BackendMovedKind records that an in-progress, paused, or restarting step
// moved from an earlier backend to the seat's current backend under the
// profile in force.
const BackendMovedKind = "drain.backend-moved"

func init() {
	if err := event.Register(BackendMovedKind, drainmodel.Utility); err != nil {
		panic(err)
	}
}

// BackendMove names the item, seat, earlier backend, and new backend for a
// profile-driven move recorded as a BackendMovedKind event.
type BackendMove struct {
	ItemID      string `json:"item_id"`
	Seat        string `json:"seat,omitempty"`
	NodeID      string `json:"node_id,omitempty"`
	RunID       string `json:"run_id,omitempty"`
	FromBackend string `json:"from_backend"`
	ToBackend   string `json:"to_backend"`
	Reason      string `json:"reason"`
}

func hasBackend(p *Policy, backend string) bool {
	if p == nil {
		return false
	}
	_, ok := p.Backends[backend]
	return ok
}

func RecordBackendMoveTx(tx *store.Tx, move BackendMove, at time.Time) error {
	if move.ItemID == "" || move.FromBackend == "" || move.ToBackend == "" || move.FromBackend == move.ToBackend {
		return nil
	}
	if move.RunID != "" {
		var exists int
		err := tx.Reader().QueryRow(
			`SELECT EXISTS(SELECT 1 FROM events WHERE kind = ? AND object_type = 'item' AND object_id = ? AND json_extract(payload, '$.run_id') = ? AND json_extract(payload, '$.from_backend') = ? AND json_extract(payload, '$.to_backend') = ?)`,
			BackendMovedKind, move.ItemID, move.RunID, move.FromBackend, move.ToBackend,
		).Scan(&exists)
		if err != nil {
			return storelog.ReadFailure(err)
		}
		if exists != 0 {
			return nil
		}
	}
	return runhistory.AppendEffectOn(tx, BackendMovedKind, "item", move.ItemID, move, at)
}

func RecordBackendMove(ctx context.Context, storePath string, move BackendMove, at time.Time) error {
	if storePath == "" || move.ItemID == "" || move.FromBackend == "" || move.ToBackend == "" || move.FromBackend == move.ToBackend {
		return nil
	}
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		return RecordBackendMoveTx(tx, move, at)
	})
}

// SeatCurrentBackend returns the active policy's backend for (itemID, seat),
// without falling back to DefaultBackend when an explicit seat is unmapped.
func SeatCurrentBackend(policy *Policy, itemID, seat string) (string, bool) {
	if policy == nil {
		return "", false
	}
	if name, ok := policy.Overrides[itemID]; ok {
		return name, hasBackend(policy, name)
	}
	if seat != "" {
		list := policy.SeatList(seat)
		if len(list) == 0 {
			return "", false
		}
		return list[0], hasBackend(policy, list[0])
	}
	if policy.DefaultBackend != "" {
		return policy.DefaultBackend, hasBackend(policy, policy.DefaultBackend)
	}
	return "", false
}

// SeatAllowsBackend reports whether the active policy still maps (itemID, seat)
// to backend (including any backend in a seat's share list).
func SeatAllowsBackend(policy *Policy, itemID, seat, backend string) bool {
	if policy == nil || backend == "" {
		return false
	}
	if name, ok := policy.Overrides[itemID]; ok {
		return name == backend
	}
	if seat != "" {
		return slices.Contains(policy.SeatList(seat), backend)
	}
	return policy.DefaultBackend == backend
}

// ResolveStepBackend chooses the backend for a step that previously ran or was
// planned on prevBackend, using the seat and the policy in force.
//
//   - If prevBackend is absent from policy, it moves to the seat's current
//     backend in policy (reason: drainmodel.ReasonBackendMissing).
//   - If prevBackend is shut (for example, closed for quota) and the seat no
//     longer maps to prevBackend while the seat's current backend is in policy,
//     it moves to the seat's current backend (reason: drainmodel.ReasonBackendWindow).
//   - If the seat has no backend at all in policy, ok is false and the caller
//     holds on the operator.
//   - Otherwise prevBackend is kept with move == nil.
func ResolveStepBackend(policy *Policy, itemID, seat, prevBackend string, shut map[string]bool) (backend string, move *BackendMove, ok bool) {
	if policy == nil {
		return prevBackend, nil, false
	}
	current, hasCurrent := SeatCurrentBackend(policy, itemID, seat)
	if prevBackend == "" {
		return current, nil, hasCurrent
	}
	if !hasBackend(policy, prevBackend) {
		if !hasCurrent {
			return prevBackend, nil, false
		}
		return current, &BackendMove{
			ItemID:      itemID,
			Seat:        seat,
			FromBackend: prevBackend,
			ToBackend:   current,
			Reason:      drainmodel.ReasonBackendMissing,
		}, true
	}
	if shut[prevBackend] && hasCurrent && current != prevBackend && !SeatAllowsBackend(policy, itemID, seat, prevBackend) {
		return current, &BackendMove{
			ItemID:      itemID,
			Seat:        seat,
			FromBackend: prevBackend,
			ToBackend:   current,
			Reason:      drainmodel.ReasonBackendWindow,
		}, true
	}
	return prevBackend, nil, true
}
