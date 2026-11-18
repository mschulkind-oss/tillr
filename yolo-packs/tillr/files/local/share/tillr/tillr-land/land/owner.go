package land

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RecoveryPrefix is the ref namespace of the owner's recovery copy. It is its own prefix
// under refs/tillr/: refs/stash belongs to the owner, and every other refs/tillr/ prefix
// already has a meaning and a lifetime.
const RecoveryPrefix = "refs/tillr/owner-recovery/"

// FaultKind is the event a landing appends when it finds, after the trunk moved, that the
// owner saved bytes the lander could not carry into the new file.
const FaultKind = "land.fault"

// ErrOwnerChanged is the answer of the final comparison: a touched path no longer holds what
// the prediction read. Nothing was changed; the caller predicts again.
var ErrOwnerChanged = errors.New("the owner changed a touched path after the prediction")

// OwnerHooks are the test seams of the owner overlay. Every field is nil in production.
// A race a test needs is forced by a hook that runs at the exact step, never by timing.
var OwnerHooks struct {
	// GitCall sees every git command the trunk runs, in order.
	GitCall func(dir string, args []string)
	// TxOpened runs when the store's write transaction opens.
	TxOpened func()
	// AfterPredict runs when the prediction and the recovery copy are complete.
	AfterPredict func()
	// AfterReplace runs after one path was replaced or removed.
	AfterReplace func(path string)
	// AfterInstall runs after every path was replaced and before the comparison reads them.
	AfterInstall func()
	// BeforeTrunkMove runs before the trunk ref moves.
	BeforeTrunkMove func()
	// AfterTrunkMove runs after the trunk ref moved.
	AfterTrunkMove func()
	// BeforeLastRead runs immediately before the last read of the replaced files.
	BeforeLastRead func()
	// BeforeUndoRead runs before undoing reads the file at path.
	BeforeUndoRead func(path string)
	// InstallFault answers an error to fail the install of path with.
	InstallFault func(path string) error
	// BeforeSwap runs for path after its parent directory was opened and before the exchange.
	BeforeSwap func(path string)
	// BeforeRollbackSwap runs before an undo exchange of path; step 1 is the restoring
	// exchange, step 2 and later are corrective ones.
	BeforeRollbackSwap func(path string, step int)
	// FailDisplacedRead answers an error for reading the displaced inode named name.
	FailDisplacedRead func(name string) error
	// FailRollbackSwap answers an error for the restoring exchange of path.
	FailRollbackSwap func(path string) error
	// FailIndexRestore answers an error for restoring the index.
	FailIndexRestore func() error
	// FailFaultEvent answers an error for writing a fault event.
	FailFaultEvent func() error
	// BeforeIndexLock runs before the lander takes the checkout's index lock.
	BeforeIndexLock func()
	// FailRestoreExchange answers an error for the exchange that undoes a failed displacement.
	FailRestoreExchange func(name string) error
	// FailUndoInspect answers an error for reading path while an install is undone.
	FailUndoInspect func(path string) error
	// AfterParentCheck runs after the last identity check of a path's parent and before the swap.
	AfterParentCheck func(path string)
	// FailComparison answers true to make the final comparison report an owner's change.
	FailComparison func() bool
}

// ResetOwnerHooks clears every hook.
func ResetOwnerHooks() {
	OwnerHooks.GitCall, OwnerHooks.TxOpened, OwnerHooks.AfterPredict = nil, nil, nil
	OwnerHooks.AfterReplace, OwnerHooks.AfterInstall, OwnerHooks.AfterTrunkMove = nil, nil, nil
	OwnerHooks.BeforeTrunkMove = nil
	OwnerHooks.BeforeSwap, OwnerHooks.BeforeRollbackSwap, OwnerHooks.FailDisplacedRead = nil, nil, nil
	OwnerHooks.FailRollbackSwap, OwnerHooks.FailIndexRestore, OwnerHooks.FailFaultEvent = nil, nil, nil
	OwnerHooks.BeforeIndexLock, OwnerHooks.FailComparison = nil, nil
	OwnerHooks.FailRestoreExchange, OwnerHooks.FailUndoInspect, OwnerHooks.AfterParentCheck = nil, nil, nil
	OwnerHooks.BeforeLastRead, OwnerHooks.BeforeUndoRead, OwnerHooks.InstallFault = nil, nil, nil
}

