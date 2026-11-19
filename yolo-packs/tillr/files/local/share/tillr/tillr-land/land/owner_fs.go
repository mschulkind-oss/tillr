package land

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
	"golang.org/x/sys/unix"
)

// The owner's checkout is written only through directory handles opened without following a
// symlink, so a parent the owner replaces with a link after the prediction is never written
// through. Every temporary is created exclusively under a random name, and a replacement is
// a rename that hands back the inode it displaced.

// node is a file, a symlink or nothing, as the lander installs or finds it.
type node struct {
	Kind  string // "", "file", "symlink"
	Exec  bool
	Bytes []byte
}

func (n node) equal(o node) bool {
	return n.Kind == o.Kind && n.Exec == o.Exec && bytes.Equal(n.Bytes, o.Bytes)
}

type rootDir struct{ fd int }

func openRoot(dir string) (*rootDir, error) {
	// Every install into the owner's checkout starts here, so the guard sees the file I/O
	// of an install begun under the store's write lock (tools/tillr/store/ioguard.go).
	if err := store.CheckIO("opening the checkout " + dir); err != nil {
		return nil, err
	}
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, Externalf("opening %s: %v", dir, err)
	}
	return &rootDir{fd}, nil
}

func (r *rootDir) close() { unix.Close(r.fd) }

// parent opens the directory that holds rel, one component at a time, never following a
// symlink. With create it makes missing directories and answers the ones it made. A component
// that is not a plain directory, or a missing one without create, is ErrOwnerChanged.
func (r *rootDir) parent(rel string, create bool) (int, string, []string, error) {
	dir, base := path.Split(rel)
	cur, err := unix.Dup(r.fd)
	if err != nil {
		return -1, "", nil, Externalf("duplicating a directory handle: %v", err)
	}
	var made []string
	walked := ""
	for _, part := range strings.Split(strings.Trim(dir, "/"), "/") {
		if part == "" {
			continue
		}
		walked = path.Join(walked, part)
		next, err := unix.Openat(cur, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err == unix.ENOENT && create {
			if err = unix.Mkdirat(cur, part, 0o755); err == nil {
				made = append(made, walked)
				next, err = unix.Openat(cur, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			}
		}
		unix.Close(cur)
		if err != nil {
			return -1, "", made, ErrOwnerChanged
		}
		cur = next
	}
	return cur, base, made, nil
}

// inspect reads what name holds in dirfd without following it. A file is answered with its
// open handle, which is how a descriptor-held save is read later.
func inspect(dirfd int, name string) (n node, f *os.File, isDir bool, err error) {
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if err == unix.ENOENT {
			return node{}, nil, false, nil
		}
		return node{}, nil, false, Externalf("reading %s: %v", name, err)
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		return node{}, nil, true, nil
	case unix.S_IFLNK:
		buf := make([]byte, 4096)
		k, err := unix.Readlinkat(dirfd, name, buf)
		if err != nil {
			return node{}, nil, false, Externalf("reading %s: %v", name, err)
		}
		return node{Kind: "symlink", Bytes: buf[:k]}, nil, false, nil
	case unix.S_IFREG:
		fd, err := unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return node{}, nil, false, Externalf("opening %s: %v", name, err)
		}
		f := os.NewFile(uintptr(fd), name)
		b, err := io.ReadAll(f)
		if err != nil {
			f.Close()
			return node{}, nil, false, Externalf("reading %s: %v", name, err)
		}
		return node{Kind: "file", Exec: st.Mode&0o111 != 0, Bytes: b}, f, false, nil
	}
	return node{}, nil, false, ErrOwnerChanged
}

// makeTemp creates want under a random exclusive name in dirfd.
func makeTemp(dirfd int, want node) (string, error) {
	for try := 0; try < 16; try++ {
		name := ".tillr-" + randomHex(8) + ".tmp"
		if want.Kind == "symlink" {
			err := unix.Symlinkat(string(want.Bytes), dirfd, name)
			if err == unix.EEXIST {
				continue
			}
			if err != nil {
				return "", err
			}
			return name, nil
		}
		fd, err := unix.Openat(dirfd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if err == unix.EEXIST {
			continue
		}
		if err != nil {
			return "", err
		}
		f := os.NewFile(uintptr(fd), name)
		mode := os.FileMode(0o644)
		if want.Exec {
			mode = 0o755
		}
		_, werr := f.Write(want.Bytes)
		if cerr := f.Chmod(mode); werr == nil {
			werr = cerr
		}
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			unix.Unlinkat(dirfd, name, 0)
			return "", werr
		}
		return name, nil
	}
	return "", unix.EEXIST
}

