package goaffected

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// LoadDurations loads per-package test durations from tools/go-test-durations.json.
func LoadDurations(root string) map[string]map[string]float64 {
	p := filepath.Join(root, "tools", "go-test-durations.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		return map[string]map[string]float64{}
	}
	var parsed map[string]map[string]float64
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed == nil {
		return map[string]map[string]float64{}
	}
	return parsed
}

// Assign distributes test names across count buckets using greedy load balancing
// matching assign() in tools/go-test-shards.py.
func Assign(names []string, durations map[string]float64, count int) [][]string {
	if count <= 0 {
		count = 1
	}
	groups := make([][]string, count)
	var known []string
	var unknown []string
	for _, name := range names {
		if durations != nil {
			if _, ok := durations[name]; ok {
				known = append(known, name)
				continue
			}
		}
		unknown = append(unknown, name)
	}
	sort.Slice(known, func(i, j int) bool {
		di, dj := durations[known[i]], durations[known[j]]
		if di != dj {
			return di > dj
		}
		return known[i] < known[j]
	})
	loads := make([]float64, count)
	minSlot := func() int {
		best := 0
		for i := 1; i < count; i++ {
			if loads[i] < loads[best] {
				best = i
			}
		}
		return best
	}
	var knownSum float64
	for _, name := range known {
		slot := minSlot()
		groups[slot] = append(groups[slot], name)
		d := durations[name]
		loads[slot] += d
		knownSum += d
	}
	estimate := 1.0
	if len(known) > 0 {
		estimate = knownSum / float64(len(known))
		if estimate == 0 {
			estimate = 1.0
		}
	}
	for _, name := range unknown {
		slot := minSlot()
		groups[slot] = append(groups[slot], name)
		loads[slot] += estimate
	}
	return groups
}
