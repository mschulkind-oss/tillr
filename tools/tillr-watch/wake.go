package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/mschulkind-oss/tillr/store"
)

var (
	errRollback     = errors.New("rollback")
	onWaitForWriter atomic.Pointer[func()]
)

type walCommit struct {
	valid    bool
	salt     [8]byte
	pageSize int64
	frame    int
	cksum    [2]uint32
	dbSize   uint32
	pages    map[uint32][]byte
}

type verifiedCommit struct {
	valid   bool
	salt    [8]byte
	frame   int
	version uint64
}

type wakeSource struct {
	wal            string
	store          *store.Store
	watcher        *fsnotify.Watcher
	ticks          <-chan time.Time
	ticker         *time.Ticker
	minWALInterval time.Duration
	lastWALWake    time.Time
	hasPass        bool
	passVersion    uint64
	commit         walCommit
	verified       verifiedCommit
}

// Watch the parent directory, not the WAL inode: SQLite may unlink and
// recreate the file. Both callers install this before the first evaluation.
func watchWAL(path string, ticks <-chan time.Time) (*wakeSource, error) {
	wal := filepath.Clean(path + "-wal")
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(filepath.Dir(wal)); err != nil {
		w.Close()
		return nil, err
	}
	s := &wakeSource{wal: wal, watcher: w, ticks: ticks}
	if ticks == nil {
		s.ticker = time.NewTicker(60 * time.Second)
		s.ticks = s.ticker.C
	}
	return s, nil
}

func watchStore(st *store.Store, ticks <-chan time.Time) (*wakeSource, error) {
	s, err := watchWAL(st.Path(), ticks)
	if err != nil {
		return nil, err
	}
	s.store = st
	return s, nil
}

func (s *wakeSource) close() {
	if s.ticker != nil {
		s.ticker.Stop()
	}
	s.watcher.Close()
}

// beginPass records the store's data_version before evaluating a pass so next
// can tell whether a WAL commit written before or during evaluation had not yet
// published its WAL-index header in -shm when this pass read the store.
func (s *wakeSource) beginPass(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	return s.store.ReadTx(ctx, func(rd *store.Reader) error {
		v, err := readDataVersion(rd)
		if err != nil {
			return err
		}
		s.passVersion = v
		s.hasPass = true
		return nil
	})
}

type wakeReason int

const (
	walWake wakeReason = iota
	timerWake
	deadlineWake
)

func (s *wakeSource) next(ctx context.Context, deadline <-chan time.Time) (wakeReason, error) {
	if ctx.Err() != nil {
		return walWake, ctx.Err()
	}
	select {
	case <-deadline:
		return deadlineWake, nil
	default:
	}
	if s.store != nil && s.hasPass {
		if reason, done, err := s.waitWALInterval(ctx, deadline); err != nil || done {
			return reason, err
		}
		reason, woke, err := s.checkUnseenCommit(ctx, deadline)
		if err != nil {
			return reason, err
		}
		if woke {
			if reason == walWake {
				s.lastWALWake = time.Now()
			}
			return reason, nil
		}
	}
	for {
		select {
		case <-ctx.Done():
			return walWake, ctx.Err()
		case <-deadline:
			return deadlineWake, nil
		case <-s.ticks:
			return timerWake, nil
		case evt, ok := <-s.watcher.Events:
			if !ok {
				return walWake, fmt.Errorf("WAL watcher closed")
			}
			if filepath.Clean(evt.Name) == s.wal {
				if err := s.drainEvents(); err != nil {
					return walWake, err
				}
				if reason, done, err := s.waitWALInterval(ctx, deadline); err != nil || done {
					return reason, err
				}
				if s.store != nil && s.hasPass {
					reason, woke, err := s.checkUnseenCommit(ctx, deadline)
					if err != nil {
						return reason, err
					}
					if !woke {
						continue
					}
					if reason == walWake {
						s.lastWALWake = time.Now()
					}
					return reason, nil
				}
				s.lastWALWake = time.Now()
				return walWake, nil
			}
		case err, ok := <-s.watcher.Errors:
			if !ok {
				return walWake, fmt.Errorf("WAL watcher closed")
			}
			if err != fsnotify.ErrEventOverflow {
				return walWake, err
			}
			// A dropped change could have fired a rule; read a fresh snapshot.
			s.lastWALWake = time.Now()
			return walWake, nil
		}
	}
}

