package store

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// otherProcessesHolding answers the processes other than this one that hold a POSIX lock on
// the store's database file or its shared-memory (-shm) file, read from /proc/locks. Every
// process with a WAL store open holds a read lock on the -shm file's DMS byte for as long as
// it has it open (SQLite's UNIX_SHM_DMS), so an empty answer means no other process has the
// store open. It opens neither file: closing any descriptor of a file drops every POSIX lock
// this process holds on it (ReadLog).
//
// A lock is matched by inode number alone. /proc/locks names the device the way the kernel's
// superblock does, which on btrfs is not the device stat reports, so a device comparison
// would miss every holder there; an inode number shared with a file on another filesystem
// can only add a holder, which refuses a truncate that was safe and never permits one that
// was not. A holder this process's PID namespace cannot see is listed with PID 0, and counts.
func otherProcessesHolding(path string) ([]int, error) {
	inodes := map[uint64]bool{}
	for _, name := range []string{path, path + "-shm"} {
		var st syscall.Stat_t
		if err := syscall.Stat(name, &st); err == nil {
			inodes[st.Ino] = true
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	f, err := os.Open("/proc/locks")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	self := os.Getpid()
	seen := map[int]bool{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// "1: POSIX  ADVISORY  READ 1234 00:2a:5678 128 128"; a blocked waiter's line starts
		// "1: -> POSIX ...", and a waiter holds nothing yet.
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 || fields[1] == "->" || fields[1] != "POSIX" {
			continue
		}
		pid, err := strconv.Atoi(fields[4])
		if err != nil {
			return nil, fmt.Errorf("reading /proc/locks: %q", scanner.Text())
		}
		parts := strings.Split(fields[5], ":")
		if len(parts) != 3 {
			return nil, fmt.Errorf("reading /proc/locks: %q", scanner.Text())
		}
		ino, err := strconv.ParseUint(parts[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("reading /proc/locks: %q", scanner.Text())
		}
		if inodes[ino] && pid != self {
			seen[pid] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	pids := make([]int, 0, len(seen))
	for pid := range seen {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids, nil
}
