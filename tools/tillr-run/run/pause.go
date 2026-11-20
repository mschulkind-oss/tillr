package run

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// Pause kinds describe the adapter's stopping and continuation capabilities.
const (
	PauseAtGap         = "stops-at-a-gap"
	PauseWhereItStands = "stops-where-it-stands"
	PauseCannotResume  = "cannot-resume"
	PauseRequestKind   = "run.pause-request"
)

// ReadPauseKind reads the declaration beside the adapter, never from policy.
func ReadPauseKind(backendsDir, adapter string) (string, error) {
	extractor := extractorFor(backendsDir, adapter)
	if extractor == "" {
		return "", Usagef("adapter %q has no directory for its pause-kind declaration", adapter)
	}
	path := filepath.Join(filepath.Dir(extractor), "pause-kind")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", Usagef("reading adapter pause-kind at %s: %v", path, err)
	}
	kind := strings.TrimSuffix(string(raw), "\n")
	switch kind {
	case PauseAtGap, PauseWhereItStands, PauseCannotResume:
		return kind, nil
	}
	return "", Usagef("%s holds %q: want one line, stops-at-a-gap, stops-where-it-stands or cannot-resume", path, string(raw))
}

type pauseRequest struct {
	TimeoutMS int64 `json:"timeout_ms"`
}

// RequestPause records the operator's request for a live run. No item, verdict or
// retry state is changed. The executor consumes it before its next terminal write.
func RequestPause(ctx context.Context, s *store.Store, id string, bound time.Duration, at time.Time) error {
	if id == "" || bound <= 0 {
		return Usagef("pause needs a run id and a positive timeout")
	}
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		var live int
		if err := tx.Reader().QueryRow(`SELECT COUNT(*) FROM lanes WHERE run_id = ? AND state = ?`, id, LaneStateLeased).Scan(&live); err != nil {
			return Internalf("reading run lease: %v", err)
		}
		if live != 1 {
			return Usagef("run %q is not live: pause only requests a running session", id)
		}
		body, _ := json.Marshal(pauseRequest{TimeoutMS: bound.Milliseconds()})
		_, err := tx.Append(event.Event{Kind: PauseRequestKind, ObjectType: "run", ObjectID: &id, Payload: body, At: at.UTC()})
		return err
	})
}

func pendingPause(ctx context.Context, s *store.Store, id string) (*pauseRequest, error) {
	var p *pauseRequest
	err := s.ReadTx(ctx, func(r *store.Reader) error {
		var kind string
		var body []byte
		err := r.QueryRow(`SELECT kind, payload FROM events WHERE object_type = 'run' AND object_id = ? AND kind IN (?, ?, ?) ORDER BY seq DESC LIMIT 1`, id, PauseRequestKind, ResumeKind, FinishKind).Scan(&kind, &body)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		if kind == PauseRequestKind {
			var request pauseRequest
			if err := json.Unmarshal(body, &request); err != nil {
				return err
			}
			p = &request
		}
		return nil
	})
	return p, err
}

