package notify

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const notifyHangGuard = 5 * time.Minute

func TestSocketPathCanonicalizesStoreAndRespectsRuntimeDir(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real", strings.Repeat("deep-segment-", 12))
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(base, "symlink-to-real")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatal(err)
	}

	realStore := filepath.Join(realDir, "state.db")
	linkStore := filepath.Join(linkDir, "state.db")
	directFileLink := filepath.Join(base, "linked.db")
	if err := os.Symlink(realStore, directFileLink); err != nil {
		t.Fatal(err)
	}

	// Before state.db exists on disk, both a symlinked parent directory and a
	// dangling file symlink resolve to the same canonical path as realStore.
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/4242")
	beforeReal, err := SocketPath(realStore)
	if err != nil {
		t.Fatal(err)
	}
	beforeLink, err := SocketPath(linkStore)
	if err != nil {
		t.Fatal(err)
	}
	beforeDirectLink, err := SocketPath(directFileLink)
	if err != nil {
		t.Fatal(err)
	}
	if beforeReal != beforeLink || beforeReal != beforeDirectLink {
		t.Fatalf("symlinked store paths hashed differently before creation: real=%q dirLink=%q fileLink=%q", beforeReal, beforeLink, beforeDirectLink)
	}

	// Create state.db and verify the socket path is unchanged.
	if err := os.WriteFile(realStore, []byte("sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	afterLink, err := SocketPath(linkStore)
	if err != nil {
		t.Fatal(err)
	}
	afterDirectLink, err := SocketPath(directFileLink)
	if err != nil {
		t.Fatal(err)
	}
	if afterLink != beforeReal || afterDirectLink != beforeReal {
		t.Fatalf("socket path changed after state.db creation: afterDir=%q afterFile=%q want=%q", afterLink, afterDirectLink, beforeReal)
	}

	resolvedReal, err := filepath.EvalSymlinks(realStore)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(resolvedReal))
	wantName := hex.EncodeToString(sum[:])[:16] + ".sock"
	wantXDG := filepath.Join("/run/user/4242", "tillr", wantName)
	if beforeReal != wantXDG {
		t.Fatalf("SocketPath with XDG_RUNTIME_DIR = %q, want %q", beforeReal, wantXDG)
	}
	if len(beforeReal) >= 104 {
		t.Fatalf("SocketPath length = %d, exceeds UNIX_PATH_MAX budget: %q", len(beforeReal), beforeReal)
	}
	if gotLock := LockPath(beforeReal); gotLock != beforeReal+".lock" {
		t.Fatalf("LockPath = %q, want %q", gotLock, beforeReal+".lock")
	}

	// Fallback when XDG_RUNTIME_DIR is unset.
	t.Setenv("XDG_RUNTIME_DIR", "")
	fallback, err := SocketPath(linkStore)
	if err != nil {
		t.Fatal(err)
	}
	wantFallback := filepath.Join("/tmp", fmt.Sprintf("tillr-%d", os.Getuid()), wantName)
	if fallback != wantFallback {
		t.Fatalf("SocketPath without XDG_RUNTIME_DIR = %q, want %q", fallback, wantFallback)
	}
}

