package presence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/mschulkind-oss/tillr/store"
)

// Owner identifies one OS process without mistaking a reused PID for it.
// Domain binds the evidence to the boot and PID namespace where it was observed.
type Owner struct {
	PID    int    `json:"pid"`
	Start  string `json:"start"`
	Domain string `json:"domain"`
	UID    uint32 `json:"uid"`
}

// SameBoot answers whether two Owner.Domain values share the same non-empty boot
// prefix before ':'. If either lacks ':' or has an empty boot prefix, it returns
// true so an unknown format is not treated as a reboot.
func SameBoot(a, b string) bool {
	ba, _, oka := strings.Cut(a, ":")
	bb, _, okb := strings.Cut(b, ":")
	if !oka || !okb || ba == "" || bb == "" {
		return true
	}
	return ba == bb
}

// ProcessStart returns the kernel start time field from /proc/<pid>/stat, or ""
// if the process is a zombie ('Z').
func ProcessStart(pid int) (string, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return "", fmt.Errorf("process %d has malformed stat", pid)
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) < 20 {
		return "", fmt.Errorf("process %d stat has no start time", pid)
	}
	if fields[0] == "Z" {
		return "", nil
	}
	return fields[19], nil
}

// OwnerReads counts /proc identity reads, so tests can pin that claims and reaps
// inside a write transaction never make one.
var OwnerReads atomic.Int64

var processOwner = sync.OnceValues(readOwner)

// CurrentOwner answers a copy of this process's identity. Only the first call
// reads /proc.
func CurrentOwner() (*Owner, error) {
	owner, err := processOwner()
	if err != nil {
		return nil, err
	}
	cp := *owner
	return &cp, nil
}

func readOwner() (*Owner, error) {
	OwnerReads.Add(1)
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return nil, fmt.Errorf("identifying loop boot: %w", err)
	}
	ns, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		return nil, fmt.Errorf("identifying loop PID namespace: %w", err)
	}
	start, err := ProcessStart(os.Getpid())
	if err != nil {
		return nil, err
	}
	return &Owner{
		PID:    os.Getpid(),
		Start:  start,
		Domain: strings.TrimSpace(string(boot)) + ":" + ns,
		UID:    uint32(os.Geteuid()),
	}, nil
}

// ProcessGone answers whether err means the /proc entry no longer exists.
func ProcessGone(err error) bool {
	return os.IsNotExist(err) || errors.Is(err, syscall.ESRCH)
}

// OwnerDead answers whether owner (in the same Domain and UID as here) is dead.
func OwnerDead(owner, here *Owner) (bool, error) {
	if owner == nil || here == nil || owner.Domain != here.Domain || owner.UID != here.UID {
		return false, nil
	}
	start, err := ProcessStart(owner.PID)
	if ProcessGone(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return start != owner.Start, nil
}

// StoreIdentity returns the canonical resolved path for a store file.
func StoreIdentity(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

// RunProcessEnvironment reads /proc/<pid>/environ only when the process is owned by uid.
func RunProcessEnvironment(dir string, uid uint32) ([]byte, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, fmt.Errorf("process ownership unavailable for %s", dir)
	}
	if stat.Uid != uid {
		return nil, nil
	}
	return os.ReadFile(filepath.Join(dir, "environ"))
}

// LiveRunIDs answers, from one process-table scan, every run ID a live process
// carries for this store.
func LiveRunIDs(storePath string) (map[string]bool, error) {
	storePath, err := StoreIdentity(storePath)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	live := map[string]bool{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		env, err := RunProcessEnvironment(filepath.Join("/proc", entry.Name()), uint32(os.Geteuid()))
		if ProcessGone(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("checking process %d before recovering runs: %w", pid, err)
		}
		runID, hasStore := "", false
		for _, v := range bytes.Split(env, []byte{0}) {
			if id, ok := strings.CutPrefix(string(v), "TILLR_RUN_ID="); ok {
				runID = id
			}
			hasStore = hasStore || string(v) == "TILLR_LOOP_STORE="+storePath
		}
		if !hasStore || runID == "" {
			continue
		}
		live[runID] = true
	}
	return live, nil
}

// StreamState describes whether a file exists and whether any process holds it
// open for writing.
type StreamState int

const (
	StreamAbsent StreamState = iota
	StreamUnwritten
	StreamWritten
)

func (s StreamState) String() string {
	return [...]string{"absent", "unwritten", "written"}[s]
}

// OpenForWriting answers whether any process on this kernel, in any PID namespace,
// holds path open for writing via F_SETLEASE(F_RDLCK). A path component that exceeds
// NAME_MAX (ENAMETOOLONG) cannot exist on this filesystem, so it is absent like ENOENT.
func OpenForWriting(path string) (StreamState, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) || errors.Is(err, syscall.ENAMETOOLONG) {
		return StreamAbsent, nil
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
			return StreamUnwritten, nil
		case syscall.EAGAIN:
			return StreamWritten, nil
		case syscall.EINTR:
			continue
		default:
			return 0, fmt.Errorf("taking a read lease on %s: %w", path, errno)
		}
	}
}