// waitForPause observes natural exits, durable pause requests and settled idle
// sessions. At-gap adapters watch TILLR_PAUSE_REQUEST and exit at a tool-call gap;
// the runner enforces the straggler bound. Other adapters receive TERM immediately.
func waitForPause(ctx context.Context, s *store.Store, cmd *exec.Cmd, id string, mark execMark, kind, path string, after func(time.Duration) <-chan time.Time, monitor *settledMonitor) (error, bool, bool, error) {
	if after == nil {
		after = time.After
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var sample <-chan time.Time
	for {
		// Prefer a natural exit to a pending observation.
		select {
		case err := <-done:
			return err, false, false, nil
		default:
		}
		select {
		case err := <-done:
			return err, false, false, nil
		case at := <-sample:
			if monitor.sample(ctx, cmd.Process.Pid) {
				select {
				case err := <-done:
					return err, false, false, nil
				default:
				}
				if ctx.Err() == nil {
					killErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
					if killErr != nil && killErr != syscall.ESRCH {
						// A failed signal is not evidence of termination.
						monitor.idle = 0
						sample = monitor.after(SettledSampleInterval)
						continue
					}
					waitErr := <-done
					if cleanupErr := endRunProcesses(cmd.Process.Pid, mark); cleanupErr != nil {
						return waitErr, false, false, cleanupErr
					}
					// The parent can exit naturally between the channel check and
					// the signal. Preserve that exit rather than claiming we ended it.
					if exitErr, ok := waitErr.(*exec.ExitError); ok && killErr == nil {
						if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() && status.Signal() == syscall.SIGKILL {
							monitor.end = &settledEnd{monitor.settledAt, at.UTC().Format(time.RFC3339Nano)}
						}
					}
					return waitErr, false, false, nil
				}
			}
			sample = monitor.after(SettledSampleInterval)
		case <-ticker.C:
			if monitor.records != nil {
				if err := monitor.records(); err != nil {
					_ = endRunProcesses(cmd.Process.Pid, mark)
					return <-done, false, false, Internalf("recording worker progress and sessions: %v", err)
				}
			}
			if sample == nil && monitor.observe() == nil && monitor.reportReady(ctx) {
				monitor.sample(ctx, cmd.Process.Pid) // Baseline, not an idle sample.
				sample = monitor.after(SettledSampleInterval)
			}
			request, err := pendingPause(ctx, s, id)
			if err != nil {
				if ctx.Err() != nil {
					if cleanupErr := endRunProcesses(cmd.Process.Pid, mark); cleanupErr != nil {
						return <-done, false, false, cleanupErr
					}
					return <-done, false, false, nil
				}
				_ = endRunProcesses(cmd.Process.Pid, mark)
				return <-done, false, false, Internalf("reading pause request: %v", err)
			}
			if request == nil {
				continue
			}
			if err := os.WriteFile(path, []byte("pause\n"), 0600); err != nil {
				_ = endRunProcesses(cmd.Process.Pid, mark)
				return <-done, false, false, Internalf("handing pause request to adapter: %v", err)
			}
			// A request with no timeout_ms (the loop's build migration) has no straggler
			// bound for an at-gap adapter: it stops at its next gap or ends on its own, and
			// nothing is killed. Only a bounded request, the operator's, ends a straggler.
			bound := time.Duration(request.TimeoutMS) * time.Millisecond
			if kind != PauseAtGap {
				bound = 10 * time.Second
				_ = signalRunProcesses(cmd.Process.Pid, mark, syscall.SIGTERM)
			}
			var elapsed <-chan time.Time
			if bound > 0 {
				elapsed = after(bound)
			}
			select {
			case err := <-done:
				// A harness may exit while its shell is still running. Pausing must not
				// leave that shell writing concurrently with the next session.
				if cleanupErr := endRunProcesses(cmd.Process.Pid, mark); cleanupErr != nil {
					return err, true, kind != PauseAtGap, cleanupErr
				}
				return err, true, kind != PauseAtGap, nil
			case <-elapsed:
				cleanupErr := endRunProcesses(cmd.Process.Pid, mark)
				return <-done, true, true, cleanupErr
			case <-ctx.Done():
				cleanupErr := endRunProcesses(cmd.Process.Pid, mark)
				return <-done, false, false, cleanupErr
			}
		case <-ctx.Done():
			cleanupErr := endRunProcesses(cmd.Process.Pid, mark)
			return <-done, false, false, cleanupErr
		}
	}
}

// continuationSessionID recovers the durably claimed session even if the executor
// died after its claim committed but before publishing the lane's session-id file.
func continuationSessionID(ctx context.Context, s *store.Store, req Request, lane string) (string, error) {
	// The staged session file remains a required part of the lane. Recover a
	// stale id, not an unstaged lane whose session file was deliberately removed.
	staged, err := ReadSessionID(lane)
	if err != nil || staged == "" {
		return staged, err
	}
	var claimed *resume
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var body []byte
		err := r.QueryRow(`SELECT payload FROM events WHERE object_type='run' AND object_id=? AND kind=? ORDER BY seq DESC LIMIT 1`, req.RunID, ResumeKind).Scan(&body)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		var segment resume
		if err := json.Unmarshal(body, &segment); err != nil {
			return err
		}
		if segment.RunID == req.RunID && segment.ItemID == req.ItemID && segment.LaneID == req.LaneID && segment.Backend == req.Backend {
			if !IsSessionID(segment.SessionID) {
				return Internalf("run %s has an invalid claimed session id", req.RunID)
			}
			claimed = &segment
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if claimed != nil {
		return claimed.SessionID, nil
	}
	return staged, nil
}

// prepareContinuation supplies the adapter's first prompt without replacing the
// staged brief. A non-resumable adapter gets a new UUID, but the run and lane stay.
func prepareContinuation(req Request, kind, lane, worktree, durable string) (string, string, error) {
	mode, prompt, _, err := prepareContinuationSession(req, kind, lane, worktree, durable)
	return mode, prompt, err
}

// Mint in memory. Publishing session-id before the resume claim would lose the
// original session if the executor died before claiming a backend handoff.
func prepareContinuationSession(req Request, kind, lane, worktree, durable string) (string, string, string, error) {
	mode, session := "new", ""
	if !req.Resume && !req.SessionTurn {
		return mode, "", "", nil
	}
	mode = "resume"
	noticePath := filepath.Join(durable, "pause-notice")
	notice, err := os.ReadFile(noticePath)
	if err != nil && !os.IsNotExist(err) {
		return "", "", "", Internalf("reading pause notice: %v", err)
	}
	restarting := len(notice) > 0
	if req.ResumePrompt != "" {
		notice = []byte(req.ResumePrompt + "\n")
	}
	if len(notice) == 0 {
		notice = []byte("Continue the same attempt from where you stopped. The session was interrupted; keep existing work and make only the one requested commit.\n")
	}
	if req.SessionTurn {
		brief, err := os.ReadFile(filepath.Join(lane, "brief.md"))
		if err != nil {
			return "", "", "", Usagef("reading returned turn brief: %v", err)
		}
		notice = append(notice, []byte("\nThis is a new run record in your existing session. Keep the earlier work, repair the refusal, and make one new commit for this turn; the prior commit is not the new candidate. The current run identity, scratch directory and requirements follow.\n")...)
		notice = append(notice, brief...)
	}
	if req.FreshSession || kind == PauseCannotResume {
		mode = "new"
		var uuid [16]byte
		if _, err := rand.Read(uuid[:]); err != nil {
			return "", "", "", Internalf("minting fresh session: %v", err)
		}
		uuid[6] = (uuid[6] & 15) | 64
		uuid[8] = (uuid[8] & 63) | 128
		session = fmt.Sprintf("%x-%x-%x-%x-%x", uuid[:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:])
		reason := "The earlier session was cut short."
		if restarting {
			reason = "The earlier session was stopped for a restart and was interrupted by a restart."
		}
		if req.ResumePrompt != "" {
			reason = req.ResumePrompt
		}
		notice = []byte(fmt.Sprintf("%s Continue the same attempt in a fresh session, preserving the worktree and its uncommitted changes. Transcript: %s. Worktree: %s.\n", reason, filepath.Join(lane, req.RunID+".log"), worktree))
		report := filepath.Join(durable, "report.md")
		if _, err := os.Stat(report); err == nil {
			notice = append(notice, []byte("Report: "+report+".\n")...)
		}
		brief, err := os.ReadFile(filepath.Join(lane, "brief.md"))
		if err != nil {
			return "", "", "", Usagef("reading the fresh session's brief: %v", err)
		}
		notice = append(notice, brief...)
	}
	prompt := filepath.Join(durable, "resume-prompt")
	if err := os.WriteFile(prompt, notice, 0600); err != nil {
		return "", "", "", Internalf("writing continuation prompt: %v", err)
	}
	if err := os.Remove(noticePath); err != nil && !os.IsNotExist(err) {
		return "", "", "", Internalf("consuming restart notice: %v", err)
	}
	return mode, prompt, session, nil
}
