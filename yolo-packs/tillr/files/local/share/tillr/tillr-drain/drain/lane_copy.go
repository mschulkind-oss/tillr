package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// laneCopyClearedKind records a lane staging reset although it held a commit neither the
// base nor trunk contains, because a run pin (refs/tillr/runs/<run_id>) already retains that
// commit or a recorded landing names it as the landed head. Its object is the lane, and its payload is laneCopyCleared.
const laneCopyClearedKind = "drain.lane-copy-cleared"

func init() {
	if err := event.Register(laneCopyClearedKind, Utility); err != nil {
		panic(err)
	}
}

// laneCopyCleared is drain.lane-copy-cleared's payload. Landed says whether a recorded landing
// (a land.landing event whose result is "landed") names the commit or one of the retaining
// runs, and RunID is that run, else the first retaining one: a landed copy's change is on the
// trunk under its squash commit, and an unlanded one stays with its item, which restages it
// from the pin.
type laneCopyCleared struct {
	LaneID string   `json:"lane_id"`
	SHA    string   `json:"sha"`
	Refs   []string `json:"refs"`
	Landed bool     `json:"landed"`
	RunID  string   `json:"run_id,omitempty"`
}

// retainingRunPins answers the run pins that contain sha. A held candidate's lane is
// released only after its pin is read back, and a run pin is never pruned, so a lane commit
// one of them contains is a copy and not the only copy of anyone's work. Preserved-work refs
// (refs/tillr/preserved/) do not count: retention prunes them.
func (s *LaneStager) retainingRunPins(ctx context.Context, sha string) ([]string, error) {
	out, err := s.git(ctx, s.Root, "for-each-ref", "--contains", sha, "--format=%(refname)", "refs/tillr/runs/")
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			refs = append(refs, line)
		}
	}
	return refs, nil
}

// clearableLaneCopy answers whether a lane commit neither the base nor trunk contains may be
// reset away, and records why when it may. It may when a run pin contains it, or when a
// recorded landing (a land.landing event whose result is "landed") names it as the landed
// head: a squash landing puts the change on the trunk under a different sha, so the trunk's
// ancestry never contains the candidate itself. Anything else is the only copy of work
// nobody landed, and is refused by the caller.
//
// A stager given no store can read no landing, so only a pin clears; it records nothing,
// because the pin is the retention and the event only the account of it.
func (s *LaneStager) clearableLaneCopy(ctx context.Context, lane, sha string) (bool, error) {
	refs, err := s.retainingRunPins(ctx, sha)
	if err != nil {
		return false, err
	}
	if s.Store == "" {
		return len(refs) > 0, nil
	}
	payload := laneCopyCleared{LaneID: lane, SHA: sha, Refs: refs}
	if refs == nil {
		payload.Refs = []string{}
	}
	r, err := store.OpenRead(ctx, s.Store)
	if err != nil {
		return false, err
	}
	err = r.ReadTx(ctx, func(reader *store.Reader) error {
		runs := []string{}
		for _, ref := range refs {
			runs = append(runs, strings.TrimPrefix(ref, "refs/tillr/runs/"))
		}
		var runID string
		err := reader.QueryRow(`SELECT json_extract(payload, '$.run_id') FROM events WHERE kind = 'land.landing' AND json_extract(payload, '$.result') = 'landed' AND json_extract(payload, '$.head_sha') = ? ORDER BY seq DESC LIMIT 1`, sha).Scan(&runID)
		if err == nil {
			payload.Landed, payload.RunID = true, runID
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		for _, id := range runs {
			err := reader.QueryRow(`SELECT json_extract(payload, '$.run_id') FROM events WHERE kind = 'land.landing' AND json_extract(payload, '$.result') = 'landed' AND json_extract(payload, '$.run_id') = ? LIMIT 1`, id).Scan(&runID)
			if err == sql.ErrNoRows {
				continue
			}
			if err != nil {
				return err
			}
			payload.Landed, payload.RunID = true, runID
			return nil
		}
		if len(runs) > 0 {
			payload.RunID = runs[0]
		}
		return nil
	})
	r.Close()
	if err != nil {
		return false, err
	}
	if len(refs) == 0 && !payload.Landed {
		return false, nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}
	w, err := store.Open(ctx, s.Store, true)
	if err != nil {
		return false, err
	}
	defer w.Close()
	id := lane
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	err = w.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: laneCopyClearedKind, ObjectType: "lane", ObjectID: &id, Payload: body, At: now})
		return err
	})
	return err == nil, err
}
