package drain

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

type sqlReader interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

type signatureDigest [4]uint64

func (d *signatureDigest) add(sum [32]byte) {
	d[0] += binary.LittleEndian.Uint64(sum[0:8])
	d[1] += binary.LittleEndian.Uint64(sum[8:16])
	d[2] += binary.LittleEndian.Uint64(sum[16:24])
	d[3] += binary.LittleEndian.Uint64(sum[24:32])
}

func (d signatureDigest) sum(schema versionSchema) [32]byte {
	var buf [33]byte
	if schema.hasCriteria {
		buf[0] |= 1
	}
	if schema.hasClaimClass {
		buf[0] |= 2
	}
	if schema.hasEdges {
		buf[0] |= 4
	}
	binary.LittleEndian.PutUint64(buf[1:9], d[0])
	binary.LittleEndian.PutUint64(buf[9:17], d[1])
	binary.LittleEndian.PutUint64(buf[17:25], d[2])
	binary.LittleEndian.PutUint64(buf[25:33], d[3])
	return sha256.Sum256(buf[:])
}

func hashSignatureRows(r sqlReader, acc *signatureDigest, tag byte, query string, colCount int) error {
	rows, err := r.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()
	vals := make([]any, colCount)
	ptrs := make([]any, colCount)
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	buf := make([]byte, 0, 1024)
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		buf = append(buf[:0], tag)
		for _, v := range vals {
			switch val := v.(type) {
			case nil:
				buf = append(buf, 0)
			case int64:
				buf = binary.LittleEndian.AppendUint64(append(buf, 1), uint64(val))
			case float64:
				buf = binary.LittleEndian.AppendUint64(append(buf, 2), math.Float64bits(val))
			case bool:
				var b byte
				if val {
					b = 1
				}
				buf = append(buf, 3, b)
			case string:
				buf = binary.LittleEndian.AppendUint64(append(buf, 4), uint64(len(val)))
				buf = append(buf, val...)
			case []byte:
				buf = binary.LittleEndian.AppendUint64(append(buf, 5), uint64(len(val)))
				buf = append(buf, val...)
			default:
				s := fmt.Sprint(val)
				buf = binary.LittleEndian.AppendUint64(append(buf, 6), uint64(len(s)))
				buf = append(buf, s...)
			}
		}
		acc.add(sha256.Sum256(buf))
	}
	return rows.Err()
}

// Detect raw edits without refolding the event log or building sorted JSON
// aggregates in SQLite's temp pager. Include values rather than lengths so an
// unlogged same-size replacement still invalidates the view.
func readStartItemsSignature(r *store.Reader, schema versionSchema) ([32]byte, error) {
	return readStartItemsSignatureOn(r, schema)
}

func readStartItemsSignatureOn(r sqlReader, schema versionSchema) ([32]byte, error) {
	fields := []string{"item_id", "updated_at", "state", "title", "rank", "body_ref", "design_doc"}
	for _, col := range []string{"allowed_paths", "waiting_on", "seat", "express", "workflow", "workflow_args", "judge_mode", "prose", "store_declared"} {
		if schema.itemColumns[col] {
			fields = append(fields, col)
		}
	}
	var acc signatureDigest
	if err := hashSignatureRows(r, &acc, 'I', `SELECT `+strings.Join(fields, ", ")+` FROM items`, len(fields)); err != nil {
		return [32]byte{}, err
	}
	if schema.hasEdges {
		if err := hashSignatureRows(r, &acc, 'E', `SELECT item_id, blocked_by FROM item_edges`, 2); err != nil {
			return [32]byte{}, err
		}
	}
	if schema.hasCriteria {
		cols := []string{"item_id", "criterion_id", "text", "state"}
		if schema.hasClaimClass {
			cols = append(cols, "claim_class")
		}
		if err := hashSignatureRows(r, &acc, 'C', `SELECT `+strings.Join(cols, ", ")+` FROM item_criteria`, len(cols)); err != nil {
			return [32]byte{}, err
		}
	}
	return acc.sum(schema), nil
}

