package drain

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

// migrateBuildRuns requests every older worker's pause without waiting for any of
// them. The dispatch fact stays historical until a continuation actually starts.
func (l *Loop) migrateBuildRuns(ctx context.Context, result *LoopResult) error {
	if l.cfg.Restart == nil {
		return nil
	}
	hash, err := l.build.Hash()
	if err != nil {
		return err
	}
	cfg, err := settings.Read(l.cfg.Root)
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	var readers []*pendingWork
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		works, err := readPending(tx.Reader())
		if err != nil {
			return err
		}
		facts, err := readInputDecisions(tx.Reader())
		if err != nil {
			return err
		}
		for _, w := range works {
			d := w.Decision
			if w.Done || w.Ready || w.Examining || d.RunRequest == nil {
				continue
			}
			if len(d.Parallel) > 0 {
				migrated, err := l.claimBuildReaders(ctx, tx, w, facts, hash)
				if err != nil {
					return err
				}
				if migrated != nil {
					readers = append(readers, migrated)
				}
				continue
			}
			id := d.RunRequest.RunID
			if f := facts[id]; f.Inputs != nil && f.Inputs.TillrBuild == hash {
				continue
			}
			rec, err := terminalRecord(tx.Reader(), id)
			if err != nil {
				return err
			}
			if rec != nil {
				var latest string
				err := tx.Reader().QueryRow(`SELECT kind FROM events WHERE object_id=? AND kind IN (?,?) ORDER BY seq DESC LIMIT 1`, id, run.ResumeKind, run.FinishKind).Scan(&latest)
				if err != nil && err != sql.ErrNoRows {
					return err
				}
				if latest != run.ResumeKind {
					continue
				}
			}
			var leased int
			if err := tx.Reader().QueryRow(`SELECT count(*) FROM lanes WHERE run_id=? AND state=?`, id, run.LaneStateLeased).Scan(&leased); err != nil {
				return err
			}
			if leased != 1 {
				continue
			}
			var raw []byte
			err = tx.Reader().QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=? ORDER BY seq DESC LIMIT 1`, run.PauseRequestKind, id).Scan(&raw)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			var previous struct {
				Build string `json:"tillr_build"`
			}
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &previous); err != nil {
					return err
				}
			}
			if previous.Build == hash {
				continue
			}
			if err := appendEffect(tx, run.PauseRequestKind, id, map[string]any{"timeout_ms": cfg.Roll.PauseTimeoutSeconds * 1000, "tillr_build": hash}, l.cfg.Now()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, w := range readers {
		timing := &PassTiming{TickID: w.Decision.PassID, AtMS: l.cfg.Now().UnixMilli(), GateBudgetMS: l.gateBudget.Milliseconds(), Applied: 1, DecisionCounts: map[string]int{ActionDispatch: 1}}
		if err := l.observePass(timing, "build reader replacement"); err != nil {
			return err
		}
		result.Ticks++
		for _, node := range parallelNodes(w.Decision) {
			if node.BuildRestartFrom != "" {
				if err := l.interruptBuildRun(node.BuildRestartFrom); err != nil {
					return err
				}
			}
		}
		d, err := l.stageParallel(ctx, w.Decision, w.BaseSHA)
		if err != nil {
			return err
		}
		l.launchRun(ctx, d, l.cfg.Now(), nil, result)
	}
	return nil
}

// recordResumedBuild copies the last dispatch fact, changing only its build hash.
// A legacy run with no fact has no historical inputs to copy.
func (l *Loop) recordResumedBuild(ctx context.Context, req run.Request) error {
	hash, err := l.build.Hash()
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		facts, err := readInputDecisions(tx.Reader())
		if err != nil {
			return err
		}
		d := facts[req.RunID]
		inputs := RunInputs{}
		if d.Inputs != nil {
			inputs = *d.Inputs
		}
		inputs.TillrBuild = hash
		d.Inputs = &inputs
		return appendEffect(tx, inputRecordedKind, req.RunID, d, l.cfg.Now())
	})
}
