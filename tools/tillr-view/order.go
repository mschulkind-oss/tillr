package main

import (
	"encoding/json"
	"sort"
	"strconv"
)

// inOrder follows the place command: parked and finished items have no place.
func inOrder(state string) bool {
	return state != "not-started" && state != "landed" && state != "closed" && state != "resolved-elsewhere"
}

// itemPlaces counts the full order before any face filters it. Empty keys sort
// after keyed items by id; sync later supplies keys without changing these places.
func itemPlaces(items []storeItem) map[string]int {
	ordered := append([]storeItem(nil), items...)
	sort.Slice(ordered, func(i, j int) bool {
		return rankBefore(ordered[i].Rank, ordered[i].ItemID, ordered[j].Rank, ordered[j].ItemID)
	})
	places := map[string]int{}
	for _, it := range ordered {
		if inOrder(it.State) {
			places[it.ItemID] = len(places) + 1
		}
	}
	return places
}

func placeText(place int) string {
	if place == 0 {
		return "—"
	}
	return strconv.Itoa(place)
}

// The stream decoder still accepts legacy item records with rank. Presentation
// hides that stored key; only records written by item writers carry it.
func (v itemRecord) MarshalJSON() ([]byte, error) {
	type fields itemRecord
	return json.Marshal(struct {
		fields
		Rank *string `json:"rank,omitempty"`
	}{fields: fields(v)})
}