func TestPublishWithoutRelayAndStalledListenerNeverBlockOrSpawn(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	storePath := filepath.Join(t.TempDir(), "state.db")
	sockPath, err := SocketPath(storePath)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(sockPath)

	err = Publish(storePath, Frame{
		Topic:  TopicQueueChanged,
		ItemID: "item-1",
		Seq:    7,
		AtMS:   1763936280000,
	})
	if err != nil {
		t.Fatalf("Publish without relay returned error: %v", err)
	}
	if _, statErr := os.Stat(sockPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Publish without relay created socket %s: stat err = %v", sockPath, statErr)
	}

	// Bind a stalled Unix listener that accepts the connection, reads the pub
	// frame, and holds the socket open without sending an ack. Publish must hit
	// its internal PublishTimeout deadline, close its client socket (delivering
	// io.EOF to the stalled server), and return nil without blocking.
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	stalled, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stalled.SetUnlinkOnClose(true)
	defer stalled.Close()

	type stalledObservation struct {
		line string
		eof  error
	}
	observed := make(chan stalledObservation, 1)
	releaseServer := make(chan struct{})
	defer close(releaseServer)
	go func() {
		srvConn, acceptErr := stalled.AcceptUnix()
		if acceptErr != nil {
			return
		}
		defer srvConn.Close()
		br := bufio.NewReader(srvConn)
		line, readErr := br.ReadString('\n')
		if readErr != nil {
			observed <- stalledObservation{eof: readErr}
			return
		}
		_, eofErr := br.ReadByte()
		observed <- stalledObservation{line: line, eof: eofErr}
		<-releaseServer
	}()

	pubDone := make(chan error, 1)
	go func() {
		pubDone <- Publish(storePath, Frame{Topic: TopicQueueChanged, Seq: 8})
	}()

	select {
	case err := <-pubDone:
		if err != nil {
			t.Fatalf("Publish against stalled listener returned error: %v", err)
		}
	case <-time.After(notifyHangGuard):
		t.Fatal("Publish against stalled listener deadlocked instead of timing out")
	}

	select {
	case obs := <-observed:
		if !errors.Is(obs.eof, io.EOF) {
			t.Fatalf("stalled listener read error = %v after Publish returned, want io.EOF", obs.eof)
		}
		var got wireMsg
		if err := json.Unmarshal([]byte(obs.line), &got); err != nil {
			t.Fatalf("unmarshaling published frame %q: %v", obs.line, err)
		}
		if got.Op != "pub" || got.Topic != TopicQueueChanged || got.Seq != 8 || !got.Ack {
			t.Fatalf("stalled listener received %+v, want op=pub topic=queue.changed seq=8 ack=true", got)
		}
	case <-time.After(notifyHangGuard):
		t.Fatal("stalled listener did not observe client socket close after Publish returned")
	}
}

func TestSubscriberQueueExact256BoundaryAndResync(t *testing.T) {
	q := newSubscriberQueue()
	defer q.close()

	// Exactly 256 frames fit without overflow.
	for i := 1; i <= SubscriberBufferSize; i++ {
		line, err := encodeFrameLine(Frame{Topic: TopicQueueChanged, Seq: int64(i)})
		if err != nil {
			t.Fatal(err)
		}
		q.push(line)
	}
	for i := 1; i <= SubscriberBufferSize; i++ {
		raw, ok := q.pop()
		if !ok {
			t.Fatalf("queue closed early at frame %d", i)
		}
		var f Frame
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		if f.Topic != TopicQueueChanged || f.Seq != int64(i) {
			t.Fatalf("frame %d = %+v, want seq %d", i, f, i)
		}
	}

	// Pushing >256 frames without draining drops the 256 queued frames and emits
	// one {"topic":"resync"} frame, coalescing further overflow frames until read.
	for i := 1; i <= SubscriberBufferSize+5; i++ {
		line, err := encodeFrameLine(Frame{Topic: TopicQueueChanged, Seq: int64(i)})
		if err != nil {
			t.Fatal(err)
		}
		q.push(line)
	}
	raw, ok := q.pop()
	if !ok {
		t.Fatal("queue closed before delivering resync")
	}
	if string(raw) != "{\"topic\":\"resync\"}\n" {
		t.Fatalf("overflow frame = %q, want %q", string(raw), "{\"topic\":\"resync\"}\n")
	}

	// After resync is delivered, subsequent frames flow normally again.
	afterLine, err := encodeFrameLine(Frame{Topic: TopicTrunkMoved, Seq: 999})
	if err != nil {
		t.Fatal(err)
	}
	q.push(afterLine)
	nextRaw, ok := q.pop()
	if !ok {
		t.Fatal("queue closed after resync")
	}
	var after Frame
	if err := json.Unmarshal(nextRaw, &after); err != nil {
		t.Fatal(err)
	}
	if after.Topic != TopicTrunkMoved || after.Seq != 999 {
		t.Fatalf("frame after resync = %+v, want trunk.moved seq 999", after)
	}
}