func (s *wakeSource) waitWALInterval(ctx context.Context, deadline <-chan time.Time) (wakeReason, bool, error) {
	if s.minWALInterval <= 0 || s.lastWALWake.IsZero() {
		return walWake, false, nil
	}
	rem := time.Until(s.lastWALWake.Add(s.minWALInterval))
	if rem <= 0 {
		return walWake, false, nil
	}
	timer := time.NewTimer(rem)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return walWake, true, ctx.Err()
	case <-deadline:
		if s.store != nil && s.hasPass {
			closed := make(chan time.Time)
			close(closed)
			reason, woke, err := s.checkUnseenCommit(ctx, closed)
			if err != nil {
				return reason, true, err
			}
			if woke && reason == walWake {
				s.lastWALWake = time.Now()
				return walWake, true, nil
			}
		}
		return deadlineWake, true, nil
	case <-s.ticks:
		return timerWake, true, nil
	case <-timer.C:
		if err := s.drainEvents(); err != nil {
			return walWake, true, err
		}
		return walWake, false, nil
	}
}

// Coalesce already-queued notifications; the next pass reads the latest snapshot.
func (s *wakeSource) drainEvents() error {
	for {
		select {
		case _, ok := <-s.watcher.Events:
			if !ok {
				return fmt.Errorf("WAL watcher closed")
			}
		default:
			return nil
		}
	}
}

// SQLite writes dirty frames (including the commit record) to -wal before it
// updates the shared-memory WAL-index header in -shm, which emits no fsnotify
// event. Before sleeping, check whether -shm advanced after beginPass or -wal
// already holds a committed transaction not yet visible in -shm. Never open
// -shm directly: closing any descriptor on -shm drops POSIX record locks held
// by SQLite connections in this process.
func (s *wakeSource) checkUnseenCommit(ctx context.Context, deadline <-chan time.Time) (wakeReason, bool, error) {
	var publishedAfterPass, unpublishedCommit bool
	err := s.store.ReadTx(ctx, func(rd *store.Reader) error {
		v, err := readDataVersion(rd)
		if err != nil {
			return err
		}
		if v != s.passVersion {
			publishedAfterPass = true
			return nil
		}
		s.advanceWALCommit()
		if !s.commit.valid || s.commit.frame == 0 {
			return nil
		}
		if s.verified.valid && s.verified.salt == s.commit.salt && s.verified.frame == s.commit.frame && s.verified.version == v {
			return nil
		}
		visible, err := committedPagesVisible(rd, s.commit)
		if err != nil {
			return err
		}
		if !visible {
			unpublishedCommit = true
			return nil
		}
		s.verified = verifiedCommit{valid: true, salt: s.commit.salt, frame: s.commit.frame, version: v}
		return nil
	})
	if err != nil {
		return walWake, false, err
	}
	if publishedAfterPass {
		if err := s.drainEvents(); err != nil {
			return walWake, true, err
		}
		return walWake, true, nil
	}
	if unpublishedCommit {
		reason, err := s.waitForWriter(ctx, deadline)
		return reason, true, err
	}
	return walWake, false, nil
}

func readDataVersion(rd *store.Reader) (uint64, error) {
	// Touch page 1 first so SQLite opens the deferred WAL read transaction and
	// updates pPager->iDataVersion when another connection has committed.
	var size int
	if err := rd.QueryRow(`SELECT length(data) FROM sqlite_dbpage WHERE pgno = 1`).Scan(&size); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	var v uint64
	if err := rd.QueryRow(`PRAGMA data_version`).Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}

