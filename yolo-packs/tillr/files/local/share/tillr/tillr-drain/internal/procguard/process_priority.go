package procguard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/tillr-drain/heavyrun"
	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr/store"
	"golang.org/x/sys/unix"
)

const (
	defaultCgroupMountRoot    = "/sys/fs/cgroup"
	defaultProcSelfCgroupPath = "/proc/self/cgroup"
)

// ProcessClass identifies one of tillr's four processor-share classes.
type ProcessClass string

const (
	ProcessClassRepair ProcessClass = "repair"
	ProcessClassProof  ProcessClass = "proof"
	ProcessClassGate   ProcessClass = "gate"
	ProcessClassWorker ProcessClass = "worker"
)

var ProcessClassOrder = []ProcessClass{
	ProcessClassRepair,
	ProcessClassProof,
	ProcessClassGate,
	ProcessClassWorker,
}

var processClassWeights = map[ProcessClass]int{
	ProcessClassRepair: 400,
	ProcessClassProof:  200,
	ProcessClassGate:   100,
	ProcessClassWorker: 50,
}

var ProcessClassNiceOffsets = map[ProcessClass]int{
	ProcessClassRepair: 0,
	ProcessClassProof:  5,
	ProcessClassGate:   10,
	ProcessClassWorker: 15,
}

// ProcessPrioritySummary is the durable JSON summary persisted on drain_sessions
// and reported by `tillr drain why`.
type ProcessPrioritySummary struct {
	Mechanism         string         `json:"mechanism"`
	CgroupRoot        string         `json:"cgroup_root,omitempty"`
	UnavailableReason string         `json:"unavailable_reason,omitempty"`
	Classes           map[string]int `json:"classes"`
}

// ProcessPriority holds the loop's startup-selected priority mechanism
// (either cgroup v2 cpu.weight subgroup descriptors or POSIX nice targets).
type ProcessPriority struct {
	summary     ProcessPrioritySummary
	cgroupFDs   map[ProcessClass]*os.File
	niceTargets map[ProcessClass]int
}

func init() {
	if len(os.Args) > 1 && os.Args[1] == "--tillr-exec-nice" {
		defer store.FlushSQLiteTraceAtExit()
		code := execWithNice(os.Args[2:])
		store.FlushSQLiteTraceAtExit()
		os.Exit(code)
	}
}

func execWithNice(args []string) int {
	if len(args) < 3 || args[1] != "--" {
		fmt.Fprintln(os.Stderr, "tillr-exec-nice: usage: --tillr-exec-nice <nice> -- <argv...>")
		return 2
	}
	target, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-exec-nice: invalid nice %q: %v\n", args[0], err)
		return 2
	}
	if err := unix.Setpriority(unix.PRIO_PROCESS, 0, target); err != nil {
		fmt.Fprintf(os.Stderr, "tillr-exec-nice: setpriority(%d): %v\n", target, err)
		return 4
	}
	argv := args[2:]
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-exec-nice: lookpath %s: %v\n", argv[0], err)
		return 127
	}
	if err := syscall.Exec(bin, argv, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "tillr-exec-nice: exec %s: %v\n", bin, err)
		return 126
	}
	return 0
}

// InitProcessPriority probes cgroup v2 cpu.weight subgroup support once at loop
// startup and falls back to POSIX nice when cgroup subgroups cannot be created.
func InitProcessPriority(storePath, cgroupMount, procSelfCgroup string) *ProcessPriority {
	if cgroupMount == "" {
		cgroupMount = defaultCgroupMountRoot
	}
	if procSelfCgroup == "" {
		procSelfCgroup = defaultProcSelfCgroupPath
	}

	p, failPath, failErr := tryInitCgroupPriority(storePath, cgroupMount, procSelfCgroup)
	if failErr == nil {
		return p
	}
	return NewNicePriority(fmt.Sprintf("%s: %v", failPath, failErr))
}

func currentProcessNice() int {
	raw, err := unix.Getpriority(unix.PRIO_PROCESS, 0)
	if err != nil {
		return 0
	}
	if runtime.GOOS == "linux" {
		// Linux sys_getpriority returns 20 - nice (1..40 for nice 19..-20);
		// golang.org/x/sys/unix returns that raw kernel value without conversion.
		return 20 - raw
	}
	return raw
}