// LoopPresencePath is where owner's presence file lives: beside the store, so every
// container that mounts the store sees the same inode.
func LoopPresencePath(identity string, owner Owner) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\n%d\n%s\n%d", owner.Domain, owner.PID, owner.Start, owner.UID)))
	return filepath.Join(identity+".loops", hex.EncodeToString(sum[:16])+".present")
}

// OpenLoopPresence creates owner's presence file and answers it open for writing.
func OpenLoopPresence(identity string, owner Owner) (*os.File, error) {
	path := LoopPresencePath(identity, owner)
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

var heldPresence struct {
	sync.Mutex
	files map[string]*os.File
}

// HoldLoopPresence opens this process's presence file for the store once and keeps it.
func HoldLoopPresence(storePath string) error {
	identity, err := StoreIdentity(storePath)
	if err != nil {
		return err
	}
	owner, err := CurrentOwner()
	if err != nil {
		return err
	}
	heldPresence.Lock()
	defer heldPresence.Unlock()
	if heldPresence.files[identity] != nil {
		return nil
	}
	f, err := OpenLoopPresence(identity, *owner)
	if err != nil {
		return fmt.Errorf("recording this loop's presence beside %s: %w", identity, err)
	}
	if heldPresence.files == nil {
		heldPresence.files = map[string]*os.File{}
	}
	heldPresence.files[identity] = f
	return nil
}

// FromOtherDomain answers whether owner is this user's loop from another process domain.
func FromOtherDomain(owner, here *Owner) bool {
	return owner != nil && here != nil && owner.UID == here.UID && owner.Domain != here.Domain
}

// EndedSessionOwners answers, for each loop owner that recorded drain sessions,
// whether its latest session is ended. Ordering by started_at ASC, rowid ASC ensures
// an un-ended pre-exec row from an in-place binary restart does not hide a later
// ended session of the same OS process.
func EndedSessionOwners(r *store.Reader) (map[Owner]bool, error) {
	rows, err := r.Query(`SELECT loop_owner, ended_at IS NOT NULL FROM drain_sessions WHERE loop_owner IS NOT NULL ORDER BY started_at ASC, rowid ASC`)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") || strings.Contains(err.Error(), "no such column") {
			return map[Owner]bool{}, nil
		}
		return nil, err
	}
	defer rows.Close()
	ended := map[Owner]bool{}
	for rows.Next() {
		var raw []byte
		var done bool
		if err := rows.Scan(&raw, &done); err != nil {
			return nil, err
		}
		var owner Owner
		if err := json.Unmarshal(raw, &owner); err != nil {
			return nil, err
		}
		ended[owner] = done
	}
	return ended, rows.Err()
}

// ForeignOwnerGone answers whether a loop from another process domain is proven gone:
// its boot ID differs from here's, its latest session is ended, or its presence file
// exists with no writer.
func ForeignOwnerGone(identity string, owner, here Owner, ended map[Owner]bool) bool {
	if !SameBoot(owner.Domain, here.Domain) {
		return true
	}
	if ended[owner] {
		return true
	}
	state, err := OpenForWriting(LoopPresencePath(identity, owner))
	return err == nil && state == StreamUnwritten
}

