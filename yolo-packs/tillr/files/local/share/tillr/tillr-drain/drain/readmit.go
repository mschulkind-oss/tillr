package drain

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	ItemBlock          string `json:"item_block"`
	WorkflowDefinition string `json:"workflow_definition,omitempty"`
	AllowedPaths       string `json:"allowed_paths"`
	BackendPolicy      string `json:"backend_policy"`
	TillrBuild         string `json:"tillr_build"`
}

func inputHash(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

// blockFields are the stored item fields the item-block input hashes beside the
// document's body. They are read from the store at dispatch, or from the item's
// record as of a past dispatch when an earlier run's input is recomputed.
type blockFields struct {
	Title, BodyRef, DesignDoc, Seat, Workflow string
	Criteria                                  [][2]string
}

// itemBlockHash is the one spelling of RunInputs.ItemBlock, so a hash recomputed over
// another commit's body compares equal exactly when that commit carries the block.
func itemBlockHash(f blockFields, body string) (string, error) {
	return inputHash([]any{f.Title, f.BodyRef, f.DesignDoc, f.Seat, f.Workflow, f.Criteria, body})
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
	var workflowBody, workflowError string
	if row.Workflow != "" && item.WorkflowRefValid(row.Workflow) {
		raw, err := os.ReadFile(filepath.Join(root, "config", "tillr", "workflows", row.Workflow+".json"))
		workflowBody = string(raw)
		if err != nil {
			workflowError = err.Error()
		}
	}
	result := &RunInputs{}
	if result.WorkflowDefinition, err = inputHash([]any{workflowBody, workflowError}); err != nil {
		return nil, err
	}
	// Express eligibility affects allocation, not execution inputs. Keep this encoding
	// stable so upgrading cannot invalidate an unchanged in-flight workflow.
	if result.ItemBlock, err = itemBlockHash(blockFields{Title: title, BodyRef: bodyRef, DesignDoc: row.DesignDoc, Seat: row.Seat, Workflow: row.Workflow, Criteria: criteria}, body); err != nil {
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
	if result.TillrBuild, err = build.Hash(); err != nil {
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
	priors := []priorRun{}
	for _, prior := range p.priors[itemID] {
		if prior.Class != AttemptInterrupted {
			priors = append(priors, prior)
		}
	}
	if len(priors) == 0 {
		return "", false
	}
	spent := false
	for _, prior := range priors {
		if p.history.judgeHolds[prior.RunID] == ReasonJudgeUnreadable {
			continue
		}
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
		if before.WorkflowDefinition != now.WorkflowDefinition {
			changes = append(changes, "workflow-definition")
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