func TestServeWireProtocolSingleInstancePermissionsAndIdleExit(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	storePath := filepath.Join(t.TempDir(), "state.db")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan string, 1)
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- Serve(ctx, ServeConfig{
			StorePath: storePath,
			IdleExit:  150 * time.Millisecond,
			Ready:     ready,
		})
	}()

	var sockPath string
	select {
	case sockPath = <-ready:
	case err := <-serveDone:
		t.Fatalf("Serve exited before ready: %v", err)
	case <-time.After(notifyHangGuard):
		t.Fatal("timed out waiting for relay ready")
	}

	dirInfo, err := os.Stat(filepath.Dir(sockPath))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("runtime dir permissions = %04o, want 0700", got)
	}
	sockInfo, err := os.Stat(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := sockInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket permissions = %04o, want 0600", got)
	}

	// A second Serve on the same store refuses via flock(LOCK_EX|LOCK_NB) without
	// unlinking the active socket.
	err = Serve(ctx, ServeConfig{StorePath: storePath, IdleExit: time.Second})
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Serve error = %v, want ErrAlreadyRunning", err)
	}

	// Connect a raw wire subscriber matching Section 4.2's handshake verbatim,
	// and verify the relay stays alive past IdleExit while connected.
	rawSub, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rawSub.Close()
	if _, err := rawSub.Write([]byte(`{"op":"sub","topics":["run.*","candidate.*","gate.*","trunk.*","queue.*"]}` + "\n")); err != nil {
		t.Fatal(err)
	}

	// Also connect via Go Subscribe to synchronize registration deterministically.
	typedSub, err := Subscribe(ctx, storePath, "run.*")
	if err != nil {
		t.Fatal(err)
	}
	defer typedSub.Close()

	// Stay connected longer than IdleExit (150ms) to prove active subscribers
	// suppress idle shutdown.
	select {
	case err := <-serveDone:
		t.Fatalf("Serve exited while subscribers were connected: %v", err)
	case <-time.After(220 * time.Millisecond):
	}

	// Oversized (> 4 KiB) frame is dropped without crashing the relay.
	oversizedConn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = oversizedConn.Write([]byte(`{"op":"pub","topic":"run.exited","item_id":"` + strings.Repeat("x", MaxFrameBytes) + `"}` + "\n"))
	_ = oversizedConn.Close()

	// Unmatched topic (docs.changed) is filtered out; matched topic (run.exited) is relayed.
	if err := Publish(storePath, Frame{Topic: TopicDocsChanged, Seq: 1}); err != nil {
		t.Fatal(err)
	}

	rawPub, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawPub.Write([]byte(`{"op":"pub","topic":"run.exited","item_id":"item-9","run_id":"run-9","seq":4812,"at_ms":1763936280000}` + "\n")); err != nil {
		t.Fatal(err)
	}
	_ = rawPub.Close()

	readCtx, readCancel := context.WithTimeout(ctx, notifyHangGuard)
	defer readCancel()
	got, err := typedSub.Next(readCtx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Topic != TopicRunExited || got.ItemID != "item-9" || got.RunID != "run-9" || got.Seq != 4812 || got.AtMS != 1763936280000 {
		t.Fatalf("typed subscriber got %+v", got)
	}

	_ = rawSub.SetReadDeadline(time.Now().Add(notifyHangGuard))
	rawLine, err := bufio.NewReader(rawSub).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var rawGot Frame
	if err := json.Unmarshal([]byte(rawLine), &rawGot); err != nil {
		t.Fatalf("unmarshaling raw frame %q: %v", rawLine, err)
	}
	if rawGot != got {
		t.Fatalf("raw subscriber got %+v, want %+v", rawGot, got)
	}

	// Disconnect all subscribers: relay must exit cleanly after IdleExit and unlink <sock>.
	_ = rawSub.Close()
	_ = typedSub.Close()

	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("Serve idle exit returned error: %v", err)
		}
	case <-time.After(notifyHangGuard):
		t.Fatal("Serve did not exit after subscribers disconnected and idle-exit elapsed")
	}

	if _, statErr := os.Stat(sockPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("socket %s still exists after idle exit: %v", sockPath, statErr)
	}
	_ = os.Remove(LockPath(sockPath))
}