// ForeignRunLive answers whether a run started from another process domain may still
// be running. A reboot (!SameBoot) proves the run is gone even without a locator.
func ForeignRunLive(paths []string, hasLocator bool, ownerDomain, hereDomain string) bool {
	if !SameBoot(ownerDomain, hereDomain) {
		return false
	}
	if !hasLocator {
		return true
	}
	for _, path := range paths {
		state, err := OpenForWriting(path)
		if err != nil || state == StreamWritten {
			return true
		}
	}
	return false
}

// SessionOwnerGone answers whether a drain session owner is proven gone.
func SessionOwnerGone(r *store.Reader, storePath string, owner Owner) bool {
	here, err := CurrentOwner()
	if err != nil || here == nil {
		return false
	}
	if FromOtherDomain(&owner, here) {
		if !SameBoot(owner.Domain, here.Domain) {
			return true
		}
		ended, err := EndedSessionOwners(r)
		if err == nil && ended[owner] {
			return true
		}
		identity, err := StoreIdentity(storePath)
		if err != nil {
			return false
		}
		state, err := OpenForWriting(LoopPresencePath(identity, owner))
		return err == nil && state == StreamUnwritten
	}
	dead, err := OwnerDead(&owner, here)
	return err == nil && dead
}

var sessionIDShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type replayRequest struct {
	RunID        string `json:"run_id"`
	ItemID       string `json:"item_id"`
	LaneID       string `json:"lane_id"`
	Backend      string `json:"backend"`
	Resume       bool   `json:"resume,omitempty"`
	FreshSession bool   `json:"fresh_session,omitempty"`
}

type replayDecision struct {
	ItemID     string           `json:"item_id"`
	LaneID     string           `json:"lane_id"`
	RunRequest *replayRequest   `json:"run_request,omitempty"`
	Parallel   []replayDecision `json:"parallel,omitempty"`
}

type replayWork struct {
	Examining   bool           `json:"examining,omitempty"`
	Ready       bool           `json:"ready,omitempty"`
	ParentRunID string         `json:"parent_run_id,omitempty"`
	Decision    replayDecision `json:"decision"`
	Examiner    *Owner         `json:"examination_owner,omitempty"`
	Owner       *Owner         `json:"loop_owner,omitempty"`
	Done        bool           `json:"-"`
	Orphaned    bool           `json:"-"`
	Gated       bool           `json:"-"`
}

func replayParallelNodes(d replayDecision) []replayDecision {
	if len(d.Parallel) == 0 {
		return nil
	}
	nodes := make([]replayDecision, 0, 1+len(d.Parallel))
	nodes = append(nodes, d)
	nodes = append(nodes, d.Parallel...)
	return nodes
}

