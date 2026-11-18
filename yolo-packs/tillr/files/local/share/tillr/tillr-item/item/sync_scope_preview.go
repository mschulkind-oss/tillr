package item

import "sort"

// previewScopedRanks models the same rank allocation and placement order as the
// writer, using insertionKeys for crowded gaps. Only partial previews need the
// unread-item refusal; ordinary previews keep their existing position-only model.
func previewScopedRanks(plan syncPlan, held []orderedItem, writes []fileWrites, siblings map[string]string) error {
	if plan.allowed == nil {
		return nil
	}
	copied := map[string]string{}
	for id, from := range siblings {
		copied[id] = from
	}
	siblings = copied
	all := map[string]orderedItem{}
	protected := map[string]string{}
	created := map[string]bool{}
	last := ""
	missing := []string{}
	parking := map[string]bool{}
	for _, id := range plan.report.Parked {
		parking[id] = true
	}
	for _, v := range held {
		all[v.id] = v
		if v.rank > last {
			last = v.rank
		}
		if !plan.allowed[v.id] {
			protected[v.id] = v.rank
		}
		if InOrder(v.state) && v.rank == "" && !parking[v.id] && plan.allowed[v.id] {
			missing = append(missing, v.id)
		}
	}
	keys := appendKeys(last, len(missing))
	for i, id := range missing {
		v := all[id]
		v.rank = keys[i]
		all[id] = v
		last = keys[i]
	}
	needs := map[string]bool{}
	for _, fw := range plan.writes {
		for _, p := range fw.payloads {
			needs[p.ItemID] = p.Rank == "" && InOrder(p.State)
		}
	}
	appended := []string{}
	for _, p := range plan.placements {
		if needs[p.id] {
			appended = append(appended, p.id)
		}
	}
	keys = appendKeys(last, len(appended))
	appendRank := map[string]string{}
	for i, id := range appended {
		appendRank[id] = keys[i]
	}
	for _, fw := range writes {
		for _, p := range fw.payloads {
			rank := p.Rank
			if rank == "" && InOrder(p.State) {
				rank = all[p.ItemID].rank
				if rank == "" {
					rank = appendRank[p.ItemID]
				}
			}
			all[p.ItemID] = orderedItem{p.ItemID, rank, p.State}
			created[p.ItemID] = fw.created[p.ItemID]
		}
	}
	ordered := func(id string, source bool) []orderedItem {
		rest := []orderedItem{}
		for _, v := range all {
			if v.id == id {
				continue
			}
			if InOrder(v.state) || (source && v.rank != "") {
				rest = append(rest, v)
			}
		}
		sort.Slice(rest, func(i, j int) bool {
			a, b := rest[i], rest[j]
			if (a.rank == "") != (b.rank == "") {
				return a.rank != ""
			}
			if a.rank != b.rank {
				return a.rank < b.rank
			}
			return a.id < b.id
		})
		return rest
	}
	insert := func(id string, rest []orderedItem, pos int) error {
		key, keys := insertionKeys(rest, pos)
		if keys == nil {
			v := all[id]
			v.rank = key
			all[id] = v
			return nil
		}
		final := append(append(append([]orderedItem{}, rest[:pos]...), all[id]), rest[pos:]...)
		for i, v := range final {
			if before, protected := protected[v.id]; protected && before != keys[i] {
				return unreadRankRefusal(v.id)
			}
			v.rank = keys[i]
			all[v.id] = v
		}
		return nil
	}
	available := map[string]bool{}
	for id, v := range all {
		if !created[id] && InOrder(v.state) {
			available[id] = true
		}
	}
	recorded := map[string]bool{}
	for i, id := range plan.orderRecord {
		if !created[id] {
			continue
		}
		if InOrder(all[id].state) {
			rest := ordered(id, false)
			index := map[string]int{}
			for j, v := range rest {
				if available[v.id] {
					index[v.id] = j
				}
			}
			pos := len(rest)
			anchored := false
			for j := i - 1; j >= 0; j-- {
				if p, ok := index[plan.orderRecord[j]]; ok {
					pos = p + 1
					anchored = true
					break
				}
			}
			if !anchored {
				for j := i + 1; j < len(plan.orderRecord); j++ {
					if p, ok := index[plan.orderRecord[j]]; ok {
						pos = p
						break
					}
				}
			}
			if err := insert(id, rest, pos); err != nil {
				return err
			}
		}
		recorded[id], available[id] = true, true
	}
	if len(plan.orderRecord) > 0 {
		for _, p := range plan.placements {
			if created[p.id] && !recorded[p.id] && InOrder(all[p.id].state) {
				rest := ordered(p.id, false)
				// Place is a no-op when this item is already last.
				alreadyLast := len(rest) == 0 || all[p.id].rank > rest[len(rest)-1].rank && rest[len(rest)-1].rank != ""
				if !alreadyLast {
					if err := insert(p.id, rest, len(rest)); err != nil {
						return err
					}
				}
			}
		}
	}
	for _, p := range plan.placements {
		if !created[p.id] || !InOrder(all[p.id].state) || recorded[p.id] || p.from == "" || p.from == p.id || all[p.from].rank == "" {
			continue
		}
		rest := ordered(p.id, true)
		pos := -1
		for i, v := range rest {
			if v.id == p.from {
				pos = i + 1
				break
			}
		}
		if pos < 0 {
			continue
		}
		for i := pos; i < len(rest); i++ {
			if rest[i].rank != "" && siblings[rest[i].id] == p.from {
				pos = i + 1
			}
		}
		if err := insert(p.id, rest, pos); err != nil {
			return err
		}
		siblings[p.id] = p.from
	}
	return nil
}
