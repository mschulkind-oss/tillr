//go:build !linux

package main

import "syscall"

func checkGroupAlive(pgid int) (bool, error) {
	err := syscall.Kill(-pgid, 0)
	if err == syscall.ESRCH {
		return false, nil
	}
	return err == nil, err
}
