//go:build linux

package drain

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const defaultGateReservedCap = 4

type waiterTicket struct {
	held  bool
	class string
	pools string
}

type waiterClassLock struct {
	held  bool
	class string
}

type waiterNamespaceScan struct {
	tickets    []waiterTicket
	classLocks []waiterClassLock
}

type slotPoolCounts struct {
	existing int
	held     int
}

func inspectTestSlots(lockDir string, limits TestSlotsLimits) (testSlotsSnapshot, error) {
	info, err := os.Stat(lockDir)
	if os.IsNotExist(err) {
		return testSlotsSnapshot{}, nil
	}
	if err != nil {
		return testSlotsSnapshot{}, err
	}
	if !info.IsDir() {
		return testSlotsSnapshot{}, nil
	}

	nsDirs := []string{lockDir}
	if rulesEntries, err := os.ReadDir(filepath.Join(lockDir, "wait-rules")); err == nil {
		for _, entry := range rulesEntries {
			if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
				nsDirs = append(nsDirs, filepath.Join(lockDir, "wait-rules", entry.Name()))
			}
		}
	} else if !os.IsNotExist(err) {
		return testSlotsSnapshot{}, err
	}

	var namespaces []waiterNamespaceScan
	candidateWaitFiles := 0
	for _, nsDir := range nsDirs {
		ns, err := scanWaiterNamespace(nsDir)
		if err != nil {
			return testSlotsSnapshot{}, err
		}
		candidateWaitFiles += len(ns.tickets) + len(ns.classLocks)
		namespaces = append(namespaces, ns)
	}
	if candidateWaitFiles == 0 {
		return testSlotsSnapshot{}, nil
	}

	poolCounts := map[byte]slotPoolCounts{
		's': probePoolSlots(lockDir, "slot", limits.Ordinary),
		'p': probePoolSlots(lockDir, "proof", limits.Proof),
		'g': probePoolSlots(lockDir, "gate", limits.Gate),
	}

	return summarizeTestSlots(namespaces, poolCounts, limits), nil
}

func summarizeTestSlots(
	namespaces []waiterNamespaceScan,
	poolCounts map[byte]slotPoolCounts,
	limits TestSlotsLimits,
) testSlotsSnapshot {
	capByPool := map[byte]int{}
	heldByPool := map[byte]int{}
	freeByPool := map[byte]int{}
	totalExisting := poolCounts['s'].existing + poolCounts['p'].existing + poolCounts['g'].existing
	if totalExisting > 0 {
		for _, pool := range []byte{'s', 'p', 'g'} {
			capByPool[pool] = poolCounts[pool].existing
			heldByPool[pool] = poolCounts[pool].held
			freeByPool[pool] = capByPool[pool] - heldByPool[pool]
		}
	} else {
		capByPool['s'] = max(0, limits.Ordinary)
		capByPool['p'] = max(0, limits.Proof)
		capByPool['g'] = max(0, limits.Gate)
		freeByPool['s'] = capByPool['s']
		freeByPool['p'] = capByPool['p']
		freeByPool['g'] = capByPool['g']
	}

	waiters := 0
	eligiblePools := map[byte]bool{}
	for _, ns := range namespaces {
		classTickets := map[string]int{}
		for _, ticket := range ns.tickets {
			if !ticket.held {
				continue
			}
			waiters++
			if ticket.class != "" {
				classTickets[ticket.class]++
			}
			for i := 0; i < len(ticket.pools); i++ {
				eligiblePools[ticket.pools[i]] = true
			}
		}
		for _, cl := range ns.classLocks {
			if !cl.held || classTickets[cl.class] > 0 {
				continue
			}
			waiters++
			pools := classFallbackPools(cl.class, heldByPool['g'])
			for i := 0; i < len(pools); i++ {
				eligiblePools[pools[i]] = true
			}
		}
	}

	eligibleFree := 0
	for _, pool := range []byte{'s', 'p', 'g'} {
		if eligiblePools[pool] {
			eligibleFree += freeByPool[pool]
		}
	}

	return testSlotsSnapshot{
		TotalSlots:        capByPool['s'] + capByPool['p'] + capByPool['g'],
		FreeSlots:         freeByPool['s'] + freeByPool['p'] + freeByPool['g'],
		EligibleFreeSlots: eligibleFree,
		Waiters:           waiters,
	}
}