func committedPagesVisible(rd *store.Reader, c walCommit) (bool, error) {
	var count uint32
	if err := rd.QueryRow(`PRAGMA page_count`).Scan(&count); err != nil {
		return false, err
	}
	if count != c.dbSize {
		return false, nil
	}
	for pgno, want := range c.pages {
		if pgno > c.dbSize {
			continue
		}
		var got []byte
		err := rd.QueryRow(`SELECT data FROM sqlite_dbpage WHERE pgno = ?`, pgno).Scan(&got)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !bytes.Equal(got, want) {
			return false, nil
		}
	}
	return true, nil
}

func (s *wakeSource) waitForWriter(ctx context.Context, deadline <-chan time.Time) (wakeReason, error) {
	if fn := onWaitForWriter.Load(); fn != nil {
		(*fn)()
	}
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	timedOut := make(chan struct{})
	if deadline != nil {
		go func() {
			select {
			case <-deadline:
				close(timedOut)
				cancel()
			case <-waitCtx.Done():
			}
		}()
	}
	err := s.store.WriteTx(waitCtx, func(*store.Tx) error { return errRollback })
	if err != nil && !errors.Is(err, errRollback) {
		select {
		case <-timedOut:
			return deadlineWake, nil
		default:
		}
		if ctx.Err() != nil {
			return walWake, ctx.Err()
		}
		if store.IsBusy(err) {
			return walWake, nil
		}
		return walWake, err
	}
	if err := s.drainEvents(); err != nil {
		return walWake, err
	}
	return walWake, nil
}

