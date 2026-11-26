package runhistory

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/faults"
	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/internal/storelog"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	ItemFaultKind              = faults.HeldKind
	ItemFaultClearedKind       = faults.ClearedKind
	ReasonCandidateMismatch    = "candidate-record-mismatch"
	ReasonDefinitionSyncFailed = "definition-sync-failed"
	ReasonLandHarnessError     = "land-harness-error"
	ReasonPackSyncFailed       = "pack-sync-failed"
	ReasonDecisionCollision    = "decision-collision"
	ReasonSettleFailed         = "settle-failed"
	ReasonAdapterRefused       = "adapter-refused"
	ReasonStageFailed          = "stage-failed"
)

func init() {
	for _, kind := range []string{ItemFaultKind, ItemFaultClearedKind} {
		if err := event.Register(kind, drainmodel.Utility); err != nil {
			panic(err)
		}
	}
}

type ItemFault = faults.Hold

func ReadItemFaults(r *store.Reader) (map[string]ItemFault, error) { return faults.Read(r) }

// FaultClearDetail renders the clear command after append: the event sequence binds it to this exact fault.
func FaultClearDetail(f ItemFault, storePath string) string {
	return f.Detail + "; after correcting the cause, run " + f.ClearCommand(storePath)
}

func DecisionCollisionDetail(collision *storelog.DecisionCollisionError, action string) string {
	return collision.Error() + "; the planned " + action + " was not applied. A drain defect named two decisions alike"
}

// ExecSetupRefused recognizes the run face's usage exit with no output record.
func ExecSetupRefused(f ItemFault) bool {
	if f.Reason != ReasonSettleFailed || f.RunID == "" || !strings.HasPrefix(f.Detail, "run: ") {
		return false
	}
	_, _, ok := strings.Cut(f.Detail, " exec could not run "+f.RunID+": exit status 2: tillr-run: ")
	return ok
}

func ClearItemFaultTx(tx *store.Tx, f ItemFault, at time.Time) error {
	body, err := json.Marshal(f)
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{Kind: ItemFaultClearedKind, ObjectType: "item", ObjectID: &f.ItemID, Payload: body, At: at})
	return err
}

// SyncClearVersion returns the append sequence of the latest definition-sync fault clearance.
func SyncClearVersion(r *store.Reader) (int64, error) {
	var seq int64
	err := r.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM events WHERE kind=? AND json_extract(payload,'$.reason')=?`, ItemFaultClearedKind, ReasonDefinitionSyncFailed).Scan(&seq)
	return seq, err
}

func ClearUnrunNode(ctx context.Context, storePath, runID string) error {
	if storePath == "" || runID == "" {
		return nil
	}
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		hasNodes, err := TableExists(tx.Reader(), "node_runs")
		if err != nil || !hasNodes {
			return err
		}
		_, err = tx.Exec(`DELETE FROM node_runs WHERE run_id=? AND outcome='running'`, runID)
		return err
	})
}
