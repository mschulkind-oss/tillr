// Package patchmoves reads publication holds from run boundary observations.
// A hold does not assign blame or stop a worker; only the owner may acknowledge it.
package patchmoves

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const AcknowledgedKind = "drain.patch-move-acknowledged"

func init() {
	if err := event.Register(AcknowledgedKind, "drain"); err != nil {
		panic(err)
	}
}

type Move struct {
	Ref string `json:"ref"`
	Old string `json:"old"`
	New string `json:"new"`
}

type Hold struct {
	Move
	Observation int64
	AtMS        int64
	Runs        []string
}

// PendingSQL is also implemented by bin/patch-branches-publish (Python). Acknowledgment
// names one observation and one ref, never a whole fork or all observations of a tip.
const PendingSQL = `SELECT e.seq, e.ts, e.payload, m.value
 FROM events e, json_each(e.payload, '$.patch_moves') m
 WHERE e.kind='boundary.check' AND NOT EXISTS (
 SELECT 1 FROM events a WHERE a.kind='drain.patch-move-acknowledged'
 AND json_extract(a.payload,'$.observation')=e.seq
 AND json_extract(a.payload,'$.ref')=json_extract(m.value,'$.ref'))
 ORDER BY e.seq, json_extract(m.value,'$.ref')`

func Pending(r *store.Reader) ([]Hold, error) {
	rows, err := r.Query(PendingSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Hold{}
	for rows.Next() {
		var h Hold
		var raw, move string
		if err := rows.Scan(&h.Observation, &h.AtMS, &raw, &move); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(move), &h.Move); err != nil {
			return nil, err
		}
		var payload struct {
			Runs []struct {
				RunID string `json:"run_id"`
			} `json:"runs"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return nil, err
		}
		for _, run := range payload.Runs {
			h.Runs = append(h.Runs, run.RunID)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (h Hold) Fork() string { return strings.TrimPrefix(h.Ref, "refs/heads/patch/") }

func (h Hold) Remedy(storePath string) string {
	return fmt.Sprintf("tillr drain acknowledge-patch-move --store %s --observation %d --fork %s --by <owner>", shellQuote(storePath), h.Observation, shellQuote(h.Fork()))
}

// shellQuote keeps paths and fork names safe as single POSIX shell arguments.
func shellQuote(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r))
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// Acknowledge validates and records one outstanding hold in the caller's write transaction.
// Repeated or nonexistent acknowledgments are errors, not evidence of owner review.
func Acknowledge(tx *store.Tx, observation int64, fork, by string, at time.Time) error {
	if observation <= 0 || strings.TrimSpace(fork) == "" || strings.TrimSpace(by) == "" || at.IsZero() {
		return fmt.Errorf("acknowledgment needs an observation sequence, fork, owner and timestamp")
	}
	holds, err := Pending(tx.Reader())
	if err != nil {
		return err
	}
	for _, h := range holds {
		if h.Observation != observation || h.Fork() != fork {
			continue
		}
		body, err := json.Marshal(struct {
			Observation int64  `json:"observation"`
			Ref         string `json:"ref"`
			By          string `json:"by"`
			At          string `json:"acknowledged_at"`
		}{observation, h.Ref, by, at.UTC().Format(time.RFC3339Nano)})
		if err != nil {
			return err
		}
		id := fmt.Sprintf("%d:%s", observation, h.Ref)
		_, err = tx.Append(event.Event{Kind: AcknowledgedKind, ObjectType: "patch-move", ObjectID: &id, Payload: body, At: at})
		return err
	}
	return fmt.Errorf("no unacknowledged patch move for observation %d, fork %s", observation, fork)
}