// swapResult is a completed replacement. The displaced inode keeps the temporary name Tmp until
// the install is settled, so an undo can exchange it back instead of recreating it.
type swapResult struct {
	Displaced node
	Handle    *os.File
	Tmp       string
}

// MutationError is a replacement that failed after it changed the directory. It carries what was
// not restored and any content the failed restoration found, so the caller keeps both and cannot
// claim an exact restoration.
type MutationError struct {
	Msg   string
	Saved []savedVersion
}

func (e *MutationError) Error() string { return e.Msg }

// swap makes base hold want, atomically, and answers the inode that was displaced: the one
// actually removed from the path, whatever an editor renamed in meanwhile. A want of Kind ""
// removes the path. existed says the path must hold something (an exchange) or nothing (a
// rename that refuses to replace); a mismatch is ErrOwnerChanged and changes nothing. rel names
// the path in messages.
func swap(dirfd int, rel, base string, want node, existed bool) (swapResult, error) {
	if want.Kind == "" {
		tmp := ".tillr-" + randomHex(8) + ".tmp"
		if err := unix.Renameat2(dirfd, base, dirfd, tmp, unix.RENAME_NOREPLACE); err != nil {
			if err == unix.ENOENT {
				return swapResult{}, ErrOwnerChanged
			}
			return swapResult{}, Externalf("removing %s: %v", rel, err)
		}
		return takeDisplaced(dirfd, rel, base, tmp, false, want)
	}
	tmp, err := makeTemp(dirfd, want)
	if err != nil {
		return swapResult{}, Externalf("creating a temporary beside %s: %v", rel, err)
	}
	flags := uint(unix.RENAME_NOREPLACE)
	if existed {
		flags = unix.RENAME_EXCHANGE
	}
	if err := unix.Renameat2(dirfd, tmp, dirfd, base, flags); err != nil {
		unix.Unlinkat(dirfd, tmp, 0)
		if err == unix.ENOENT || err == unix.EEXIST {
			return swapResult{}, ErrOwnerChanged
		}
		return swapResult{}, Externalf("replacing %s: %v", rel, err)
	}
	if !existed {
		return swapResult{}, nil
	}
	return takeDisplaced(dirfd, rel, base, tmp, true, want)
}

// takeDisplaced reads the inode a swap left at tmp and keeps its handle and its name. If the
// inode cannot be read, or is a directory, the displacement is restored by an exchange that is
// itself journaled: what comes back to tmp is read, and only the lander's own installed content
// is removed.
func takeDisplaced(dirfd int, rel, base, tmp string, exchanged bool, want node) (swapResult, error) {
	var (
		n     node
		f     *os.File
		isDir bool
		err   error
	)
	if OwnerHooks.FailDisplacedRead != nil {
		err = OwnerHooks.FailDisplacedRead(base)
	}
	if err == nil {
		n, f, isDir, err = inspect(dirfd, tmp)
	}
	if err == nil && !isDir {
		return swapResult{Displaced: n, Handle: f, Tmp: tmp}, nil
	}
	if f != nil {
		f.Close()
	}
	cause := err
	if cause == nil {
		cause = ErrOwnerChanged
	}
	return swapResult{}, restoreDisplacement(dirfd, rel, base, tmp, exchanged, want, cause)
}

