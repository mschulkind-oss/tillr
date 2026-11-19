package drain

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const inputRecordedKind = "drain.inputs"

// RunInputs records the definition and execution inputs at dispatch, not at failure.
// Hashes avoid duplicating document prose in the store. Definition and policy changes
// do not re-run an item: the owner's 2026-11-17 ruling (audit finding F-01) says those
// changes are no fact about its work. They bind dispatch to the definition it read.
// TillrBuild separately identifies older sessions for same-attempt build migration.
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

func currentRunInputs(ctx context.Context, r *store.Reader, row itemRow, policy *Policy, root string, build BuildStamp) (*RunInputs, error) {
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
	// Both reads are files, so inside a gathered write they are answered from what was
	// read before it (writeGathered).
	body, err := gather(ctx, "definition body "+bodyRef+" of "+row.ItemID+" in "+root, func(context.Context) (string, error) {
		return item.DefinitionBody(root, bodyRef, row.ItemID)
	})
	if err != nil {
		return nil, err
	}
	var workflowBody, workflowError string
	if row.Workflow != "" && item.WorkflowRefValid(row.Workflow) {
		path := filepath.Join(root, "config", "tillr", "workflows", row.Workflow+".json")
		raw, err := gather(ctx, "read "+path, func(context.Context) (string, error) {
			raw, err := os.ReadFile(path)
			return string(raw), err
		})
		if isGatherMiss(err) {
			return nil, err
		}
		workflowBody = raw
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
