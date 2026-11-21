package land

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

// installed is one replaced path. displaced is the inode the replacement actually removed from
// the path, held open until the last read. dirfd anchors the directory the replacement was made
// in, so an undo goes back through the directory it wrote in, whatever the path names now.
type installed struct {
	path      string
	planned   node // what the prediction read at the path
	want      node // what was installed; Kind "" for a removal
	existed   bool
	displaced node
	handle    *os.File
	created   []string // directories this install made for the path
	swapped   bool
	dirfd     int
	base      string
	tmp       string         // the name the displaced inode keeps until the install is settled
	stuck     *MutationError // a replacement that failed after changing the directory
	// isDir marks a directory that became a file: the whole directory was exchanged for the
	// file and stands at tmp, with files and dirs naming what was read in it.
	isDir bool
	files []*dirFile
	dirs  []string
}

func (rec *installed) closeDir() {
	if rec.dirfd > 0 {
		unix.Close(rec.dirfd)
		rec.dirfd = -1
	}
}

// restoreNode is what undo puts back: the displaced inode as it reads now, so a save made
// through a descriptor opened before the replacement is included.
func (rec *installed) restoreNode() node {
	n := rec.displaced
	if rec.handle != nil {
		if _, err := rec.handle.Seek(0, io.SeekStart); err == nil {
			if b, err := io.ReadAll(rec.handle); err == nil {
				n.Bytes = b
			}
		}
	}
	return n
}

// OwnerInstall is a landing's install of its plan. It stays valid after the trunk moved, until
// Finish makes the last read.
type OwnerInstall struct {
	t *Trunk
	// ct runs everything the install owes the owner after it began: it is never canceled with
	// the request, so a rollback or a preserved save cannot vanish because Git refused to run.
	ct        *Trunk
	leftover  []string
	lockToken string
	plan      *OwnerPlan
	root      *rootDir
	done      []*installed
	lockPath  string
	origIndex []byte
	indexed   bool
	oldIdx    map[string]entry
}

// InstallFailure is an install that failed for a reason other than the owner's change. Fault is
// empty when the owner's files were put back exactly; otherwise it says what was not.
type InstallFailure struct {
	Fault string
	Err   error
}

func (e *InstallFailure) Error() string {
	if e.Fault != "" {
		return e.Err.Error() + "; " + e.Fault
	}
	return e.Err.Error()
}
func (e *InstallFailure) Unwrap() error { return e.Err }

func planned(p *ownerPath) node {
	switch p.Disk.Kind {
	case "file":
		return node{Kind: "file", Exec: p.Disk.Exec, Bytes: p.Disk.Bytes}
	case "symlink":
		return node{Kind: "symlink", Bytes: p.Disk.Bytes}
	}
	return node{}
}

