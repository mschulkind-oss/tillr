package drain

import (
	"os/exec"
	"strings"
)

// returnedJudgeLane binds a returned judge finding to the offered checkout still
// holding that exact candidate. A harness handle is not needed to own the work.
func (p *planner) returnedJudgeLane(itemID string, e *ReturnedEnding) string {
	if e == nil || e.Cause != ReasonEvalFailed || e.Restart {
		return ""
	}
	ending := p.history.endings[e.RunID]
	if ending.LaneID == "" || ending.HeadSHA == "" || !p.offersLane(ending.LaneID) {
		return ""
	}
	work := p.works[itemID]
	if work == nil || work.Candidate == nil {
		return ""
	}
	c := work.Candidate
	if c.LaneID != ending.LaneID || c.HeadSHA != ending.HeadSHA || c.Worktree == "" {
		return ""
	}
	head, err := exec.Command("git", "-C", c.Worktree, "rev-parse", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(head)) != ending.HeadSHA {
		return ""
	}
	return ending.LaneID
}