// entry is a git index/tree entry: a mode and a blob. The zero entry is absent.
type entry struct{ Mode, OID string }

func (e entry) absent() bool { return e.OID == "" }

// diskState is what a path holds in the main checkout.
type diskState struct {
	Kind    string // "", "file", "symlink", "dir"
	Exec    bool
	Bytes   []byte // file bytes, or the symlink target
	Listing string // for a dir: every non-directory entry beneath it
	Parents string // the kinds of every ancestor directory
}

func (d diskState) equal(o diskState) bool {
	return d.Kind == o.Kind && d.Exec == o.Exec && bytes.Equal(d.Bytes, o.Bytes) && d.Listing == o.Listing && d.Parents == o.Parents
}

// ownerPath is one path the landing changes, with everything prepared for it.
type ownerPath struct {
	Path         string
	Head, Land   entry // the commits' entries
	Disk         diskState
	DiskEntry    entry // the disk in stored form
	IndexNow     entry
	IndexFinal   entry
	FinalContent []byte // checked-out form, for Install
	FinalExec    bool
	FinalSymlink bool
	Install      bool // the path is rewritten
	Remove       bool // the path is removed
	OwnerContent bool
	DirInTheWay  bool
	savedStaged  entry
}

// OwnerPlan is the prediction for one landing: everything Install does, decided before the
// store's write transaction opens, and the recovery copy of the owner's content.
type OwnerPlan struct {
	Worktree  string
	Ref       string
	From, To  string
	Paths     []*ownerPath
	Conflicts []string
	// Busy names a merge, rebase or cherry-pick in progress in the main checkout.
	Busy        string
	RecoveryRef string
	// Faults are stale recovery refs reported by this landing.
	Faults []string
	runID  string
	// GitDir and HeadText identify the branch the checkout had when the prediction read it.
	GitDir, HeadText string
}

