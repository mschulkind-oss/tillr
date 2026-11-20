package main

// `tillr view restart`, and the server's side of it: the same request the loop answers
// (Architecture and invariants: docs/reference/tillr-restart.md), with no steps to protect.
// The server finishes the HTTP requests it is serving and replaces itself in place; a
// browser's live channel reconnects.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr/record"
)

// restartPoll is how often the server looks for a request. A request is a store fact, so
// the poll is the whole of the wake-up.
const restartPoll = 250 * time.Millisecond

// restartFace files one request for the observatory server and prints its id; with --wait
// it prints the answer too.
func restartFace(args []string) int {
	fs := newFlagSet("restart")
	storePath := fs.String("store", "", "the store the server reads")
	root := fs.String("root", "", "the repository whose store is read (default store: <root>/.tillr/state.db)")
	wait := fs.Bool("wait", false, "block until the server answers the request, then print the answer")
	if code := parseFlags(fs, args); code != record.ExitOK {
		if code == helpExit {
			return record.ExitOK
		}
		return code
	}
	if *storePath == "" && *root != "" {
		*storePath = filepath.Join(*root, ".tillr", "state.db")
	}
	if *storePath == "" {
		return usagef("restart needs --store <path> or --root <repository>")
	}
	if _, err := os.Stat(*storePath); err != nil {
		fmt.Fprintf(os.Stderr, "tillr-view: %v\n", err)
		return record.ExitExternal
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	request, err := restart.File(ctx, *storePath, restart.RoleView, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-view: %v\n", err)
		return record.ExitInternal
	}
	fmt.Println(request.ID)
	if !*wait {
		return record.ExitOK
	}
	answer, err := restart.AwaitAnswer(ctx, *storePath, request.ID, restartPoll)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-view: %v\n", err)
		return record.ExitInternal
	}
	fmt.Println(answer)
	if answer.Answer == restart.AnswerRefused {
		return 1
	}
	return record.ExitOK
}

// restartShutdownBound is how long a replacement waits for finite HTTP requests in flight.
// It is a variable so a test can name a short one.
var restartShutdownBound = 30 * time.Second

// serving owns the listener and the HTTP server, so a restart that cannot complete can
// bring both back: a refused or failed replacement must leave the old server serving.
type serving struct {
	srv   *server
	addr  string
	mu    sync.Mutex
	http  *http.Server
	fatal chan error
	// handler and exec are seams: the routes and syscall.Exec in production.
	handler func() http.Handler
	exec    func(string, []string, []string) error
	// reqMu guards reqs, the finite requests being served by any generation of the HTTP
	// server: a refused restart reopens a new server, and the old one's requests still count.
	reqMu sync.Mutex
	reqs  int
}

// track counts a finite request until it returns. The live channel is excluded: it is long
// by design and is closed by the sweep, not waited for.
func (sv *serving) track(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/ws" {
			next.ServeHTTP(w, r)
			return
		}
		sv.reqMu.Lock()
		sv.reqs++
		sv.reqMu.Unlock()
		defer func() {
			sv.reqMu.Lock()
			sv.reqs--
			sv.reqMu.Unlock()
		}()
		next.ServeHTTP(w, r)
	})
}

// waitIdle waits until no generation is serving a finite request, or the deadline passes.
func (sv *serving) waitIdle(deadline time.Time) error {
	for {
		sv.reqMu.Lock()
		n := sv.reqs
		sv.reqMu.Unlock()
		if n == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%d request(s) were still being served", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (sv *serving) start(listener net.Listener) {
	handler := sv.srv.routes
	if sv.handler != nil {
		handler = sv.handler
	}
	h := &http.Server{Handler: sv.track(handler()), ReadHeaderTimeout: 5 * time.Second}
	sv.mu.Lock()
	sv.http = h
	sv.mu.Unlock()
	go func() {
		// ErrServerClosed is this process shutting a server down on purpose, for a stop or
		// for a restart that may yet fail and reopen.
		if err := h.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			sv.fatal <- err
		}
	}()
}

func (sv *serving) current() *http.Server {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	return sv.http
}

