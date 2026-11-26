package policy

import "encoding/json"

// MarshalJSON retains lists, shares, and pick when a policy is saved or hashed.
func (p Policy) MarshalJSON() ([]byte, error) {
	type plain Policy
	seats := map[string]any{}
	for _, seat := range p.SeatNames() {
		list := p.SeatList(seat)
		var value any = p.Seats[seat]
		if _, ok := p.SeatLists[seat]; ok {
			value = list
		}
		if shares, ok := p.SeatShares[seat]; ok {
			entries := make([]any, 0, len(list))
			for i, name := range list {
				entry := map[string]any{"backend": name}
				if i < len(shares) {
					entry["share"] = shares[i]
				}
				entries = append(entries, entry)
			}
			value = entries
		}
		if pick := p.SeatPicks[seat]; pick != "" {
			value = map[string]any{"backends": value, "pick": pick}
		}
		seats[seat] = value
	}
	return json.Marshal(struct {
		plain
		Seats map[string]any `json:"seats,omitempty"`
	}{plain(p), seats})
}