// advanceWALCommit scans newly written frames in -wal (https://www.sqlite.org/walformat.html)
// starting after the last verified commit frame, keeping incremental O(delta) cost.
func (s *wakeSource) advanceWALCommit() {
	f, err := os.Open(s.wal)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.commit = walCommit{}
		}
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() < 32 {
		s.commit = walCommit{}
		return
	}
	var hdr [32]byte
	if _, err := f.ReadAt(hdr[:], 0); err != nil {
		s.commit = walCommit{}
		return
	}
	magic := binary.BigEndian.Uint32(hdr[0:4])
	if magic != 0x377f0682 && magic != 0x377f0683 {
		s.commit = walCommit{}
		return
	}
	bigEnd := magic == 0x377f0683
	pageSize := int64(binary.BigEndian.Uint32(hdr[8:12]))
	if pageSize == 1 {
		pageSize = 65536
	}
	if pageSize < 512 || pageSize > 65536 || pageSize&(pageSize-1) != 0 {
		s.commit = walCommit{}
		return
	}
	c1, c2 := walChecksum(bigEnd, hdr[0:24], 0, 0)
	if c1 != binary.BigEndian.Uint32(hdr[24:28]) || c2 != binary.BigEndian.Uint32(hdr[28:32]) {
		s.commit = walCommit{}
		return
	}
	var salt [8]byte
	copy(salt[:], hdr[16:24])
	frameSize := 24 + pageSize
	if !s.commit.valid || s.commit.salt != salt || s.commit.pageSize != pageSize || info.Size() < 32+int64(s.commit.frame)*frameSize {
		s.commit = walCommit{valid: true, salt: salt, pageSize: pageSize, cksum: [2]uint32{c1, c2}}
	}
	maxFrames := int((info.Size() - 32) / frameSize)
	if s.commit.frame >= maxFrames {
		return
	}
	type commitSpan struct {
		startIdx int
		endIdx   int
		dbSize   uint32
		cksum    [2]uint32
		preCksum [2]uint32
	}
	const chunkFrames = 64
	startFrame := s.commit.frame + 1
	totalFrames := maxFrames - startFrame + 1
	bufFrames := min(totalFrames, chunkFrames)
	chunk := make([]byte, int64(bufFrames)*frameSize)

	s1, s2 := s.commit.cksum[0], s.commit.cksum[1]
	txStart := startFrame
	txPreCksum := s.commit.cksum
	var spans []commitSpan
	for base := startFrame; base <= maxFrames; base += bufFrames {
		count := min(bufFrames, maxFrames-base+1)
		wantBytes := int64(count) * frameSize
		if _, err := f.ReadAt(chunk[:wantBytes], 32+int64(base-1)*frameSize); err != nil {
			break
		}
		stopped := false
		for i := 0; i < count; i++ {
			frame := chunk[int64(i)*frameSize : int64(i+1)*frameSize]
			if !bytes.Equal(frame[8:16], salt[:]) {
				stopped = true
				break
			}
			s1, s2 = walChecksum(bigEnd, frame[0:8], s1, s2)
			s1, s2 = walChecksum(bigEnd, frame[24:], s1, s2)
			if s1 != binary.BigEndian.Uint32(frame[16:20]) || s2 != binary.BigEndian.Uint32(frame[20:24]) {
				stopped = true
				break
			}
			idx := base + i
			dbSize := binary.BigEndian.Uint32(frame[4:8])
			if dbSize > 0 {
				spans = append(spans, commitSpan{
					startIdx: txStart,
					endIdx:   idx,
					dbSize:   dbSize,
					cksum:    [2]uint32{s1, s2},
					preCksum: txPreCksum,
				})
				txStart = idx + 1
				txPreCksum = [2]uint32{s1, s2}
			}
		}
		if stopped {
			break
		}
	}
	if len(spans) == 0 {
		return
	}
	firstNeeded := len(spans) - 1
	for firstNeeded > 0 && spans[firstNeeded].startIdx == spans[firstNeeded].endIdx && spans[firstNeeded].dbSize == spans[firstNeeded-1].dbSize {
		firstNeeded--
	}
	replayStart := spans[firstNeeded].startIdx
	replayEnd := spans[len(spans)-1].endIdx
	s1, s2 = spans[firstNeeded].preCksum[0], spans[firstNeeded].preCksum[1]
	var txPages map[uint32][]byte
	for base := replayStart; base <= replayEnd; base += bufFrames {
		count := min(bufFrames, replayEnd-base+1)
		wantBytes := int64(count) * frameSize
		if _, err := f.ReadAt(chunk[:wantBytes], 32+int64(base-1)*frameSize); err != nil {
			return
		}
		for i := 0; i < count; i++ {
			idx := base + i
			frame := chunk[int64(i)*frameSize : int64(i+1)*frameSize]
			s1, s2 = walChecksum(bigEnd, frame[0:8], s1, s2)
			s1, s2 = walChecksum(bigEnd, frame[24:], s1, s2)
			pgno := binary.BigEndian.Uint32(frame[0:4])
			dbSize := binary.BigEndian.Uint32(frame[4:8])
			if txPages == nil {
				txPages = make(map[uint32][]byte)
			}
			page := make([]byte, pageSize)
			copy(page, frame[24:])
			txPages[pgno] = page
			if dbSize > 0 {
				if len(txPages) == 1 && s.commit.pages != nil && dbSize == s.commit.dbSize && bytes.Equal(s.commit.pages[pgno], page) {
					// Sector-boundary padding repeats the commit frame verbatim.
					s.commit.frame = idx
					s.commit.cksum = [2]uint32{s1, s2}
				} else {
					s.commit.frame = idx
					s.commit.cksum = [2]uint32{s1, s2}
					s.commit.dbSize = dbSize
					s.commit.pages = txPages
				}
				txPages = nil
			}
		}
	}
}

func walChecksum(bigEnd bool, data []byte, s1, s2 uint32) (uint32, uint32) {
	if bigEnd {
		for i := 0; i+8 <= len(data); i += 8 {
			s1 += binary.BigEndian.Uint32(data[i:i+4]) + s2
			s2 += binary.BigEndian.Uint32(data[i+4:i+8]) + s1
		}
		return s1, s2
	}
	for i := 0; i+8 <= len(data); i += 8 {
		s1 += binary.LittleEndian.Uint32(data[i:i+4]) + s2
		s2 += binary.LittleEndian.Uint32(data[i+4:i+8]) + s1
	}
	return s1, s2
}
