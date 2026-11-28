package policy

import (
	"encoding/json"
	"sort"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
)

// QuotaCommandKey returns the canonical JSON encoding of a backend's quota
// command argv, or "" when no quota command is configured.
func QuotaCommandKey(quota []string) string {
	if len(quota) == 0 {
		return ""
	}
	raw, _ := json.Marshal(quota)
	return string(raw)
}

// QuotaPoolKey derives the vendor-neutral quota pool key for backend name:
//   - "group:" + b.QuotaGroup when quota_group is set,
//   - "cmd:" + canonicalJSON(b.Quota) when quota_group is omitted and len(b.Quota) > 0,
//   - "backend:" + name when neither is set.
func QuotaPoolKey(name string, b Backend) string {
	if b.QuotaGroup != "" {
		return "group:" + b.QuotaGroup
	}
	if len(b.Quota) > 0 {
		return "cmd:" + QuotaCommandKey(b.Quota)
	}
	return "backend:" + name
}

// CheckQuotaGroups refuses two backends (within one policy or across a profile
// list) that declare the same explicit quota_group with different non-empty
// quota commands.
func CheckQuotaGroups(policies ...*Policy) error {
	type groupRecord struct {
		backend string
		cmd     string
	}
	seen := map[string]groupRecord{}
	for _, p := range policies {
		if p == nil {
			continue
		}
		for _, name := range BackendNames(p.Backends) {
			b := p.Backends[name]
			if b.QuotaGroup == "" || len(b.Quota) == 0 {
				continue
			}
			cmd := QuotaCommandKey(b.Quota)
			if prev, ok := seen[b.QuotaGroup]; ok && prev.cmd != cmd {
				return drainmodel.Usagef("quota_group %q has conflicting quota commands on backend %q (%s) and backend %q (%s)",
					b.QuotaGroup, prev.backend, prev.cmd, name, cmd)
			}
			seen[b.QuotaGroup] = groupRecord{backend: name, cmd: cmd}
		}
	}
	return nil
}

// QuotaPoolPeers returns the sorted backend names across policies that share
// backend's quota pool, always including backend itself.
func QuotaPoolPeers(backend string, policies ...*Policy) []string {
	if backend == "" {
		return nil
	}
	keys := map[string]bool{}
	for _, p := range policies {
		if p == nil {
			continue
		}
		if b, ok := p.Backends[backend]; ok {
			keys[QuotaPoolKey(backend, b)] = true
		}
	}
	peerSet := map[string]bool{backend: true}
	if len(keys) > 0 {
		for _, p := range policies {
			if p == nil {
				continue
			}
			for name, b := range p.Backends {
				if keys[QuotaPoolKey(name, b)] {
					peerSet[name] = true
				}
			}
		}
	}
	peers := make([]string, 0, len(peerSet))
	for name := range peerSet {
		peers = append(peers, name)
	}
	sort.Strings(peers)
	return peers
}
