package drain

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RetryCandidate is the recorded work and hold an operator retry supersedes.
// Missing commits keep their hold text, but start fresh on the current trunk.
type RetryCandidate struct {
	RunID         string         `json:"run_id"`
	Cause         string         `json:"cause"`
	Detail        string         `json:"detail"`
	BaseSHA       string         `json:"base_sha,omitempty"`
	HeadSHA       string         `json:"head_sha,omitempty"`
	Available     bool           `json:"available"`
	WorkerSession *WorkerSession `json:"worker_session,omitempty"`
}

func (p *planner) retryCandidate(itemID, runID string) *RetryCandidate {
	e := p.history.endings[runID]
	// Only a recorded judge refusal or obstacle hold restores candidate work.
	// A no-op can carry an unchanged refused head without being an obstacle.
	hold, held := p.history.obstacleEndings[runID]
	held = held && hold.ItemID == itemID
	if e.Restart || e.Step == RefusalReverted || (!held && e.Step != ReasonEvalFailed) {
		return nil
	}
	c := &RetryCandidate{RunID: runID, Cause: e.Step, Detail: e.Detail, BaseSHA: e.BaseSHA, HeadSHA: e.HeadSHA}
	if held && c.Detail == "" {
		c.Detail = hold.Reason
		c.HeadSHA = hold.HeadSHA
	}
	if e.WorkerSession != nil {
		c.BaseSHA, c.HeadSHA = e.WorkerSession.BaseSHA, e.WorkerSession.HeadSHA
		c.WorkerSession = e.WorkerSession
	}
	if c.WorkerSession == nil {
		writer := runID
		if id := p.history.judgeWriters[runID]; id != "" {
			writer = id
		}
		if d := p.inputDecisions[writer]; d.Workflow != nil && d.NodeIndex >= 0 && d.NodeIndex < len(d.Workflow.Stages) && d.Workflow.Stages[d.NodeIndex].Access == "write" {
			c.WorkerSession = &WorkerSession{RunID: writer, Workflow: d.Workflow, NodeIndex: d.NodeIndex, NodeBaseSHA: d.NodeBaseSHA, Attempt: d.Attempt, BaseSHA: c.BaseSHA, HeadSHA: c.HeadSHA}
		}
	}
	if held {
		// Legacy obstacle holds recorded only a base; abandon kept their commit pinned.
		if c.BaseSHA == "" {
			c.BaseSHA = hold.BaseSHA
		}
		if c.HeadSHA == "" {
			if raw, err := exec.Command("git", "-C", p.documentRoot, "rev-parse", "--verify", pinRef(runID)).Output(); err == nil {
				c.HeadSHA = strings.TrimSpace(string(raw))
			}
		}
		for _, name := range []string{ItemBaseSHAFile, BaseSHAFile} {
			if raw, err := os.ReadFile(filepath.Join(RunDir(p.documentRoot, runID), name)); err == nil && strings.TrimSpace(string(raw)) != "" {
				c.BaseSHA = strings.TrimSpace(string(raw))
				break
			}
		}
	}
	if c.BaseSHA != "" && c.HeadSHA != "" && c.BaseSHA != c.HeadSHA {
		baseErr := exec.Command("git", "-C", p.documentRoot, "cat-file", "-e", c.BaseSHA+"^{commit}").Run()
		headErr := exec.Command("git", "-C", p.documentRoot, "cat-file", "-e", c.HeadSHA+"^{commit}").Run()
		c.Available = baseErr == nil && headErr == nil
	}
	return c
}

func retryCandidateBrief(c *RetryCandidate) string {
	if c == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n## Retried candidate\n\nPrevious run: %s.\n\nPrevious hold:\n\n%s\n\n", c.RunID, quoteBriefLines(c.Detail))
	if c.Available {
		fmt.Fprintf(&b, "This retry starts from candidate %s with original item base %s. Recheck or repair this work; a new code commit is not required when the recorded findings can be answered by evidence.\n", c.HeadSHA, c.BaseSHA)
	} else if c.HeadSHA != "" && c.HeadSHA == c.BaseSHA {
		b.WriteString("The previous run left no candidate commit. This retry starts fresh from the current trunk with the recorded hold and the operator's retry reason.\n")
	} else {
		fmt.Fprintf(&b, "The previous candidate is unavailable (recorded head: %s; original base: %s). This retry starts fresh from the current trunk; use the previous hold and the operator's retry reason to finish the work.\n", c.HeadSHA, c.BaseSHA)
	}
	return b.String()
}
