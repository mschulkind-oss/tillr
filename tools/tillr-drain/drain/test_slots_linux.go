//go:build linux

package drain

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const defaultGateReservedCap = 4

type waiterTicketInode struct {
	ino   uint64
	class string
	pools string
}

type waiterClassLockInode struct {
	ino   uint64
	class string
}

type waiterNamespaceScan struct {
	tickets    []waiterTicketInode
	classLocks []waiterClassLockInode
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

	slotInodes := map[byte][]uint64{
		's': statPoolInodes(lockDir, "slot", limits.Ordinary),
		'p': statPoolInodes(lockDir, "proof", limits.Proof),
		'g': statPoolInodes(lockDir, "gate", limits.Gate),
	}

	writeHeld, readHolders, err := readFlockTableOnce()
	if err != nil {
		return testSlotsSnapshot{}, err
	}
	snap := summarizeTestSlots(namespaces, slotInodes, limits, writeHeld, readHolders)
	if snap.Waiters > 0 && snap.EligibleFreeSlots > 0 {
		// /proc/locks is a seq_file read in 4 KiB pages; concurrent unlocks earlier
		// in the kernel lock list can shift *ppos across a page boundary and omit a
		// held slot on a single scan. Confirm free slots across 3 additional scans.
		for range 3 {
			w, _, scanErr := readFlockTableOnce()
			if scanErr != nil {
				return testSlotsSnapshot{}, scanErr
			}
			for ino := range w {
				writeHeld[ino] = true
			}
			snap = summarizeTestSlots(namespaces, slotInodes, limits, writeHeld, readHolders)
			if snap.EligibleFreeSlots == 0 {
				break
			}
		}
	}
	return snap, nil
}

func summarizeTestSlots(
	namespaces []waiterNamespaceScan,
	slotInodes map[byte][]uint64,
	limits TestSlotsLimits,
	writeHeld map[uint64]bool,
	readHolders map[uint64]int,
) testSlotsSnapshot {
	capByPool := map[byte]int{}
	heldByPool := map[byte]int{}
	freeByPool := map[byte]int{}
	totalExisting := len(slotInodes['s']) + len(slotInodes['p']) + len(slotInodes['g'])
	if totalExisting > 0 {
		for _, pool := range []byte{'s', 'p', 'g'} {
			capByPool[pool] = len(slotInodes[pool])
			held := 0
			for _, ino := range slotInodes[pool] {
				if writeHeld[ino] {
					held++
				}
			}
			heldByPool[pool] = held
			freeByPool[pool] = capByPool[pool] - held
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
			if readHolders[ticket.ino] <= 0 {
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
			holders := readHolders[cl.ino]
			if holders <= classTickets[cl.class] {
				continue
			}
			excess := holders - classTickets[cl.class]
			waiters += excess
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
			ino, ok := statInode(filepath.Join(queueDir, name))
			if !ok {
				continue
			}
			class, pools := parseWaitTicketName(name)
			out.tickets = append(out.tickets, waiterTicketInode{
				ino:   ino,
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
		if ino, ok := statInode(filepath.Join(nsDir, item.file)); ok {
			out.classLocks = append(out.classLocks, waiterClassLockInode{
				ino:   ino,
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

func statPoolInodes(lockDir, prefix string, limit int) []uint64 {
	if limit <= 0 {
		return nil
	}
	var inodes []uint64
	for i := 0; i < limit; i++ {
		path := filepath.Join(lockDir, fmt.Sprintf("%s-%d.lock", prefix, i))
		if ino, ok := statInode(path); ok {
			inodes = append(inodes, ino)
		}
	}
	return inodes
}

func statInode(path string) (uint64, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Ino, true
}

// readProcLocksSnapshot reads /proc/locks in one read(2) call from offset 0 so
// seq_file traverses the kernel lock list in a single atomic pass. Splitting
// /proc/locks across 4 KiB reads drops the kernel lock between chunks and can
// skip held locks when earlier entries unlock concurrently.
// readFlockTableOnce reads /proc/locks once and returns held FLOCK write inodes and
// per-inode FLOCK shared-read counts. Like store.Holders, locks are matched by inode
// number alone because btrfs superblock device numbers in /proc/locks differ from stat(2).
func readFlockTableOnce() (writeHeld map[uint64]bool, readHolders map[uint64]int, err error) {
	f, err := os.Open("/proc/locks")
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	var raw bytes.Buffer
	buf := make([]byte, 32*1024)
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			raw.Write(buf[:n])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, nil, readErr
		}
	}

	writeHeld = map[uint64]bool{}
	readHolders = map[uint64]int{}
	for line := range bytes.SplitSeq(raw.Bytes(), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		fields := strings.Fields(string(line))
		// A blocked waiter request has "->" in fields[1]; only held locks count.
		if len(fields) < 6 || fields[1] == "->" || fields[1] != "FLOCK" {
			continue
		}
		parts := strings.Split(fields[5], ":")
		if len(parts) != 3 {
			continue
		}
		ino, convErr := strconv.ParseUint(parts[2], 10, 64)
		if convErr != nil {
			continue
		}
		switch fields[3] {
		case "WRITE":
			writeHeld[ino] = true
		case "READ":
			readHolders[ino]++
		}
	}
	return writeHeld, readHolders, nil
}
