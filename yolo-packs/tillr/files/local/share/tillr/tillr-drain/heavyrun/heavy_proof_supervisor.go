package heavyrun

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"golang.org/x/sys/unix"
)

const (
	HeavyAttemptSpecFile  = "heavy-attempt.spec.json"
	HeavyAttemptFaceFile  = "heavy-attempt.face.json"
	HeavyAttemptDoneFile  = "heavy-attempt.done.json"
	HeavyEarlyRequestFile = "heavy-early.request.json"
	HeavyEarlyAnswerFile  = "heavy-early.answer.json"
)

// HeavyProofSpec is the serialized configuration handed to --tillr-heavy-proof.
type HeavyProofSpec struct {
	Root                     string `json:"root"`
	Work                     string `json:"work"`
	Trunk                    string `json:"trunk"`
	Store                    string `json:"store"`
	LaneRoot                 string `json:"lane_root"`
	RequestedTip             string `json:"requested_tip,omitempty"`
	ReuseRed                 bool   `json:"reuse_red,omitempty"`
	AutoRevert               bool   `json:"auto_revert,omitempty"`
	OwnerPID                 int    `json:"owner_pid"`
	OwnerStartTicks          string `json:"owner_start_ticks"`
	HeavyGateBin             string `json:"heavy_gate_bin,omitempty"`
	RollingProgram           string `json:"rolling_program,omitempty"`
	ReplayProgram            string `json:"replay_program,omitempty"`
	GateProgram              string `json:"gate_program,omitempty"`
	SelfExe                  string `json:"self_exe,omitempty"`
	HeavyLogSampleIntervalMS int64  `json:"heavy_log_sample_interval_ms,omitempty"`
	NiceTarget               *int   `json:"nice_target,omitempty"`
	Dir                      string `json:"dir"`
	FaceFile                 string `json:"face_file"`
	DoneFile                 string `json:"done_file"`
	EarlyRequestFile         string `json:"early_request_file"`
	EarlyAnswerFile          string `json:"early_answer_file"`
}

// HeavyProofFace is published atomically by --tillr-heavy-proof while it runs.
type HeavyProofFace struct {
	PID             int    `json:"pid"`
	StartTicks      string `json:"start_ticks"`
	OwnerPID        int    `json:"owner_pid"`
	OwnerStartTicks string `json:"owner_start_ticks"`
	Root            string `json:"root"`
	Trunk           string `json:"trunk"`
	Store           string `json:"store"`
	BaseSHA         string `json:"base_sha,omitempty"`
	TipSHA          string `json:"tip_sha,omitempty"`
	StartedAtMS     int64  `json:"started_at_ms"`
}

// HeavyFixPlan records the post-run fixer inputs deferred from --tillr-heavy-proof to Loop.
type HeavyFixPlan struct {
	TipSHA           string                  `json:"tip_sha"`
	Order            []string                `json:"order,omitempty"`
	ByLanding        map[string][]HeavyCheck `json:"by_landing,omitempty"`
	UnrecordedCommit string                  `json:"unrecorded_commit,omitempty"`
}

// HeavyProofDone is published atomically by --tillr-heavy-proof when the attempt ends.
type HeavyProofDone struct {
	Attempt      HeavyAttempt   `json:"attempt"`
	FixPlans     []HeavyFixPlan `json:"fix_plans,omitempty"`
	Error        string         `json:"error,omitempty"`
	CleanupLost  bool           `json:"cleanup_lost,omitempty"`
	Interrupted  bool           `json:"interrupted,omitempty"`
	FinishedAtMS int64          `json:"finished_at_ms"`
}

// HeavyEarlyRequest asks the active Loop to execute l.revertLanding for a culprit.
type HeavyEarlyRequest struct {
	RequestID   string       `json:"request_id"`
	TipSHA      string       `json:"tip_sha"`
	LandingSHA  string       `json:"landing_sha"`
	Checks      []HeavyCheck `json:"checks"`
	CreatedAtMS int64        `json:"created_at_ms"`
}

