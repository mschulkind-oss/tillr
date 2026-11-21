package drain

import (
	"context"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func (l *Loop) awaitBuildReader(ctx context.Context, id string) error {
	for {
		s, err := store.OpenRead(ctx, l.cfg.Store)
		if err != nil {
			return err
		}
		var rec *run.Record
		err = s.ReadTx(ctx, func(r *store.Reader) error { var err error; rec, err = terminalRecord(r, id); return err })
		s.Close()
		if err != nil {
			return err
		}
		here, err := currentLoopOwner()
		if err != nil {
			return err
		}
		live, err := liveRunWorkers(l.cfg.Store, id, here)
		if err != nil {
			return err
		}
		if len(live) == 0 {
			// A killed face can fail before writing run.finish. Its reserved replacement
			// still owns the group; settle only the vanished old membership, and end any
			// non-terminal (e.g. paused) runs row for this replaced reader.
			if rec == nil || !isTerminalRunState(rec.State) {
				db, err := store.Open(ctx, l.cfg.Store, true)
				if err != nil {
					return err
				}
				err = db.WriteTx(ctx, func(tx *store.Tx) error {
					if hasReadOnly, err := tableExists(tx.Reader(), "read_only_runs"); err != nil {
						return err
					} else if hasReadOnly {
						if _, err := tx.Exec(`UPDATE read_only_runs SET settled=1 WHERE run_id=?`, id); err != nil {
							return err
						}
					}
					if rec != nil && !isTerminalRunState(rec.State) {
						return endNonTerminalRunRow(ctx, tx, id, "superseded by build reader restart", true, l.cfg.Now().UTC())
					}
					return nil
				})
				db.Close()
				if err != nil {
					return err
				}
			}
			return nil
		}
		if !waitFor(ctx, 50*time.Millisecond) {
			return ctx.Err()
		}
	}
}
