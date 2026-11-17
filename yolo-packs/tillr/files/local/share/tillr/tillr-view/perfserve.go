package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// startRecord is the log's first line for one server: what is being served, from where, by
// which build, and how long the recipe took to get here. The recipe's phases arrive as
// `name=ms` pairs (tools/observatory/run.sh); `recipe_start_ms` is the Unix millisecond the
// recipe began at, so the time to listening is measured on one clock across both programs.
func (s *server) startRecord(addr, ui, startup string) map[string]any {
	host, _ := os.Hostname()
	phases, recipeStart := parseStartupPhases(startup)
	rec := map[string]any{
		"pid": os.Getpid(), "host": host, "addr": addr, "root": s.root, "store": s.storePath,
		"log_dir": s.perf.dir, "log_path": s.perf.path(), "build": s.build, "ui": ui,
		"go": runtime.Version(), "os": runtime.GOOS + "/" + runtime.GOARCH,
		"in_jail": os.Getenv("YOLO_JAIL") == "1",
	}
	if _, port, ok := strings.Cut(addr, ":"); ok {
		rec["port"] = port
	}
	if program, err := resolvePlanner(s.plannerFlag); err == nil {
		rec["planner"] = program.Path
	}
	if len(phases) > 0 {
		rec["recipe_phases_ms"] = phases
	}
	if recipeStart > 0 {
		rec["listening_ms"] = s.perf.now().UnixMilli() - recipeStart
	}
	return rec
}

// parseStartupPhases reads `a=12,b=340,recipe_start_ms=1794...`. A pair that is not a
// number is skipped: the startup record is a report, and a malformed phase is not a reason
// to refuse to serve.
func parseStartupPhases(text string) (map[string]int64, int64) {
	phases := map[string]int64{}
	var start int64
	for _, pair := range strings.Split(text, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || name == "" {
			continue
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			continue
		}
		if name == "recipe_start_ms" {
			start = n
			continue
		}
		phases[name] = n
	}
	return phases, start
}

// stopOnSignal shuts the server down on SIGINT or SIGTERM instead of letting the signal
// kill it, so the deferred closes run: the performance log's final summary is written, and
// the store handle is released rather than abandoned.
func stopOnSignal(srv *http.Server) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		signal.Stop(signals)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		// Shutdown does not wait for hijacked connections (the live channel), and a socket
		// that outlives it is closed when the process exits.
		_ = srv.Shutdown(ctx)
	}()
}