// readings is a digest of everything the prediction read that the owner can change: the branch,
// an operation in progress, and each touched path's content on disk and index entry. Two
// predictions with the same readings saw the same checkout.
func (p *OwnerPlan) readings() string {
	h := sha256.New()
	field := func(parts ...string) {
		for _, part := range parts {
			fmt.Fprintf(h, "%d:%s,", len(part), part)
		}
	}
	field(p.Worktree, p.HeadText, p.Busy, p.From, p.To)
	for _, op := range p.Paths {
		field(op.Path, op.Disk.Kind, fmt.Sprint(op.Disk.Exec), string(op.Disk.Bytes), op.Disk.Listing, op.Disk.Parents, op.IndexNow.Mode, op.IndexNow.OID)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Held answers whether the plan pauses the landing, and the paths or state to name.
func (p *OwnerPlan) Held() []string {
	if p == nil {
		return nil
	}
	if p.Busy != "" {
		return []string{p.Busy}
	}
	return p.Conflicts
}

// HasOwnerContent answers whether any touched path carries the owner's content.
func (p *OwnerPlan) HasOwnerContent() bool {
	if p == nil {
		return false
	}
	for _, path := range p.Paths {
		if path.OwnerContent {
			return true
		}
	}
	return false
}

func (t *Trunk) gitStdin(dir string, stdin []byte, env []string, args ...string) (string, error) {
	out, stderr, err := t.runGitStdin(dir, stdin, env, args...)
	if err != nil {
		return "", errors.New(failureText(stderr, err))
	}
	return out, nil
}

func absentOr(e entry) string {
	if e.absent() {
		return "absent"
	}
	return e.Mode + " " + e.OID
}

// OwnerBusy answers the in-progress state the worktree is in, or "".
func (t *Trunk) ownerBusy(worktree string) (string, error) {
	dir, err := t.gitIn(worktree, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return "", Externalf("reading the git dir of %s: %v", worktree, err)
	}
	dir = strings.TrimSpace(dir)
	for _, state := range []struct{ file, name string }{
		{"MERGE_HEAD", "merge in progress"},
		{"rebase-merge", "rebase in progress"},
		{"rebase-apply", "rebase in progress"},
		{"CHERRY_PICK_HEAD", "cherry-pick in progress"},
		{"REVERT_HEAD", "revert in progress"},
	} {
		if _, err := os.Lstat(filepath.Join(dir, state.file)); err == nil {
			return state.name, nil
		}
	}
	return "", nil
}

type rawChange struct {
	path     string
	old, new entry
}

func (t *Trunk) changes(from, to string) ([]rawChange, error) {
	out, err := t.git("diff-tree", "-r", "--no-renames", "-z", "--raw", from, to)
	if err != nil {
		return nil, Externalf("reading the paths %s..%s change in %s: %v", from, to, t.repo, err)
	}
	fields := strings.Split(out, "\x00")
	var changes []rawChange
	for i := 0; i+1 < len(fields); i += 2 {
		meta := strings.Fields(strings.TrimPrefix(fields[i], ":"))
		if len(meta) < 5 {
			continue
		}
		c := rawChange{path: fields[i+1]}
		if strings.Trim(meta[2], "0") != "" {
			c.old = entry{meta[0], meta[2]}
		}
		if strings.Trim(meta[3], "0") != "" {
			c.new = entry{meta[1], meta[3]}
		}
		changes = append(changes, c)
	}
	return changes, nil
}

func (t *Trunk) indexEntries(worktree string, paths []string) (map[string]entry, error) {
	entries := map[string]entry{}
	if len(paths) == 0 {
		return entries, nil
	}
	out, err := t.gitIn(worktree, append([]string{"ls-files", "-s", "-z", "--"}, paths...)...)
	if err != nil {
		return nil, Externalf("reading the index of %s: %v", worktree, err)
	}
	for _, rec := range strings.Split(out, "\x00") {
		meta, path, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) == 3 && f[2] == "0" {
			entries[path] = entry{f[0], f[1]}
		}
	}
	return entries, nil
}

// readDisk reads what a path holds in the checkout. It never follows a symlink.
func readDisk(worktree, path string) (diskState, error) {
	var parents []string
	parts := strings.Split(path, "/")
	for i := 1; i < len(parts); i++ {
		info, err := os.Lstat(filepath.Join(worktree, filepath.Join(parts[:i]...)))
		switch {
		case err != nil:
			parents = append(parents, "-")
		case info.Mode()&os.ModeSymlink != 0:
			parents = append(parents, "l")
		case info.IsDir():
			parents = append(parents, "d")
		default:
			parents = append(parents, "f")
		}
	}
	d := diskState{Parents: strings.Join(parents, "")}
	abs := filepath.Join(worktree, path)
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) || errors.Is(err, syscallENOTDIR) {
			return d, nil
		}
		return d, Externalf("reading %s: %v", abs, err)
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(abs)
		if err != nil {
			return d, Externalf("reading %s: %v", abs, err)
		}
		d.Kind, d.Bytes = "symlink", []byte(target)
	case info.IsDir():
		d.Kind = "dir"
		var files []string
		err := filepath.WalkDir(abs, func(p string, e os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !e.IsDir() {
				rel, _ := filepath.Rel(worktree, p)
				files = append(files, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			return d, Externalf("reading %s: %v", abs, err)
		}
		sort.Strings(files)
		d.Listing = strings.Join(files, "\n")
	default:
		b, err := os.ReadFile(abs)
		if err != nil {
			return d, Externalf("reading %s: %v", abs, err)
		}
		d.Kind, d.Exec, d.Bytes = "file", info.Mode()&0o111 != 0, b
	}
	return d, nil
}

// storedEntry converts a disk state to the entry Git would store for it: clean and EOL
// conversions apply, so a clean checked-out file equals its blob.
func (t *Trunk) storedEntry(worktree, path string, d diskState) (entry, error) {
	switch d.Kind {
	case "file":
		oid, err := t.gitStdin(worktree, d.Bytes, nil, "hash-object", "-w", "--path="+path, "--stdin")
		if err != nil {
			return entry{}, Externalf("hashing %s: %v", path, err)
		}
		mode := "100644"
		if d.Exec {
			mode = "100755"
		}
		return entry{mode, strings.TrimSpace(oid)}, nil
	case "symlink":
		oid, err := t.gitStdin(worktree, d.Bytes, nil, "hash-object", "-w", "--stdin")
		if err != nil {
			return entry{}, Externalf("hashing %s: %v", path, err)
		}
		return entry{"120000", strings.TrimSpace(oid)}, nil
	}
	return entry{}, nil
}

func (t *Trunk) isBinary(worktree, path, attrSource string, blobs ...entry) (bool, error) {
	out, err := t.gitIn(worktree, "check-attr", "--source="+attrSource, "-z", "diff", "merge", "binary", "--", path)
	if err != nil {
		return false, Externalf("reading the attributes of %s: %v", path, err)
	}
	f := strings.Split(out, "\x00")
	for i := 0; i+2 < len(f); i += 3 {
		if ((f[i+1] == "diff" || f[i+1] == "merge") && f[i+2] == "unset") || (f[i+1] == "binary" && f[i+2] == "set") {
			return true, nil
		}
	}
	for _, b := range blobs {
		if b.absent() {
			continue
		}
		content, err := t.gitIn(worktree, "cat-file", "blob", b.OID)
		if err != nil {
			return false, Externalf("reading blob %s: %v", b.OID, err)
		}
		head := content
		if len(head) > 8000 {
			head = head[:8000]
		}
		if strings.Contains(head, "\x00") {
			return true, nil
		}
	}
	return false, nil
}

// merge3 is the three-way merge of one path's entries, ours being the landing's and theirs the
// owner's. conflict is true when no result exists without markers.
func (t *Trunk) merge3(worktree, path, attrSource string, base, ours, theirs entry) (entry, bool, error) {
	switch {
	case theirs == base || theirs == ours:
		return ours, false, nil
	case ours == base:
		return theirs, false, nil
	case base.absent() || ours.absent() || theirs.absent():
		return entry{}, true, nil
	}
	for _, e := range []entry{base, ours, theirs} {
		if e.Mode == "120000" || e.Mode == "160000" {
			return entry{}, true, nil
		}
	}
	mode := ours.Mode
	switch {
	case ours.Mode == theirs.Mode:
	case ours.Mode == base.Mode:
		mode = theirs.Mode
	case theirs.Mode == base.Mode:
	default:
		return entry{}, true, nil
	}
	oid := ""
	switch {
	case ours.OID == theirs.OID:
		oid = ours.OID
	case theirs.OID == base.OID:
		oid = ours.OID
	case ours.OID == base.OID:
		oid = theirs.OID
	default:
		binary, err := t.isBinary(worktree, path, attrSource, base, ours, theirs)
		if err != nil {
			return entry{}, false, err
		}
		if binary {
			return entry{}, true, nil
		}
		merged, conflict, err := t.mergeFile(worktree, base, ours, theirs)
		if err != nil || conflict {
			return entry{}, conflict, err
		}
		oid = merged
	}
	return entry{mode, oid}, false, nil
}

func (t *Trunk) mergeFile(worktree string, base, ours, theirs entry) (string, bool, error) {
	dir, err := os.MkdirTemp("", "tillr-merge-")
	if err != nil {
		return "", false, Externalf("creating a merge scratch directory: %v", err)
	}
	defer os.RemoveAll(dir)
	var files []string
	for i, e := range []entry{ours, base, theirs} {
		content, err := t.gitIn(worktree, "cat-file", "blob", e.OID)
		if err != nil {
			return "", false, Externalf("reading blob %s: %v", e.OID, err)
		}
		name := filepath.Join(dir, fmt.Sprint(i))
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			return "", false, Externalf("writing %s: %v", name, err)
		}
		files = append(files, name)
	}
	out, stderr, err := t.runGitIn(worktree, nil, append([]string{"merge-file", "-p"}, files...)...)
	if err != nil {
		var exit interface{ ExitCode() int }
		if errors.As(err, &exit) && exit.ExitCode() > 0 && exit.ExitCode() < 127 {
			return "", true, nil
		}
		return "", false, Externalf("merging: %s", failureText(stderr, err))
	}
	oid, err := t.gitStdin(worktree, []byte(out), nil, "hash-object", "-w", "--stdin")
	if err != nil {
		return "", false, Externalf("hashing the merge: %v", err)
	}
	return strings.TrimSpace(oid), false, nil
}

// PlanOwner predicts what moving ref from from to to does to the main checkout that holds
// it, and writes the recovery copy of the owner's content on the touched paths. It changes
// nothing the owner can see: no working-tree file, index entry or owner ref is written, only
// objects and the recovery ref. It answers nil when no checkout holds the ref.
func (t *Trunk) PlanOwner(ref, runID, from, to string) (*OwnerPlan, error) {
	return t.planOwner(ref, runID, from, to, true)
}

// PredictOwner answers the paths, or the in-progress state, on which landing the tree to onto
// from would pause because of the owner's working tree. It writes no ref and no recovery copy,
// so a waiting drain asks it as often as it likes.
func (t *Trunk) PredictOwner(ref, from, to string) ([]string, error) {
	plan, err := t.planOwner(ref, "", from, to, false)
	if err != nil {
		return nil, err
	}
	return plan.Held(), nil
}

func (t *Trunk) planOwner(ref, runID, from, to string, record bool) (*OwnerPlan, error) {
	worktree, err := t.WorktreeFor(ref)
	if err != nil || worktree == "" {
		return nil, err
	}
	plan := &OwnerPlan{Worktree: worktree, Ref: ref, From: from, To: to, runID: runID}
	if plan.GitDir, plan.HeadText, err = t.headIdentity(worktree); err != nil {
		return nil, err
	}
	if record {
		stale, err := t.settleRecovery(worktree)
		if err != nil {
			return nil, err
		}
		for _, f := range stale {
			plan.Faults = append(plan.Faults, f.Text)
		}
	}
	if plan.Busy, err = t.ownerBusy(worktree); err != nil || plan.Busy != "" {
		return plan, err
	}
	changes, err := t.changes(from, to)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, c := range changes {
		names = append(names, c.path)
	}
	index, err := t.indexEntries(worktree, names)
	if err != nil {
		return nil, err
	}
	removed := map[string]bool{}
	for _, c := range changes {
		if c.new.absent() {
			removed[c.path] = true
		}
	}
	conflicts := map[string]bool{}
	var inWay []string
	for _, c := range changes {
		p := &ownerPath{Path: c.path, Head: c.old, Land: c.new, IndexNow: index[c.path]}
		if p.Disk, err = readDisk(worktree, c.path); err != nil {
			return nil, err
		}
		if strings.Contains(p.Disk.Parents, "l") {
			conflicts[c.path] = true
		}
		for i, kind := range p.Disk.Parents {
			parent := strings.Join(strings.Split(c.path, "/")[:i+1], "/")
			if kind == 'f' && !removed[parent] {
				conflicts[c.path] = true
			}
		}
		if c.old.Mode == "160000" || c.new.Mode == "160000" {
			conflicts[c.path] = true
		}
		diskIsDir := p.Disk.Kind == "dir"
		if diskIsDir && !c.new.absent() {
			var left []string
			for _, f := range strings.Split(p.Disk.Listing, "\n") {
				if f != "" && !removed[f] {
					left = append(left, f)
				}
			}
			if len(left) > 0 {
				conflicts[c.path] = true
				inWay = append(inWay, left...)
			}
			p.DirInTheWay = true
			p.DiskEntry = entry{}
		} else if p.DiskEntry, err = t.storedEntry(worktree, c.path, p.Disk); err != nil {
			return nil, err
		}
		p.OwnerContent = p.DiskEntry != c.old || p.IndexNow != c.old
		if conflicts[c.path] {
			plan.Paths = append(plan.Paths, p)
			continue
		}
		wfin, wconf, err := t.merge3(worktree, c.path, to, c.old, c.new, p.DiskEntry)
		if err != nil {
			return nil, err
		}
		ifin, iconf, err := t.merge3(worktree, c.path, to, c.old, c.new, p.IndexNow)
		if err != nil {
			return nil, err
		}
		if wconf || iconf {
			conflicts[c.path] = true
			plan.Paths = append(plan.Paths, p)
			continue
		}
		p.IndexFinal = ifin
		switch {
		case wfin.absent():
			p.Remove = p.Disk.Kind != ""
		case wfin != p.DiskEntry:
			content, err := t.gitIn(worktree, "--attr-source="+to, "cat-file", "--filters", "--path="+c.path, wfin.OID)
			if err != nil {
				return nil, Externalf("reading the checked-out form of %s: %v", c.path, err)
			}
			p.Install, p.FinalContent, p.FinalExec = true, []byte(content), wfin.Mode == "100755"
			if wfin.Mode == "120000" {
				p.FinalSymlink = true
				blob, err := t.gitIn(worktree, "cat-file", "blob", wfin.OID)
				if err != nil {
					return nil, Externalf("reading %s: %v", wfin.OID, err)
				}
				p.FinalContent = []byte(blob)
			}
		}
		plan.Paths = append(plan.Paths, p)
	}
	for path := range conflicts {
		plan.Conflicts = append(plan.Conflicts, path)
	}
	plan.Conflicts = append(plan.Conflicts, inWay...)
	sort.Strings(plan.Conflicts)
	if len(plan.Conflicts) > 0 || !record {
		return plan, nil
	}
	if err := t.writeRecovery(plan); err != nil {
		return nil, err
	}
	if OwnerHooks.AfterPredict != nil {
		OwnerHooks.AfterPredict()
	}
	return plan, nil
}

// recoveryName is the ref of one preparation's copy. The name is unique per preparation, so
// a copy a crashed landing of the same run left is never overwritten or removed by a retry.
func recoveryName(kind, runID string) string {
	return RecoveryPrefix + kind + "/" + runID + "-" + randomHex(6)
}

// writeRecovery copies the owner's uncommitted content on the touched paths to the
// recovery ref: the working-tree bytes under worktree/, a differing staged blob under
// index/. Nothing is removed from the checkout.
func (t *Trunk) writeRecovery(plan *OwnerPlan) error {
	var lines []string
	for _, p := range plan.Paths {
		if !p.OwnerContent {
			continue
		}
		if p.Disk.Kind == "file" || p.Disk.Kind == "symlink" {
			oid, err := t.gitStdin(plan.Worktree, p.Disk.Bytes, nil, "hash-object", "-w", "--stdin")
			if err != nil {
				return Externalf("writing the recovery copy of %s: %v", p.Path, err)
			}
			mode := "100644"
			if p.Disk.Kind == "symlink" {
				mode = "120000"
			}
			lines = append(lines, mode+" "+strings.TrimSpace(oid)+"\tworktree/"+p.Path)
		}
		if p.IndexNow != p.Head && !p.IndexNow.absent() {
			lines = append(lines, p.IndexNow.Mode+" "+p.IndexNow.OID+"\tindex/"+p.Path)
		}
	}
	if len(lines) == 0 {
		return nil
	}
	ref, err := t.commitCopy(plan.Worktree, recoveryName("copy", plan.runID), lines, "owner content on paths landed by "+plan.runID)
	plan.RecoveryRef = ref
	return err
}

func (t *Trunk) commitCopy(worktree, ref string, lines []string, message string) (string, error) {
	identity, err := t.LandingIdentity()
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "tillr-recovery-")
	if err != nil {
		return "", Externalf("creating a recovery scratch directory: %v", err)
	}
	defer os.RemoveAll(dir)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(dir, "index"),
		"GIT_AUTHOR_NAME=" + identity.Name, "GIT_AUTHOR_EMAIL=" + identity.Email,
		"GIT_COMMITTER_NAME=" + identity.Name, "GIT_COMMITTER_EMAIL=" + identity.Email}
	if _, err := t.gitStdin(worktree, []byte(strings.Join(lines, "\n")+"\n"), env, "update-index", "--add", "--index-info"); err != nil {
		return "", Externalf("writing the recovery index: %v", err)
	}
	tree, err := t.gitStdin(worktree, nil, env, "write-tree")
	if err != nil {
		return "", Externalf("writing the recovery tree: %v", err)
	}
	commit, err := t.gitStdin(worktree, nil, env, "commit-tree", strings.TrimSpace(tree), "-m", message)
	if err != nil {
		return "", Externalf("writing the recovery commit: %v", err)
	}
	if _, err := t.gitIn(worktree, "update-ref", ref, strings.TrimSpace(commit)); err != nil {
		return "", Externalf("writing %s: %v", ref, err)
	}
	return ref, nil
}

