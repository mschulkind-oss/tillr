package item

import "strings"

// orderItems puts dependencies before the items they block.
func orderItems(items []itemPayload) ([]itemPayload, error) {
	index := make(map[string]int, len(items))
	for i, it := range items {
		index[it.ItemID] = i
	}
	placed := make([]bool, len(items))
	ordered := make([]itemPayload, 0, len(items))
	for len(ordered) < len(items) {
		progressed := false
		for i, it := range items {
			if placed[i] || !blockersPlaced(it, index, placed) {
				continue
			}
			placed[i] = true
			ordered = append(ordered, it)
			progressed = true
			break
		}
		if progressed {
			continue
		}
		stuck := make([]string, 0, len(items)-len(ordered))
		for i, it := range items {
			if !placed[i] {
				stuck = append(stuck, it.ItemID)
			}
		}
		return nil, Rejectionf("edge-cycle", stuck[0],
			"items %s are blocked by each other, so none of them can be written first",
			strings.Join(stuck, ", "))
	}
	return ordered, nil
}

// blockersPlaced reports whether every blocker this entry names that is also an entry of
// the same document has already been written. A blocker that is not in the document is
// the store's to answer: Apply refuses it as edge-target-missing if no such item exists.
func blockersPlaced(it itemPayload, index map[string]int, placed []bool) bool {
	for _, blocker := range it.BlockedBy {
		if j, inDocument := index[blocker]; inDocument && !placed[j] {
			return false
		}
	}
	return true
}
