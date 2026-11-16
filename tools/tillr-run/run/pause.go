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

// waitForPause leaves the harness alone until a durable pause request arrives.
// At-gap adapters watch TILLR_PAUSE_REQUEST and exit at a tool-call gap. The
// runner enforces the straggler bound; other adapters receive TERM immediately.
func waitForPause(ctx context.Context, s *store.Store, cmd *exec.Cmd, id, kind, path string) (error, bool, bool, error) {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return err, false, false, nil
		case <-ticker.C:
			request, err := pendingPause(ctx, s, id)
			if err != nil {
				if ctx.Err() != nil {
					return <-done, false, false, nil
				}
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				return <-done, false, false, Internalf("reading pause request: %v", err)
			}
			if request == nil {
				continue
			}
			if err := os.WriteFile(path, []byte("pause\n"), 0600); err != nil {
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				return <-done, false, false, Internalf("handing pause request to adapter: %v", err)
			}
			bound := time.Duration(request.TimeoutMS) * time.Millisecond
			if kind != PauseAtGap {
				bound = 10 * time.Second
				syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			}
			timer := time.NewTimer(bound)
			select {
			case err := <-done:
				timer.Stop()
				// A harness may exit while its shell is still running. Pausing must not
				// leave that shell writing concurrently with the next session.
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				return err, true, kind != PauseAtGap, nil
			case <-timer.C:
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				return <-done, true, true, nil
			case <-ctx.Done():
				timer.Stop()
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				return <-done, false, false, nil
			}
		case <-ctx.Done():
			return <-done, false, false, nil
		}
	}
}

// prepareContinuation supplies the adapter's first prompt without replacing the
// staged brief. A non-resumable adapter gets a new UUID, but the run and lane stay.
func prepareContinuation(req Request, kind, lane, worktree, durable string) (string, string, error) {
	mode := "new"
	if !req.Resume {
		return mode, "", nil
	}
	mode = "resume"
	noticePath := filepath.Join(durable, "pause-notice")
	notice, err := os.ReadFile(noticePath)
	if err != nil && !os.IsNotExist(err) {
		return "", "", Internalf("reading pause notice: %v", err)
	}
	restarting := len(notice) > 0
	if len(notice) == 0 {
		notice = []byte("Continue the same attempt from where you stopped. The session was interrupted; keep existing work and make only the one requested commit.\n")
	}
	if kind == PauseCannotResume {
		mode = "new"
		var uuid [16]byte
		if _, err := rand.Read(uuid[:]); err != nil {
			return "", "", Internalf("minting fresh session: %v", err)
		}
		uuid[6] = (uuid[6] & 15) | 64
		uuid[8] = (uuid[8] & 63) | 128
		session := fmt.Sprintf("%x-%x-%x-%x-%x", uuid[:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:])
		if err := os.WriteFile(filepath.Join(lane, SessionIDFile), []byte(session+"\n"), 0600); err != nil {
			return "", "", Internalf("writing fresh session: %v", err)
		}
		reason := "The earlier session was cut short."
		if restarting {
			reason = "The earlier session was stopped for a restart and was interrupted by a restart."
		}
		notice = []byte(fmt.Sprintf("%s Continue the same attempt in a fresh session, preserving the worktree and its uncommitted changes. Transcript: %s. Worktree: %s.\n", reason, filepath.Join(lane, req.RunID+".log"), worktree))
		report := filepath.Join(durable, "report.md")
		if _, err := os.Stat(report); err == nil {
			notice = append(notice, []byte("Report: "+report+".\n")...)
		}
		brief, err := os.ReadFile(filepath.Join(lane, "brief.md"))
		if err != nil {
			return "", "", Usagef("reading the fresh session's brief: %v", err)
		}
		notice = append(notice, brief...)
	}
	prompt := filepath.Join(durable, "resume-prompt")
	if err := os.WriteFile(prompt, notice, 0600); err != nil {
		return "", "", Internalf("writing continuation prompt: %v", err)
	}
	if err := os.Remove(noticePath); err != nil && !os.IsNotExist(err) {
		return "", "", Internalf("consuming restart notice: %v", err)
	}
	return mode, prompt, nil
}
