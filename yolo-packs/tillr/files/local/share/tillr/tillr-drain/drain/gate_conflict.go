package drain

import (
	"context"
	"os/exec"
	"strings"
	"sync"
)

// activeGate tracks one running candidate gate so a trunk landing can test
// whether the candidate still squashes onto the moved tip and stop the gate
// immediately when it no longer applies.
type activeGate struct {
	candidate   Candidate
	cancel      context.CancelFunc
	checkedTip  string // guarded by Loop.activeGatesMu
	mu          sync.Mutex
	conflicts   []string
	conflictTip string
}

func (g *activeGate) hasConflict() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.conflicts) > 0
}

func (g *activeGate) conflict() ([]string, string) {
	if g == nil {
		return nil, ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.conflicts...), g.conflictTip
}

func (g *activeGate) setConflict(reports []string, tip string) {
	g.mu.Lock()
	if len(g.conflicts) == 0 {
		g.conflicts = append([]string(nil), reports...)
		g.conflictTip = tip
	}
	cancel := g.cancel
	g.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// candidateUsesTrunkSquash reports whether this candidate lands by three-way
// squashing its BaseSHA..HeadSHA range onto the trunk ref. Fork delivery
// branches (BaseRef != "") and remote-sync replay items follow different paths.
func candidateUsesTrunkSquash(candidate Candidate) bool {
	return candidate.BaseRef == "" &&
		!IsRemoteSyncItem(candidate.ItemID) &&
		candidate.BaseSHA != "" &&
		candidate.HeadSHA != ""
}

// mergeTreeConflictReports extracts CONFLICT lines from `git merge-tree --write-tree`
// stdout, matching tools/tillr-land/land/trunk.go.
func mergeTreeConflictReports(out string) []string {
	var reports []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "CONFLICT") {
			reports = append(reports, line)
		}
	}
	return reports
}

// checkTrunkSquashConflict tests whether candidate's BaseSHA..HeadSHA squash
// still applies cleanly onto tip using one `git merge-tree --write-tree`
// process, without creating a worktree or modifying any ref or index.
func (l *Loop) checkTrunkSquashConflict(ctx context.Context, tip string, candidate Candidate) ([]string, error) {
	if !candidateUsesTrunkSquash(candidate) || l.cfg.Root == "" || tip == "" || tip == candidate.BaseSHA {
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", l.cfg.Root,
		"merge-tree", "--write-tree", "--merge-base="+candidate.BaseSHA, tip, candidate.HeadSHA)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil {
		return nil, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if reports := mergeTreeConflictReports(string(out)); len(reports) > 0 {
		return reports, nil
	}
	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		msg = err.Error()
	}
	return nil, Externalf("checking squash of %s onto %s: %s", shortSHA(candidate.HeadSHA), shortSHA(tip), msg)
}

// prepareActiveGate checks whether candidate's squash applies to the current
// trunk tip before starting its gate, and registers the gate for during-gate
// checks if it applies. Checking and registration close the window where a
// landing moves the trunk between the pre-gate check and registration.
func (l *Loop) prepareActiveGate(ctx context.Context, candidate Candidate, cancel context.CancelFunc) (*activeGate, []string, string, error) {
	if !candidateUsesTrunkSquash(candidate) || l.cfg.Root == "" {
		return nil, nil, "", nil
	}
	tip, err := l.revParse(ctx, l.cfg.Root, l.trunkRef())
	if err != nil {
		return nil, nil, "", err
	}
	for {
		conflicts, err := l.checkTrunkSquashConflict(ctx, tip, candidate)
		if err != nil {
			return nil, nil, "", err
		}
		if len(conflicts) > 0 {
			return nil, conflicts, tip, nil
		}
		l.activeGatesMu.Lock()
		if l.lastLandedTip != "" && l.lastLandedTip != tip {
			tip = l.lastLandedTip
			l.activeGatesMu.Unlock()
			continue
		}
		g := &activeGate{candidate: candidate, cancel: cancel, checkedTip: tip}
		if l.activeGates == nil {
			l.activeGates = map[*activeGate]struct{}{}
		}
		l.activeGates[g] = struct{}{}
		l.activeGatesMu.Unlock()
		return g, nil, "", nil
	}
}

func (l *Loop) unregisterActiveGate(g *activeGate) {
	if g == nil {
		return
	}
	l.activeGatesMu.Lock()
	delete(l.activeGates, g)
	l.activeGatesMu.Unlock()
}

// checkActiveGatesOnTrunkMove runs one `git merge-tree --write-tree` process
// per running candidate gate against the new trunk tip. Any candidate that no
// longer applies has its gate context canceled at once so it stops and moves
// to conflict repair without waiting for the gate to finish.
func (l *Loop) checkActiveGatesOnTrunkMove(ctx context.Context, tip string) {
	if tip == "" || l.cfg.Root == "" {
		return
	}
	l.activeGatesMu.Lock()
	l.lastLandedTip = tip
	var toCheck []*activeGate
	for g := range l.activeGates {
		if g.checkedTip != tip && !g.hasConflict() {
			g.checkedTip = tip
			toCheck = append(toCheck, g)
		}
	}
	l.activeGatesMu.Unlock()
	if len(toCheck) == 0 {
		return
	}
	var wg sync.WaitGroup
	for _, g := range toCheck {
		wg.Add(1)
		go func(g *activeGate) {
			defer wg.Done()
			conflicts, err := l.checkTrunkSquashConflict(ctx, tip, g.candidate)
			if err != nil || len(conflicts) == 0 {
				return
			}
			g.setConflict(conflicts, tip)
		}(g)
	}
	wg.Wait()
}
