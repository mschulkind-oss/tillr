package drain

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PolicyState is the last policy accepted by a loop, not a fresh interpretation
// of the file on disk. Content retains seat lists in their original JSON shape.
type PolicyState struct {
	Ref      string    `json:"ref"`
	Hash     string    `json:"hash"`
	DiskHash string    `json:"disk_hash"`
	Content  string    `json:"content"`
	Refusal  string    `json:"refusal,omitempty"`
	At       time.Time `json:"at"`
}

type policyChange struct {
	At          time.Time `json:"at"`
	OldHash     string    `json:"old_hash"`
	NewHash     string    `json:"new_hash"`
	InForceHash string    `json:"in_force_hash"`
	Refusal     string    `json:"refusal,omitempty"`
}

func PolicyStateFile(root string) string { return filepath.Join(LoopDir(root), "backend-policy.json") }

func ReadPolicyState(root string) (PolicyState, error) {
	var state PolicyState
	raw, err := os.ReadFile(PolicyStateFile(root))
	if err != nil {
		return state, Usagef("reading the policy in force: %v", err)
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return state, Usagef("reading the policy in force: %v", err)
	}
	if contentHash([]byte(state.Content)) != state.Hash {
		return state, Usagef("the recorded policy content does not match its hash")
	}
	if _, err := decodePolicy(state.Ref, []byte(state.Content)); err != nil {
		return state, err
	}
	return state, nil
}

func contentHash(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }

func readPolicyContent(ref string) (*Policy, []byte, string, error) {
	raw, err := os.ReadFile(ref)
	if err != nil {
		return nil, nil, "", Usagef("reading the policy %s: %v", ref, err)
	}
	p, err := decodePolicy(ref, raw)
	return p, raw, contentHash(raw), err
}

// EffectivePolicy uses the loop's accepted snapshot when it names this file.
// Without a loop snapshot, a standalone read still checks its explicit input.
func EffectivePolicy(root, ref string) (*Policy, error) {
	state, err := ReadPolicyState(root)
	if err != nil {
		if _, statErr := os.Stat(PolicyStateFile(root)); os.IsNotExist(statErr) {
			return LoadPolicy(ref)
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
			return LoadPolicy(ref)
		}
	}
	return decodePolicy(state.Ref, []byte(state.Content))
}

func (l *Loop) currentPolicy() *Policy {
	l.policyMu.RLock()
	defer l.policyMu.RUnlock()
	return l.cfg.Policy
}

// refreshPolicy is called only by the pass goroutine. A request already handed
// to a worker is immutable; replacing this pointer cannot rewrite that request.
func (l *Loop) refreshPolicy() error {
	if l.cfg.PolicyRef == "" {
		return nil
	}
	p, raw, hash, err := readPolicyContent(l.cfg.PolicyRef)
	refusal := ""
	if err != nil {
		refusal = err.Error()
	}
	old := l.policyState
	if l.policyRecorded && old.DiskHash == hash && old.Refusal == refusal {
		return nil
	}
	next := old
	next.Ref = l.cfg.PolicyRef
	next.DiskHash, next.Refusal, next.At = hash, refusal, l.cfg.Now().UTC()
	if err == nil {
		next.Hash, next.Content = hash, string(raw)
	}
	change := policyChange{At: next.At, OldHash: old.DiskHash, NewHash: hash, InForceHash: next.Hash, Refusal: refusal}
	if err := os.MkdirAll(LoopDir(l.cfg.Root), 0o755); err != nil {
		return Externalf("recording policy: %v", err)
	}
	history, err := os.OpenFile(filepath.Join(LoopDir(l.cfg.Root), "backend-policy-changes.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return Externalf("recording policy change: %v", err)
	}
	encodeErr := json.NewEncoder(history).Encode(change)
	closeErr := history.Close()
	if encodeErr != nil {
		return Externalf("recording policy change: %v", encodeErr)
	}
	if closeErr != nil {
		return Externalf("recording policy change: %v", closeErr)
	}
	body, err := json.Marshal(next)
	if err != nil {
		return Internalf("encoding policy in force: %v", err)
	}
	path := PolicyStateFile(l.cfg.Root)
	if err := os.WriteFile(path+".new", body, 0o644); err != nil {
		return Externalf("recording policy in force: %v", err)
	}
	if err := os.Rename(path+".new", path); err != nil {
		return Externalf("recording policy in force: %v", err)
	}
	l.policyState, l.policyRecorded = next, true
	if refusal != "" {
		l.reportf("loop: policy change %s -> %s refused: %s\n", old.DiskHash, hash, refusal)
	} else {
		l.policyMu.Lock()
		l.cfg.Policy = p
		l.policyMu.Unlock()
		l.reportf("loop: policy change %s -> %s accepted\n", old.DiskHash, hash)
	}
	l.reportf("loop: policy in force %s: %s\n", next.Hash, l.currentPolicy().MappingLine())
	return nil
}

func (p Policy) MappingLine() string {
	parts := []string{"default=" + p.DefaultBackend}
	for _, seat := range p.seatNames() {
		parts = append(parts, seat+"="+strings.Join(p.seatList(seat), ","))
	}
	return strings.Join(parts, "; ")
}

// Description includes ordered seats, all backend model/effort values and overrides.
func (p Policy) Description() string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s\n", p.MappingLine())
	for _, name := range backendNames(p.Backends) {
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