// MoveOwner is the final move. It takes the checkout's index lock, which Git's own checkout,
// commit and add take too, and keeps it through the install, the one atomic replacement of the
// index, the guarded ref update and any rollback, so none of them runs in the middle. Then the
// comparison of every touched path against the prediction, and the install through anchored
// directory handles. Nothing is computed here. It answers ErrOwnerChanged, having put every file
// back, when the owner changed a path or the checkout after prediction, and errTrunkChanged
// when the trunk tip itself moved. ErrIndexBusy means another command holds the index lock.
func (t *Trunk) MoveOwner(plan *OwnerPlan, ref, to, from string) (*OwnerInstall, error) {
	worktree, err := t.WorktreeFor(ref)
	if err != nil {
		return nil, err
	}
	if plan == nil || worktree != plan.Worktree {
		// The checkout no longer holds the trunk branch, or never did: write no file there.
		if plan != nil {
			t.dropRecovery(plan)
		}
		return nil, t.Move(ref, to, from)
	}
	root, err := openRoot(plan.Worktree)
	if err != nil {
		return nil, err
	}
	cleanup := *t
	base := t.ctx
	if base == nil {
		base = context.Background()
	}
	cleanup.ctx = context.WithoutCancel(base)
	in := &OwnerInstall{t: t, ct: &cleanup, plan: plan, root: root}
	retry := func(cause error) (*OwnerInstall, error) {
		uerr := in.undo()
		in.unlockIndex()
		in.root.close()
		if uerr != nil {
			var failure *InstallFailure
			if errors.As(cause, &failure) && failure.Fault == "" {
				failure.Fault = uerr.Error()
				return nil, failure
			}
			return nil, &InstallFailure{Err: cause, Fault: uerr.Error()}
		}
		var failure *InstallFailure
		switch {
		case errors.Is(cause, ErrOwnerChanged):
			in.ct.dropRecovery(plan)
			return nil, ErrOwnerChanged
		case cause == errTrunkChanged:
			return nil, cause
		case errors.As(cause, &failure):
			return nil, cause
		}
		return nil, &InstallFailure{Err: cause}
	}
	if err := in.lockIndex(); err != nil {
		in.root.close()
		if errors.Is(err, ErrOwnerChanged) || errors.Is(err, ErrIndexBusy) {
			t.dropRecovery(plan)
		}
		return nil, err
	}
	if OwnerHooks.FailComparison != nil && OwnerHooks.FailComparison() {
		in.unlockIndex()
		in.root.close()
		t.dropRecovery(plan)
		return nil, ErrOwnerChanged
	}
	if err := t.checkPlan(plan, from); err != nil {
		in.unlockIndex()
		in.root.close()
		return nil, err
	}
	if err := in.install(); err != nil {
		return retry(err)
	}
	if OwnerHooks.AfterInstall != nil {
		OwnerHooks.AfterInstall()
	}
	if len(in.changedFiles()) > 0 || in.displacedDirsGrew() {
		return retry(ErrOwnerChanged)
	}
	if err := plan.guard(); err != nil {
		return retry(err)
	}
	if err := in.writeIndex(); err != nil {
		return retry(err)
	}
	if OwnerHooks.BeforeTrunkMove != nil {
		OwnerHooks.BeforeTrunkMove()
	}
	if err := plan.guard(); err != nil {
		return retry(err)
	}
	if _, err := t.gitIn(worktree, "-c", "tillr.move=final", "update-ref", "-m", "tillr-land: "+to, normalizeRef(ref), to, from); err != nil {
		return retry(t.failedMove(ref, to, from, Externalf("moving %s to %s in %s: %v", ref, to, worktree, err)))
	}
	// Published: nothing is undone from here. The names of the displaced inodes go; their
	// handles stay open for the last read.
	in.dropTemps()
	in.unlockIndex()
	if OwnerHooks.AfterTrunkMove != nil {
		OwnerHooks.AfterTrunkMove()
	}
	return in, nil
}

// dropTemps removes the temporary names of the displaced inodes after publication, each only
// while the name still is the inode the lander holds.
func (in *OwnerInstall) dropTemps() {
	for _, rec := range in.done {
		if rec.tmp == "" || rec.dirfd < 0 {
			continue
		}
		if rec.isDir {
			if !removeDisplacedDir(rec.dirfd, rec.tmp, rec.files, rec.dirs) {
				in.leftover = append(in.leftover, fmt.Sprintf("%s: the directory it replaced holds files the landing did not read and is left at %s beside the path", rec.path, rec.tmp))
			}
			rec.tmp = ""
			continue
		}
		var byName, byHandle unix.Stat_t
		if rec.handle != nil && unix.Fstatat(rec.dirfd, rec.tmp, &byName, unix.AT_SYMLINK_NOFOLLOW) == nil && unix.Fstat(int(rec.handle.Fd()), &byHandle) == nil &&
			byName.Dev == byHandle.Dev && byName.Ino == byHandle.Ino {
			unix.Unlinkat(rec.dirfd, rec.tmp, 0)
		} else if rec.handle == nil && rec.displaced.Kind == "symlink" {
			unix.Unlinkat(rec.dirfd, rec.tmp, 0)
		} else {
			in.leftover = append(in.leftover, fmt.Sprintf("%s: %s is left beside the path", rec.path, rec.tmp))
		}
		rec.tmp = ""
	}
}

// ErrIndexBusy is an index lock some other process holds: ordinary Git work in progress. It is
// transient contention, not a fact about the owner's content, so the landing is attempted again
// when the lock is free and nothing is held or refused meanwhile.
var ErrIndexBusy = errors.New("a Git command holds the checkout's index lock")

const indexLockKind = "tillr-land-index-lock"