func NewNicePriority(unavailableReason string) *ProcessPriority {
	return NewNicePriorityWithBase(currentProcessNice(), unavailableReason)
}

func NewNicePriorityWithBase(base int, unavailableReason string) *ProcessPriority {
	targets := make(map[ProcessClass]int, len(ProcessClassOrder))
	classes := make(map[string]int, len(ProcessClassOrder))
	for _, class := range ProcessClassOrder {
		target := min(19, base+ProcessClassNiceOffsets[class])
		targets[class] = target
		classes[string(class)] = target
	}
	return &ProcessPriority{
		summary: ProcessPrioritySummary{
			Mechanism:         "nice",
			UnavailableReason: unavailableReason,
			Classes:           classes,
		},
		niceTargets: targets,
	}
}

func tryInitCgroupPriority(storePath, cgroupMount, procSelfCgroup string) (*ProcessPriority, string, error) {
	relPath, err := readUnifiedCgroupRelPath(procSelfCgroup)
	if err != nil {
		return nil, procSelfCgroup, err
	}
	ownCG := cgroupMount
	if trimmed := strings.TrimPrefix(relPath, "/"); trimmed != "" {
		ownCG = filepath.Join(cgroupMount, trimmed)
	}
	controllersPath := filepath.Join(ownCG, "cgroup.controllers")
	controllersRaw, err := os.ReadFile(controllersPath)
	if err != nil {
		return nil, controllersPath, err
	}
	if !hasController(string(controllersRaw), "cpu") {
		return nil, controllersPath, errors.New("cpu controller not available")
	}

	rootName := "tillr-" + StorePriorityHash(storePath)
	var createdDirs []string
	cleanupDirs := func() {
		for i := len(createdDirs) - 1; i >= 0; i-- {
			_ = os.Remove(createdDirs[i])
		}
	}

	var cgRoot string
	alreadyDelegated := false
	if ownCG != cgroupMount {
		parentCG := filepath.Dir(ownCG)
		if parentSubtree, readErr := os.ReadFile(filepath.Join(parentCG, "cgroup.subtree_control")); readErr == nil && hasController(string(parentSubtree), "cpu") {
			alreadyDelegated = true
			cgRoot = filepath.Join(parentCG, rootName)
		}
	}

	if alreadyDelegated {
		if err := ensureDirRecorded(cgRoot, &createdDirs); err != nil {
			cleanupDirs()
			return nil, cgRoot, err
		}
		subtreePath := filepath.Join(cgRoot, "cgroup.subtree_control")
		if err := writeCgroupFile(subtreePath, "+cpu"); err != nil {
			cleanupDirs()
			return nil, subtreePath, err
		}
	} else {
		supervisorCG := filepath.Join(ownCG, "supervisor")
		if err := ensureDirRecorded(supervisorCG, &createdDirs); err != nil {
			cleanupDirs()
			return nil, supervisorCG, err
		}
		ownProcsPath := filepath.Join(ownCG, "cgroup.procs")
		procsRaw, err := os.ReadFile(ownProcsPath)
		if err != nil {
			cleanupDirs()
			return nil, ownProcsPath, err
		}
		supProcsPath := filepath.Join(supervisorCG, "cgroup.procs")
		for _, field := range strings.Fields(string(procsRaw)) {
			if err := appendCgroupFile(supProcsPath, field+"\n"); err != nil {
				cleanupDirs()
				return nil, supProcsPath, err
			}
		}
		ownSubtreePath := filepath.Join(ownCG, "cgroup.subtree_control")
		if err := writeCgroupFile(ownSubtreePath, "+cpu"); err != nil {
			cleanupDirs()
			return nil, ownSubtreePath, err
		}
		cgRoot = filepath.Join(ownCG, rootName)
		if err := ensureDirRecorded(cgRoot, &createdDirs); err != nil {
			cleanupDirs()
			return nil, cgRoot, err
		}
		subtreePath := filepath.Join(cgRoot, "cgroup.subtree_control")
		if err := writeCgroupFile(subtreePath, "+cpu"); err != nil {
			cleanupDirs()
			return nil, subtreePath, err
		}
	}

	fds := make(map[ProcessClass]*os.File, len(ProcessClassOrder))
	classes := make(map[string]int, len(ProcessClassOrder))
	closeFDs := func() {
		for _, f := range fds {
			_ = f.Close()
		}
	}

	for _, class := range ProcessClassOrder {
		classDir := filepath.Join(cgRoot, string(class))
		if err := ensureDirRecorded(classDir, &createdDirs); err != nil {
			closeFDs()
			cleanupDirs()
			return nil, classDir, err
		}
		weight := processClassWeights[class]
		weightPath := filepath.Join(classDir, "cpu.weight")
		if err := writeCgroupFile(weightPath, strconv.Itoa(weight)); err != nil {
			closeFDs()
			cleanupDirs()
			return nil, weightPath, err
		}
		fd, err := unix.Open(classDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			closeFDs()
			cleanupDirs()
			return nil, classDir, err
		}
		fds[class] = os.NewFile(uintptr(fd), classDir)
		classes[string(class)] = weight
	}

	return &ProcessPriority{
		summary: ProcessPrioritySummary{
			Mechanism:  "cgroup",
			CgroupRoot: cgRoot,
			Classes:    classes,
		},
		cgroupFDs: fds,
	}, "", nil
}

