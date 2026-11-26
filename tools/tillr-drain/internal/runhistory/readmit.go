package runhistory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const InputRecordedKind = "drain.inputs"

func init() {
	if err := event.Register(InputRecordedKind, drainmodel.Utility); err != nil {
		panic(err)
	}
}

// RunInputs records the definition and execution inputs at dispatch, not at failure.
type RunInputs struct {
	ItemBlock          string `json:"item_block"`
	WorkflowDefinition string `json:"workflow_definition,omitempty"`
	AllowedPaths       string `json:"allowed_paths"`
	BackendPolicy      string `json:"backend_policy"`
	TillrBuild         string `json:"tillr_build"`
}

// BlockFields are the stored item fields the item-block input hashes beside the
// document's body.
type BlockFields struct {
	Title, BodyRef, DesignDoc, Seat, Workflow string
	Criteria                                  [][2]string
}

// ItemBlockHash is the one spelling of RunInputs.ItemBlock, so a hash recomputed over
// another commit's body compares equal exactly when that commit carries the block.
func ItemBlockHash(f BlockFields, body string) (string, error) {
	return drainmodel.InputHash([]any{f.Title, f.BodyRef, f.DesignDoc, f.Seat, f.Workflow, f.Criteria, body})
}

type StoredBlockFields struct {
	Title    string
	BodyRef  string
	Criteria [][2]string
}

func ReadStoredBlockFields(r *store.Reader, id string) (StoredBlockFields, error) {
	var f StoredBlockFields
	if err := r.QueryRow(`SELECT title, COALESCE(body_ref,'') FROM items WHERE item_id = ?`, id).Scan(&f.Title, &f.BodyRef); err != nil {
		return StoredBlockFields{}, err
	}
	rows, err := r.Query(`SELECT criterion_id, text FROM item_criteria WHERE item_id = ? ORDER BY criterion_id`, id)
	if err != nil {
		return StoredBlockFields{}, err
	}
	f.Criteria = [][2]string{}
	for rows.Next() {
		var c [2]string
		if err := rows.Scan(&c[0], &c[1]); err != nil {
			rows.Close()
			return StoredBlockFields{}, err
		}
		f.Criteria = append(f.Criteria, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return StoredBlockFields{}, err
	}
	return f, nil
}

// ComputeRunInputs computes RunInputs from already-read stored block fields, item
// metadata, policy value/seat-lists pair, workspace root, and build hash.
func ComputeRunInputs(ctx context.Context, itemID, designDoc, seat, workflow string, allowed []string, fields StoredBlockFields, policyVal, policySeatLists any, root, buildHash string) (*RunInputs, error) {
	// Both reads are files, so inside a gathered write they are answered from what was
	// read before it (WriteGathered).
	body, err := Gather(ctx, "definition body "+fields.BodyRef+" of "+itemID+" in "+root, func(context.Context) (string, error) {
		return item.DefinitionBody(root, fields.BodyRef, itemID)
	})
	if err != nil {
		return nil, err
	}
	var workflowBody, workflowError string
	if workflow != "" && item.WorkflowRefValid(workflow) {
		path := filepath.Join(root, "config", "tillr", "workflows", workflow+".json")
		raw, err := Gather(ctx, "read "+path, func(context.Context) (string, error) {
			raw, err := os.ReadFile(path)
			return string(raw), err
		})
		if IsGatherMiss(err) {
			return nil, err
		}
		workflowBody = raw
		if err != nil {
			workflowError = err.Error()
		}
	}
	result := &RunInputs{}
	if result.WorkflowDefinition, err = drainmodel.InputHash([]any{workflowBody, workflowError}); err != nil {
		return nil, err
	}
	// Express eligibility affects allocation, not execution inputs. Keep this encoding
	// stable so upgrading cannot invalidate an unchanged in-flight workflow.
	if result.ItemBlock, err = ItemBlockHash(BlockFields{Title: fields.Title, BodyRef: fields.BodyRef, DesignDoc: designDoc, Seat: seat, Workflow: workflow, Criteria: fields.Criteria}, body); err != nil {
		return nil, err
	}
	if result.AllowedPaths, err = drainmodel.InputHash(allowed); err != nil {
		return nil, err
	}
	if result.BackendPolicy, err = drainmodel.InputHash([]any{policyVal, policySeatLists}); err != nil {
		return nil, err
	}
	result.TillrBuild = buildHash
	return result, nil
}

// ReadLatestInputPayloads returns the latest raw JSON payload for each run with an
// InputRecordedKind event.
func ReadLatestInputPayloads(r *store.Reader) (map[string]json.RawMessage, error) {
	events, err := r.EventsOfKind(0, InputRecordedKind)
	if err != nil {
		return nil, err
	}
	latest := make(map[string]json.RawMessage, len(events))
	for _, e := range events {
		if e.Kind != InputRecordedKind || e.ObjectID == nil {
			continue
		}
		latest[*e.ObjectID] = e.Payload
	}
	return latest, nil
}
