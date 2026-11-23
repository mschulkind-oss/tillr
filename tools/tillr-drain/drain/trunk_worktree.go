package drain

import (
	"context"
	"encoding/json"

	"github.com/mschulkind-oss/tillr-drain/fixer"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const trunkCoreWorktreeKind = "drain.trunk-core-worktree"

func init() {
	if err := event.Register(trunkCoreWorktreeKind, Utility); err != nil {
		panic(err)
	}
}

// Observe only the shared repository config, not an included, per-worktree,
// global, or command-line setting. Override the work tree for this read so the
// setting being inspected cannot redirect Git or make it chdir into a stale path.
func (l *Loop) observeTrunkCoreWorktree(ctx context.Context) error {
	if l.nonLanding {
		return nil
	}
	config, worktree, set, err := fixer.CheckCoreWorktreeConfig(ctx, l.cfg.Root)
	if err != nil || !set {
		return err
	}
	body, err := json.Marshal(map[string]string{"config_path": config, "worktree": worktree})
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, false)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: trunkCoreWorktreeKind, ObjectType: "repository", ObjectID: &config, Payload: body, At: l.cfg.Now()})
		return err
	})
}
