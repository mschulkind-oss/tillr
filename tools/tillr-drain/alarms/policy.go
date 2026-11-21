package alarms

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ReadPolicy reads the loop's accepted policy, not an unaccepted edit on disk.
// A caller can name an explicit policy, as on drain why, when there is no matching
// snapshot. Absence of both leaves only recorded facts to report.
func ReadPolicy(root, ref string) (*Policy, error) {
	snapshot := filepath.Join(root, ".tillr", "loop", "backend-policy.json")
	raw, err := os.ReadFile(snapshot)
	var state struct {
		Ref     string    `json:"ref"`
		Content string    `json:"content"`
		Hash    string    `json:"hash"`
		At      time.Time `json:"at"`
	}
	if err == nil {
		if err := json.Unmarshal(raw, &state); err != nil {
			return nil, fmt.Errorf("reading %s: %w", snapshot, err)
		}
		if fmt.Sprintf("%x", sha256.Sum256([]byte(state.Content))) != state.Hash {
			return nil, fmt.Errorf("policy content in %s does not match its hash", snapshot)
		}
		if ref == "" {
			return decodePolicy([]byte(state.Content), state.At)
		}
		a, err := filepath.Abs(ref)
		if err != nil {
			return nil, err
		}
		b, err := filepath.Abs(state.Ref)
		if err != nil {
			return nil, err
		}
		if a == b {
			return decodePolicy([]byte(state.Content), state.At)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if ref == "" {
		return nil, nil
	}
	raw, err = os.ReadFile(ref)
	if err != nil {
		return nil, err
	}
	return decodePolicy(raw, time.Time{})
}

func decodePolicy(raw []byte, at time.Time) (*Policy, error) {
	p := Policy{Since: at}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("reading quota policy: %w", err)
	}
	return &p, nil
}
