package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// migrateBuildRuns requests every older worker's pause without waiting for any of
// them. The dispatch fact stays historical until a continuation actually starts.
//
// Only a dispatch fact that names another build makes a run older: a run with no fact has
// an unknown build and finishes where it started. The pause is requested only of an
// adapter that declares it stops at a gap (its pause-kind), and with no straggler bound, so
// the worker stops at its next gap or at its own end and nothing kills it. A worker whose
// adapter cannot stop at a gap finishes on the build it started under.
func (l *Loop) migrateBuildRuns(ctx context.Context, result *LoopResult) error {
	if l.cfg.Restart == nil {
		return nil
	}
	hash, err := l.build.Hash()
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
			if f, ok := facts[id]; !ok || f.Inputs == nil || f.Inputs.TillrBuild == hash {
				continue
			}
			if adapterPauseKind(l.cfg.Root, d.RunRequest) != run.PauseAtGap {
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
			if err := appendEffect(tx, run.PauseRequestKind, id, map[string]any{"tillr_build": hash}, l.cfg.Now()); err != nil {
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

// adapterPauseKind answers the pause kind req's adapter declares in the pause-kind file
// beside it, or "" when it declares none. A relative adapter path names the repository's
// file, as the backend policy spells it.
func adapterPauseKind(root string, req *run.Request) string {
	if req == nil || len(req.Command) == 0 {
		return ""
	}
	adapter := req.Command[0]
	if !filepath.IsAbs(adapter) {
		adapter = filepath.Join(root, adapter)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(adapter), "pause-kind"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
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