// settleRecovery removes a recovery ref left by a crash when the disk still holds its
// content, and reports the ref and each path when it does not.
// ownerFault is one reported artifact: the ref and the sentence about it.
type ownerFault struct{ Ref, Text string }

func (t *Trunk) settleRecovery(worktree string) ([]ownerFault, error) {
	out, err := t.git("for-each-ref", "--format=%(refname)", RecoveryPrefix+"copy/")
	if err != nil {
		return nil, Externalf("listing %s: %v", RecoveryPrefix, err)
	}
	var faults []ownerFault
	for _, ref := range pathLines(out) {
		listing, err := t.git("ls-tree", "-r", "-z", ref)
		if err != nil {
			return nil, Externalf("reading %s: %v", ref, err)
		}
		matches := true
		var differing []string
		for _, rec := range strings.Split(listing, "\x00") {
			meta, name, ok := strings.Cut(rec, "\t")
			if !ok || !strings.HasPrefix(name, "worktree/") {
				continue
			}
			path := strings.TrimPrefix(name, "worktree/")
			d, err := readDisk(worktree, path)
			if err != nil {
				return nil, err
			}
			want, err := t.git("cat-file", "blob", strings.Fields(meta)[2])
			if err != nil {
				return nil, Externalf("reading %s: %v", ref, err)
			}
			if d.Kind == "" || !bytes.Equal(d.Bytes, []byte(want)) {
				matches = false
				differing = append(differing, path)
			}
		}
		if matches {
			if _, err := t.git("update-ref", "-d", ref); err != nil {
				return nil, Externalf("removing %s: %v", ref, err)
			}
			continue
		}
		faults = append(faults, ownerFault{ref, fmt.Sprintf("%s holds an earlier landing's copy of the owner's content; the checkout differs at %s", ref, strings.Join(differing, ", "))})
	}
	return faults, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// headIdentity answers the git dir of the checkout and the text of its HEAD: the branch it holds.
func (t *Trunk) headIdentity(worktree string) (string, string, error) {
	dir, err := t.gitIn(worktree, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return "", "", Externalf("reading the git dir of %s: %v", worktree, err)
	}
	dir = strings.TrimSpace(dir)
	head, err := os.ReadFile(filepath.Join(dir, "HEAD"))
	if err != nil {
		return "", "", Externalf("reading HEAD of %s: %v", worktree, err)
	}
	return dir, string(head), nil
}

// guard answers ErrOwnerChanged unless the checkout still holds the branch the prediction read.
// It is a file read, made before every mutation.
func (p *OwnerPlan) guard() error {
	head, err := os.ReadFile(filepath.Join(p.GitDir, "HEAD"))
	if err != nil || string(head) != p.HeadText {
		return ErrOwnerChanged
	}
	return nil
}

func (p *OwnerPlan) faults() []string {
	if p == nil {
		return nil
	}
	return p.Faults
}
