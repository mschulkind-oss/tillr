package policy

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
)

// StartOption records one resolved loop start option and where it came from.
type StartOption struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Source string `json:"source"`
	Detail string `json:"detail,omitempty"`
}

// PolicyState is the last policy accepted by a loop, not a fresh interpretation
// of the file on disk. Content retains seat lists in their original JSON shape.
type PolicyState struct {
	Ref          string        `json:"ref"`
	Source       string        `json:"source,omitempty"`
	Profile      string        `json:"profile,omitempty"`
	Profiles     []string      `json:"profiles,omitempty"`
	ProfileIndex int           `json:"profile_index"`
	Hash         string        `json:"hash"`
	DiskHash     string        `json:"disk_hash"`
	Content      string        `json:"content"`
	Refusal      string        `json:"refusal,omitempty"`
	StartOptions []StartOption `json:"start_options,omitempty"`
	At           time.Time     `json:"at"`
}

type PolicyChange struct {
	At          time.Time `json:"at"`
	OldHash     string    `json:"old_hash"`
	NewHash     string    `json:"new_hash"`
	InForceHash string    `json:"in_force_hash"`
	Refusal     string    `json:"refusal,omitempty"`
}

func PolicyStateFile(root string) string {
	return filepath.Join(drainmodel.LoopDir(root), "backend-policy.json")
}

func ReadPolicyState(root string) (PolicyState, error) {
	var state PolicyState
	raw, err := os.ReadFile(PolicyStateFile(root))
	if err != nil {
		return state, drainmodel.Usagef("reading the policy in force: %v", err)
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return state, drainmodel.Usagef("reading the policy in force: %v", err)
	}
	if ContentHash([]byte(state.Content)) != state.Hash {
		return state, drainmodel.Usagef("the recorded policy content does not match its hash")
	}
	if _, err := DecodePolicy(state.Ref, []byte(state.Content)); err != nil {
		return state, err
	}
	return state, nil
}

func ContentHash(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }

func ReadPolicyContent(ref string) (*Policy, []byte, string, error) {
	raw, err := os.ReadFile(ref)
	if err != nil {
		return nil, nil, "", drainmodel.Usagef("reading the policy %s: %v", ref, err)
	}
	p, err := DecodePolicy(ref, raw)
	return p, raw, ContentHash(raw), err
}

// EffectivePolicy uses the loop's accepted snapshot when it names this file.
// Without a loop snapshot, a standalone read still checks its explicit input.
func EffectivePolicy(root, ref string) (*Policy, error) {
	withRoot := func(p *Policy, err error) (*Policy, error) {
		if err == nil && p != nil && root != "" {
			p.Root = root
		}
		return p, err
	}
	state, err := ReadPolicyState(root)
	if err != nil {
		if _, statErr := os.Stat(PolicyStateFile(root)); os.IsNotExist(statErr) {
			return withRoot(LoadPolicy(ref))
		}
		return nil, err
	}
	if ref != "" {
		a, e := filepath.Abs(ref)
		if e != nil {
			return nil, e
		}
		b, e := filepath.Abs(state.Ref)
		if e != nil {
			return nil, e
		}
		if a != b {
			return withRoot(LoadPolicy(ref))
		}
	}
	return withRoot(DecodePolicy(state.Ref, []byte(state.Content)))
}

