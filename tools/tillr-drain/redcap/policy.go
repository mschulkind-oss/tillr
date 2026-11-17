package redcap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// GatePolicy is the repository's gate settings. Loading them is separate from
// executing a landing strategy; subsequent gate slices consume these values.
type GatePolicy struct {
	LandingStrategy string
	AutoRevert      bool
	BudgetMS        int64
	RedCap          struct{ Landings, Minutes int64 }
	LaneBase        string
}

// ReadGatePolicy reads the fixed repository path, defaulting only absent files
// and keys. An explicit null is a value, not an absent key, and is refused.
func ReadGatePolicy(root string) (policy GatePolicy, absent bool, err error) {
	policy.LandingStrategy = "before-merge"
	policy.BudgetMS = 10000
	policy.RedCap.Landings, policy.RedCap.Minutes = 20, 60
	policy.LaneBase = "green"
	path := filepath.Join(root, "config", "tillr", "gate-policy.json")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return policy, true, nil
	}
	if err != nil {
		return policy, false, fmt.Errorf("reading gate policy %s: %v", path, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return policy, false, fmt.Errorf("gate policy %s must be a JSON object: %s (%v)", path, raw, err)
	}
	invalid := func(key string, value json.RawMessage, expected string) error {
		return fmt.Errorf("gate policy %s: %s=%s; want %s", path, key, value, expected)
	}
	for _, field := range []struct {
		key           string
		target        *string
		first, second string
	}{
		{"landing_strategy", &policy.LandingStrategy, "before-merge", "after-merge"},
		{"lane_base", &policy.LaneBase, "green", "tip"},
	} {
		if value, ok := fields[field.key]; ok {
			if json.Unmarshal(value, field.target) != nil || (*field.target != field.first && *field.target != field.second) || string(value) == "null" {
				return policy, false, invalid(field.key, value, field.first+" or "+field.second)
			}
		}
	}
	if value, ok := fields["auto_revert"]; ok {
		if json.Unmarshal(value, &policy.AutoRevert) != nil || string(value) == "null" {
			return policy, false, invalid("auto_revert", value, "a boolean")
		}
	}
	integer := func(fields map[string]json.RawMessage, key, name string, target *int64, minimum int64) error {
		if value, ok := fields[key]; ok {
			if json.Unmarshal(value, target) != nil || string(value) == "null" || *target < minimum {
				expected := "a non-negative integer"
				if minimum == 1 {
					expected = "a positive integer"
				}
				return invalid(name, value, expected)
			}
		}
		return nil
	}
	if err := integer(fields, "budget_ms", "budget_ms", &policy.BudgetMS, 1); err != nil {
		return policy, false, err
	}
	if value, ok := fields["red_cap"]; ok {
		var capFields map[string]json.RawMessage
		if json.Unmarshal(value, &capFields) != nil || capFields == nil {
			return policy, false, invalid("red_cap", value, "an object with landings and minutes")
		}
		if err := integer(capFields, "landings", "red_cap.landings", &policy.RedCap.Landings, 0); err != nil {
			return policy, false, err
		}
		if err := integer(capFields, "minutes", "red_cap.minutes", &policy.RedCap.Minutes, 0); err != nil {
			return policy, false, err
		}
	}
	return policy, false, nil
}

// StartupLine prints all five effective settings, including the safe absent-file
// defaults. Zero disables each cap bound; both zero means no cap.
func (p GatePolicy) StartupLine(absent bool) string {
	cap := fmt.Sprintf("%d landings/%d minutes", p.RedCap.Landings, p.RedCap.Minutes)
	if p.RedCap.Landings == 0 && p.RedCap.Minutes == 0 {
		cap = "no cap"
	}
	line := fmt.Sprintf("loop: gate policy landing_strategy=%s auto_revert=%t budget_ms=%d red_cap=%s lane_base=%s", p.LandingStrategy, p.AutoRevert, p.BudgetMS, cap, p.LaneBase)
	if absent {
		line += " (file absent)"
	}
	return line
}
