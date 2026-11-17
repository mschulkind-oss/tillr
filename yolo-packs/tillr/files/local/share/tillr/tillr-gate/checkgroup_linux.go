//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// This is process-lifetime observation only, not a fallback CPU counter. Zombies
// have exited and cannot access the tree; PID 1 may retain them until later.
func checkGroupAlive(pgid int) (bool, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		text := string(raw)
		end := strings.LastIndex(text, ")")
		if end < 0 {
			return false, fmt.Errorf("process %s has unreadable lifetime fields", entry.Name())
		}
		fields := strings.Fields(text[end+1:])
		if len(fields) < 3 {
			return false, fmt.Errorf("process %s has incomplete lifetime fields", entry.Name())
		}
		group, err := strconv.Atoi(fields[2])
		if err != nil {
			return false, err
		}
		if group == pgid && fields[0] != "Z" && fields[0] != "X" {
			return true, nil
		}
	}
	return false, nil
}