func readUnifiedCgroupRelPath(procSelfCgroup string) (string, error) {
	raw, err := os.ReadFile(procSelfCgroup)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "0::") {
			return strings.TrimPrefix(line, "0::"), nil
		}
	}
	return "", errors.New("unified cgroup v2 hierarchy (0::) not found")
}

func hasController(content, name string) bool {
	for _, field := range strings.Fields(content) {
		if field == name {
			return true
		}
	}
	return false
}

func StorePriorityHash(storePath string) string {
	identity := storePath
	if storePath != "" {
		if resolved, err := ProcessStoreIdentity(storePath); err == nil && resolved != "" {
			identity = resolved
		}
	}
	if identity == "" {
		identity = "default"
	}
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])[:12]
}

func ensureDirRecorded(path string, created *[]string) error {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return nil
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	*created = append(*created, path)
	return nil
}

func writeCgroupFile(path, value string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString(value)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func appendCgroupFile(path, value string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString(value)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// Summary returns the JSON-serializable summary of the active priority policy.
func (p *ProcessPriority) Summary() ProcessPrioritySummary {
	if p == nil {
		return ProcessPrioritySummary{}
	}
	classes := make(map[string]int, len(p.summary.Classes))
	for k, v := range p.summary.Classes {
		classes[k] = v
	}
	out := p.summary
	out.Classes = classes
	return out
}

// LogLine returns the single startup log line specified by gate-test-capacity.md §4.3.
func (p *ProcessPriority) LogLine() string {
	if p == nil {
		return ""
	}
	if p.summary.Mechanism == "cgroup" {
		return fmt.Sprintf(
			"loop: process priority: cgroup v2 (%s; cpu.weight repair=%d proof=%d gate=%d worker=%d)",
			p.summary.CgroupRoot,
			p.summary.Classes[string(ProcessClassRepair)],
			p.summary.Classes[string(ProcessClassProof)],
			p.summary.Classes[string(ProcessClassGate)],
			p.summary.Classes[string(ProcessClassWorker)],
		)
	}
	return fmt.Sprintf(
		"loop: process priority: nice (repair=%d proof=%d gate=%d worker=%d; cgroup unavailable: %s)",
		p.summary.Classes[string(ProcessClassRepair)],
		p.summary.Classes[string(ProcessClassProof)],
		p.summary.Classes[string(ProcessClassGate)],
		p.summary.Classes[string(ProcessClassWorker)],
		p.summary.UnavailableReason,
	)
}

// Close releases any open cgroup directory file descriptors.
func (p *ProcessPriority) Close() error {
	if p == nil {
		return nil
	}
	var firstErr error
	for class, f := range p.cgroupFDs {
		if f != nil {
			if err := f.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		delete(p.cgroupFDs, class)
	}
	return firstErr
}

// Mechanism returns "cgroup" or "nice", or "" if p is nil.
func (p *ProcessPriority) Mechanism() string {
	if p == nil {
		return ""
	}
	return p.summary.Mechanism
}

// CgroupFD returns the open cgroup directory file descriptor for class when
// cgroup is in force. If cgroup was selected at startup and the descriptor is
// missing, it returns an error naming <cg_root>/<class>.
func (p *ProcessPriority) CgroupFD(class ProcessClass) (int, error) {
	if p == nil || p.summary.Mechanism != "cgroup" {
		return -1, nil
	}
	f := p.cgroupFDs[class]
	if f == nil {
		return -1, drainmodel.Externalf("missing cgroup descriptor for %s", filepath.Join(p.summary.CgroupRoot, string(class)))
	}
	return int(f.Fd()), nil
}

// DropCgroupFDForTest removes a single class's cgroup descriptor to test
// missing-descriptor error handling from outside package procguard.
func (p *ProcessPriority) DropCgroupFDForTest(class ProcessClass) {
	if p != nil {
		delete(p.cgroupFDs, class)
	}
}

// NiceTarget returns the target niceness for class when nice is in force.
func (p *ProcessPriority) NiceTarget(class ProcessClass) (int, bool) {
	if p == nil || p.summary.Mechanism != "nice" {
		return 0, false
	}
	target, ok := p.niceTargets[class]
	return target, ok
}

// ApplyToCmd configures cmd to start in class under the active mechanism.
// Under cgroup, it sets SysProcAttr.UseCgroupFD and CgroupFD (preserving other
// SysProcAttr fields). Under nice, it wraps cmd with `--tillr-exec-nice <target> --`
// using selfExe (or os.Executable() when empty).
func (p *ProcessPriority) ApplyToCmd(cmd *exec.Cmd, class ProcessClass, selfExe string) error {
	if p == nil || cmd == nil {
		return nil
	}
	switch p.summary.Mechanism {
	case "cgroup":
		fd, err := p.CgroupFD(class)
		if err != nil {
			return err
		}
		var attr syscall.SysProcAttr
		if cmd.SysProcAttr != nil {
			attr = *cmd.SysProcAttr
		}
		attr.UseCgroupFD = true
		attr.CgroupFD = fd
		cmd.SysProcAttr = &attr
		return nil
	case "nice":
		target, ok := p.NiceTarget(class)
		if !ok {
			return nil
		}
		if selfExe == "" {
			exe, err := os.Executable()
			if err != nil {
				return drainmodel.Externalf("locating executable for nice wrapper: %v", err)
			}
			selfExe = exe
		}
		origArgs := cmd.Args
		if len(origArgs) == 0 {
			origArgs = []string{cmd.Path}
		}
		wrapped := make([]string, 0, len(origArgs)+4)
		wrapped = append(wrapped, selfExe, "--tillr-exec-nice", strconv.Itoa(target), "--")
		wrapped = append(wrapped, origArgs...)
		cmd.Path = selfExe
		cmd.Args = wrapped
		return nil
	default:
		return nil
	}
}

type HeavyRepairKey struct{}

// ApplyHeavyToCmd configures cmd for heavy proof (or repair when HeavyRepairKey is set on ctx).
func (p *ProcessPriority) ApplyHeavyToCmd(ctx context.Context, cmd *exec.Cmd) error {
	if p == nil || cmd == nil {
		return nil
	}
	class := ProcessClassProof
	if repair, _ := ctx.Value(HeavyRepairKey{}).(bool); repair {
		class = ProcessClassRepair
	}
	switch p.Mechanism() {
	case "cgroup":
		fd, err := p.CgroupFD(class)
		if err != nil {
			return err
		}
		var attr syscall.SysProcAttr
		if cmd.SysProcAttr != nil {
			attr = *cmd.SysProcAttr
		}
		attr.UseCgroupFD = true
		attr.CgroupFD = fd
		cmd.SysProcAttr = &attr
		return nil
	case "nice":
		target, ok := p.NiceTarget(class)
		if !ok {
			return nil
		}
		env := cmd.Env
		if env == nil {
			env = os.Environ()
		}
		cmd.Env = append(WithoutProcessEnv(env, heavyrun.HeavyNiceEnv), fmt.Sprintf("%s=%d", heavyrun.HeavyNiceEnv, target))
		return nil
	default:
		return nil
	}
}
