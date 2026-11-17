package drain

import (
	"context"
	"strings"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func (l *Loop) refuseParallelBoundary(ctx context.Context, d Decision, base, head string, result *LoopResult, unstarted bool) error {
	if unstarted {
		s, err := store.Open(ctx, l.cfg.Store, true)
		if err != nil {
			return err
		}
		err = s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := run.ReleaseLaneInTx(ctx, tx, d.LaneID, "read-only boundary changed before restart", l.cfg.Now())
			return err
		})
		s.Close()
		if err != nil {
			return err
		}
	}
	names := []string{}
	for _, node := range parallelNodes(d) {
		names = append(names, node.Workflow.Stages[node.NodeIndex].ID)
		if err := l.nodeOutcome(ctx, node, "refused"); err != nil {
			return err
		}
	}
	c := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: d.RunRequest.RunID, Worktree: l.laneTarget(d.LaneID), BaseSHA: base, HeadSHA: head, RefusalDetail: "item read-only boundary changed while nodes ran: " + strings.Join(names, ", ")}
	if err := l.refuse(ctx, c, result, RefusalFence); err != nil {
		return err
	}
	if stop := l.abandon(ctx, d.LaneID, d.RunRequest.RunID, head, c.RefusalDetail); stop != "" {
		return Externalf("lane %s retained after preservation failed", d.LaneID)
	}
	return l.markExamined(ctx, d)
}
