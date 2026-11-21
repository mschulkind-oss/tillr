package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/definition"
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
	LaneID     string   `json:"lane_id"`
	SHA        string   `json:"sha"`
	Refs       []string `json:"refs"`
	Landed     bool     `json:"landed"`
	Closed     bool     `json:"closed,omitempty"`
	Superseded bool     `json:"superseded,omitempty"`
	ItemID     string   `json:"item_id,omitempty"`
	RunID      string   `json:"run_id,omitempty"`
	Reauthored bool     `json:"reauthored,omitempty"`
}

// retainingRunPins answers the run pins that contain sha. A held candidate's lane is
// released only after its pin is read back, and a run pin is never pruned, so a lane commit
// one of them contains is a copy and not the only copy of anyone's work. Preserved-work refs
// (refs/tillr/preserved/) do not count: retention prunes them.
func (s *LaneStager) retainingRunPins(ctx context.Context, sha string) ([]string, error) {
	out, err := s.git(ctx, s.Root, "for-each-ref", "--contains", sha, "--format=%(refname)", "refs/tillr/runs/", "refs/tillr/runs-long/")
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
func (s *LaneStager) clearableLaneCopy(ctx context.Context, lane, itemID, sha, checkout string) (bool, error) {
	// A fork-only object cannot be contained by any root pin. Do not fetch
	// it until the store has established that this item owns the commit.
	var rootObjectErr error
	if checkout != "" {
		_, rootObjectErr = s.git(ctx, s.Root, "cat-file", "-e", sha+"^{commit}")
	}
	var refs []string
	var err error
	if rootObjectErr == nil {
		refs, err = s.retainingRunPins(ctx, sha)
		if err != nil {
			return false, err
		}
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
	var activeForeignOwner bool
	var ownRunID string
	err = r.ReadTx(ctx, func(reader *store.Reader) error {
		works, err := readPending(reader)
		if err != nil {
			return err
		}
		for _, w := range works {
			if w != nil && !w.Done && !w.LaneReleased && w.Decision.LaneID == lane && w.Decision.ItemID != itemID {
				activeForeignOwner = true
				return nil
			}
		}
		if len(refs) == 0 && itemID != "" {
			ownRunID, err = findOwnRunForCommit(reader, itemID, lane, sha)
			if err != nil {
				return err
			}
		}
		runs := []string{}
		for _, ref := range refs {
			if runID, ok := strings.CutPrefix(ref, "refs/tillr/runs/"); ok {
				runs = append(runs, runID)
			}
		}
		var runID string
		err = reader.QueryRow(`SELECT json_extract(payload, '$.run_id') FROM events WHERE kind = 'land.landing' AND json_extract(payload, '$.result') = 'landed' AND json_extract(payload, '$.head_sha') = ? ORDER BY seq DESC LIMIT 1`, sha).Scan(&runID)
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
		// Check landings table in case the landing was recorded without a land.landing event.
		var itemID string
		var landingCols int
		if err := reader.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('landings') WHERE name IN ('result', 'head_sha', 'run_id', 'item_id')`).Scan(&landingCols); err == nil && landingCols == 4 {
			err = reader.QueryRow(`SELECT run_id, item_id FROM landings WHERE result = 'landed' AND head_sha = ? ORDER BY rowid DESC LIMIT 1`, sha).Scan(&runID, &itemID)
			if err == nil {
				payload.Landed, payload.RunID, payload.ItemID = true, runID, itemID
				return nil
			}
			if err != sql.ErrNoRows {
				return err
			}
		}
		// Check if sha belongs to a superseded candidate or a landed/closed item.
		err = reader.QueryRow(`SELECT COALESCE(object_id, ''), COALESCE(json_extract(payload, '$.candidate.item_id'), '') FROM events WHERE kind = 'drain.candidate-superseded' AND (json_extract(payload, '$.pin_sha') = ? OR json_extract(payload, '$.head_sha') = ? OR json_extract(payload, '$.candidate.head_sha') = ? OR json_extract(payload, '$.candidate.worker_head_sha') = ?) ORDER BY seq DESC LIMIT 1`, sha, sha, sha, sha).Scan(&runID, &itemID)
		if err == nil {
			payload.Superseded = true
			if runID != "" {
				payload.RunID = runID
			}
			if itemID != "" {
				payload.ItemID = itemID
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		err = reader.QueryRow(`SELECT COALESCE(json_extract(payload, '$.run_id'), object_id, ''), COALESCE(json_extract(payload, '$.item_id'), '') FROM events WHERE kind IN ('drain.candidate', 'drain.retirement', 'drain.prepared-candidate', 'drain.candidate-gated', 'drain.judge-landed') AND (json_extract(payload, '$.head_sha') = ? OR json_extract(payload, '$.worker_head_sha') = ?) ORDER BY seq DESC LIMIT 1`, sha, sha).Scan(&runID, &itemID)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == sql.ErrNoRows {
			err = reader.QueryRow(`SELECT COALESCE(json_extract(payload, '$.run_id'), ''), COALESCE(json_extract(payload, '$.item_id'), '') FROM events WHERE kind = 'drain.terminal-item' AND json_extract(payload, '$.head_sha') = ? ORDER BY seq DESC LIMIT 1`, sha).Scan(&runID, &itemID)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
		}
		if err == sql.ErrNoRows {
			err = reader.QueryRow(`SELECT COALESCE(object_id, '') FROM events WHERE kind = 'drain.refusal' AND json_extract(payload, '$.worker_session.head_sha') = ? ORDER BY seq DESC LIMIT 1`, sha).Scan(&runID)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
		}
		if err == sql.ErrNoRows {
			err = reader.QueryRow(`SELECT COALESCE(object_id, '') FROM events WHERE kind = 'gate.verdict' AND json_extract(payload, '$.head_sha') = ? ORDER BY seq DESC LIMIT 1`, sha).Scan(&runID)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
		}
		if err == sql.ErrNoRows {
			err = reader.QueryRow(`SELECT COALESCE(json_extract(payload, '$.parent_run_id'), ''), COALESCE(json_extract(payload, '$.decision.item_id'), '') FROM events WHERE kind = 'drain.node-advanced' AND json_extract(payload, '$.decision.node_base_sha') = ? ORDER BY seq DESC LIMIT 1`, sha).Scan(&runID, &itemID)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
		}
		if runID == "" && len(runs) > 0 {
			runID = runs[0]
		}
		if runID != "" {
			if payload.RunID == "" {
				payload.RunID = runID
			}
			var supersededCount int
			if err := reader.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'drain.candidate-superseded' AND object_id = ?`, runID).Scan(&supersededCount); err != nil {
				return err
			}
			if supersededCount > 0 {
				payload.Superseded = true
				if itemID != "" {
					payload.ItemID = itemID
				}
				return nil
			}
			if itemID == "" {
				err := reader.QueryRow(`SELECT item_id FROM runs WHERE run_id = ?`, runID).Scan(&itemID)
				if err == sql.ErrNoRows {
					err = reader.QueryRow(`SELECT item_id FROM dispatches WHERE run_id = ? ORDER BY dispatched_at DESC LIMIT 1`, runID).Scan(&itemID)
				}
				if err != nil && err != sql.ErrNoRows {
					return err
				}
			}
		}
		if itemID != "" {
			payload.ItemID = itemID
			var state string
			err := reader.QueryRow(`SELECT state FROM items WHERE item_id = ?`, itemID).Scan(&state)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			if err == nil {
				declared, readErr := definition.ForReader(reader)
				if readErr != nil {
					return readErr
				}
				if state == StateLanded {
					payload.Landed = true
				} else if state == "superseded" {
					payload.Superseded = true
				} else if finishedState(declared.Parsed, state) {
					payload.Closed = true
				}
			}
		}
		return nil
	})
	r.Close()
	if err != nil {
		return false, err
	}
	if activeForeignOwner {
		return false, nil
	}
	if ownRunID != "" {
		if err := writeRunPin(ctx, s.git, s.Root, checkout, ownRunID, sha, true); err != nil {
			return false, err
		}
		refs = append(refs, pinRef(ownRunID))
		payload.Refs = refs
		if payload.RunID == "" {
			payload.RunID = ownRunID
		}
	}
	if len(refs) == 0 && !payload.Landed && !payload.Closed && !payload.Superseded {
		t, err := land.OpenTrunk(s.Root)
		if err != nil {
			return false, err
		}
		t.SetContext(ctx)
		tip, err := t.Head(TrunkRef)
		if err != nil {
			return false, err
		}
		_, same, err := t.IdentityRewrite(sha, tip)
		if err != nil || !same {
			return false, err
		}
		payload.Reauthored = true
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

func findOwnRunForCommit(reader *store.Reader, itemID, lane, sha string) (string, error) {
	var runID string
	err := reader.QueryRow(`SELECT object_id FROM events WHERE kind = ? AND object_type = 'run' AND json_extract(payload, '$.item_id') = ? AND json_extract(payload, '$.head_sha') = ? ORDER BY seq DESC LIMIT 1`, candidateReadyKind, itemID, sha).Scan(&runID)
	if err == nil && runID != "" {
		return runID, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	err = reader.QueryRow(`SELECT json_extract(payload, '$.run_id') FROM events WHERE kind = ? AND object_type = 'call' AND json_extract(payload, '$.item_id') = ? AND json_extract(payload, '$.commit') = ? ORDER BY seq DESC LIMIT 1`, callOffered, itemID, sha).Scan(&runID)
	if err == nil && runID != "" {
		return runID, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	err = reader.QueryRow(`SELECT COALESCE(json_extract(payload, '$.parent_run_id'), '') FROM events WHERE kind = ? AND object_type = 'run' AND json_extract(payload, '$.decision.item_id') = ? AND json_extract(payload, '$.decision.node_base_sha') = ? ORDER BY seq DESC LIMIT 1`, nodeAdvancedKind, itemID, sha).Scan(&runID)
	if err == nil && runID != "" {
		return runID, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	err = reader.QueryRow(`SELECT COALESCE(json_extract(payload, '$.candidate.run_id'), '') FROM events WHERE kind = ? AND object_type = 'run' AND json_extract(payload, '$.candidate.item_id') = ? AND json_extract(payload, '$.candidate.head_sha') = ? ORDER BY seq DESC LIMIT 1`, candidateLaneKind, itemID, sha).Scan(&runID)
	if err == nil && runID != "" {
		return runID, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	err = reader.QueryRow(`SELECT COALESCE(json_extract(payload, '$.decision.returned.worker_session.run_id'), '') FROM events WHERE kind = ? AND object_type = 'run' AND json_extract(payload, '$.decision.item_id') = ? AND json_extract(payload, '$.decision.returned.worker_session.head_sha') = ? ORDER BY seq DESC LIMIT 1`, dispatchStartedKind, itemID, sha).Scan(&runID)
	if err == nil && runID != "" {
		return runID, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	return "", nil
}
