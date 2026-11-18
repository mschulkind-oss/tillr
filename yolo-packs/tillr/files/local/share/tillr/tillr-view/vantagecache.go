package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mschulkind-oss/tillr/vantage"
)

// The board's one walk of Vantage's index.
//
// Measured 2026-11-17 on this repository (466 indexed documents, a 3.5 MB index): one
// `uvx vantage-check index --format json` takes 11.4-12.1 s, and before this cache three
// routes each ran it on every request — /why through the planner it spawns (whose own work
// is 0.23 s of the 12 s), /questions, and every item's /doc. Home and Items re-read /why
// every 5 s, so one open tab kept a tree walk running back to back, at ~130% of a core.
//
// The index is a function of the planning documents and Vantage's configuration, so the
// cache key is a fingerprint of exactly those inputs, read without running Vantage: the path,
// size and modification time of every Markdown file under the root that Vantage could read
// (its walk skips hidden entries; the walk here skips the same), plus `.vantage.toml` and
// `.vantage/`. Any edit, addition, deletion or rename of a document changes the fingerprint,
// so an answer recorded in a document is served on the first request after the write, exactly
// as before; the walk costs tens of milliseconds where the index costs seconds. Two inputs it
// cannot see — the Vantage release `uvx` resolves, and an edit that keeps both size and
// modification time — are bounded by vantageMaxAge: no cached index is served older than that.
//
// One walk serves every concurrent request: a request that arrives while the index for its
// fingerprint is running waits for that run instead of starting another. The run is not tied
// to the request that started it, so a browser that navigates away does not discard eleven
// seconds of work the next request needs — before, an aborted /why killed the planner and left
// its `vantage-check` child running as an orphan, to be started again by the next poll.
const vantageMaxAge = 10 * time.Minute

type vantageCache struct {
	root    string
	command []string
	now     func() time.Time
	maxAge  time.Duration
	// run and fingerprint are the two effects, injectable so a test drives the cache with a
	// recorded clock and no Vantage.
	run         func(ctx context.Context, root string, command []string) ([]byte, error)
	fingerprint func(root string) (string, error)

	// fingerprintCustom, when set, is trusted for a caller-named command too (tests only).
	fingerprintCustom func(root string) (string, error)
	runs              atomic.Uint64

	mu       sync.Mutex
	fp       string
	raw      []byte
	at       time.Time
	inflight *vantageFlight
}

type vantageFlight struct {
	fp   string
	done chan struct{}
	raw  []byte
	err  error
}

func newVantageCache(root string, command []string) *vantageCache {
	return &vantageCache{root: root, command: command, now: time.Now, maxAge: vantageMaxAge,
		run: vantage.RunIndexCommand, fingerprint: vantageFingerprint}
}

// vantageLookup is how one request's index was answered, for the performance log.
type vantageLookup struct {
	Cache         string  `json:"cache"` // hit, miss, shared, or error
	MS            float64 `json:"ms"`
	FingerprintMS float64 `json:"fingerprint_ms"`
	AgeMS         float64 `json:"age_ms,omitempty"`
}

// vantagePrefetchKey carries one request's index lookup, made before its read transaction
// opened (prefetch), to the handler that reads it inside the transaction.
type vantagePrefetchKey struct{ c *vantageCache }

type vantagePrefetched struct {
	raw  []byte
	look vantageLookup
	err  error
}

// prefetch looks the index up BEFORE next opens its read transaction, and hands the answer
// to the get inside it through the request's context. It exists because the server's store
// handle has one connection (store.OpenRead): a Vantage walk made inside a read transaction
// held that one connection for the whole walk, so every other request — every pane of the
// page, and the live channel's poll — queued behind it. Measured on 2026-11-17 against the
// live store: a cold /why held the live channel's poll for 14,759 ms.
func (c *vantageCache) prefetch(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if c == nil {
			next(w, req)
			return
		}
		raw, look, err := c.lookup(req.Context())
		ctx := context.WithValue(req.Context(), vantagePrefetchKey{c}, &vantagePrefetched{raw, look, err})
		next(w, req.WithContext(ctx))
	}
}

// get answers the index document for the tree as it is now: the request's prefetched
// lookup when prefetch made one, a lookup of its own otherwise.
func (c *vantageCache) get(ctx context.Context) ([]byte, vantageLookup, error) {
	if p, ok := ctx.Value(vantagePrefetchKey{c}).(*vantagePrefetched); ok {
		return p.raw, p.look, p.err
	}
	return c.lookup(ctx)
}