// lockIndex takes <gitdir>/index.lock without waiting. The lock is created by linking a file that
// already holds the lander's ownership record, so the record and the lock appear together; a lock
// whose record names this host and a process that no longer exists is the lander's own leftover and
// is reclaimed. Any other existing lock is Git's or a live process's and is ErrIndexBusy. The branch
// is checked again once the lock is held.
func (in *OwnerInstall) lockIndex() error {
	if OwnerHooks.BeforeIndexLock != nil {
		OwnerHooks.BeforeIndexLock()
	}
	path := filepath.Join(in.plan.GitDir, "index.lock")
	in.lockToken = randomHex(12)
	body, _ := json.Marshal(map[string]any{"kind": indexLockKind, "pid": os.Getpid(), "host": lockHostname(), "token": in.lockToken})
	for attempt := 0; ; attempt++ {
		tmp := path + ".tillr-" + randomHex(6)
		if err := os.WriteFile(tmp, body, 0o600); err != nil {
			return Externalf("locking the index of %s: %v", in.plan.Worktree, err)
		}
		err := unix.Linkat(unix.AT_FDCWD, tmp, unix.AT_FDCWD, path, 0)
		os.Remove(tmp)
		if err == nil {
			break
		}
		if err != unix.EEXIST {
			return Externalf("locking the index of %s: %v", in.plan.Worktree, err)
		}
		if attempt > 0 || !reclaimOwnLock(path) {
			return ErrIndexBusy
		}
	}
	in.lockPath = path
	// A copy of the index a lander that died left behind is its own and goes.
	if stale, _ := filepath.Glob(filepath.Join(in.plan.GitDir, "index.tillr-*")); len(stale) > 0 {
		for _, f := range stale {
			os.Remove(f)
		}
	}
	if err := in.plan.guard(); err != nil {
		in.unlockIndex()
		return err
	}
	return nil
}

// reclaimOwnLock removes an index.lock only when its record proves it is the lander's own and its
// process is dead on this host; a Git lock, another host's, a live process's or an unreadable one
// is never touched. The file is moved aside first and its record checked again, so a lock another
// lander took in between goes back.
func reclaimOwnLock(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	record := lockRecord(data)
	if record == nil || record["kind"] != indexLockKind || record["host"] != lockHostname() {
		return false
	}
	pid, ok := lockPid(record)
	if !ok || lockProcessAlive(pid) {
		return false
	}
	aside := path + ".stale-" + randomHex(6)
	if err := unix.Renameat2(unix.AT_FDCWD, path, unix.AT_FDCWD, aside, unix.RENAME_NOREPLACE); err != nil {
		return false
	}
	moved, err := os.ReadFile(aside)
	if err == nil && string(moved) == string(data) {
		os.Remove(aside)
		return true
	}
	unix.Renameat2(unix.AT_FDCWD, aside, unix.AT_FDCWD, path, unix.RENAME_NOREPLACE)
	return false
}

// ownsLock answers whether index.lock still holds this install's record.
func (in *OwnerInstall) ownsLock() bool {
	if in.lockPath == "" {
		return false
	}
	data, err := os.ReadFile(in.lockPath)
	if err != nil {
		return false
	}
	record := lockRecord(data)
	return record != nil && record["kind"] == indexLockKind && record["token"] == in.lockToken
}

func (in *OwnerInstall) unlockIndex() {
	if in.lockPath != "" {
		if in.ownsLock() {
			os.Remove(in.lockPath)
		}
		in.lockPath = ""
	}
}

func (t *Trunk) dropRecovery(plan *OwnerPlan) {
	if plan != nil && plan.RecoveryRef != "" {
		t.git("update-ref", "-d", plan.RecoveryRef)
		plan.RecoveryRef = ""
	}
}

// checkPlan is c4's comparison: the branch, the tip, the in-progress state, every touched
// path on disk and in the index.
func (t *Trunk) checkPlan(plan *OwnerPlan, from string) error {
	changed := func() error { t.dropRecovery(plan); return ErrOwnerChanged }
	if err := plan.guard(); err != nil {
		return changed()
	}
	at, err := t.gitIn(plan.Worktree, "rev-parse", "--verify", normalizeRef(plan.Ref)+"^{commit}")
	if err != nil {
		return Externalf("reading %s in %s: %v", plan.Ref, plan.Worktree, err)
	}
	if at = strings.TrimSpace(at); at != from {
		return errTrunkChanged
	}
	if busy, err := t.ownerBusy(plan.Worktree); err != nil {
		return err
	} else if busy != plan.Busy {
		return changed()
	}
	var names []string
	for _, p := range plan.Paths {
		names = append(names, p.Path)
		d, err := readDisk(plan.Worktree, p.Path)
		if err != nil {
			return err
		}
		if !d.equal(p.Disk) {
			return changed()
		}
	}
	index, err := t.indexEntries(plan.Worktree, names)
	if err != nil {
		return err
	}
	for _, p := range plan.Paths {
		if index[p.Path] != p.IndexNow {
			return changed()
		}
	}
	if err := plan.guard(); err != nil {
		return changed()
	}
	return nil
}

