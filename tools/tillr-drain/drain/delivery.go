package drain

import "fmt"

// deliveryDecision describes the planner's delivery target independently of path hints.
type deliveryDecision struct {
	Kind string `json:"kind"`
	Fork string `json:"fork,omitempty"`
}

// pieces returns delivery targets in landing order. An empty target denotes the repository.
func (d deliveryDecision) pieces(known map[string]bool) ([]string, error) {
	switch d.Kind {
	case "repo":
		if d.Fork != "" {
			return nil, fmt.Errorf("repository delivery cannot name a fork")
		}
		return []string{""}, nil
	case "fork", "both":
		if d.Fork == "" || !known[d.Fork] {
			return nil, fmt.Errorf("unknown delivery fork %q", d.Fork)
		}
		if d.Kind == "both" {
			return []string{d.Fork, ""}, nil
		}
		return []string{d.Fork}, nil
	default:
		return nil, fmt.Errorf("invalid delivery kind %q", d.Kind)
	}
}
