package drain

import (
	"encoding/binary"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// A rule matches one child (including a missing ancestor of the target), or
// Markdown files and directory changes if name is empty.
type startWatchRule struct {
	name   string
	change startWatchChange
}

type linuxStartWatcher struct {
	file   *os.File
	fd     int
	paths  startWatchPaths
	notify chan startWatchChange
	done   chan struct{}

	mu     sync.Mutex
	closed bool
	docs   map[string]bool
	rules  map[int32][]startWatchRule
}

func newStartWatcher(paths startWatchPaths) startWatcher {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil
	}
	// The Go poller waits for readability and interrupts Read on Close.
	w := &linuxStartWatcher{
		file: os.NewFile(uintptr(fd), "tillr-start-inotify"), fd: fd, paths: paths,
		notify: make(chan startWatchChange, 1), done: make(chan struct{}),
	}
	w.bind()
	go w.readLoop()
	return w
}

func (w *linuxStartWatcher) events() <-chan startWatchChange { return w.notify }

func (w *linuxStartWatcher) close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	_ = w.file.Close()
	<-w.done
}

func (w *linuxStartWatcher) updateDocs(items map[string]itemRow) {
	docs := make(map[string]bool)
	for _, row := range items {
		if row.DesignDoc != "" {
			path := row.DesignDoc
			if !filepath.IsAbs(path) {
				path = filepath.Join(w.paths.root, path)
			}
			docs[filepath.Dir(path)] = true
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed && !maps.Equal(docs, w.docs) {
		w.docs = docs
		w.bind()
	}
}

// Bind on startup, on a change to document directories, or when a watched
// directory moves, disappears or gets a new subdirectory. For a missing
// directory, watch the nearest existing ancestor for its creation instead.
func (w *linuxStartWatcher) bind() {
	const mask = unix.IN_ONLYDIR | unix.IN_MODIFY | unix.IN_CLOSE_WRITE | unix.IN_CREATE |
		unix.IN_MOVED_TO | unix.IN_MOVED_FROM | unix.IN_DELETE | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF
	bound := make(map[int32][]startWatchRule)
	add := func(dir, name string, change startWatchChange) {
		for {
			wd, err := unix.InotifyAddWatch(w.fd, dir, mask)
			if err == nil {
				bound[int32(wd)] = append(bound[int32(wd)], startWatchRule{name, change})
				return
			}
			parent := filepath.Dir(dir)
			if err != unix.ENOENT || parent == dir {
				return // The safety timer still runs if notification is unavailable.
			}
			dir, name = parent, filepath.Base(dir)
		}
	}
	file := func(path string, change startWatchChange) {
		if path != "" {
			add(filepath.Dir(path), filepath.Base(path), change)
		}
	}
	paths := w.paths
	if paths.store != "" {
		for _, suffix := range []string{"", "-wal", "-journal"} {
			file(paths.store+suffix, startWatchStore)
		}
	}
	file(paths.index, startWatchExternal)
	if paths.gitDir != "" {
		file(filepath.Join(paths.gitDir, "HEAD"), startWatchExternal)
	}
	if paths.commonDir != "" {
		for _, ref := range []string{"packed-refs", "refs/heads/" + trunkBranchFor(paths.root), "refs/tillr/green"} {
			file(filepath.Join(paths.commonDir, ref), startWatchExternal)
		}
	}
	if paths.root != "" {
		add(paths.root, "", startWatchExternal)
		docs := filepath.Join(paths.root, "docs")
		// Include docs itself even before it is created.
		add(docs, "", startWatchExternal)
		_ = filepath.WalkDir(docs, func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() && path != docs {
				add(path, "", startWatchExternal)
			}
			return nil
		})
	}
	for dir := range w.docs {
		add(dir, "", startWatchExternal)
	}
	for wd := range w.rules {
		if _, keep := bound[wd]; !keep {
			_, _ = unix.InotifyRmWatch(w.fd, uint32(wd))
		}
	}
	w.rules = bound
}

func (w *linuxStartWatcher) readLoop() {
	defer close(w.done)
	defer close(w.notify)
	buf := make([]byte, 32*1024)
	for {
		n, err := w.file.Read(buf)
		if err != nil {
			return
		}
		if change := w.changes(buf[:n]); change != 0 {
			// Only this goroutine sends. Merge a pending wake without blocking
			// when a refresh is still in flight.
			select {
			case previous := <-w.notify:
				change |= previous
			default:
			}
			w.notify <- change
		}
	}
}

func (w *linuxStartWatcher) changes(buf []byte) startWatchChange {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0
	}
	var change startWatchChange
	rebind := false
	for len(buf) >= unix.SizeofInotifyEvent {
		wd := int32(binary.NativeEndian.Uint32(buf))
		mask := binary.NativeEndian.Uint32(buf[4:])
		size := uint64(binary.NativeEndian.Uint32(buf[12:])) + unix.SizeofInotifyEvent
		if size > uint64(len(buf)) {
			break
		}
		name := strings.TrimRight(string(buf[unix.SizeofInotifyEvent:size]), "\x00")
		buf = buf[size:]
		if mask&unix.IN_Q_OVERFLOW != 0 {
			change |= startWatchExternal | startWatchStore
			rebind = true
			continue
		}
		self := mask&(unix.IN_DELETE_SELF|unix.IN_MOVE_SELF|unix.IN_IGNORED) != 0
		dir := mask&unix.IN_ISDIR != 0
		for _, rule := range w.rules[wd] {
			if self || (rule.name != "" && rule.name == name) ||
				(rule.name == "" && (dir || strings.HasSuffix(name, ".md"))) {
				change |= rule.change
				rebind = rebind || self || dir
			}
		}
	}
	if rebind {
		w.bind()
	}
	return change
}
