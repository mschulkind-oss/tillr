package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func attachmentPath(store, owner string) string {
	return filepath.Join(store+".watch-attached", fmt.Sprintf("%x.lock", sha256.Sum256([]byte(owner))))
}

// A wait keeps this descriptor open through its cleanup. POSIX record locks
// belong to the process; closing any descriptor for the file releases them.
func attach(store, owner string) (*os.File, error) {
	path := attachmentPath(store, owner)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("attachment %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0600)
	if err != nil {
		return nil, err
	}
	lock := syscall.Flock_t{Type: syscall.F_RDLCK, Whence: 0, Start: 0, Len: 1}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_SETLK, &lock); err != nil {
		f.Close()
		return nil, fmt.Errorf("attachment %s: %w", path, err)
	}
	return f, nil
}

func isAttached(store, owner string) (bool, error) {
	path := attachmentPath(store, owner)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	lock := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0, Start: 0, Len: 1}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_GETLK, &lock); err != nil {
		return false, fmt.Errorf("attachment %s: %w", path, err)
	}
	return lock.Type != syscall.F_UNLCK, nil
}
