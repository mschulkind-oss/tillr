package item

import (
	"sort"

	"github.com/mschulkind-oss/tillr/store"
)

// SyncPlacement reports a queue position after this sync, never its stored key.
type SyncPlacement struct {
	ItemID string `json:"item_id"`
	Place  int    `json:"place"`
	From   string `json:"from,omitempty"`
}

func placementReport(places map[string]int, ids map[string]string) []SyncPlacement {
	result := []SyncPlacement{}
	for id, from := range ids {
		if p := places[id]; p != 0 {
			result = append(result, SyncPlacement{id, p, from})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Place < result[j].Place })
	return result
}

// previewPlacements uses the planned definitions and the store's current order.
// It models positions, not keys, so a dry run never opens a writer or leaves an event.
func previewPlacements(r *store.Reader, plan syncPlan) ([]SyncPlacement, error) {
	all := []orderedItem{}
	siblings := map[string]string{}
	exists := false
	if r != nil {
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='items')`).Scan(&exists); err != nil {
			return nil, err
		}
	}
	if exists {
		rows, err := r.Query(`SELECT item_id,rank,state FROM items ORDER BY CASE WHEN rank='' THEN 1 ELSE 0 END,rank,item_id`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var v orderedItem
			if err := rows.Scan(&v.id, &v.rank, &v.state); err != nil {
				rows.Close()
				return nil, err
			}
			all = append(all, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		rows, err = r.Query(`SELECT object_id,json_extract(payload,'$.placed_from') FROM events WHERE kind=? AND json_extract(payload,'$.placed_from') IS NOT NULL ORDER BY seq`, UpdateKind)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, from string
			if err := rows.Scan(&id, &from); err != nil {
				rows.Close()
				return nil, err
			}
			siblings[id] = from
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	placed := map[string]string{}
	available := map[string]bool{}
	newIDs := map[string]bool{}
	keyed := map[string]bool{}
	order := []string{}
	parking := map[string]bool{}
	for _, id := range plan.report.Parked {
		parking[id] = true
	}
	for _, v := range all {
		if v.rank != "" {
			order = append(order, v.id)
			keyed[v.id] = true
			available[v.id] = true
		}
	}
	for _, v := range all {
		if InOrder(v.state) && v.rank == "" && !parking[v.id] && (plan.allowed == nil || plan.allowed[v.id]) {
			order = append(order, v.id)
			placed[v.id] = ""
			keyed[v.id] = true
			available[v.id] = true
		}
	}
	writes, err := previewAcceptedWrites(r, plan.writes, all)
	if err != nil {
		return nil, err
	}
	if err := previewScopedRanks(plan, all, writes, siblings); err != nil {
		return nil, err
	}
	payloads := map[string]itemPayload{}
	for _, fw := range writes {
		for _, p := range fw.payloads {
			payloads[p.ItemID] = p
			newIDs[p.ItemID] = fw.created[p.ItemID]
		}
	}
	// Parking clears the old key, so it cannot remain a source for a new
	// item's from mark. Unparking appends a new key, even for legacy keyed
	// parked rows. Remove both transitions' old positions before appending.
	moves := append(append([]string{}, plan.report.Parked...), plan.report.Unparked...)
	for _, id := range moves {
		if _, accepted := payloads[id]; !accepted {
			continue
		}
		delete(keyed, id)
		delete(available, id)
		delete(placed, id)
		rest := []string{}
		for _, v := range order {
			if v != id {
				rest = append(rest, v)
			}
		}
		order = rest
	}
	for _, p := range plan.placements {
		desired, accepted := payloads[p.id]
		if !accepted || !InOrder(desired.State) || keyed[p.id] {
			continue
		}
		order = append(order, p.id)
		keyed[p.id] = true
		placed[p.id] = ""
	}
	// Definitions may unpark a keyed item, so membership is computed at the end.
	states := map[string]string{}
	for _, v := range all {
		states[v.id] = v.state
	}
	for id, p := range payloads {
		states[id] = p.State
	}
	move := func(id string, posFor func([]string) int) {
		rest := []string{}
		for _, v := range order {
			if v != id {
				rest = append(rest, v)
			}
		}
		pos := posFor(rest)
		order = append(append(append([]string{}, rest[:pos]...), id), rest[pos:]...)
	}
	recorded := map[string]bool{}
	for i, id := range plan.orderRecord {
		if !newIDs[id] || !keyed[id] {
			continue
		}
		move(id, func(rest []string) int {
			index := map[string]int{}
			for j, v := range rest {
				if available[v] && InOrder(states[v]) {
					index[v] = j
				}
			}
			for j := i - 1; j >= 0; j-- {
				if p, ok := index[plan.orderRecord[j]]; ok {
					return p + 1
				}
			}
			for j := i + 1; j < len(plan.orderRecord); j++ {
				if p, ok := index[plan.orderRecord[j]]; ok {
					return p
				}
			}
			return len(rest)
		})
		recorded[id] = true
		available[id] = true
	}
	if len(plan.orderRecord) != 0 {
		for _, p := range plan.placements {
			if newIDs[p.id] && keyed[p.id] && !recorded[p.id] {
				move(p.id, func(rest []string) int { return len(rest) })
			}
		}
	}
	for _, p := range plan.placements {
		if !newIDs[p.id] || !InOrder(states[p.id]) || recorded[p.id] || p.from == "" || p.from == p.id || !keyed[p.from] {
			continue
		}
		move(p.id, func(rest []string) int {
			pos := 0
			for i, v := range rest {
				if v == p.from {
					pos = i + 1
					break
				}
			}
			for i := pos; i < len(rest); i++ {
				if siblings[rest[i]] == p.from {
					pos = i + 1
				}
			}
			return pos
		})
		siblings[p.id] = p.from
		placed[p.id] = p.from
	}
	places := map[string]int{}
	for _, id := range order {
		if InOrder(states[id]) {
			places[id] = len(places) + 1
		}
	}
	return placementReport(places, placed), nil
}

// previewAcceptedWrites models the edge rules and file savepoints that Apply
// enforces. A refused file must not occupy a hypothetical place or shift the
// numbers reported for successful files. Definitions have already been checked
// by planSync; edge existence and cycles depend on preceding successful writes.
func previewAcceptedWrites(r *store.Reader, writes []fileWrites, held []orderedItem) ([]fileWrites, error) {
	graph := map[string][]string{}
	for _, v := range held {
		graph[v.id] = nil
	}
	if len(held) != 0 {
		rows, err := r.Query(`SELECT item_id,blocked_by FROM item_edges`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, blocker string
			if err := rows.Scan(&id, &blocker); err != nil {
				rows.Close()
				return nil, err
			}
			graph[id] = append(graph[id], blocker)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	reaches := func(g map[string][]string, start, target string) bool {
		pending := []string{start}
		seen := map[string]bool{}
		for len(pending) != 0 {
			id := pending[0]
			pending = pending[1:]
			if id == target {
				return true
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			pending = append(pending, g[id]...)
		}
		return false
	}
	accepted := []fileWrites{}
	for _, fw := range writes {
		next := map[string][]string{}
		for id, edges := range graph {
			next[id] = edges
		}
		valid := true
		for _, p := range fw.payloads {
			next[p.ItemID] = nil
			for _, blocker := range p.BlockedBy {
				if _, exists := next[blocker]; !exists || reaches(next, blocker, p.ItemID) {
					valid = false
					break
				}
				next[p.ItemID] = append(next[p.ItemID], blocker)
			}
			if !valid {
				break
			}
		}
		if valid {
			graph = next
			accepted = append(accepted, fw)
		}
	}
	return accepted, nil
}
