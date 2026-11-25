package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mschulkind-oss/tillr-drain/restart"
)

func servingOn(t *testing.T, handler http.Handler) (*serving, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	sv := &serving{srv: &server{}, addr: addr, fatal: make(chan error, 4), handler: func() http.Handler { return handler }}
	sv.start(listener)
	t.Cleanup(func() { sv.current().Close() })
	return sv, addr
}

func fixtureRestarter(t *testing.T) (*restart.Restarter, restart.Request) {
	t.Helper()
	store := newServeHarness(t).store
	request, err := restart.File(context.Background(), store, restart.RoleView, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return &restart.Restarter{Store: store, Role: restart.RoleView, Running: restart.Build{Line: "old", Digest: "old"}, Executable: "/x", Disk: func(string) string { return "new" }}, request
}

func get(addr string) error {
	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// A finite request in flight finishes before the exec; the exec is the last thing to happen.
func TestReplaceWaitsForAFiniteRequestBeforeTheExec(t *testing.T) {
	var finished, execAfterFinish atomic.Bool
	sv, addr := servingOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(600 * time.Millisecond)
		finished.Store(true)
	}))
	go func() { _ = get(addr) }()
	time.Sleep(150 * time.Millisecond)
	rs, request := fixtureRestarter(t)
	sv.exec = func(string, []string, []string) error {
		execAfterFinish.Store(finished.Load())
		return errors.New("stop here")
	}
	_ = sv.replace(context.Background(), rs, request)
	if !execAfterFinish.Load() {
		t.Fatal("the process was replaced while a request was still being served")
	}
}

// A request that outlives the bound is not cut off by an exec: the restart is refused and the
// server serves again.
func TestReplaceRefusesWhileARequestOutlivesTheBound(t *testing.T) {
	old := restartShutdownBound
	restartShutdownBound = 200 * time.Millisecond
	defer func() { restartShutdownBound = old }()
	release := make(chan struct{})
	sv, addr := servingOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("slow") != "" {
			<-release
		}
	}))
	defer close(release)
	go func() { http.Get("http://" + addr + "/?slow=1") }()
	time.Sleep(150 * time.Millisecond)
	rs, request := fixtureRestarter(t)
	var execed atomic.Bool
	sv.exec = func(string, []string, []string) error { execed.Store(true); return nil }
	if err := sv.replace(context.Background(), rs, request); err == nil || execed.Load() {
		t.Fatalf("replace = %v, exec called = %v", err, execed.Load())
	}
	if err := get(addr); err != nil {
		t.Fatalf("the server is not serving after the refusal: %v", err)
	}
}

// An exec that fails after the shutdown leaves the server listening again.
func TestAFailedExecReopensTheListener(t *testing.T) {
	sv, addr := servingOn(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	rs, request := fixtureRestarter(t)
	sv.exec = func(string, []string, []string) error { return errors.New("the candidate is gone") }
	if err := sv.replace(context.Background(), rs, request); err == nil {
		t.Fatal("replace succeeded")
	}
	if err := get(addr); err != nil {
		t.Fatalf("no server after a failed exec: %v", err)
	}
}

// The live channel is closed with "going away" so the browser reconnects, and the close does
// not wait on the client.
func TestRestartClosesTheLiveChannelGoingAway(t *testing.T) {
	srv := &server{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		srv.trackSocket(conn)
		defer srv.untrackSocket(conn)
		for {
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
		}
	}))
	defer ts.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	deadline := time.Now().Add(hangGuard)
	for {
		srv.socketMu.Lock()
		n := len(srv.sockets)
		srv.socketMu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the socket was never tracked")
		}
		time.Sleep(10 * time.Millisecond)
	}
	srv.closeSockets()
	conn.SetReadDeadline(time.Now().Add(hangGuard))
	_, _, err = conn.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseGoingAway) {
		t.Fatalf("the client saw %v, want a going-away close", err)
	}
}

// A refusal reopens a new server generation, but the request the first one still serves
// counts: a second restart does not exec until that request has finished.
func TestASecondRestartWaitsForRequestsSurvivingTheFirstRefusal(t *testing.T) {
	old := restartShutdownBound
	restartShutdownBound = 300 * time.Millisecond
	defer func() { restartShutdownBound = old }()
	release := make(chan struct{})
	sv, addr := servingOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("slow") != "" {
			<-release
		}
	}))
	go func() { http.Get("http://" + addr + "/?slow=1") }()
	time.Sleep(150 * time.Millisecond)
	rs, request := fixtureRestarter(t)
	var execs atomic.Int32
	sv.exec = func(string, []string, []string) error { execs.Add(1); return errors.New("stop here") }
	if err := sv.replace(context.Background(), rs, request); err == nil || execs.Load() != 0 {
		t.Fatalf("first replace = %v, execs %d", err, execs.Load())
	}
	if err := sv.replace(context.Background(), rs, request); err == nil || execs.Load() != 0 {
		t.Fatalf("the second replace = %v and exec'd %d time(s) while the first generation's request was still active", err, execs.Load())
	}
	close(release)
	time.Sleep(100 * time.Millisecond)
	_ = sv.replace(context.Background(), rs, request)
	if execs.Load() != 1 {
		t.Fatalf("once the request finished the replacement should exec: execs %d", execs.Load())
	}
}

// An upgrade that has been admitted but is not yet tracked holds the closing sweep: the
// socket it produces is closed before the restart goes on, and a later upgrade is refused.
func TestAnUpgradeInFlightIsClosedBeforeTheRestartProceeds(t *testing.T) {
	srv := &server{}
	paused, proceed := make(chan struct{}), make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !srv.beginUpgrade() {
			http.Error(w, "restarting", http.StatusServiceUnavailable)
			return
		}
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			srv.endUpgrade()
			return
		}
		close(paused) // hijacked, not yet tracked
		<-proceed
		srv.trackSocket(conn)
		srv.endUpgrade()
		defer srv.untrackSocket(conn)
		for {
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
		}
	}))
	defer ts.Close()
	url := "ws" + strings.TrimPrefix(ts.URL, "http")
	clients := make(chan *websocket.Conn, 1)
	go func() {
		conn, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err == nil {
			clients <- conn
		}
	}()
	<-paused
	swept := make(chan struct{})
	go func() { srv.closeSockets(); close(swept) }()
	select {
	case <-swept:
		t.Fatal("the sweep finished while an upgrade was in flight and untracked")
	case <-time.After(5100 * time.Millisecond):
		// The upgrade remains in flight beyond the old five-second sweep limit.
		// A deadline must never allow the process to exec with an untracked socket.
	}
	if _, resp, err := websocket.DefaultDialer.Dial(url, nil); err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("an upgrade begun while draining was admitted: %v", err)
	}
	close(proceed)
	<-swept
	conn := <-clients
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(hangGuard))
	if _, _, err := conn.ReadMessage(); !websocket.IsCloseError(err, websocket.CloseGoingAway) {
		t.Fatalf("the in-flight upgrade's socket saw %v, want a going-away close", err)
	}
	srv.openUpgrades()
	if !srv.beginUpgrade() {
		t.Fatal("upgrades stay refused after a restart that did not replace the process")
	}
}
