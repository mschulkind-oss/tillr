package drain

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/workflow"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// CommandEntry registers a fixed program and argv. Flag types are string, number,
// or path (a relative repository path); workflow data is appended as argv only.
type CommandEntry = workflow.CommandEntry

func resolveCommand(root string, stage *WorkflowStage) error {
	return workflow.ResolveCommand(root, stage)
}

func commandPath(root, path string) error {
	return workflow.CommandPath(root, path)
}

func commandRequest(s WorkflowStage, itemID, laneID, runID string) *run.Request {
	return &run.Request{RunID: runID, ItemID: itemID, NodeID: s.ID, Node: s.ID, LaneID: laneID, Backend: "command", Command: append([]string(nil), s.Command...), WorkingDir: WorktreeDir, TimeoutMS: s.TimeoutMS, EnvRefs: []string{}}
}

func commandStage(d Decision) bool {
	return d.Workflow != nil && (d.Workflow.Stages[d.NodeIndex].Kind == "command" || d.Workflow.Stages[d.NodeIndex].Kind == "join")
}

func joinStage(d Decision) bool {
	return d.Workflow != nil && d.NodeIndex >= 0 && d.NodeIndex < len(d.Workflow.Stages) && d.Workflow.Stages[d.NodeIndex].Kind == "join"
}

// CommandOnlyWorkflows reports whether root declares at least one workflow under
// config/tillr/workflows and every stage of every expanded workflow is a command stage.
// A loop over such a root needs no agent backend policy to start.
func CommandOnlyWorkflows(root string) bool {
	if root == "" {
		return false
	}
	dir := filepath.Join(root, "config", "tillr", "workflows")
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	var paths []string
	if err := filepath.WalkDir(dir, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(p, ".json") {
			paths = append(paths, p)
		}
		return nil
	}); err != nil || len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		cases, err := workflow.WorkflowCheckArguments(root, p)
		if err != nil || len(cases) == 0 {
			return false
		}
		for _, args := range cases {
			symbols := map[string]bool{}
			for _, value := range args {
				values, _ := value.([]any)
				for _, v := range values {
					if s, ok := v.(string); ok && strings.HasPrefix(s, workflow.WorkflowCheckPathPrefix) {
						symbols[s] = true
					}
				}
			}
			w, err := workflow.ExpandWorkflow(root, p, nil, map[string]bool{}, args, symbols)
			if err != nil || len(w.Stages) == 0 {
				return false
			}
			for _, s := range w.Stages {
				if s.Kind != "command" {
					return false
				}
			}
		}
	}
	return true
}

// replayCommandHold completes the cleanup of a recorded terminal hold. A pin
// means the earlier examination retained its head before resetting the lane;
// comparing the reset worktree to NodeBaseSHA would reclassify that hold and
// pin the trunk over the builder's commit. Never update the retained pin here.
func (l *Loop) replayCommandHold(ctx context.Context, d Decision) (bool, error) {
	s, err := store.Open(ctx, l.cfg.Store, false)
	if err != nil {
		return false, err
	}
	var outcome string
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT outcome FROM node_runs WHERE run_id=?`, d.RunRequest.RunID).Scan(&outcome)
	})
	s.Close()
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if outcome != "failed" && outcome != "fault" && outcome != "refused" {
		return false, nil
	}
	ref := "refs/tillr/runs/" + d.RunRequest.RunID
	pins, err := l.git(ctx, l.cfg.Root, "for-each-ref", "--format=%(refname)", ref)
	if err != nil {
		return false, err
	}
	pinned := false
	for _, name := range strings.Split(pins, "\n") {
		if name == ref {
			pinned = true
		}
	}
	if !pinned {
		// Classification persisted but cleanup did not reach the pin: the
		// original worktree remains available for the ordinary settlement.
		return false, nil
	}
	if err := l.preserveWork(ctx, d.LaneID, d.RunRequest.RunID, "replayed command node hold"); err != nil {
		return true, err
	}
	if err := l.resetLane(ctx, d.LaneID); err != nil {
		return true, err
	}
	return true, l.markExamined(ctx, d)
}
