package preserved

import (
	"encoding/json"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	Kind       = "drain.work-preserved"
	PrunedKind = "drain.work-pruned"
	Prefix     = "refs/tillr/preserved/"
)

func init() {
	for _, kind := range []string{Kind, PrunedKind} {
		if err := event.Register(kind, "drain"); err != nil {
			panic(err)
		}
	}
}

// PreservedFile describes the difference from the lane's HEAD. A dash means
// Git counted a binary change rather than text lines. OutsideFence is evidence,
// not permission to land the file.
type PreservedFile struct {
	Path         string `json:"path"`
	Added        string `json:"added"`
	Deleted      string `json:"deleted"`
	OutsideFence bool   `json:"outside_fence"`
}

// PreservedWork is a settlement record for earlier uncommitted work. Its ref is
// recovery evidence only: it is never a candidate, gated tree or landing input.
type PreservedWork struct {
	ItemID     string          `json:"item_id"`
	RunID      string          `json:"run_id"`
	LaneID     string          `json:"lane_id"`
	Ref        string          `json:"ref"`
	Repository string          `json:"repository"`
	Reason     string          `json:"reason"`
	Summary    string          `json:"summary"`
	Files      []PreservedFile `json:"files"`
	IndexRef   string          `json:"index_ref,omitempty"`
	IndexFiles []PreservedFile `json:"index_files,omitempty"`
	// Unmerged names the paths the lane's index held unmerged; the snapshot took them
	// from the working copy.
	Unmerged []string `json:"unmerged,omitempty"`
	Pruned   bool     `json:"pruned,omitempty"`
}

// ReadPreservedWork projects settlement and retention events in append order.
// Pruned records remain visible to history readers, but not to a later brief.
func ReadPreservedWork(r *store.Reader) ([]PreservedWork, error) {
	events, err := r.Events(0)
	if err != nil {
		return nil, err
	}
	out := []PreservedWork{}
	for _, e := range events {
		switch e.Kind {
		case Kind:
			var p PreservedWork
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return nil, err
			}
			out = append(out, p)
		case PrunedKind:
			var p PreservedWork
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return nil, err
			}
			for i := range out {
				if out[i].Ref == p.Ref && out[i].Repository == p.Repository {
					out[i].Pruned = true
				}
			}
		}
	}
	return out, nil
}