// reopen binds the address again and serves with a fresh HTTP server.
func (sv *serving) reopen() error {
	var err error
	for attempt := 0; attempt < 50; attempt++ {
		var listener net.Listener
		if listener, err = net.Listen("tcp", sv.addr); err == nil {
			sv.start(listener)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return err
}

// run serves until a signal or a fatal error and returns the exit code.
func (sv *serving) run(ctx context.Context, listener net.Listener) int {
	sv.fatal = make(chan error, 4)
	sv.start(listener)
	go sv.watch(ctx)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	select {
	case <-signals:
		stop, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		sv.srv.closeSockets()
		_ = sv.current().Shutdown(stop)
		return record.ExitOK
	case err := <-sv.fatal:
		fmt.Fprintf(os.Stderr, "tillr-view: %v\n", err)
		return record.ExitInternal
	}
}

// watch answers the request this process was exec'd for, then polls for new ones.
func (sv *serving) watch(ctx context.Context) {
	srv := sv.srv
	exe, err := os.Executable()
	if err != nil {
		return
	}
	exe = strings.TrimSuffix(exe, " (deleted)")
	build := srv.build
	rs := &restart.Restarter{
		Store: srv.storePath, Role: restart.RoleView,
		Running:    restart.Build{Line: build.String(), Digest: build.Digest},
		Executable: exe, Disk: restart.FileDigest, Candidate: restart.LauncherCandidate(exe),
		Check: func(ctx context.Context, executable string) error {
			args := append(append([]string(nil), os.Args[1:]...), "--check")
			out, err := exec.CommandContext(ctx, executable, args...).CombinedOutput()
			if err == nil {
				return nil
			}
			if strings.TrimSpace(string(out)) == "" {
				return err
			}
			return errors.New(strings.TrimSpace(string(out)))
		},
	}
	if adopted, err := rs.Adopt(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "tillr-view serve: recording the restart answer: %v\n", err)
	} else if adopted {
		fmt.Fprintf(os.Stderr, "tillr-view serve: restarted in place onto build %s\n", build)
	}
	if err := restart.PublishBuild(ctx, srv.storePath, restart.BuildFact{Role: restart.RoleView, Revision: build.Revision, Time: build.Time, Modified: build.Modified, Digest: build.Digest, Executable: build.Executable, PID: os.Getpid()}); err != nil {
		fmt.Fprintf(os.Stderr, "tillr-view serve: publishing the running build: %v\n", err)
	}
	ticker := time.NewTicker(restartPoll)
	defer ticker.Stop()
	for range ticker.C {
		pending, err := rs.Pending(ctx)
		if err != nil || !pending {
			continue
		}
		request, replace, err := rs.Settle(ctx)
		if err != nil || !replace {
			continue
		}
		fmt.Fprintf(os.Stderr, "tillr-view serve: restart %s: finishing requests in flight, then replacing build %s in place\n", request.ID, build)
		if err := sv.replace(ctx, rs, request); err != nil {
			fmt.Fprintf(os.Stderr, "tillr-view serve: restart %s refused: %v; still serving\n", request.ID, err)
			if errors.Is(err, restart.ErrSuperseded) {
				continue
			}
			if recordErr := rs.Refuse(ctx, request, err.Error()); recordErr != nil {
				fmt.Fprintf(os.Stderr, "tillr-view serve: recording the refusal: %v\n", recordErr)
			}
		}
	}
}

// replace stops accepting, closes the live channel (browsers reconnect), waits for every
// finite request to finish and only then execs. A request that does not finish within the
// bound, or an exec that fails, leaves the server serving again on the same address.
func (sv *serving) replace(ctx context.Context, rs *restart.Restarter, request restart.Request) error {
	sv.srv.closeSockets()
	deadline := time.Now().Add(restartShutdownBound)
	shutdown, cancel := context.WithDeadline(context.Background(), deadline)
	err := sv.current().Shutdown(shutdown)
	cancel()
	if err == nil {
		// The server just shut down finished its own requests, but an earlier generation,
		// kept alive by an earlier refusal, may still be serving one.
		err = sv.waitIdle(deadline)
	}
	if err != nil {
		sv.recover()
		return fmt.Errorf("requests were still in flight after %s: %v", restartShutdownBound, err)
	}
	err = rs.Replace(ctx, request, os.Args, sv.exec)
	sv.recover()
	if err != nil {
		if errors.Is(err, restart.ErrSuperseded) {
			return err
		}
		return fmt.Errorf("exec: %w", err)
	}
	return nil
}

// recover brings the server back after a restart that did not replace the process.
func (sv *serving) recover() {
	if err := sv.reopen(); err != nil {
		sv.fatal <- err
	}
	sv.srv.openUpgrades()
}
