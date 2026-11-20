// Architecture and invariants: docs/reference/tillr-queue-persistence.md
package item

import (
	"encoding/json"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// item_sync_residue holds only the last whole writing sync's observation, not an event.
// Its singleton key enforces one row; the caller's sync transaction replaces it atomically.
func init() {
	if err := store.RegisterDDL("item-sync-residue", `CREATE TABLE IF NOT EXISTS item_sync_residue (
 singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
 synced_at TEXT NOT NULL,
 unshared_open_ids TEXT NOT NULL,
 unshared_open_oldest TEXT NOT NULL,
 stale_doc_block_ids TEXT NOT NULL
 );`); err != nil {
		panic(err)
	}
}

func replaceSyncResidue(tx *store.Tx, report SyncReport, at time.Time) error {
	open, err := json.Marshal(report.UnsharedOpenIDs)
	if err != nil {
		return err
	}
	stale, err := json.Marshal(report.StaleDocBlockIDs)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO item_sync_residue(singleton,synced_at,unshared_open_ids,unshared_open_oldest,stale_doc_block_ids)
 VALUES(1,?,?,?,?) ON CONFLICT(singleton) DO UPDATE SET
 synced_at=excluded.synced_at, unshared_open_ids=excluded.unshared_open_ids,
 unshared_open_oldest=excluded.unshared_open_oldest, stale_doc_block_ids=excluded.stale_doc_block_ids`,
		at.UTC().Format(time.RFC3339Nano), string(open), report.UnsharedOpenOldest, string(stale))
	return err
}