func (d startStoreDelta) refreshItems(r *store.Reader, s *startSnapshot, at time.Time, touched map[string]bool) error {
	if len(d.items) == 0 {
		return nil
	}
	s.itemsByID, s.states, s.edges = maps.Clone(s.itemsByID), maps.Clone(s.states), maps.Clone(s.edges)
	s.waits, s.trunkWaiters, s.defectWaiters = maps.Clone(s.waits), maps.Clone(s.trunkWaiters), maps.Clone(s.defectWaiters)
	s.conflicts, s.contentVersions = maps.Clone(s.conflicts), maps.Clone(s.contentVersions)
	s.requests = maps.Clone(s.requests)
	var hasRequests bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='request_words')`).Scan(&hasRequests); err != nil {
		return err
	}
	// Folding the conflict events is enough; there is no conflict projection table.
	for _, e := range d.items {
		if e.Kind == item.ConflictKind {
			var c item.Conflict
			if err := json.Unmarshal(e.Payload, &c); err != nil {
				return err
			}
			s.conflicts[c.ItemID] = c
		}
	}
	var changedBlockers []string
	for _, e := range d.items {
		id := *e.ObjectID
		oldState, oldEdges := s.states[id], s.edges[id]
		if touched[id] {
			continue
		}
		touched[id] = true
		row, found, err := readItemByID(r, id)
		if err != nil {
			return err
		}
		if !found {
			delete(s.itemsByID, id)
			delete(s.states, id)
			delete(s.edges, id)
			delete(s.contentVersions, id)
			delete(s.conflicts, id)
			delete(s.requests, id)
		} else {
			if hasRequests && !s.requests[id] {
				var request bool
				if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM request_words WHERE item_id=?)`, id).Scan(&request); err != nil {
					return err
				}
				if request {
					s.requests[id] = true
				}
			}
			s.itemsByID[id], s.states[id] = row, row.State
			s.edges[id] = nil
			err = readContentRows(r, `SELECT blocked_by FROM item_edges WHERE item_id=? ORDER BY blocked_by`, []any{id}, func(rows *sql.Rows) error {
				var blocker string
				if err := rows.Scan(&blocker); err != nil {
					return err
				}
				s.edges[id] = append(s.edges[id], blocker)
				return nil
			})
			if err != nil {
				return err
			}
		}
		if oldState != s.states[id] || !slices.Equal(oldEdges, s.edges[id]) {
			changedBlockers = append(changedBlockers, id)
		}
		if err := s.refreshWait(r, id, row.State); err != nil {
			return err
		}
	}
	// Versions include the blockers' states, not just this item's own fields.
	for _, id := range changedBlockers {
		for _, dependent := range s.reverseEdges[id] {
			touched[dependent] = true
		}
	}
	for id := range touched {
		if _, ok := s.itemsByID[id]; ok {
			version, err := readItemContentVersion(r, s.versionSchema, id)
			if err != nil {
				return err
			}
			s.contentVersions[id] = version
		}
	}
	return nil
}

func (d startStoreDelta) touchEventItems(prev, next startSnapshot, touched map[string]bool) {
	for _, e := range d.relevant {
		if e.ObjectID != nil {
			switch e.ObjectType {
			case "item":
				touched[*e.ObjectID] = true
			case "run":
				for _, s := range []startSnapshot{prev, next} {
					if w := s.pending.byRun[*e.ObjectID]; w != nil {
						touched[w.Decision.ItemID] = true
					}
				}
			}
		}
		var payload struct {
			ItemID string `json:"item_id"`
		}
		if json.Unmarshal(e.Payload, &payload) == nil && payload.ItemID != "" {
			touched[payload.ItemID] = true
		}
	}
}

func (s *startSnapshot) refreshWait(r *store.Reader, id, state string) error {
	delete(s.waits, id)
	delete(s.trunkWaiters, id)
	delete(s.defectWaiters, id)
	if state != s.def.Blocked {
		return nil
	}
	wait, _, err := item.ReadWaitingOn(r, id)
	if err != nil || wait == nil {
		return err
	}
	s.waits[id] = *wait
	if blocker, ok := strings.CutPrefix(wait.Waiter, "item:"); ok && s.def.Completes == "landing" {
		switch {
		case IsTrunkFixer(blocker):
			s.trunkWaiters[id] = blocker
		case IsDefectItem(blocker):
			s.defectWaiters[id] = blocker
		}
	}
	return nil
}

func (s *startSnapshot) rebuildReverseEdges() {
	s.reverseEdges = map[string][]string{}
	for _, row := range s.items {
		for _, blocker := range s.edges[row.id] {
			s.reverseEdges[blocker] = append(s.reverseEdges[blocker], row.id)
		}
	}
}

func (s *startSnapshot) refreshAttempt(r *store.Reader, id string) error {
	if _, exists := s.itemsByID[id]; !exists {
		delete(s.attempts, id)
		delete(s.nodeCounts, id)
		return nil
	}
	attempt, count, err := readAttemptInputs(r, s.hasNodes, id, s.priors[id], s.pending.byRun)
	if err != nil {
		return err
	}
	s.attempts[id], s.nodeCounts[id] = attempt, count
	return nil
}
