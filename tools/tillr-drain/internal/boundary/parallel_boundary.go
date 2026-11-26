package boundary

import (
	"context"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// RefuseParallelBoundary releases an unstarted lane when needed, records node refusals,
// and settles the parallel-boundary refusal through the caller's callbacks.
func RefuseParallelBoundary(
	ctx context.Context,
	env drainmodel.Env,
	laneID string,
	unstarted bool,
	nodeCount int,
	refuseNode func(ctx context.Context, index int) (string, error),
	settle func(ctx context.Context, detail string) error,
) error {
	if unstarted {
		s, err := store.Open(ctx, env.StorePath, true)
		if err != nil {
			return err
		}
		err = s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := run.ReleaseLaneInTx(ctx, tx, laneID, "read-only boundary changed before restart", env.Now())
			return err
		})
		s.Close()
		if err != nil {
			return err
		}
	}
	names := make([]string, 0, nodeCount)
	for i := 0; i < nodeCount; i++ {
		name, err := refuseNode(ctx, i)
		if err != nil {
			return err
		}
		names = append(names, name)
	}
	return settle(ctx, "item read-only boundary changed while nodes ran: "+strings.Join(names, ", "))
}