func restoreDisplacement(dirfd int, rel, base, tmp string, exchanged bool, want node, cause error) error {
	flags := uint(unix.RENAME_NOREPLACE)
	if exchanged {
		flags = unix.RENAME_EXCHANGE
	}
	var rerr error
	if OwnerHooks.FailRestoreExchange != nil {
		rerr = OwnerHooks.FailRestoreExchange(base)
	}
	if rerr == nil {
		rerr = unix.Renameat2(dirfd, tmp, dirfd, base, flags)
	}
	if rerr != nil {
		return &MutationError{Msg: fmt.Sprintf("%s was replaced and could not be restored after its displaced file could not be read (%v; restoring: %v): the owner's file is kept at %s beside it, and the landing's content is at the path", rel, cause, rerr, tmp)}
	}
	if !exchanged {
		return cause
	}
	// The exchange put back what was displaced; tmp now holds what stood at the path.
	n, f, isDir, ierr := inspect(dirfd, tmp)
	if f != nil {
		f.Close()
	}
	if ierr == nil && !isDir && n.equal(want) {
		unix.Unlinkat(dirfd, tmp, 0) // the lander's own installed content, confirmed
		return cause
	}
	if ierr != nil {
		return &MutationError{Msg: fmt.Sprintf("%s was restored, but what stood at the path meanwhile could not be read (%v) and is left at %s", rel, ierr, tmp)}
	}
	// An owner's newer save stood at the path: it goes back, and the original returns to tmp.
	if err := unix.Renameat2(dirfd, tmp, dirfd, base, unix.RENAME_EXCHANGE); err != nil {
		return &MutationError{Msg: fmt.Sprintf("%s: an owner's newer save is left at %s and the original is at the path (%v)", rel, tmp, err)}
	}
	me := &MutationError{Msg: fmt.Sprintf("%s: a save made while its displaced file was read stays at the path; the owner's original is kept at %s and in a saved ref", rel, tmp)}
	if orig, f, _, oerr := inspect(dirfd, tmp); oerr == nil {
		if f != nil {
			f.Close()
		}
		me.Saved = append(me.Saved, versionOf(rel+".tillr-original", orig))
	}
	return me
}

// sameDir answers whether dirfd is still the directory the checkout path rel resolves to now.
func (r *rootDir) sameDir(dirfd int, rel string) bool {
	fd, _, _, err := r.parent(rel, false)
	if err != nil {
		return false
	}
	defer unix.Close(fd)
	var a, b unix.Stat_t
	if unix.Fstat(dirfd, &a) != nil || unix.Fstat(fd, &b) != nil {
		return false
	}
	return a.Dev == b.Dev && a.Ino == b.Ino
}

// dirFile is one file of a directory the install displaced whole. sub is its path inside the
// directory; handle stays open until the last read, as a displaced file's does.
type dirFile struct {
	path, sub string
	planned   node
	handle    *os.File
}

// exchangeDir replaces the directory base with want in one exchange, so the directory and
// everything in it stand under a temporary name beside the path. It then reads the whole displaced
// directory: every file there must be one the landing deletes, holding what the prediction read
// (expected, keyed by checkout path). Anything else is the owner's, and the directory is exchanged
// back before ErrOwnerChanged is answered. dirs are the subdirectories, parents first.
func exchangeDir(dirfd int, rel, base string, want node, expected map[string]node) (tmp string, files []*dirFile, dirs []string, err error) {
	tmp, err = makeTemp(dirfd, want)
	if err != nil {
		return "", nil, nil, Externalf("creating a temporary beside %s: %v", rel, err)
	}
	if err := unix.Renameat2(dirfd, tmp, dirfd, base, unix.RENAME_EXCHANGE); err != nil {
		unix.Unlinkat(dirfd, tmp, 0)
		if err == unix.ENOENT {
			return "", nil, nil, ErrOwnerChanged
		}
		return "", nil, nil, Externalf("replacing %s: %v", rel, err)
	}
	files, dirs, cause := readDisplacedDir(dirfd, tmp, "", rel, expected)
	if cause == nil && len(files) != len(expected) {
		cause = ErrOwnerChanged // a file the landing deletes is no longer there
	}
	if cause == nil {
		return tmp, files, dirs, nil
	}
	closeDirFiles(files)
	return "", nil, nil, restoreDisplacement(dirfd, rel, base, tmp, true, want, cause)
}

func closeDirFiles(files []*dirFile) {
	for _, f := range files {
		if f.handle != nil {
			f.handle.Close()
			f.handle = nil
		}
	}
}

// readDisplacedDir reads the directory at root/sub in dirfd, never following a link.
func readDisplacedDir(dirfd int, root, sub, rel string, expected map[string]node) (files []*dirFile, dirs []string, err error) {
	fd, err := unix.Openat(dirfd, path.Join(root, sub), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, ErrOwnerChanged
	}
	dir := os.NewFile(uintptr(fd), root)
	names, err := dir.Readdirnames(-1)
	if err != nil {
		dir.Close()
		return nil, nil, Externalf("reading %s: %v", path.Join(rel, sub), err)
	}
	defer dir.Close()
	sort.Strings(names)
	for _, name := range names {
		n, f, isDir, err := inspect(fd, name)
		child := path.Join(sub, name)
		if err != nil {
			return files, dirs, err
		}
		if isDir {
			dirs = append(dirs, child)
			more, deeper, err := readDisplacedDir(dirfd, root, child, rel, expected)
			files, dirs = append(files, more...), append(dirs, deeper...)
			if err != nil {
				return files, dirs, err
			}
			continue
		}
		full := path.Join(rel, child)
		files = append(files, &dirFile{path: full, sub: child, planned: n, handle: f})
		if want, ok := expected[full]; !ok || !n.equal(want) {
			return files, dirs, ErrOwnerChanged
		}
	}
	return files, dirs, nil
}

