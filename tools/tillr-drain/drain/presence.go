package drain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/mschulkind-oss/tillr/store"
)

// Process evidence that survives a PID namespace.
//
// A LoopOwner's Domain is the boot and PID namespace it was recorded in, and ownerDead
// judges only owners of this loop's own domain: a PID from another namespace names
// nothing here. A jail relaunch replaces the PID namespace while the bind-mounted store
// survives, so every claim recorded before it names a domain this loop cannot judge by
// PID. On 2026-11-17 six runs of a relaunched jail stayed run-live, with their lanes
// leased, for as long as the new loop ran.
//
// The kernel still answers across namespaces about files: the store and the run records
// are the same inodes in every container that mounts the repository. Two answers are
// used. A loop is gone when every drain session it recorded is ended (it ended them
// itself on its way out), or when its presence file — opened for writing at session
// start and held until the process exits — has no writer. A run's face is gone when
// neither of the two stream files ProcessSteps gave it (run.record.json and run.stderr)
// is open for writing by any process: the face and every descendant that inherited
// those streams hold them for as long as they live.

type streamState int

const (
	streamAbsent streamState = iota
	streamUnwritten
	streamWritten
)

func (s streamState) String() string {
	return [...]string{"absent", "unwritten", "written"}[s]
}

// openForWriting answers whether any process on this kernel, in any PID namespace, holds
// path open for writing. Linux grants a read lease (F_SETLEASE with F_RDLCK) on a file
// only while no descriptor anywhere has it open for writing, and refuses with EAGAIN
// otherwise; the lease is dropped at once. It needs no timeout and does not disturb a
// reader. Any other refusal (a filesystem without leases, another user's file) is
// answered as the error: the caller cannot judge and must keep the claim.
func openForWriting(path string) (streamState, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return streamAbsent, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	fd := f.Fd()
	for {
		_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_SETLEASE, syscall.F_RDLCK)
		switch errno {
		case 0:
			if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_SETLEASE, syscall.F_UNLCK); errno != 0 {
				return 0, fmt.Errorf("releasing the read lease on %s: %w", path, errno)
			}
			return streamUnwritten, nil
		case syscall.EAGAIN:
			return streamWritten, nil
		case syscall.EINTR:
			continue
		default:
			return 0, fmt.Errorf("taking a read lease on %s: %w", path, errno)
		}
	}
}

// loopPresencePath is where owner's presence file lives: beside the store, so every
// container that mounts the store sees the same inode.
func loopPresencePath(identity string, owner LoopOwner) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\n%d\n%s\n%d", owner.Domain, owner.PID, owner.Start, owner.UID)))
	return filepath.Join(identity+".loops", hex.EncodeToString(sum[:16])+".present")
}

// openLoopPresence creates owner's presence file and answers it open for writing. The
// file is created and opened in one call, so no reader ever sees it without its writer.
func openLoopPresence(identity string, owner LoopOwner) (*os.File, error) {
	path := loopPresencePath(identity, owner)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(owner)
	if err == nil {
		_, err = f.Write(append(raw, '\n'))
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// heldPresence is every presence file this process holds, by store identity. A file is
// never closed: the process's exit is what it records.
var heldPresence struct {
	sync.Mutex
	files map[string]*os.File
}

// holdLoopPresence opens this process's presence file for the store once and keeps it.
func holdLoopPresence(storePath string) error {
	identity, err := processStoreIdentity(storePath)
	if err != nil {
		return err
	}
	owner, err := currentLoopOwner()
	if err != nil {
		return err
	}
	heldPresence.Lock()
	defer heldPresence.Unlock()
	if heldPresence.files[identity] != nil {
		return nil
	}
	f, err := openLoopPresence(identity, *owner)
	if err != nil {
		return fmt.Errorf("recording this loop's presence beside %s: %w", identity, err)
	}
	if heldPresence.files == nil {
		heldPresence.files = map[string]*os.File{}
	}
	heldPresence.files[identity] = f
	return nil
}

// fromOtherDomain answers whether owner is this user's loop from another process domain:
// one ownerDead cannot judge, and the evidence here can.
func fromOtherDomain(owner, here *LoopOwner) bool {
	return owner != nil && here != nil && owner.UID == here.UID && owner.Domain != here.Domain
}

// endedSessionOwners answers, for each loop owner that recorded drain sessions, whether
// every one of them is ended.
func endedSessionOwners(r *store.Reader) (map[LoopOwner]bool, error) {
	rows, err := r.Query(`SELECT loop_owner, ended_at IS NOT NULL FROM drain_sessions WHERE loop_owner IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ended := map[LoopOwner]bool{}
	for rows.Next() {
		var raw []byte
		var done bool
		if err := rows.Scan(&raw, &done); err != nil {
			return nil, err
		}
		var owner LoopOwner
		if err := json.Unmarshal(raw, &owner); err != nil {
			return nil, err
		}
		if prior, seen := ended[owner]; seen {
			done = done && prior
		}
		ended[owner] = done
	}
	return ended, rows.Err()
}

// foreignOwnerGone answers whether a loop from another process domain is proven gone:
// it ended every session it recorded, or its presence file exists with no writer. A
// presence file that cannot be judged proves nothing.
func foreignOwnerGone(identity string, owner LoopOwner, ended map[LoopOwner]bool) bool {
	if ended[owner] {
		return true
	}
	state, err := openForWriting(loopPresencePath(identity, owner))
	return err == nil && state == streamUnwritten
}

// runStreamLocator is a Steps that starts run faces with their streams in per-run files,
// and says where, so a loop in another process domain can judge the face by them.
type runStreamLocator interface {
	runOutputFiles(runID string) []string
}

// foreignRunLive answers whether a run started from another process domain may still be
// running: some process holds one of its stream files open for writing, or the files
// cannot be located or judged. Absent files mean no face was started there:
// ProcessSteps creates both before it starts the face.
func foreignRunLive(locator runStreamLocator, runID string) bool {
	if runID == "" {
		return false
	}
	if locator == nil {
		return true
	}
	for _, path := range locator.runOutputFiles(runID) {
		state, err := openForWriting(path)
		if err != nil || state == streamWritten {
			return true
		}
	}
	return false
}
