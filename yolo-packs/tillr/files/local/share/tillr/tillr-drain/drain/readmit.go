package drain

import (
	"context"
	"encoding/json"

	"github.com/mschulkind-oss/tillr-drain/internal/runhistory"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func currentRunInputs(ctx context.Context, r *store.Reader, row itemRow, policy *Policy, root string, build BuildStamp) (*RunInputs, error) {
	fields, err := readStoredBlockFields(r, row.ItemID)
	if err != nil {
		return nil, err
	}
	return currentRunInputsFromStored(ctx, row, fields, policy, root, build)
}

func currentRunInputsFromStored(ctx context.Context, row itemRow, fields storedBlockFields, policy *Policy, root string, build BuildStamp) (*RunInputs, error) {
	var seatLists any
	if policy != nil {
		seatLists = policy.SeatLists
	}
	buildHash, err := build.Hash()
	if err != nil {
		return nil, err
	}
	return runhistory.ComputeRunInputs(ctx, row.ItemID, row.DesignDoc, row.Seat, row.Workflow, row.Allowed, fields, policy, seatLists, root, buildHash)
}

func readInputDecisions(r *store.Reader) (map[string]Decision, error) {
	latest, err := runhistory.ReadLatestInputPayloads(r)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Decision, len(latest))
	for id, payload := range latest {
		var d Decision
		if err := json.Unmarshal(payload, &d); err != nil {
			return nil, err
		}
		out[id] = d
	}
	return out, nil
}

func (l *Loop) orphanedRun(ctx context.Context, rec run.Record) (bool, error) {
	r, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, err
	}
	defer r.Close()
	orphaned := false
	err = r.ReadTx(ctx, func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		work := works[rec.ItemID]
		orphaned = work != nil && work.Decision.RunRequest != nil && work.Decision.RunRequest.RunID == rec.RunID && work.Orphaned
		return nil
	})
	return orphaned, err
}
