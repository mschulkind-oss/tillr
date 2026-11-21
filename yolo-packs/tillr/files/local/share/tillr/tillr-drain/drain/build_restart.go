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
	// Each pending run's adapter declares its pause kind in a file; the write is gathered so
	// those files are read with no write open (gathered.go).
	err = writeGathered(ctx, s, func(ctx context.Context, tx *store.Tx) error {
		readers = nil
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
			if w.Done || w.Ready || w.Examining || w.Orphaned || d.RunRequest == nil {
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
			if err := l.requestBuildPause(ctx, tx, d.RunRequest, facts, hash); err != nil {
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
				l.reportf("loop: restarting reader run %s as %s on %s: its adapter does not declare a tool-call gap\n", node.BuildRestartFrom, node.RunRequest.RunID, node.LaneID)
				if err := l.interruptBuildRun(node.BuildRestartFrom); err != nil {
					return err
				}
			}
		}
		d, err := l.stageParallel(ctx, w.Decision, w.BaseSHA)
		if err != nil {
			if cancelErr := l.cancelStaging(context.WithoutCancel(ctx), w.Decision); cancelErr != nil {
				return cancelErr
			}
			if holdErr := l.holdStageFailed(ctx, w.Decision, "stage parallel", err); holdErr != nil {
				return holdErr
			}
			continue
		}
		l.launchRun(ctx, d, l.cfg.Now(), nil, result)
	}
	return nil
}

// requestBuildPause applies the same migration rule to a serial worker or an
// individual reader. A reader's group, not the individual run, owns its lane.
func (l *Loop) requestBuildPause(ctx context.Context, tx *store.Tx, req *run.Request, facts map[string]Decision, hash string) error {
	id := req.RunID
	if f := facts[id]; f.Inputs == nil || f.Inputs.TillrBuild == "" || f.Inputs.TillrBuild == hash {
		return nil
	}
	kind, err := adapterPauseKind(ctx, l.cfg.Root, req)
	if err != nil || kind != run.PauseAtGap {
		return err
	}
	leaseID := id
	if req.ReadOnlyGroup != "" {
		leaseID = req.ReadOnlyGroup
	}
	var leased bool
	if err := tx.Reader().QueryRow(`SELECT EXISTS(SELECT 1 FROM lanes WHERE lane_id=? AND run_id=? AND state=?)`, req.LaneID, leaseID, run.LaneStateLeased).Scan(&leased); err != nil || !leased {
		return err
	}
	// Look at the latest segment action, not just the last pause's build. A
	// resume can consume that pause even if the loop later returns to this build.
	var latest string
	var raw []byte
	err = tx.Reader().QueryRow(`SELECT kind,payload FROM events WHERE object_type='run' AND object_id=? AND kind IN (?,?,?) ORDER BY seq DESC LIMIT 1`, id, run.PauseRequestKind, run.ResumeKind, run.FinishKind).Scan(&latest, &raw)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	switch latest {
	case run.FinishKind:
		return nil
	case run.PauseRequestKind:
		var previous struct {
			Build string `json:"tillr_build"`
		}
		if err := json.Unmarshal(raw, &previous); err != nil {
			return err
		}
		// An operator/quota pause retains its own bounds and reason.
		if previous.Build == "" || previous.Build == hash {
			return nil
		}
	}
	return appendEffect(tx, run.PauseRequestKind, id, map[string]any{"tillr_build": hash}, l.cfg.Now())
}

// adapterPauseKind answers the pause kind req's adapter declares in the pause-kind file
// beside it, or "" when it declares none. A relative adapter path names the repository's
// file, as the backend policy spells it.
// The only error is a gathered write's miss (gathered.go); an unreadable file declares none.
func adapterPauseKind(ctx context.Context, root string, req *run.Request) (string, error) {
	if req == nil || len(req.Command) == 0 {
		return "", nil
	}
	adapter := req.Command[0]
	if !filepath.IsAbs(adapter) {
		adapter = filepath.Join(root, adapter)
	}
	path := filepath.Join(filepath.Dir(adapter), "pause-kind")
	raw, err := gather(ctx, "read "+path, func(context.Context) (string, error) {
		raw, err := os.ReadFile(path)
		return string(raw), err
	})
	if isGatherMiss(err) {
		return "", err
	}
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(raw), nil
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