// HeavyEarlyAnswer carries the Loop's revert outcome back to --tillr-heavy-proof.
type HeavyEarlyAnswer struct {
	RequestID    string        `json:"request_id"`
	Outcome      RevertOutcome `json:"outcome"`
	Error        string        `json:"error,omitempty"`
	CleanupLost  bool          `json:"cleanup_lost,omitempty"`
	AnsweredAtMS int64         `json:"answered_at_ms"`
}

// HeavyProofCallbacks supplies drain-layer hooks to --tillr-heavy-proof without
// importing drain from heavyrun (D-GR47).
type HeavyProofCallbacks struct {
	MakeJudge                func(HeavyProofSpec) (func(context.Context, string, string) ([]HeavyCheck, error), error)
	WithWorktreeRegistryLock func(context.Context, string, func() error) error
	UnlockRegisteredWorktree func(context.Context, string, string, string) (string, time.Time, bool, error)
}

func ReadHeavyProofFace(dir string) (HeavyProofFace, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, HeavyAttemptFaceFile))
	if err != nil {
		return HeavyProofFace{}, false
	}
	var face HeavyProofFace
	if json.Unmarshal(raw, &face) != nil || face.PID <= 0 {
		return HeavyProofFace{}, false
	}
	return face, true
}

func ReadHeavyProofDone(dir string) (HeavyProofDone, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, HeavyAttemptDoneFile))
	if err != nil {
		return HeavyProofDone{}, false
	}
	var done HeavyProofDone
	if json.Unmarshal(raw, &done) != nil || done.FinishedAtMS == 0 {
		return HeavyProofDone{}, false
	}
	return done, true
}

func ReadHeavyEarlyRequest(dir string) (HeavyEarlyRequest, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, HeavyEarlyRequestFile))
	if err != nil {
		return HeavyEarlyRequest{}, false
	}
	var req HeavyEarlyRequest
	if json.Unmarshal(raw, &req) != nil || req.RequestID == "" {
		return HeavyEarlyRequest{}, false
	}
	return req, true
}

func ReadHeavyEarlyAnswer(dir string) (HeavyEarlyAnswer, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, HeavyEarlyAnswerFile))
	if err != nil {
		return HeavyEarlyAnswer{}, false
	}
	var ans HeavyEarlyAnswer
	if json.Unmarshal(raw, &ans) != nil || ans.RequestID == "" {
		return HeavyEarlyAnswer{}, false
	}
	return ans, true
}

func WriteHeavyEarlyAnswer(dir string, ans HeavyEarlyAnswer) error {
	return PublishHeavyJSON(dir, HeavyEarlyAnswerFile, ans)
}

func HeavyProofRunning(face HeavyProofFace) bool {
	if face.PID <= 0 || face.StartTicks == "" {
		return false
	}
	running, err := HeavySupervisorRunning(face.PID, face.StartTicks)
	return err == nil && running
}

