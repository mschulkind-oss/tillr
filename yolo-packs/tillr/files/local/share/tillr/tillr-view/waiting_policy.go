package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The snapshot is written atomically by drain at a pass boundary. Reading it
// never validates or activates a different policy on behalf of the loop.
func policyWaitingRows(root string, rows []waitingRow) ([]waitingRow, error) {
	path := filepath.Join(root, ".tillr", "loop", "backend-policy.json")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return rows, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading policy in force: %w", err)
	}
	var state struct {
		Refusal string `json:"refusal"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("reading policy in force: %w", err)
	}
	if state.Refusal != "" {
		rows = append(rows, waitingRow{ItemID: "backend-policy", Class: "needs you", Where: "policy", Why: "policy on disk is not the policy in force: " + strings.Join(strings.Fields(state.Refusal), " "), Mover: "the owner", ClassSource: "rule"})
	}
	return rows, nil
}