// RefreshPolicy reloads policyRef on the pass goroutine. A request already handed
// to a worker is immutable; replacing the policy pointer cannot rewrite that request.
func RefreshPolicy(root, policyRef string, now func() time.Time, mu *sync.RWMutex, active **Policy, state *PolicyState, recorded *bool, reportf func(string, ...any)) error {
	if policyRef == "" {
		return nil
	}
	p, raw, hash, err := ReadPolicyContent(policyRef)
	refusal := ""
	if err != nil {
		refusal = err.Error()
	}
	old := *state
	if *recorded && old.DiskHash == hash && old.Refusal == refusal {
		return nil
	}
	next := old
	next.Ref = policyRef
	next.DiskHash, next.Refusal, next.At = hash, refusal, now().UTC()
	if err == nil {
		p.Root = root
		next.Hash, next.Content = hash, string(raw)
	}
	change := PolicyChange{At: next.At, OldHash: old.DiskHash, NewHash: hash, InForceHash: next.Hash, Refusal: refusal}
	if err := os.MkdirAll(drainmodel.LoopDir(root), 0o755); err != nil {
		return drainmodel.Externalf("recording policy: %v", err)
	}
	history, err := os.OpenFile(filepath.Join(drainmodel.LoopDir(root), "backend-policy-changes.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return drainmodel.Externalf("recording policy change: %v", err)
	}
	encodeErr := json.NewEncoder(history).Encode(change)
	closeErr := history.Close()
	if encodeErr != nil {
		return drainmodel.Externalf("recording policy change: %v", encodeErr)
	}
	if closeErr != nil {
		return drainmodel.Externalf("recording policy change: %v", closeErr)
	}
	body, err := json.Marshal(next)
	if err != nil {
		return drainmodel.Internalf("encoding policy in force: %v", err)
	}
	path := PolicyStateFile(root)
	if err := os.WriteFile(path+".new", body, 0o644); err != nil {
		return drainmodel.Externalf("recording policy in force: %v", err)
	}
	if err := os.Rename(path+".new", path); err != nil {
		return drainmodel.Externalf("recording policy in force: %v", err)
	}
	*state, *recorded = next, true
	if refusal != "" {
		reportf("loop: policy change %s -> %s refused: %s\n", old.DiskHash, hash, refusal)
	} else {
		mu.Lock()
		*active = p
		mu.Unlock()
		reportf("loop: policy change %s -> %s accepted\n", old.DiskHash, hash)
	}
	mu.RLock()
	cur := *active
	mu.RUnlock()
	reportf("loop: policy in force %s: %s\n", next.Hash, cur.MappingLine())
	return nil
}

func (p Policy) MappingLine() string {
	parts := []string{"default=" + p.DefaultBackend}
	for _, seat := range p.SeatNames() {
		names := append([]string(nil), p.SeatList(seat)...)
		for i, share := range p.SeatShares[seat] {
			if i < len(names) {
				names[i] += fmt.Sprintf(":%g", share)
			}
		}
		mapping := seat + "=" + strings.Join(names, ",")
		if pick := p.SeatPicks[seat]; pick != "" {
			mapping += " pick=" + pick
		}
		parts = append(parts, mapping)
	}
	return strings.Join(parts, "; ")
}

// Description includes ordered seats, all backend model/effort values and overrides.
func (p Policy) Description() string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s\n", p.MappingLine())
	for _, name := range BackendNames(p.Backends) {
		b := p.Backends[name]
		models := b.Models
		if len(models) == 0 {
			models = []string{b.Model}
		}
		effort := b.Effort
		if effort == "" {
			effort = "(unspecified)"
		}
		fmt.Fprintf(&out, "backend %s: models=%s effort=%s\n", name, strings.Join(models, ","), effort)
	}
	ids := make([]string, 0, len(p.Overrides))
	for id := range p.Overrides {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Fprintf(&out, "override %s=%s\n", id, p.Overrides[id])
	}
	return out.String()
}

// AllPolicies returns the ordered list of profile policies with active
// overriding the entry at activeIdx, or a single-element slice holding active
// when no profile list is configured.
func AllPolicies(active *Policy, profiles []*Policy, activeIdx int) ([]*Policy, int) {
	if len(profiles) == 0 {
		if active == nil {
			return nil, 0
		}
		return []*Policy{active}, 0
	}
	out := make([]*Policy, len(profiles))
	copy(out, profiles)
	if activeIdx < 0 || activeIdx >= len(out) {
		activeIdx = 0
	}
	if active != nil {
		out[activeIdx] = active
	}
	return out, activeIdx
}
