package drain

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const inputRecordedKind = "drain.inputs"
const ReasonInputsChanged = "inputs-changed"
const ReasonReadmissionExhausted = "readmission-exhausted"

// RunInputs records the definition and execution inputs at dispatch, not at failure.
// Hashes avoid duplicating document prose in the store. Queue rank, state, timestamps
// and the base commit are not causes for an automatic second attempt.
type RunInputs struct {
	ItemBlock     string `json:"item_block"`
	AllowedPaths  string `json:"allowed_paths"`
	BackendPolicy string `json:"backend_policy"`
	TillrBuild    string `json:"tillr_build"`
}

func inputHash(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

func currentRunInputs(r *store.Reader, row itemRow, policy *Policy, root string, build BuildStamp) (*RunInputs, error) {
	var title, bodyRef string
	if err := r.QueryRow(`SELECT title, COALESCE(body_ref,'') FROM items WHERE item_id = ?`, row.ItemID).Scan(&title, &bodyRef); err != nil {
		return nil, err
	}
	rows, err := r.Query(`SELECT criterion_id, text FROM item_criteria WHERE item_id = ? ORDER BY criterion_id`, row.ItemID)
	if err != nil {
		return nil, err
	}
	criteria := [][2]string{}
	for rows.Next() {
		var c [2]string
		if err := rows.Scan(&c[0], &c[1]); err != nil {
			rows.Close()
			return nil, err
		}
		criteria = append(criteria, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	body, err := item.DefinitionBody(root, bodyRef, row.ItemID)
	if err != nil {
		return nil, err
	}
	result := &RunInputs{}
	if result.ItemBlock, err = inputHash([]any{title, bodyRef, row.DesignDoc, row.Seat, criteria, body}); err != nil {
		return nil, err
	}
	if result.AllowedPaths, err = inputHash(row.Allowed); err != nil {
		return nil, err
	}
	// SeatLists is intentionally excluded from Policy's JSON: include it explicitly.
	if result.BackendPolicy, err = inputHash([]any{policy, func() any {
		if policy == nil {
			return nil
		}
		return policy.SeatLists
	}()}); err != nil {
		return nil, err
	}
	// A relocation of the same executable is not a new build.
	if result.TillrBuild, err = inputHash([]any{build.Revision, build.Modified, build.Digest}); err != nil {
		return nil, err
	}
	return result, nil
}

func readInputDecisions(r *store.Reader) (map[string]Decision, error) {
	events, err := r.Events(0)
	if err != nil {
		return nil, err
	}
	out := map[string]Decision{}
	for _, e := range events {
		if e.Kind != inputRecordedKind || e.ObjectID == nil {
			continue
		}
		var d Decision
		if err := json.Unmarshal(e.Payload, &d); err != nil {
			return nil, err
		}
		out[*e.ObjectID] = d
	}
	return out, nil
}

// readmission compares only a recorded basis. Legacy runs without one stay held;
// an unchanged edit timestamp or advancing trunk cannot create an allowance.
func (p *planner) readmission(itemID string, now *RunInputs) (string, bool) {
	priors := p.priors[itemID]
	if len(priors) == 0 {
		return "", false
	}
	spent := false
	for _, prior := range priors {
		d := p.inputDecisions[prior.RunID]
		if d.Action == ActionRetry {
			spent = false
		}
		if d.Readmitted {
			spent = true
		}
	}
	if spent {
		return "", true
	}
	last := priors[len(priors)-1]
	before := p.inputDecisions[last.RunID].Inputs
	changes := []string{}
	if before != nil {
		if before.ItemBlock != now.ItemBlock {
			changes = append(changes, "item-block")
		}
		if before.AllowedPaths != now.AllowedPaths {
			changes = append(changes, "allowed-paths")
		}
		if before.BackendPolicy != now.BackendPolicy {
			changes = append(changes, "backend-policy")
		}
		if before.TillrBuild != now.TillrBuild {
			changes = append(changes, "tillr-build")
		}
	}
	// Only a loop-owned run that was examined and produced no commit qualifies.
	// A signal alone is not evidence: a standalone runner or a committed run must hold.
	work := p.works[itemID]
	if work != nil && work.Done && work.Decision.RunRequest != nil && work.Decision.RunRequest.RunID == last.RunID && work.Candidate == nil && p.history.refusals[last.RunID] == RefusalNoOp && work.Orphaned {
		changes = append(changes, "earlier run orphaned by a loop stop")
	}
	if len(changes) == 0 {
		return "", false
	}
	return ReasonInputsChanged + ": " + strings.Join(changes, ", "), false
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

func init() {
	if err := event.Register(inputRecordedKind, Utility); err != nil {
		panic(err)
	}
}