// SuperviseHeavyProof runs one detached trunk proof and SearchFirstRed culprit
// search in its own session, bound to (OwnerPID, OwnerStartTicks) so it survives
// syscall.Exec and reaps its descendant tree within 50 ms on owner exit.
func SuperviseHeavyProof(args []string, cb HeavyProofCallbacks) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "heavy proof supervisor needs a spec path")
		return 2
	}
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ready := os.NewFile(3, "heavy proof ready pipe")
	raw, err := os.ReadFile(args[0])
	if err != nil {
		_ = ready.Close()
		fmt.Fprintln(os.Stderr, "heavy proof supervisor reading spec:", err)
		return 4
	}
	var spec HeavyProofSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		_ = ready.Close()
		fmt.Fprintln(os.Stderr, "heavy proof supervisor decoding spec:", err)
		return 4
	}
	if err := PrepareHeavyTree(); err != nil {
		_ = ready.Close()
		fmt.Fprintln(os.Stderr, "heavy proof supervisor subreaper:", err)
		return 4
	}
	if spec.NiceTarget != nil {
		if err := unix.Setpriority(unix.PRIO_PROCESS, 0, *spec.NiceTarget); err != nil {
			_ = ready.Close()
			_ = FinishHeavyTree()
			_ = PublishHeavyJSON(spec.Dir, HeavyAttemptDoneFile, HeavyProofDone{
				Error:        fmt.Sprintf("setpriority(%d): %v", *spec.NiceTarget, err),
				FinishedAtMS: time.Now().UnixMilli(),
			})
			return 0
		}
	}
	pid, startTicks, err := HeavySupervisorIdentity()
	if err != nil {
		_ = ready.Close()
		fmt.Fprintln(os.Stderr, "heavy proof supervisor identity:", err)
		return 4
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hold, busy, holdErr := AcquireLoopLock(ctx, spec.Root)
	if holdErr != nil {
		_ = ready.Close()
		_ = FinishHeavyTree()
		_ = PublishHeavyJSON(spec.Dir, HeavyAttemptDoneFile, HeavyProofDone{
			Attempt:      HeavyAttempt{PendingTip: spec.RequestedTip},
			Error:        holdErr.Error(),
			FinishedAtMS: time.Now().UnixMilli(),
		})
		return 0
	}
	if busy {
		_ = ready.Close()
		_ = FinishHeavyTree()
		_ = PublishHeavyJSON(spec.Dir, HeavyAttemptDoneFile, HeavyProofDone{
			Attempt:      HeavyAttempt{HeavyOutcome: HeavyOutcome{Busy: true}, PendingTip: spec.RequestedTip},
			FinishedAtMS: time.Now().UnixMilli(),
		})
		return 0
	}
	defer hold.Close()

	baseSHA, _ := heavy.Green(ctx, spec.Root)
	tipSHA := spec.RequestedTip
	if tipSHA == "" {
		trunk := spec.Trunk
		if trunk == "" {
			trunk = DefaultTrunkRef
		}
		tipSHA, _ = heavy.GitOutput(ctx, spec.Root, "rev-parse", trunk+"^{commit}")
	}

	face := HeavyProofFace{
		PID:             pid,
		StartTicks:      startTicks,
		OwnerPID:        spec.OwnerPID,
		OwnerStartTicks: spec.OwnerStartTicks,
		Root:            spec.Root,
		Trunk:           spec.Trunk,
		Store:           spec.Store,
		BaseSHA:         baseSHA,
		TipSHA:          tipSHA,
		StartedAtMS:     time.Now().UnixMilli(),
	}
	if err := PublishHeavyJSON(spec.Dir, HeavyAttemptFaceFile, face); err != nil {
		_ = ready.Close()
		fmt.Fprintln(os.Stderr, "heavy proof supervisor writing face:", err)
		return 4
	}
	_ = ready.Close()

	var interrupted atomic.Bool
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-sigCh:
				interrupted.Store(true)
				cancel()
				return
			case <-ticker.C:
				if spec.OwnerPID > 0 && spec.OwnerStartTicks != "" {
					if running, err := HeavySupervisorRunning(spec.OwnerPID, spec.OwnerStartTicks); err == nil && !running {
						interrupted.Store(true)
						cancel()
						return
					}
				}
			}
		}
	}()

	var judgeFn func(context.Context, string, string) ([]HeavyCheck, error)
	var makeErr error
	if cb.MakeJudge != nil {
		judgeFn, makeErr = cb.MakeJudge(spec)
	} else {
		makeErr = Usagef("heavy proof supervisor has no judge factory")
	}
	if makeErr != nil {
		cancel()
		<-watchDone
		_ = FinishHeavyTree()
		_ = PublishHeavyJSON(spec.Dir, HeavyAttemptDoneFile, HeavyProofDone{
			Attempt:      HeavyAttempt{PendingTip: spec.RequestedTip},
			Error:        makeErr.Error(),
			Interrupted:  interrupted.Load(),
			FinishedAtMS: time.Now().UnixMilli(),
		})
		return 0
	}

	var fixMu sync.Mutex
	fixPlan := HeavyFixPlan{
		TipSHA:    tipSHA,
		ByLanding: map[string][]HeavyCheck{},
	}
	var earlySeq atomic.Int64

	runner := HeavyRunner{
		Root:                     spec.Root,
		Work:                     spec.Work,
		Trunk:                    spec.Trunk,
		Store:                    spec.Store,
		Lock:                     hold,
		ReuseRed:                 spec.ReuseRed,
		DeferSettlement:          true,
		Judge:                    judgeFn,
		WithWorktreeRegistryLock: cb.WithWorktreeRegistryLock,
		UnlockRegisteredWorktree: cb.UnlockRegisteredWorktree,
		OnUnrecorded: func(_ context.Context, sha string) {
			fixMu.Lock()
			fixPlan.UnrecordedCommit = sha
			fixMu.Unlock()
		},
		Fix: func(_ context.Context, sha string, checks []HeavyCheck) error {
			fixMu.Lock()
			if len(fixPlan.ByLanding[sha]) == 0 {
				fixPlan.Order = append(fixPlan.Order, sha)
			}
			fixPlan.ByLanding[sha] = append([]HeavyCheck(nil), checks...)
			fixMu.Unlock()
			return nil
		},
	}
	if spec.AutoRevert {
		runner.Revert = func(rctx context.Context, tip, landing string, checks []HeavyCheck, _ HeavyReplay) (RevertOutcome, error) {
			reqID := fmt.Sprintf("early-%d-%d", time.Now().UnixNano(), earlySeq.Add(1))
			req := HeavyEarlyRequest{
				RequestID:   reqID,
				TipSHA:      tip,
				LandingSHA:  landing,
				Checks:      checks,
				CreatedAtMS: time.Now().UnixMilli(),
			}
			if err := PublishHeavyJSON(spec.Dir, HeavyEarlyRequestFile, req); err != nil {
				return RevertOutcome{}, err
			}
			ticker := time.NewTicker(25 * time.Millisecond)
			defer ticker.Stop()
			for {
				if ans, ok := ReadHeavyEarlyAnswer(spec.Dir); ok && ans.RequestID == reqID {
					_ = os.Remove(filepath.Join(spec.Dir, HeavyEarlyRequestFile))
					_ = os.Remove(filepath.Join(spec.Dir, HeavyEarlyAnswerFile))
					if ans.CleanupLost {
						return ans.Outcome, &heavyCleanupUnconfirmed{detail: ans.Error}
					}
					if ans.Error != "" {
						return ans.Outcome, Externalf("%s", ans.Error)
					}
					return ans.Outcome, nil
				}
				select {
				case <-rctx.Done():
					return RevertOutcome{}, rctx.Err()
				case <-ticker.C:
				}
			}
		}
	}

	attempt, runErr := runner.RunAttempt(ctx, spec.RequestedTip)
	cancel()
	<-watchDone
	_ = FinishHeavyTree()

	done := HeavyProofDone{
		Attempt:      attempt,
		Interrupted:  interrupted.Load(),
		FinishedAtMS: time.Now().UnixMilli(),
	}
	fixMu.Lock()
	if len(attempt.Runs) > 0 {
		fixPlan.TipSHA = attempt.Runs[len(attempt.Runs)-1].TipSHA
	}
	if len(fixPlan.Order) > 0 || fixPlan.UnrecordedCommit != "" {
		done.FixPlans = []HeavyFixPlan{fixPlan}
	}
	fixMu.Unlock()
	if runErr != nil {
		done.Error = runErr.Error()
		done.CleanupLost = CleanupLost(runErr)
	}
	_ = PublishHeavyJSON(spec.Dir, HeavyAttemptDoneFile, done)
	return 0
}