func (in *OwnerInstall) install() error {
	var actions []*ownerPath
	for _, p := range in.plan.Paths {
		if p.Remove || p.Install {
			actions = append(actions, p)
		}
	}
	sort.SliceStable(actions, func(i, j int) bool {
		if actions[i].Remove != actions[j].Remove {
			return actions[i].Remove
		}
		return actions[i].Path < actions[j].Path
	})
	// A directory that becomes a file is replaced whole, so the deletions beneath it are not
	// carried out one by one: the exchange covers them.
	covered := map[string]map[string]node{}
	for _, p := range actions {
		if p.Install && p.Disk.Kind == "dir" {
			covered[p.Path] = map[string]node{}
		}
	}
	under := func(file string) string {
		for dir := range covered {
			if strings.HasPrefix(file, dir+"/") {
				return dir
			}
		}
		return ""
	}
	for _, p := range actions {
		if dir := under(p.Path); dir != "" && p.Remove {
			covered[dir][p.Path] = planned(p)
		}
	}
	for _, p := range actions {
		if p.Remove && under(p.Path) != "" {
			continue
		}
		rec := &installed{path: p.Path, planned: planned(p), existed: p.Disk.Kind == "file" || p.Disk.Kind == "symlink", dirfd: -1}
		if p.Install {
			rec.want = node{Kind: "file", Exec: p.FinalExec, Bytes: p.FinalContent}
			if p.FinalSymlink {
				rec.want = node{Kind: "symlink", Bytes: p.FinalContent}
			}
		}
		in.done = append(in.done, rec)
		if OwnerHooks.InstallFault != nil {
			if err := OwnerHooks.InstallFault(p.Path); err != nil {
				return &InstallFailure{Err: err}
			}
		}
		dirfd, base, made, err := in.root.parent(p.Path, rec.want.Kind != "")
		rec.created = made
		if err != nil {
			return err
		}
		rec.dirfd, rec.base = dirfd, base
		if OwnerHooks.BeforeSwap != nil {
			OwnerHooks.BeforeSwap(p.Path)
		}
		// The branch and the parent are checked as near the mutation as they can be.
		if err := in.plan.guard(); err != nil {
			return err
		}
		if !in.root.sameDir(dirfd, p.Path) {
			return ErrOwnerChanged
		}
		if p.Disk.Kind == "dir" && p.Install {
			tmp, files, dirs, err := exchangeDir(dirfd, p.Path, base, rec.want, covered[p.Path])
			if err != nil {
				var me *MutationError
				if errors.As(err, &me) {
					rec.stuck = me
				}
				return err
			}
			rec.swapped, rec.isDir, rec.tmp, rec.files, rec.dirs = true, true, tmp, files, dirs
			if !in.root.sameDir(dirfd, p.Path) {
				return ErrOwnerChanged
			}
			if OwnerHooks.AfterReplace != nil {
				OwnerHooks.AfterReplace(p.Path)
			}
			continue
		}
		if p.Disk.Kind == "dir" {
			if err := removeEmptyTree(dirfd, base); err != nil {
				return err
			}
		}
		if OwnerHooks.AfterParentCheck != nil {
			OwnerHooks.AfterParentCheck(p.Path)
		}
		res, err := swap(dirfd, p.Path, base, rec.want, rec.existed)
		if err != nil {
			var me *MutationError
			if errors.As(err, &me) {
				rec.stuck = me
			}
			return err
		}
		disp := res.Displaced
		rec.swapped, rec.displaced, rec.handle, rec.tmp = true, disp, res.Handle, res.Tmp
		if !in.root.sameDir(dirfd, p.Path) {
			return ErrOwnerChanged // undone through the anchored handle, not the path
		}
		if rec.want.Kind == "" {
			in.root.removeEmptyDirs(emptyParentsOf(p.Path))
		}
		if OwnerHooks.AfterReplace != nil {
			OwnerHooks.AfterReplace(p.Path)
		}
		if rec.existed && !disp.equal(rec.planned) {
			return ErrOwnerChanged
		}
	}
	return nil
}