// DeadRuns answers which currently leased runs (in lanes or unsettled read_only_runs)
// are proven dead (their loop owner is gone and the run itself has no live process).
// Unowned leases remain protected unless drain.orphaned is active on them.
func DeadRuns(r *store.Reader, storePath, loopDir string) (map[string]bool, error) {
	leased := map[string]bool{}
	rows, err := r.Query(`SELECT run_id FROM lanes WHERE state = 'leased' AND run_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		leased[id] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	var hasROR int
	if err := r.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='read_only_runs'`).Scan(&hasROR); err != nil {
		return nil, err
	}
	if hasROR > 0 {
		rRows, err := r.Query(`SELECT run_id FROM read_only_runs WHERE settled = 0`)
		if err != nil {
			return nil, err
		}
		for rRows.Next() {
			var id string
			if err := rRows.Scan(&id); err != nil {
				rRows.Close()
				return nil, err
			}
			leased[id] = true
		}
		if err := rRows.Err(); err != nil {
			rRows.Close()
			return nil, err
		}
		rRows.Close()
	}

	dead := map[string]bool{}
	if len(leased) == 0 {
		return dead, nil
	}

	events, err := r.EventsOfKind(0,
		"drain.started",
		"drain.node-advanced",
		"drain.graph-changed",
		"drain.examination-claimed",
		"drain.examination.owner",
		"drain.examination-released",
		"drain.examined",
		"drain.done",
		"drain.candidate-gated",
		"drain.judge-landed",
		"gate.verdict",
		"land.landing",
		"drain.orphaned",
		"run.resume",
		"run.finish",
	)
	if err != nil {
		return nil, err
	}

	works := map[string]*replayWork{}
	byRun := map[string]*replayWork{}
	judged := map[string]bool{}
	segments := map[string]bool{}

	for _, e := range events {
		if e.ObjectID == nil {
			continue
		}
		switch e.Kind {
		case "drain.started", "drain.node-advanced", "drain.graph-changed":
			var work replayWork
			if err := json.Unmarshal(e.Payload, &work); err != nil {
				return nil, err
			}
			works[work.Decision.ItemID] = &work
			byRun[*e.ObjectID] = &work
			for _, node := range replayParallelNodes(work.Decision) {
				if node.RunRequest != nil {
					byRun[node.RunRequest.RunID] = &work
				}
			}
		case "drain.examination-claimed":
			if work := byRun[*e.ObjectID]; work != nil {
				if err := json.Unmarshal(e.Payload, &work.Owner); err != nil {
					return nil, err
				}
				work.Examining = true
			}
		case "drain.examination.owner":
			if work := byRun[*e.ObjectID]; work != nil {
				if err := json.Unmarshal(e.Payload, &work.Examiner); err != nil {
					return nil, err
				}
			}
		case "drain.examination-released":
			if work := byRun[*e.ObjectID]; work != nil {
				work.Examining = false
			}
			for _, work := range works {
				if work.Ready && work.ParentRunID == *e.ObjectID {
					work.Examining = false
				}
			}
		case "drain.examined", "drain.done":
			if work := byRun[*e.ObjectID]; work != nil {
				work.Done = true
				work.Examiner = nil
				work.Examining = false
			}
		case "drain.candidate-gated", "drain.judge-landed":
			if work := byRun[*e.ObjectID]; work != nil {
				work.Gated = true
				work.Done = true
				work.Examiner = nil
				work.Examining = false
			}
		case "gate.verdict":
			if e.ObjectType == "run" {
				judged[*e.ObjectID] = true
				if work := byRun[*e.ObjectID]; work != nil {
					work.Done = true
					work.Examiner = nil
					work.Examining = false
				}
			}
		case "land.landing":
			var landing struct {
				RunID string `json:"run_id"`
			}
			if err := json.Unmarshal(e.Payload, &landing); err != nil {
				return nil, err
			}
			judged[landing.RunID] = true
			if work := byRun[landing.RunID]; work != nil {
				work.Done = true
				work.Examiner = nil
				work.Examining = false
			}
		case "drain.orphaned":
			if work := byRun[*e.ObjectID]; work != nil {
				work.Orphaned = true
			}
		case "run.resume":
			var segment struct {
				Request   *replayRequest `json:"request,omitempty"`
				RunID     string         `json:"run_id"`
				ItemID    string         `json:"item_id"`
				LaneID    string         `json:"lane_id"`
				Backend   string         `json:"backend"`
				SessionID string         `json:"session_id"`
				ResumedAt string         `json:"resumed_at"`
			}
			if err := json.Unmarshal(e.Payload, &segment); err != nil {
				return nil, err
			}
			key := fmt.Sprintf("%s@%d:%s", *e.ObjectID, e.At.UnixMilli(), e.Payload)
			if segments[key] {
				continue
			}
			segments[key] = true
			work := byRun[*e.ObjectID]
			if e.ObjectType != "run" || work == nil || judged[*e.ObjectID] || work.Gated || work.Decision.RunRequest == nil {
				continue
			}
			req := work.Decision.RunRequest
			for _, node := range replayParallelNodes(work.Decision) {
				if node.RunRequest != nil && node.RunRequest.RunID == *e.ObjectID {
					req = node.RunRequest
					break
				}
			}
			if segment.RunID != req.RunID || segment.ItemID != req.ItemID || segment.LaneID != work.Decision.LaneID || !sessionIDShape.MatchString(segment.SessionID) || segment.ResumedAt == "" {
				continue
			}
			if segment.Request != nil && segment.Backend != req.Backend && len(work.Decision.Parallel) == 0 {
				next := *segment.Request
				if next.RunID == req.RunID && next.ItemID == req.ItemID && next.LaneID == req.LaneID && next.Backend == segment.Backend && next.Resume && next.FreshSession {
					next.Resume, next.FreshSession = false, false
					work.Decision.RunRequest = &next
					req = &next
				}
			}
			if segment.Backend != req.Backend {
				continue
			}
			var resumingOwner *Owner
			if !work.Done {
				resumingOwner = work.Examiner
				if resumingOwner == nil {
					resumingOwner = work.Owner
				}
			}
			work.Done, work.Orphaned, work.Examining = false, false, false
			work.Owner, work.Examiner = resumingOwner, nil
		case "run.finish":
			// Replay tracks ownership; run.finish does not clear loop ownership.
		}
	}

	here, err := CurrentOwner()
	if err != nil {
		return nil, err
	}

	var (
		endedLoaded bool
		ended       map[Owner]bool
		identity    string
		liveLoaded  bool
		live        map[string]bool
	)
	getLive := func() (map[string]bool, error) {
		if liveLoaded {
			return live, nil
		}
		var err error
		live, err = LiveRunIDs(storePath)
		if err != nil {
			return nil, err
		}
		liveLoaded = true
		return live, nil
	}
	ownerIsDead := func(owner *Owner) (bool, error) {
		if owner == nil {
			return false, nil
		}
		if FromOtherDomain(owner, here) {
			if !endedLoaded {
				var err error
				ended, err = EndedSessionOwners(r)
				if err != nil {
					return false, err
				}
				identity, err = StoreIdentity(storePath)
				if err != nil {
					return false, err
				}
				endedLoaded = true
			}
			return ForeignOwnerGone(identity, *owner, *here, ended), nil
		}
		return OwnerDead(owner, here)
	}
	runIsLive := func(id, ownerDomain string) (bool, error) {
		if id == "" {
			return false, nil
		}
		if ownerDomain != "" && here != nil && ownerDomain != here.Domain {
			hasLocator := loopDir != ""
			paths := []string{
				filepath.Join(loopDir, id, "run.record.json"),
				filepath.Join(loopDir, id, "run.stderr"),
			}
			return ForeignRunLive(paths, hasLocator, ownerDomain, here.Domain), nil
		}
		liveMap, err := getLive()
		if err != nil {
			return false, err
		}
		return liveMap[id], nil
	}

	for id := range leased {
		work := byRun[id]
		if work == nil {
			continue
		}
		if work.Orphaned {
			ownerDomain := ""
			if work.Owner != nil {
				ownerDomain = work.Owner.Domain
			} else if work.Examiner != nil {
				ownerDomain = work.Examiner.Domain
			}
			isLive, err := runIsLive(id, ownerDomain)
			if err != nil {
				return nil, err
			}
			if !isLive {
				dead[id] = true
				for _, node := range replayParallelNodes(work.Decision) {
					if node.RunRequest != nil && node.RunRequest.RunID != "" {
						sibLive, err := runIsLive(node.RunRequest.RunID, ownerDomain)
						if err != nil {
							return nil, err
						}
						if !sibLive {
							dead[node.RunRequest.RunID] = true
						}
					}
				}
			}
			continue
		}
		if work.Owner == nil && work.Examiner == nil {
			continue
		}
		if work.Owner != nil {
			d, err := ownerIsDead(work.Owner)
			if err != nil {
				return nil, err
			}
			if !d {
				continue
			}
		}
		if work.Examiner != nil {
			d, err := ownerIsDead(work.Examiner)
			if err != nil {
				return nil, err
			}
			if !d {
				continue
			}
		}
		ownerDomain := ""
		if work.Owner != nil {
			ownerDomain = work.Owner.Domain
		} else if work.Examiner != nil {
			ownerDomain = work.Examiner.Domain
		}
		isLive, err := runIsLive(id, ownerDomain)
		if err != nil {
			return nil, err
		}
		if !isLive {
			dead[id] = true
			for _, node := range replayParallelNodes(work.Decision) {
				if node.RunRequest != nil && node.RunRequest.RunID != "" {
					sibLive, err := runIsLive(node.RunRequest.RunID, ownerDomain)
					if err != nil {
						return nil, err
					}
					if !sibLive {
						dead[node.RunRequest.RunID] = true
					}
				}
			}
		}
	}
	return dead, nil
}
