package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	ReasonInfrastructureRepeat = "infrastructure-repeat"
	infrastructureFailureKind  = "drain.infrastructure-failure"
)

func init() {
	if err := event.Register(infrastructureFailureKind, Utility); err != nil {
		panic(err)
	}
}

func infrastructureDetail(rec run.Record, log string) string {
	cause := fmt.Sprintf("run %s on %s (%s)", rec.RunID, rec.Backend, rec.State)
	if rec.EndCause != "" {
		cause += ", " + rec.EndCause
	}
	if rec.ExitCode != nil {
		cause += fmt.Sprintf(", exit %d", *rec.ExitCode)
	}
	if words := infrastructureWords(log); words != "" {
		cause += ": " + words
	}
	return cause
}

func infrastructureWords(log string) string {
	log = strings.TrimSpace(log)
	if i := strings.LastIndexByte(log, '\n'); i >= 0 {
		log = strings.TrimSpace(log[i+1:])
	}
	return log
}

// Resume only a recorded session on the same lane and route when its adapter
// supports a continuation. An exit before starting has no session to resume.
func canResumeInfrastructure(rec *run.Record, req *run.Request) bool {
	if rec == nil || req == nil || rec.State != run.StateExited || rec.LaneID != req.LaneID || rec.Backend != req.Backend ||
		rec.Route == "" || rec.Route != req.Route() || len(req.Command) == 0 {
		return false
	}
	capability, err := os.ReadFile(filepath.Join(filepath.Dir(req.Command[0]), "pause-kind"))
	if err != nil {
		return false
	}
	switch strings.TrimSpace(string(capability)) {
	case run.PauseAtGap, run.PauseWhereItStands:
		return designTranscriptAvailable(rec)
	default:
		return false
	}
}

func (l *Loop) stageInfrastructureSession(ctx context.Context, d *Decision, spec *StageSpec) error {
	if d.InfrastructureFrom == "" || d.RunRequest.SessionTurn {
		return nil
	}
	var rec *run.Record
	if err := l.readStore(ctx, func(r *store.Reader) error {
		var err error
		rec, err = terminalRecord(r, d.InfrastructureFrom)
		return err
	}); err != nil {
		return err
	}
	if !canResumeInfrastructure(rec, d.RunRequest) {
		return nil
	}
	session, err := ReadSessionID(RunDir(l.cfg.Root, rec.RunID))
	if err != nil || session == "" {
		return err
	}
	spec.SessionID = session
	d.RunRequest.SessionTurn = true
	d.RunRequest.ResumePrompt = "The backend has recovered. Continue this work on the same backend after the failure of " + rec.RunID + ". Check the current checkout before proceeding."
	d.WorkerSessionReason = "resumed-worker"
	return nil
}

func (l *Loop) countInfrastructureFailure(result *LoopResult, itemID string) {
	l.mu.Lock()
	result.InfraFailures++
	l.infraStreak++
	streak := l.infraStreak
	l.mu.Unlock()
	l.reportf("loop: %s failed in the infrastructure class (%d consecutive)\n", itemID, streak)
}

type infrastructureFailure struct {
	RunID   string `json:"run_id"`
	Backend string `json:"backend"`
	Node    string `json:"node"`
	Cause   string `json:"cause"`
	Words   string `json:"words"`
	Detail  string `json:"detail"`
}

func readRecordedInfrastructureFailure(r *store.Reader, itemID, field, value string) (infrastructureFailure, error) {
	var failure infrastructureFailure
	var raw []byte
	err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=? AND json_extract(payload, ?)=? ORDER BY seq DESC LIMIT 1`, infrastructureFailureKind, itemID, field, value).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return failure, nil
	}
	if err != nil {
		return failure, err
	}
	err = json.Unmarshal(raw, &failure)
	return failure, err
}

func isBackendFailure(rec run.Record, log string) bool {
	_, _, reset, refusal := refusedWindow(rec)
	return ClassifyAttempt(rec, log) == AttemptInfrastructure && (!refusal || reset.IsZero())
}

// Record the measured words at settlement. Compare failures of the same execution
// slot, not unrelated nodes. Clearing the item fault starts a new comparison.
func (l *Loop) recordInfrastructureFailure(ctx context.Context, d Decision, rec run.Record, log string) (bool, error) {
	if !isBackendFailure(rec, log) {
		return false, nil
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return false, err
	}
	defer s.Close()
	failure := infrastructureFailure{
		RunID: rec.RunID, Backend: rec.Backend, Node: d.Seat,
		Cause: rec.State + ":" + rec.EndCause, Words: normalizeEvidence(infrastructureWords(log), rec.RunID),
		Detail: infrastructureDetail(rec, log),
	}
	if d.RunRequest != nil {
		failure.Node += ":" + d.RunRequest.Node
	}
	if rec.ExitCode != nil {
		failure.Cause += fmt.Sprintf(":%d", *rec.ExitCode)
	}
	held := false
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		var raw []byte
		err := tx.QueryRow(`SELECT payload FROM events
 WHERE kind=? AND object_id=? AND json_extract(payload,'$.node')=?
 AND (json_extract(payload,'$.run_id')=? OR seq > COALESCE((SELECT max(seq) FROM events
 WHERE kind=? AND object_id=? AND json_extract(payload,'$.reason')=?),0))
 ORDER BY seq DESC LIMIT 1`, infrastructureFailureKind, d.ItemID, failure.Node, failure.RunID, itemFaultClearedKind, d.ItemID, ReasonInfrastructureRepeat).Scan(&raw)
		var prior infrastructureFailure
		if err == nil {
			if err := json.Unmarshal(raw, &prior); err != nil {
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if prior.RunID == failure.RunID {
			faults, err := ReadItemFaults(tx.Reader())
			held = faults[d.ItemID].Reason == ReasonInfrastructureRepeat
			return err
		}
		if err := appendEffectOn(tx, infrastructureFailureKind, "item", d.ItemID, failure, l.cfg.Now()); err != nil {
			return err
		}
		if prior.RunID == "" || prior.Backend != failure.Backend || prior.Cause != failure.Cause || prior.Words != failure.Words {
			return nil
		}
		held = true
		return l.holdItemFaultTx(tx, Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: rec.RunID}, ReasonInfrastructureRepeat,
			"identical infrastructure endings: "+prior.Detail+"; "+failure.Detail)
	})
	return held, err
}