// changedNodes answers the displaced files that hold other content than the prediction read:
// an owner's save that reached the inode actually removed from the path.
func (in *OwnerInstall) changedNodes() map[string]node {
	saved := map[string]node{}
	for _, rec := range in.done {
		for _, f := range rec.files {
			if f.handle == nil {
				continue
			}
			now := f.planned
			if _, err := f.handle.Seek(0, io.SeekStart); err == nil {
				if b, err := io.ReadAll(f.handle); err == nil {
					now.Bytes = b
				}
			}
			if !now.equal(f.planned) {
				saved[f.path] = now
			}
		}
		if !rec.swapped || !rec.existed {
			continue
		}
		if now := rec.restoreNode(); !now.equal(rec.planned) {
			saved[rec.path] = now
		}
	}
	return saved
}

func (in *OwnerInstall) changedFiles() map[string]node { return in.changedNodes() }

// displacedDirsGrew answers whether a directory the install displaced holds a file that was not
// there when it was read: the owner's, so the install is undone before the trunk moves.
func (in *OwnerInstall) displacedDirsGrew() bool {
	for _, rec := range in.done {
		if rec.isDir && rec.tmp != "" && displacedDirGrew(rec.dirfd, rec.tmp, rec.files, rec.dirs) {
			return true
		}
	}
	return false
}

// writeIndex replaces the index once: the touched entries are applied to a copy and the copy is
// renamed over the index while the lander holds the index lock. Untouched entries keep their
// stat data.
func (in *OwnerInstall) writeIndex() error {
	in.oldIdx = map[string]entry{}
	var lines []string
	for _, p := range in.plan.Paths {
		if p.IndexFinal == p.IndexNow {
			continue
		}
		in.oldIdx[p.Path] = p.IndexNow
		lines = append(lines, indexLine(p.IndexFinal, p.Path))
	}
	if len(lines) == 0 {
		return nil
	}
	index := filepath.Join(in.plan.GitDir, "index")
	orig, err := os.ReadFile(index)
	if err != nil {
		return Externalf("reading the index of %s: %v", in.plan.Worktree, err)
	}
	in.origIndex = orig
	alt := filepath.Join(in.plan.GitDir, "index.tillr-"+randomHex(6))
	defer os.Remove(alt)
	if err := os.WriteFile(alt, orig, 0o600); err != nil {
		return Externalf("copying the index of %s: %v", in.plan.Worktree, err)
	}
	if _, err := in.t.gitStdin(in.plan.Worktree, []byte(strings.Join(lines, "\n")+"\n"), []string{"GIT_INDEX_FILE=" + alt}, "update-index", "--index-info"); err != nil {
		return Externalf("preparing the index of %s: %v", in.plan.Worktree, err)
	}
	if err := os.Rename(alt, index); err != nil {
		return Externalf("installing the index of %s: %v", in.plan.Worktree, err)
	}
	in.indexed = true
	return nil
}

func indexLine(e entry, path string) string {
	if e.absent() {
		return "0 " + strings.Repeat("0", 40) + "\t" + path
	}
	return e.Mode + " " + e.OID + "\t" + path
}

// restoreIndex puts the original index back. It stays pending until the restoration is confirmed.
func (in *OwnerInstall) restoreIndex() error {
	if !in.indexed {
		return nil
	}
	if OwnerHooks.FailIndexRestore != nil {
		if err := OwnerHooks.FailIndexRestore(); err != nil {
			return err
		}
	}
	alt := filepath.Join(in.plan.GitDir, "index.tillr-"+randomHex(6))
	if err := os.WriteFile(alt, in.origIndex, 0o600); err != nil {
		return err
	}
	if err := os.Rename(alt, filepath.Join(in.plan.GitDir, "index")); err != nil {
		os.Remove(alt)
		return err
	}
	in.indexed = false
	return nil
}

// savedVersion is content kept in a recovery ref under a name in its tree: a regular file, or a
// symlink whose target is Bytes.
type savedVersion struct {
	Name  string
	Bytes []byte
	Mode  string
}

func versionOf(path string, n node) savedVersion {
	mode := "100644"
	if n.Kind == "symlink" {
		mode = "120000"
	} else if n.Exec {
		mode = "100755"
	}
	return savedVersion{Name: "worktree/" + path, Bytes: n.Bytes, Mode: mode}
}