func scanWaiterNamespace(nsDir string) (waiterNamespaceScan, error) {
	var out waiterNamespaceScan
	queueDir := filepath.Join(nsDir, "wait-queue")
	if entries, err := os.ReadDir(queueDir); err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".wait") {
				continue
			}
			exists, held := probeFlockHeld(filepath.Join(queueDir, name), syscall.LOCK_EX)
			if !exists {
				continue
			}
			class, pools := parseWaitTicketName(name)
			out.tickets = append(out.tickets, waiterTicket{
				held:  held,
				class: class,
				pools: pools,
			})
		}
	} else if !os.IsNotExist(err) {
		return out, err
	}

	for _, item := range []struct {
		file  string
		class string
	}{
		{"repair-waiting.lock", "repair"},
		{"proof-waiting.lock", "proof"},
		{"gate-waiting.lock", "gate"},
	} {
		if exists, held := probeFlockHeld(filepath.Join(nsDir, item.file), syscall.LOCK_EX); exists {
			out.classLocks = append(out.classLocks, waiterClassLock{
				held:  held,
				class: item.class,
			})
		}
	}
	return out, nil
}

func parseWaitTicketName(name string) (class, pools string) {
	stem := strings.TrimSuffix(name, ".wait")
	switch {
	case strings.HasPrefix(stem, "00-"):
		class = "repair"
	case strings.HasPrefix(stem, "01-"):
		class = "proof"
	case strings.HasPrefix(stem, "02-"):
		class = "gate"
	case strings.HasPrefix(stem, "03-"):
		class = "ordinary"
	}
	if idx := strings.LastIndexByte(stem, '-'); idx >= 0 && idx+1 < len(stem) {
		raw := stem[idx+1:]
		var b strings.Builder
		for i := 0; i < len(raw); i++ {
			switch raw[i] {
			case 's', 'p', 'g':
				if !strings.ContainsRune(b.String(), rune(raw[i])) {
					b.WriteByte(raw[i])
				}
			}
		}
		pools = b.String()
	}
	if pools == "" {
		pools = classFallbackPools(class, 0)
	}
	return class, pools
}

func classFallbackPools(class string, heldGate int) string {
	switch class {
	case "repair":
		return "spg"
	case "proof":
		return "sp"
	case "gate":
		if heldGate >= defaultGateReservedCap {
			return "s"
		}
		return "sg"
	default:
		return "s"
	}
}

func probePoolSlots(lockDir, prefix string, limit int) slotPoolCounts {
	var counts slotPoolCounts
	for i := 0; i < limit; i++ {
		path := filepath.Join(lockDir, fmt.Sprintf("%s-%d.lock", prefix, i))
		exists, held := probeFlockHeld(path, syscall.LOCK_SH)
		if !exists {
			continue
		}
		counts.existing++
		if held {
			counts.held++
		}
	}
	return counts
}

// probeFlockHeld reports whether path exists and currently has a flock(2)
// lock conflicting with probeHow (LOCK_SH for exclusive slot locks, LOCK_EX
// for shared waiter locks). Probing each lock file directly checks the
// inode's lock state atomically in the kernel instead of scanning /proc/locks,
// whose 4 KiB seq_file pages can omit or duplicate entries under concurrent
// lock churn.
func probeFlockHeld(path string, probeHow int) (exists, held bool) {
	f, err := os.Open(path)
	if err != nil {
		return false, false
	}
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), probeHow|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return true, true
	}
	return true, false
}