func (c *vantageCache) lookup(ctx context.Context) ([]byte, vantageLookup, error) {
	start := c.now()
	var look vantageLookup
	fingerprint := c.fingerprint
	if c.fingerprintCustom != nil {
		fingerprint = c.fingerprintCustom
	}
	fp, err := fingerprint(c.root)
	look.FingerprintMS = msSince(c.now, start)
	if err != nil {
		// A tree that cannot be fingerprinted cannot be cached against: run it as before.
		fp = "unfingerprinted:" + c.now().String()
	}
	if len(c.command) > 0 && c.fingerprintCustom == nil {
		// A caller-named index command (`--questions-cmd`) reads inputs this fingerprint
		// cannot see — a test's stub reads a file it rewrites — so its answer is never
		// reused: every request runs it, exactly as before the cache.
		fp = "uncached:" + strconv.FormatUint(c.runs.Add(1), 10)
	}
	c.mu.Lock()
	if c.raw != nil && c.fp == fp && c.now().Sub(c.at) < c.maxAge {
		raw := c.raw
		look.Cache, look.AgeMS = "hit", round1(float64(c.now().Sub(c.at).Microseconds())/1000)
		c.mu.Unlock()
		look.MS = msSince(c.now, start)
		return raw, look, nil
	}
	flight := c.inflight
	look.Cache = "shared"
	if flight == nil || flight.fp != fp {
		look.Cache = "miss"
		flight = &vantageFlight{fp: fp, done: make(chan struct{})}
		c.inflight = flight
		go c.fly(flight)
	}
	c.mu.Unlock()
	select {
	case <-flight.done:
	case <-ctx.Done():
		look.Cache = "error"
		look.MS = msSince(c.now, start)
		return nil, look, ctx.Err()
	}
	look.MS = msSince(c.now, start)
	if flight.err != nil {
		look.Cache = "error"
		return nil, look, flight.err
	}
	return flight.raw, look, nil
}

func (c *vantageCache) fly(f *vantageFlight) {
	raw, err := c.run(context.Background(), c.root, c.command)
	if err == nil {
		// A document the tool answered that is not an index is a failure too, and it is not
		// cached: the next request asks again rather than being told the same refusal for
		// ten minutes.
		if _, perr := vantage.ParseIndex(raw, vantage.IndexCommandName(c.command)).Questions(""); perr != nil {
			err = perr
		}
	}
	f.raw, f.err = raw, err
	c.mu.Lock()
	if err == nil {
		c.fp, c.raw, c.at = f.fp, raw, c.now()
	}
	if c.inflight == f {
		c.inflight = nil
	}
	c.mu.Unlock()
	close(f.done)
}

// source is a QuestionSource over the cached index: what /questions, /doc and /waiting ask.
// A failed index is the same refusal VantageQuestions would carry, so every document it is
// asked about answers `questions-unknown`.
func (c *vantageCache) source(ctx context.Context) vantage.QuestionSource {
	raw, look, err := c.get(ctx)
	phase(ctx, "vantage", look)
	if err != nil {
		return vantage.Preloaded(c.root, vantage.IndexError(fmt.Errorf("running %s: %w", vantage.IndexCommandName(c.command), err)))
	}
	return vantage.Preloaded(c.root, vantage.ParseIndex(raw, vantage.IndexCommandName(c.command)))
}

// vantageFingerprint hashes what Vantage's index is computed from. It is deliberately a
// superset — every non-hidden `*.md`, whether or not Vantage's own include and exclude rules
// keep it — because a fingerprint that misses an input serves a stale index, and one that
// sees an extra file only costs a recomputation.
func vantageFingerprint(root string) (string, error) {
	h := sha256.New()
	add := func(path string, info fs.FileInfo) {
		rel, _ := filepath.Rel(root, path)
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", rel, info.Size(), info.ModTime().UnixNano())
	}
	for _, name := range []string{".vantage.toml", ".vantage"} {
		top := filepath.Join(root, name)
		_ = filepath.WalkDir(top, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if info, err := d.Info(); err == nil {
				add(path, info)
			}
			return nil
		})
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return nil
		}
		if path != root && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		info, err := d.Info()
		if d.Type()&fs.ModeSymlink != 0 {
			// A linked document changes when its target does, so the target's clock is read.
			info, err = os.Stat(path)
		}
		if err != nil {
			return nil
		}
		add(path, info)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)[:12]), nil
}