type undoReport struct {
	versions []savedVersion
	failures []string
	seen     map[string]int
}

// keep records a version for the saved ref: a file or symlink that differs from what the prediction
// read, or any with force.
func (r *undoReport) keep(path string, n node, planned node, force bool) {
	if n.Kind == "" || (!force && n.equal(planned)) {
		return
	}
	if r.seen == nil {
		r.seen = map[string]int{}
	}
	r.seen[path]++
	v := versionOf(path, n)
	if k := r.seen[path]; k > 1 {
		v.Name = fmt.Sprintf("worktree/%s.tillr-saved-%d", path, k)
	}
	r.versions = append(r.versions, v)
}

// undo puts the owner's files back, newest replacement first, through the directory handles the
// install wrote through, and only while the lander still holds the index lock: without it the
// branch and the index are not the lander's to rewrite. Each file is read before it is replaced;
// bytes other than what was installed are the owner's save. Every newer save stays at the path or
// in a saved ref, an unreadable path or a failed step is reported with what was not restored, and
// nothing is claimed that was not confirmed. It undoes only an install that never reached the trunk.
func (in *OwnerInstall) undo() error {
	rep := &undoReport{}
	if !in.ownsLock() {
		rep.failures = append(rep.failures, "the checkout's index lock is no longer the lander's, so no file or index entry was rewritten; the landing's content is still in the checkout")
		for _, rec := range in.done {
			if rec.swapped && rec.existed {
				rep.keep(rec.path, rec.restoreNode(), rec.planned, true)
			}
		}
	} else {
		for i := len(in.done) - 1; i >= 0; i-- {
			rec := in.done[i]
			if rec.stuck != nil {
				rep.failures = append(rep.failures, rec.stuck.Msg)
				rep.versions = append(rep.versions, rec.stuck.Saved...)
			}
			if !rec.swapped || rec.dirfd < 0 {
				continue
			}
			in.undoOne(rec, rep)
			if !rec.existed {
				in.root.removeEmptyDirs(rec.created)
			}
		}
		if err := in.restoreIndex(); err != nil {
			rep.failures = append(rep.failures, fmt.Sprintf("the index was not restored (%v): it still holds the landing's entries for %s", err, strings.Join(in.touchedIndexPaths(), ", ")))
			rep.versions = append(rep.versions, in.indexSnapshot()...)
		}
	}
	for _, rec := range in.done {
		if rec.handle != nil {
			rec.handle.Close()
			rec.handle = nil
		}
		closeDirFiles(rec.files)
		rec.closeDir()
	}
	if len(rep.versions) == 0 && len(rep.failures) == 0 {
		return nil
	}
	text := ""
	if len(rep.versions) > 0 {
		ref, err := in.ct.keepVersions(in.plan, rep.versions)
		if err != nil {
			return Externalf("the owner's content was NOT fully restored: %s; the content to keep could not be saved: %v", strings.Join(rep.failures, "; "), err)
		}
		text = "content kept in " + ref
	}
	if len(rep.failures) > 0 {
		return Externalf("the owner's content was NOT fully restored: %s; %s", strings.Join(rep.failures, "; "), text)
	}
	return Externalf("the owner saved bytes while the install was undone: %s", text)
}

