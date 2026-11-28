package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/tillr-drain/quota"
)

type skippedProfileQuota struct {
	Profile         string              `json:"profile"`
	Backend         string              `json:"backend"`
	Windows         []quota.QuotaWindow `json:"windows,omitempty"`
	Reserve         float64             `json:"reserve"`
	ReopenThreshold float64             `json:"reopen_threshold,omitempty"`
	ResetAt         *time.Time          `json:"reset_at,omitempty"`
}

type profileSummary struct {
	Profile         string                `json:"profile,omitempty"`
	Profiles        []string              `json:"profiles,omitempty"`
	ProfileIndex    int                   `json:"profile_index"`
	Source          string                `json:"source,omitempty"`
	Why             string                `json:"why,omitempty"`
	SwitchedFrom    string                `json:"switched_from,omitempty"`
	SwitchedAt      *time.Time            `json:"switched_at,omitempty"`
	SkippedProfiles []skippedProfileQuota `json:"skipped_profiles,omitempty"`
}

func readProfileState(root string) (*profileSummary, map[string]bool, error) {
	if root == "" {
		return nil, nil, nil
	}
	path := filepath.Join(root, ".tillr", "loop", "backend-policy.json")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var state struct {
		Profile         string                `json:"profile,omitempty"`
		Profiles        []string              `json:"profiles,omitempty"`
		ProfileIndex    int                   `json:"profile_index"`
		Source          string                `json:"source,omitempty"`
		Why             string                `json:"why,omitempty"`
		SwitchedFrom    string                `json:"switched_from,omitempty"`
		SwitchedAt      *time.Time            `json:"switched_at,omitempty"`
		SkippedProfiles []skippedProfileQuota `json:"skipped_profiles,omitempty"`
		Content         string                `json:"content"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, nil, err
	}
	var backends map[string]bool
	if state.Content != "" {
		var pol struct {
			Backends map[string]json.RawMessage `json:"backends"`
		}
		if err := json.Unmarshal([]byte(state.Content), &pol); err == nil && len(pol.Backends) > 0 {
			backends = make(map[string]bool, len(pol.Backends))
			for name := range pol.Backends {
				backends[name] = true
			}
		}
	}
	if state.Profile == "" && len(state.Profiles) == 0 && state.Source == "" {
		return nil, backends, nil
	}
	return &profileSummary{
		Profile:         state.Profile,
		Profiles:        state.Profiles,
		ProfileIndex:    state.ProfileIndex,
		Source:          state.Source,
		Why:             state.Why,
		SwitchedFrom:    state.SwitchedFrom,
		SwitchedAt:      state.SwitchedAt,
		SkippedProfiles: state.SkippedProfiles,
	}, backends, nil
}
