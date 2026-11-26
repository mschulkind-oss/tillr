package drain

import (
	"context"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func (l *Loop) awaitJudgeRecord(ctx context.Context, id string) (run.Record, error) {
	for {
		db, err := store.OpenRead(ctx, l.cfg.Store)
		if err != nil {
			return run.Record{}, err
		}
		var rec *run.Record
		var active int
		err = db.ReadTx(ctx, func(r *store.Reader) error {
			var err error
			rec, err = terminalRecord(r, id)
			if err != nil {
				return err
			}
			return r.QueryRow(`SELECT count(*) FROM runs WHERE run_id=? AND state='running'`, id).Scan(&active)
		})
		db.Close()
		if err != nil {
			return run.Record{}, err
		}
		if rec != nil && rec.State != "running" {
			return *rec, nil
		}
		if active == 0 {
			here, err := currentLoopOwner()
			if err != nil {
				return run.Record{}, err
			}
			faces, err := liveRunFaces(l.cfg.Store, id, here)
			if err != nil {
				return run.Record{}, err
			}
			if len(faces) == 0 {
				db, err := store.OpenRead(ctx, l.cfg.Store)
				if err != nil {
					return run.Record{}, err
				}
				err = db.ReadTx(ctx, func(r *store.Reader) error {
					var err error
					rec, err = terminalRecord(r, id)
					return err
				})
				db.Close()
				if err != nil {
					return run.Record{}, err
				}
				if rec != nil && rec.State != "running" {
					return *rec, nil
				}
				return run.Record{}, Externalf("judge handoff has no terminal record")
			}
		}
		if !waitFor(ctx, 100*time.Millisecond) {
			return run.Record{}, ctx.Err()
		}
	}
}