func (in *OwnerInstall) touchedIndexPaths() []string {
	var paths []string
	for p := range in.oldIdx {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// indexSnapshot is the original index entry of every touched path, kept as blobs; a path with
// no entry is recorded by a marker.
func (in *OwnerInstall) indexSnapshot() []savedVersion {
	var out []savedVersion
	for _, path := range in.touchedIndexPaths() {
		e := in.oldIdx[path]
		if e.absent() {
			out = append(out, savedVersion{"index-absent/" + path, []byte("no index entry before the landing\n"), "100644"})
			continue
		}
		blob, err := in.ct.gitIn(in.plan.Worktree, "cat-file", "blob", e.OID)
		if err != nil {
			blob = "unreadable blob " + e.OID + "\n"
		}
		out = append(out, savedVersion{"index/" + path, []byte(blob), "100644"})
	}
	return out
}

// undoOne restores one path by exchanging the retained original back by name, so the original
// inode returns instead of a copy. Whatever the exchange displaces is read: the lander's own
// installed content is removed, anything else is an owner's newer save, which goes back to the path
// while the version it superseded is kept.
func (in *OwnerInstall) undoOne(rec *installed, rep *undoReport) {
	restore := rec.restoreNode()
	if !rec.existed {
		restore = node{}
	}
	fail := func(format string, args ...any) {
		rep.keep(rec.path, restore, rec.planned, true)
		msg := fmt.Sprintf(format, args...)
		if rec.tmp != "" {
			msg += fmt.Sprintf("; the owner's original is kept at %s beside it", rec.tmp)
		}
		rep.failures = append(rep.failures, msg)
	}
	if OwnerHooks.BeforeUndoRead != nil {
		OwnerHooks.BeforeUndoRead(rec.path)
	}
	var cur node
	var isDir bool
	var err error
	var f *os.File
	if OwnerHooks.FailUndoInspect != nil {
		err = OwnerHooks.FailUndoInspect(rec.path)
	}
	if err == nil {
		cur, f, isDir, err = inspect(rec.dirfd, rec.base)
	}
	if f != nil {
		f.Close()
	}
	if err != nil {
		fail("%s could not be read to undo the install (%v) and still holds what the install left", rec.path, err)
		return
	}
	if isDir || !cur.equal(rec.want) {
		// The owner saved at the path after the replacement: that is the newer content, and the
		// original it superseded is kept when it differs from what the prediction read.
		if rec.isDir {
			rep.failures = append(rep.failures, fmt.Sprintf("%s was saved by the owner after its directory was replaced; the directory is kept at %s beside it", rec.path, rec.tmp))
			rec.tmp = ""
			return
		}
		rep.keep(rec.path, restore, rec.planned, false)
		in.unlinkTmp(rec)
		return
	}
	if OwnerHooks.FailRollbackSwap != nil {
		if err := OwnerHooks.FailRollbackSwap(rec.path); err != nil {
			fail("%s still holds the landing's content (%v)", rec.path, err)
			return
		}
	}
	step := 1
	if OwnerHooks.BeforeRollbackSwap != nil {
		OwnerHooks.BeforeRollbackSwap(rec.path, step)
	}
	if rec.isDir {
		// The same exchange made again: the directory returns whole, saved bytes included.
		if err := unix.Renameat2(rec.dirfd, rec.tmp, rec.dirfd, rec.base, unix.RENAME_EXCHANGE); err != nil {
			fail("%s still holds the landing's content (%v)", rec.path, err)
			return
		}
		unix.Unlinkat(rec.dirfd, rec.tmp, 0) // the lander's own content, read above
		rec.tmp = ""
		return
	}
	if !rec.existed {
		in.removeAdded(rec, rep)
		return
	}
	if rec.want.Kind == "" {
		if err := unix.Renameat2(rec.dirfd, rec.tmp, rec.dirfd, rec.base, unix.RENAME_NOREPLACE); err != nil {
			fail("%s was removed by the install and could not be put back (%v)", rec.path, err)
			return
		}
		rec.tmp = ""
		return
	}
	placed, expected := restore, rec.want
	for {
		if err := unix.Renameat2(rec.dirfd, rec.tmp, rec.dirfd, rec.base, unix.RENAME_EXCHANGE); err != nil {
			fail("%s still holds the landing's content (%v)", rec.path, err)
			return
		}
		disp, h, isDir, err := inspect(rec.dirfd, rec.tmp)
		if h != nil {
			h.Close()
		}
		if err != nil || isDir {
			rep.failures = append(rep.failures, fmt.Sprintf("%s was exchanged but what it displaced could not be read (%v); it is left at %s", rec.path, err, rec.tmp))
			rec.tmp = ""
			return
		}
		if disp.equal(expected) {
			unix.Unlinkat(rec.dirfd, rec.tmp, 0) // the lander's own content, confirmed
			rec.tmp = ""
			return
		}
		// An owner's newer save was displaced: it goes back to the path on the next exchange, and
		// what is on the path now is superseded.
		step++
		if OwnerHooks.BeforeRollbackSwap != nil {
			OwnerHooks.BeforeRollbackSwap(rec.path, step)
		}
		rep.keep(rec.path, placed, rec.planned, false)
		placed, expected = disp, placed
	}
}

// removeAdded removes a file the install added, reading it first.
func (in *OwnerInstall) removeAdded(rec *installed, rep *undoReport) {
	aside := ".tillr-" + randomHex(8) + ".tmp"
	if err := unix.Renameat2(rec.dirfd, rec.base, rec.dirfd, aside, unix.RENAME_NOREPLACE); err != nil {
		rep.failures = append(rep.failures, fmt.Sprintf("%s was added by the install and could not be removed (%v)", rec.path, err))
		return
	}
	n, f, isDir, err := inspect(rec.dirfd, aside)
	if f != nil {
		f.Close()
	}
	if err == nil && !isDir && n.equal(rec.want) {
		unix.Unlinkat(rec.dirfd, aside, 0)
		return
	}
	// The owner saved over it in the gap: put it back, or keep it.
	if rerr := unix.Renameat2(rec.dirfd, aside, rec.dirfd, rec.base, unix.RENAME_NOREPLACE); rerr != nil {
		rep.keep(rec.path, n, node{}, true)
		rep.failures = append(rep.failures, fmt.Sprintf("%s holds the owner's newer content that could not be put back (%v); it is left at %s", rec.path, rerr, aside))
	}
}

func (in *OwnerInstall) unlinkTmp(rec *installed) {
	if rec.tmp != "" {
		unix.Unlinkat(rec.dirfd, rec.tmp, 0)
		rec.tmp = ""
	}
}

func closeFd(fd int) { unix.Close(fd) }

func (t *Trunk) keepVersions(plan *OwnerPlan, versions []savedVersion) (string, error) {
	var lines []string
	for _, v := range versions {
		oid, err := t.gitStdin(plan.Worktree, v.Bytes, nil, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", Externalf("keeping the saved bytes of %s: %v", v.Name, err)
		}
		mode := v.Mode
		if mode == "" {
			mode = "100644"
		}
		lines = append(lines, mode+" "+strings.TrimSpace(oid)+"\t"+v.Name)
	}
	return t.commitCopy(plan.Worktree, recoveryName("saved", plan.runID), lines, "bytes the owner saved on landed paths during "+plan.runID)
}

// Finish is the last read of the replaced files: after the receipt is durable, or after a
// published landing's receipt failed. It never undoes the published checkout. With nothing
// saved it removes the recovery copy; saved bytes are kept in a ref and answered as faults. Its Git
// work uses the cleanup Trunk, which a canceled request cannot stop.
func (in *OwnerInstall) Finish() []string {
	if in == nil {
		return nil
	}
	if OwnerHooks.BeforeLastRead != nil {
		OwnerHooks.BeforeLastRead()
	}
	saved := in.changedNodes()
	for _, rec := range in.done {
		if rec.handle != nil {
			rec.handle.Close()
			rec.handle = nil
		}
		closeDirFiles(rec.files)
		rec.closeDir()
	}
	in.root.close()
	faults := append([]string(nil), in.leftover...)
	if len(saved) == 0 {
		in.ct.dropRecovery(in.plan)
		return faults
	}
	var names []string
	var versions []savedVersion
	for p := range saved {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		versions = append(versions, versionOf(p, saved[p]))
	}
	ref, err := in.ct.keepVersions(in.plan, versions)
	if err != nil {
		return append(faults, fmt.Sprintf("the owner saved bytes at %s after the trunk moved and they could not be kept: %v", strings.Join(names, ", "), err))
	}
	// The copy of the owner's earlier content is merged into the checkout; the saved ref holds
	// what the merge could not carry.
	in.ct.dropRecovery(in.plan)
	return append(faults, fmt.Sprintf("the owner saved bytes at %s after the trunk moved: kept in %s", strings.Join(names, ", "), ref))
}

// pendingSaved answers a description of every saved-bytes ref an earlier attempt of this run left,
// with the paths its tree holds.
func (t *Trunk) pendingSaved(runID string) (refs, descriptions []string, err error) {
	out, err := t.git("for-each-ref", "--format=%(refname)", RecoveryPrefix+"saved/")
	if err != nil {
		return nil, nil, Externalf("listing %s: %v", RecoveryPrefix+"saved/", err)
	}
	for _, ref := range pathLines(out) {
		if !strings.HasPrefix(ref, RecoveryPrefix+"saved/"+runID+"-") {
			continue
		}
		names, _ := t.git("ls-tree", "-r", "--name-only", ref)
		refs = append(refs, ref)
		descriptions = append(descriptions, fmt.Sprintf("owner bytes saved after the trunk moved are kept in %s (%s)", ref, strings.Join(pathLines(names), ", ")))
	}
	return refs, descriptions, nil
}