func TestSlowSubscriberOverSocketReceivesResyncWithoutBlockingFastSubscriber(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	storePath := filepath.Join(t.TempDir(), "state.db")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan string, 1)
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- Serve(ctx, ServeConfig{
			StorePath: storePath,
			IdleExit:  notifyHangGuard,
			Ready:     ready,
		})
	}()
	var sockPath string
	select {
	case sockPath = <-ready:
	case <-time.After(notifyHangGuard):
		t.Fatal("timed out waiting for relay")
	}
	t.Cleanup(func() { _ = os.Remove(LockPath(sockPath)) })

	slowSub, err := Subscribe(ctx, storePath, "queue.*")
	if err != nil {
		t.Fatal(err)
	}
	defer slowSub.Close()

	// Wedge slowSub past both its kernel socket buffer and its 256-frame ring buffer
	// without reading a single byte from slowSub.
	const burstCount = 800
	burst := make([]Frame, burstCount)
	for i := 0; i < burstCount; i++ {
		burst[i] = Frame{
			Topic:  TopicQueueChanged,
			ItemID: "item-burst",
			Seq:    int64(i + 1),
		}
	}
	pubCtx, pubCancel := context.WithTimeout(ctx, notifyHangGuard)
	defer pubCancel()
	if err := PublishContext(pubCtx, storePath, burst...); err != nil {
		t.Fatal(err)
	}

	// With slowSub completely wedged, connect fastSub, verify ordinary Publish
	// returns without blocking on slowSub, and verify fastSub receives a live batch.
	fastSub, err := Subscribe(ctx, storePath, "queue.*")
	if err != nil {
		t.Fatal(err)
	}
	defer fastSub.Close()

	if err := Publish(storePath, Frame{
		Topic:  TopicQueueChanged,
		ItemID: "item-nonblocking-probe",
		Seq:    999,
	}); err != nil {
		t.Fatal(err)
	}

	const fastCount = 20
	live := make([]Frame, fastCount)
	for i := 0; i < fastCount; i++ {
		live[i] = Frame{
			Topic:  TopicQueueChanged,
			ItemID: "item-live",
			Seq:    int64(1001 + i),
		}
	}
	if err := PublishContext(pubCtx, storePath, live...); err != nil {
		t.Fatal(err)
	}

	readCtx, readCancel := context.WithTimeout(ctx, notifyHangGuard)
	defer readCancel()
	var seenLive int
	for seenLive < fastCount {
		f, err := fastSub.Next(readCtx)
		if err != nil {
			t.Fatalf("fast subscriber read: %v", err)
		}
		if f.ItemID == "item-nonblocking-probe" {
			continue
		}
		wantSeq := int64(1001 + seenLive)
		if f.Topic != TopicQueueChanged || f.ItemID != "item-live" || f.Seq != wantSeq {
			t.Fatalf("fast subscriber frame %d = %+v, want seq %d", seenLive+1, f, wantSeq)
		}
		seenLive++
	}

	// Now drain slowSub: it fell >256 frames behind and must receive {"topic":"resync"}.
	sawResync := false
	for i := 0; i < burstCount+fastCount; i++ {
		f, err := slowSub.Next(readCtx)
		if err != nil {
			t.Fatalf("reading from slow subscriber before resync: %v", err)
		}
		if f.Topic == TopicResync {
			sawResync = true
			break
		}
	}
	if !sawResync {
		t.Fatal("slow subscriber >256 frames behind never received resync frame")
	}

	cancel()
	<-serveDone
}
