//go:build linux

package bgactors

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

// DocsWatcher watches tracked Markdown directories for edits and signals
// changes to the document/comment sync actor.
type DocsWatcher interface {
	Events() <-chan struct{}
	UpdateDocs(dirs map[string]bool)
	Close()
}

type linuxDocsWatcher struct {
	file   *os.File
	fd     int
	root   string
	notify chan struct{}
	done   chan struct{}

	mu     sync.Mutex
	closed bool
	docs   map[string]bool
	rules  map[int32]bool
}

// NewDocsWatcher creates a Linux inotify watcher over root and its docs tree.
func NewDocsWatcher(root string) DocsWatcher {
	if root == "" {
		return nil
	}
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil
	}
	w := &linuxDocsWatcher{
		file:   os.NewFile(uintptr(fd), "tillr-docs-inotify"),
		fd:     fd,
		root:   root,
		notify: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	w.bind()
	go w.readLoop()
	return w
}

func (w *linuxDocsWatcher) Events() <-chan struct{} { return w.notify }

func (w *linuxDocsWatcher) Close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	_ = w.file.Close()
	<-w.done
}

func (w *linuxDocsWatcher) UpdateDocs(dirs map[string]bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed && !maps.Equal(dirs, w.docs) {
		w.docs = maps.Clone(dirs)
		w.bind()
	}
}

func (w *linuxDocsWatcher) bind() {
	const mask = unix.IN_ONLYDIR | unix.IN_MODIFY | unix.IN_CLOSE_WRITE | unix.IN_CREATE |
		unix.IN_MOVED_TO | unix.IN_MOVED_FROM | unix.IN_DELETE | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF
	bound := make(map[int32]bool)
	add := func(dir string) {
		for {
			wd, err := unix.InotifyAddWatch(w.fd, dir, mask)
			if err == nil {
				bound[int32(wd)] = true
				return
			}
			parent := filepath.Dir(dir)
			if err != unix.ENOENT || parent == dir {
				return
			}
			dir = parent
		}
	}
	add(w.root)
	docs := filepath.Join(w.root, "docs")
	add(docs)
	_ = filepath.WalkDir(docs, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && path != docs {
			add(path)
		}
		return nil
	})
	for dir := range w.docs {
		add(dir)
	}
	for wd := range w.rules {
		if !bound[wd] {
			_, _ = unix.InotifyRmWatch(w.fd, uint32(wd))
		}
	}
	w.rules = bound
}

func (w *linuxDocsWatcher) readLoop() {
	defer close(w.done)
	defer close(w.notify)
	buf := make([]byte, 32*1024)
	for {
		n, err := w.file.Read(buf)
		if err != nil {
			return
		}
		if w.changed(buf[:n]) {
			select {
			case w.notify <- struct{}{}:
			default:
			}
		}
	}
}

func (w *linuxDocsWatcher) changed(buf []byte) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return false
	}
	matched := false
	rebind := false
	for len(buf) >= unix.SizeofInotifyEvent {
		mask := binary.NativeEndian.Uint32(buf[4:])
		size := uint64(binary.NativeEndian.Uint32(buf[12:])) + unix.SizeofInotifyEvent
		if size > uint64(len(buf)) {
			break
		}
		name := strings.TrimRight(string(buf[unix.SizeofInotifyEvent:size]), "\x00")
		buf = buf[size:]
		if mask&unix.IN_Q_OVERFLOW != 0 {
			matched = true
			rebind = true
			continue
		}
		self := mask&(unix.IN_DELETE_SELF|unix.IN_MOVE_SELF|unix.IN_IGNORED) != 0
		dir := mask&unix.IN_ISDIR != 0
		if self || dir || strings.HasSuffix(name, ".md") {
			matched = true
			rebind = rebind || self || dir
		}
	}
	if rebind {
		w.bind()
	}
	return matched
}