// displacedDirGrew answers whether the displaced directory at tmp holds a name that was not read
// there after the exchange: a file the owner created in it since.
func displacedDirGrew(dirfd int, tmp string, files []*dirFile, dirs []string) bool {
	known := map[string]bool{"": true}
	for _, f := range files {
		known[f.sub] = true
	}
	for _, d := range dirs {
		known[d] = true
	}
	for sub := range known {
		if sub != "" && !contains(dirs, sub) {
			continue
		}
		fd, err := unix.Openat(dirfd, path.Join(tmp, sub), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return true
		}
		dir := os.NewFile(uintptr(fd), tmp)
		names, err := dir.Readdirnames(-1)
		dir.Close()
		if err != nil {
			return true
		}
		for _, name := range names {
			if !known[path.Join(sub, name)] {
				return true
			}
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// removeDisplacedDir removes a displaced directory after the landing is published: the files that
// were read, each only while its name still is the inode the lander holds, then the directories
// left empty, deepest first. It never removes a tree recursively, so anything that appeared there
// meanwhile keeps the directory, and false is answered.
func removeDisplacedDir(dirfd int, tmp string, files []*dirFile, dirs []string) bool {
	for _, f := range files {
		name := path.Join(tmp, f.sub)
		if f.handle == nil {
			if f.planned.Kind == "symlink" {
				unix.Unlinkat(dirfd, name, 0)
			}
			continue
		}
		var byName, byHandle unix.Stat_t
		if unix.Fstatat(dirfd, name, &byName, unix.AT_SYMLINK_NOFOLLOW) == nil && unix.Fstat(int(f.handle.Fd()), &byHandle) == nil &&
			byName.Dev == byHandle.Dev && byName.Ino == byHandle.Ino {
			unix.Unlinkat(dirfd, name, 0)
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		unix.Unlinkat(dirfd, path.Join(tmp, dirs[i]), unix.AT_REMOVEDIR)
	}
	return unix.Unlinkat(dirfd, tmp, unix.AT_REMOVEDIR) == nil
}

// removeEmptyTree removes the directory name and the directories beneath it, all of which must
// be empty of files: a file there is the owner's and is ErrOwnerChanged.
func removeEmptyTree(dirfd int, name string) error {
	fd, err := unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err == unix.ENOENT {
		return nil
	}
	if err != nil {
		return ErrOwnerChanged
	}
	dir := os.NewFile(uintptr(fd), name)
	names, err := dir.Readdirnames(-1)
	if err != nil {
		dir.Close()
		return Externalf("reading %s: %v", name, err)
	}
	for _, entry := range names {
		if err := removeEmptyTree(fd, entry); err != nil {
			dir.Close()
			return err
		}
	}
	dir.Close()
	if err := unix.Unlinkat(dirfd, name, unix.AT_REMOVEDIR); err != nil {
		return ErrOwnerChanged
	}
	return nil
}

// removeEmptyDirs removes the listed directories, deepest first, when they are empty.
func (r *rootDir) removeEmptyDirs(dirs []string) {
	for i := len(dirs) - 1; i >= 0; i-- {
		pfd, base, _, err := r.parent(dirs[i], false)
		if err != nil {
			continue
		}
		unix.Unlinkat(pfd, base, unix.AT_REMOVEDIR)
		unix.Close(pfd)
	}
}

// emptyParentsOf answers the directories above rel, deepest first, up to the root.
func emptyParentsOf(rel string) []string {
	var dirs []string
	for dir := path.Dir(rel); dir != "." && dir != "/"; dir = path.Dir(dir) {
		dirs = append([]string{dir}, dirs...)
	}
	return dirs
}

func rootCloseFd(fd int) { unix.Close(fd) }
