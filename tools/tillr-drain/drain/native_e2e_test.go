package drain_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-run/native"
	"github.com/mschulkind-oss/tillr/store"
)

// nativeFakeGap names one part of the native agent backend that an offline fake
// provider cannot reach, so the follow-on live proof can target only these gaps.
type nativeFakeGap struct {
	Name   string
	Files  string
	Reason string
}

var nativeFakeCoverageGaps = []nativeFakeGap{
	{
		Name:   "bedrock-converse-stream-wire",
		Files:  "tools/tillr-run/native/bedrock.go",
		Reason: "TILLR_NATIVE_MOCK_URL selects LoopbackProvider instead of NewBedrockProvider, so ConverseStream JSON framing, inference-profile ARN/region routing, Claude thinking/effort additionalModelRequestFields, cachePoint breakpoints, and Bedrock ThrottlingException/ServiceQuotaExceededException parsing are not exercised against a live endpoint.",
	},
	{
		Name:   "bedrock-sigv4-and-bearer-auth",
		Files:  "tools/tillr-run/native/sigv4.go, tools/tillr-run/native/auth.go",
		Reason: "Offline runs clear AWS_* env vars and bypass ResolveBedrockAuth/ResolveBedrockRegion, so AWS SigV4 canonical request signing, AWS_BEARER_TOKEN_BEDROCK bearer auth, and --account suffixed env resolution are not verified against AWS.",
	},
	{
		Name:   "bedrock-eventstream-binary-framing",
		Files:  "tools/tillr-run/native/eventstream.go",
		Reason: "LoopbackProvider speaks JSON/SSE rather than application/vnd.amazon.eventstream binary frames with CRC32 prelude/message checks over TLS.",
	},
	{
		Name:   "live-tls-and-stream-idle-timeouts",
		Files:  "tools/tillr-run/native/bedrock.go, tools/tillr-run/native/loop.go",
		Reason: "Loopback HTTP on 127.0.0.1 does not exercise TLS handshakes, remote response-header timeouts, or real network stream chunk-idle stalls.",
	},
	{
		Name:   "live-provider-usage-and-pricing",
		Files:  "tools/tillr-run/extract/usage.go, tools/tillr-run/native/bedrock.go",
		Reason: "Token counts, cache read/write tokens, reasoning tokens, and USD cost in this test are synthetic values returned by the scripted fake rather than live provider billing receipts.",
	},
	{
		Name:   "github-copilot-wire-and-pi-auth-borrowing",
		Files:  "docs/design/tillr-native-agent-backend.md (Slice 6)",
		Reason: "GitHub Copilot SSE wire adapters (/chat/completions, /v1/messages, /responses) and read-only pi auth.json borrowing with in-memory token refresh are Slice 6 and not yet implemented.",
	},
}

// nativeE2EFixture holds a throwaway git repository, store, lane root, seat
// profile, and real tillr binaries, all under t.TempDir() and completely apart
// from the working repository.
type nativeE2EFixture struct {
	root        string
	storePath   string
	laneRoot    string
	policyPath  string
	policy      *drain.Policy
	template    string
	baseSHA     string
	now         time.Time
	gateBin     string
	landBin     string
	runBin      string
	itemBin     string
	runNative   string
	checksDir   string
	globalGitCf string
}

func newNativeE2EFixture(t *testing.T) nativeE2EFixture {
	t.Helper()

	// Guarantee no live provider credentials leak into the offline test.
	for _, key := range []string{
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN",
		"AWS_BEARER_TOKEN_BEDROCK",
		"AWS_PROFILE",
		"GITHUB_TOKEN",
		"GH_TOKEN",
	} {
		t.Setenv(key, "")
	}

	tmp := t.TempDir()
	root := filepath.Join(tmp, "repo")
	storePath := filepath.Join(tmp, "store", "board.sqlite3")
	laneRoot := filepath.Join(tmp, "lanes")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(laneRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	// Global git config outside the repo so tillr-land reads a non-lane-writable identity.
	globalGitCf := filepath.Join(tmp, "gitconfig")
	writeFile(t, globalGitCf, "[user]\n\tname = Fixture Landing\n\temail = landing@example.invalid\n")
	t.Setenv("GIT_CONFIG_GLOBAL", globalGitCf)

	// Initialize a throwaway repository with one fenced file, one out-of-fence file,
	// and a real fast tillr-gate check declaration.
	git(t, root, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(root, "greeting.txt"), "hello world\n")
	writeFile(t, filepath.Join(root, "outside.txt"), "untouched\n")
	checksDir := filepath.Join(root, "tools", "gate", "checks")
	if err := os.MkdirAll(checksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(checksDir, "greeting-check.json"), `{
  "id": "greeting-check",
  "label": "Throwaway repo greeting check",
  "command": ["sh", "-c", "test -s greeting.txt"],
  "scope": "tree",
  "tier": "fast",
  "budget_ms": 5000,
  "reads": ["greeting.txt"],
  "tools": ["sh"]
}
`)
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "chore: initial throwaway repo")
	baseSHA := git(t, root, "rev-parse", "HEAD")
	git(t, root, "update-ref", "refs/remotes/origin/main", baseSHA)

	// Locate the real run-native adapter and brief template in this checkout, and build
	// the real tillr-run, tillr-item, tillr-gate, and tillr-land binaries.
	srcRoot := repoRoot(t)
	runNative := filepath.Join(srcRoot, "tools", "tillr-run", "backends", "native", "run-native")
	template := filepath.Join(srcRoot, "tools", "tillr-run", "backends", "brief-template.md")
	runBin := sibling(t, "run")
	itemBin := sibling(t, "item")
	gateBin := sibling(t, "gate")
	landBin := sibling(t, "land")
	t.Setenv("TILLR_RUN_BIN", runBin)

	// Write a seat profile that puts every seat of a build item on the native backend.
	policyPath := filepath.Join(tmp, "native-seat-profile.json")
	policyDoc := map[string]any{
		"default_backend": "native",
		"seats": map[string]string{
			"design":     "native",
			"build":      "native",
			"review":     "native",
			"scout":      "native",
			"classify":   "native",
			"plan":       "native",
			"edit":       "native",
			"test-write": "native",
			"test-run":   "native",
			"docs":       "native",
			"verify":     "native",
			"tricky":     "native",
			"triage":     "native",
		},
		"read_only_seats": []string{"scout", "classify", "test-run", "triage"},
		"backends": map[string]any{
			"native": map[string]any{
				"adapter":         runNative,
				"model":           "mock/native-e2e",
				"effort":          "medium",
				"timeout_seconds": 120,
			},
		},
	}
	rawPolicy, err := json.MarshalIndent(policyDoc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, policyPath, string(rawPolicy))
	pol, err := drain.LoadPolicy(policyPath)
	if err != nil {
		t.Fatalf("LoadPolicy(%s): %v", policyPath, err)
	}

	return nativeE2EFixture{
		root:        root,
		storePath:   storePath,
		laneRoot:    laneRoot,
		policyPath:  policyPath,
		policy:      pol,
		template:    template,
		baseSHA:     baseSHA,
		now:         time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC),
		gateBin:     gateBin,
		landBin:     landBin,
		runBin:      runBin,
		itemBin:     itemBin,
		runNative:   runNative,
		checksDir:   checksDir,
		globalGitCf: globalGitCf,
	}
}

func (f nativeE2EFixture) applyItem(t *testing.T, itemID, title string, allowedPaths []string) {
	t.Helper()
	rec := envelope("item.record", "item-"+itemID, map[string]any{
		"item_id":       itemID,
		"title":         title,
		"state":         "in-queue",
		"blocked_by":    []string{},
		"rank":          "",
		"allowed_paths": allowedPaths,
		"criteria": []any{
			map[string]any{
				"criterion_id": "c1",
				"text":         title,
				"state":        "not-done",
			},
		},
		"seat": "build",
	})
	recPath := filepath.Join(t.TempDir(), itemID+".json")
	writeFile(t, recPath, rec)
	driveOK(t, f.itemBin, "apply", "--store", f.storePath, "--input", recPath, "--now", f.now.Format(time.RFC3339))
}

// fencedLaneStager wraps the real LaneStager and optionally pins fence.txt when
// a test wants to verify pre-write fence enforcement on a single-worker item.
type fencedLaneStager struct {
	inner *drain.LaneStager
	fence []string
}

func (s fencedLaneStager) Stage(ctx context.Context, spec drain.StageSpec) error {
	if err := s.inner.Stage(ctx, spec); err != nil {
		return err
	}
	if len(s.fence) > 0 {
		return writeFence(s.inner.LaneRoot, spec.LaneID, s.fence)
	}
	return nil
}

func (f nativeE2EFixture) runLoop(t *testing.T, fence []string) (drain.LoopResult, string, error) {
	t.Helper()
	steps := &drain.ProcessSteps{
		RunBin:         f.runBin,
		ItemBin:        f.itemBin,
		Store:          f.storePath,
		LaneRoot:       f.laneRoot,
		Root:           f.root,
		Now:            func() time.Time { return f.now },
		GateProgram:    "tillr-loop gate --tillr-gate " + f.gateBin + " --registry " + f.checksDir,
		LandProgram:    "tillr-loop land --store " + f.storePath + " --tillr-land " + f.landBin,
		VerdictProgram: "tillr-loop verdict --tillr-item " + f.itemBin,
	}
	stager := fencedLaneStager{
		inner: &drain.LaneStager{
			Root:     f.root,
			LaneRoot: f.laneRoot,
			Store:    f.storePath,
			Template: f.template,
			Now:      func() time.Time { return f.now },
		},
		fence: fence,
	}
	var report bytes.Buffer
	loop, err := drain.NewLoop(drain.LoopConfig{
		ExitWhenDrained: true,
		Root:            f.root,
		Store:           f.storePath,
		LaneRoot:        f.laneRoot,
		Lanes:           []string{"lane-1"},
		Policy:          f.policy,
		Steps:           steps,
		Stage:           stager,
		Now:             func() time.Time { return f.now },
		Report:          &report,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	res, err := loop.Run(context.Background())
	return res, report.String(), err
}

func lastToolResult(req native.TurnRequest) (native.ContentBlock, bool) {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		m := req.Messages[i]
		for j := len(m.Content) - 1; j >= 0; j-- {
			if m.Content[j].Type == native.BlockToolResult {
				return m.Content[j], true
			}
		}
	}
	return native.ContentBlock{}, false
}

func writeMockTurnResponse(w http.ResponseWriter, resp native.TurnResponse) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	cost := 0.001
	if resp.Usage.Input == 0 && resp.Usage.Output == 0 {
		resp.Usage = native.Usage{Input: 100, Output: 25, TotalTokens: 125, CostUSD: &cost}
	}
	line, _ := json.Marshal(map[string]any{
		"type":     "response",
		"response": resp,
	})
	_, _ = w.Write(append(line, '\n'))
}

func startFakeProvider(t *testing.T, handler func(turn int, req native.TurnRequest, w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	turn := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req native.TurnRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad turn request: "+err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		turn++
		n := turn
		mu.Unlock()
		handler(n, req, w)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("TILLR_NATIVE_MOCK_URL", srv.URL)
	return srv
}

func TestNativeBackendEndToEndWithFakeProvider(t *testing.T) {
	t.Run("land_item_on_throwaway_trunk", func(t *testing.T) {
		f := newNativeE2EFixture(t)
		f.applyItem(t, "item-native-land", "Update greeting.txt via native backend", []string{"greeting.txt"})

		var mu sync.Mutex
		var sawReadContent string
		var sawEditOK bool
		var sawBashOK bool

		startFakeProvider(t, func(turn int, req native.TurnRequest, w http.ResponseWriter) {
			switch turn {
			case 1:
				writeMockTurnResponse(w, native.TurnResponse{
					ID:         "turn-1",
					StopReason: native.StopToolUse,
					Content: []native.ContentBlock{{
						Type:  native.BlockToolUse,
						ID:    "call-read-1",
						Name:  "read_file",
						Input: json.RawMessage(`{"path":"greeting.txt"}`),
					}},
				})
			case 2:
				if tr, ok := lastToolResult(req); ok && !tr.IsError {
					mu.Lock()
					sawReadContent = tr.Content
					mu.Unlock()
				}
				writeMockTurnResponse(w, native.TurnResponse{
					ID:         "turn-2",
					StopReason: native.StopToolUse,
					Content: []native.ContentBlock{{
						Type:  native.BlockToolUse,
						ID:    "call-edit-1",
						Name:  "edit_file",
						Input: json.RawMessage(`{"path":"greeting.txt","edits":[{"old_text":"hello world","new_text":"hello native"}]}`),
					}},
				})
			case 3:
				if tr, ok := lastToolResult(req); ok && !tr.IsError {
					mu.Lock()
					sawEditOK = true
					mu.Unlock()
				}
				writeMockTurnResponse(w, native.TurnResponse{
					ID:         "turn-3",
					StopReason: native.StopToolUse,
					Content: []native.ContentBlock{{
						Type:  native.BlockToolUse,
						ID:    "call-bash-1",
						Name:  "bash",
						Input: json.RawMessage(`{"command":"test \"$(cat greeting.txt)\" = \"hello native\" && git add greeting.txt && git commit -m 'feat: update greeting via native backend'"}`),
					}},
				})
			case 4:
				if tr, ok := lastToolResult(req); ok && !tr.IsError {
					mu.Lock()
					sawBashOK = true
					mu.Unlock()
				}
				writeMockTurnResponse(w, native.TurnResponse{
					ID:         "turn-4",
					StopReason: native.StopToolUse,
					Content: []native.ContentBlock{{
						Type:  native.BlockToolUse,
						ID:    "call-land-1",
						Name:  "land",
						Input: json.RawMessage(`{}`),
					}},
				})
			default:
				writeMockTurnResponse(w, native.TurnResponse{
					ID:         fmt.Sprintf("turn-%d", turn),
					StopReason: native.StopEndTurn,
					Content:    []native.ContentBlock{{Type: native.BlockText, Text: "RESULT: done\n"}},
				})
			}
		})

		res, report, err := f.runLoop(t, nil)
		if err != nil {
			t.Fatalf("loop.Run: %v\nreport:\n%s", err, report)
		}
		if res.Landed != 1 {
			t.Fatalf("res.Landed = %d, want 1 (res=%+v)\nreport:\n%s", res.Landed, res, report)
		}
		if !strings.Contains(sawReadContent, "hello world") {
			t.Fatalf("read_file result = %q, want greeting.txt contents", sawReadContent)
		}
		if !sawEditOK || !sawBashOK {
			t.Fatalf("expected edit_file (%v) and bash (%v) tool results to succeed", sawEditOK, sawBashOK)
		}

		// Verify the throwaway trunk actually moved and contains the edited file.
		newTip := git(t, f.root, "rev-parse", "main")
		if newTip == f.baseSHA {
			t.Fatalf("throwaway trunk main did not advance from %s", f.baseSHA)
		}
		landedBody := git(t, f.root, "show", "main:greeting.txt")
		if landedBody != "hello native" {
			t.Fatalf("main:greeting.txt = %q, want %q", landedBody, "hello native")
		}

		// Verify the throwaway store recorded a native run, a landing, and item state=done.
		db, err := store.OpenRead(context.Background(), f.storePath)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var backend, harness, itemState string
		var landingCount int
		if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
			if err := r.QueryRow(`SELECT backend, harness FROM runs WHERE item_id = ? ORDER BY rowid DESC LIMIT 1`, "item-native-land").Scan(&backend, &harness); err != nil {
				return fmt.Errorf("runs query: %w", err)
			}
			if err := r.QueryRow(`SELECT count(*) FROM landings WHERE item_id = ?`, "item-native-land").Scan(&landingCount); err != nil {
				return fmt.Errorf("landings query: %w", err)
			}
			return r.QueryRow(`SELECT state FROM items WHERE item_id = ?`, "item-native-land").Scan(&itemState)
		}); err != nil {
			t.Fatal(err)
		}
		if backend != "native" {
			t.Fatalf("run backend = %q, want native", backend)
		}
		if !strings.HasPrefix(harness, "tillr-native/") {
			t.Fatalf("run harness = %q, want prefix tillr-native/", harness)
		}
		if landingCount != 1 {
			t.Fatalf("landings count = %d, want 1", landingCount)
		}
		if itemState != "landed" {
			t.Fatalf("item state = %q, want landed", itemState)
		}

		// Confirm the real gate ran and recorded a passing verdict for this item.
		var gateVerdicts int
		if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
			return r.QueryRow(`SELECT count(*) FROM events WHERE kind = 'gate.verdict'`).Scan(&gateVerdicts)
		}); err != nil {
			t.Fatal(err)
		}
		if gateVerdicts == 0 {
			t.Fatalf("expected at least one gate.verdict event from real tillr-gate\nreport:\n%s", report)
		}
	})

	t.Run("worker_reports_obstacle", func(t *testing.T) {
		f := newNativeE2EFixture(t)
		f.applyItem(t, "item-native-obstacle", "Ambiguous greeting format", []string{"greeting.txt"})

		startFakeProvider(t, func(turn int, req native.TurnRequest, w http.ResponseWriter) {
			switch turn {
			case 1:
				writeMockTurnResponse(w, native.TurnResponse{
					ID:         "obs-1",
					StopReason: native.StopToolUse,
					Content: []native.ContentBlock{{
						Type:  native.BlockToolUse,
						ID:    "call-obs-1",
						Name:  "obstacle",
						Input: json.RawMessage(`{"kind":"owner","question":"Should greeting.txt end with an exclamation mark?"}`),
					}},
				})
			default:
				// If a design seat is dispatched to answer the owner question, escalate to owner.
				writeMockTurnResponse(w, native.TurnResponse{
					ID:         fmt.Sprintf("obs-%d", turn),
					StopReason: native.StopEndTurn,
					Content: []native.ContentBlock{{
						Type: native.BlockText,
						Text: "OBSTACLE: owner\nQUESTION: Should greeting.txt end with an exclamation mark?\nRESULT: blocked\n",
					}},
				})
			}
		})

		res, report, err := f.runLoop(t, nil)
		if err != nil {
			t.Fatalf("loop.Run: %v\nreport:\n%s", err, report)
		}
		if res.Landed != 0 {
			t.Fatalf("res.Landed = %d, want 0\nreport:\n%s", res.Landed, report)
		}
		if tip := git(t, f.root, "rev-parse", "main"); tip != f.baseSHA {
			t.Fatalf("throwaway trunk moved on obstacle: %s != %s", tip, f.baseSHA)
		}

		db, err := store.OpenRead(context.Background(), f.storePath)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var itemState, backend string
		if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
			if err := r.QueryRow(`SELECT state FROM items WHERE item_id = ?`, "item-native-obstacle").Scan(&itemState); err != nil {
				return err
			}
			return r.QueryRow(`SELECT backend FROM runs WHERE item_id = ? ORDER BY rowid ASC LIMIT 1`, "item-native-obstacle").Scan(&backend)
		}); err != nil {
			t.Fatal(err)
		}
		if backend != "native" {
			t.Fatalf("run backend = %q, want native", backend)
		}
		if itemState != "blocked" {
			t.Fatalf("item state after owner obstacle = %q, want blocked\nreport:\n%s", itemState, report)
		}
	})

	t.Run("worker_ends_without_change", func(t *testing.T) {
		f := newNativeE2EFixture(t)
		f.applyItem(t, "item-native-nochange", "Verify greeting.txt already says hello world", []string{"greeting.txt"})

		startFakeProvider(t, func(turn int, req native.TurnRequest, w http.ResponseWriter) {
			// Distinguish worker turn (has no_change tool) from any verifier/review turn.
			hasNoChangeTool := false
			for _, td := range req.Tools {
				if td.Name == "no_change" {
					hasNoChangeTool = true
					break
				}
			}
			if hasNoChangeTool {
				if turn == 1 {
					writeMockTurnResponse(w, native.TurnResponse{
						ID:         "nc-1",
						StopReason: native.StopToolUse,
						Content: []native.ContentBlock{{
							Type:  native.BlockToolUse,
							ID:    "call-read-nc",
							Name:  "read_file",
							Input: json.RawMessage(`{"path":"greeting.txt"}`),
						}},
					})
					return
				}
				writeMockTurnResponse(w, native.TurnResponse{
					ID:         "nc-2",
					StopReason: native.StopToolUse,
					Content: []native.ContentBlock{{
						Type:  native.BlockToolUse,
						ID:    "call-nochange-1",
						Name:  "no_change",
						Input: json.RawMessage(`{"cause":"already satisfied","evidence":"read_file greeting.txt returned hello world","why_no_change":"greeting.txt already contains hello world"}`),
					}},
				})
				return
			}
			// Verifier/review seat confirms the no-change finding.
			writeMockTurnResponse(w, native.TurnResponse{
				ID:         fmt.Sprintf("nc-verify-%d", turn),
				StopReason: native.StopEndTurn,
				Content:    []native.ContentBlock{{Type: native.BlockText, Text: "Verified: greeting.txt already contains hello world.\nRESULT: done\n"}},
			})
		})

		res, report, err := f.runLoop(t, nil)
		if err != nil {
			t.Fatalf("loop.Run: %v\nreport:\n%s", err, report)
		}
		if res.Landed != 0 {
			t.Fatalf("res.Landed = %d, want 0\nreport:\n%s", res.Landed, report)
		}
		if tip := git(t, f.root, "rev-parse", "main"); tip != f.baseSHA {
			t.Fatalf("throwaway trunk moved on no_change: %s != %s", tip, f.baseSHA)
		}

		db, err := store.OpenRead(context.Background(), f.storePath)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var itemState string
		var landingCount int
		if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
			if err := r.QueryRow(`SELECT state FROM items WHERE item_id = ?`, "item-native-nochange").Scan(&itemState); err != nil {
				return err
			}
			return r.QueryRow(`SELECT count(*) FROM landings WHERE item_id = ?`, "item-native-nochange").Scan(&landingCount)
		}); err != nil {
			t.Fatal(err)
		}
		if landingCount != 0 {
			t.Fatalf("landings count = %d, want 0", landingCount)
		}
		var backend string
		if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
			return r.QueryRow(`SELECT backend FROM runs WHERE item_id = ? ORDER BY rowid ASC LIMIT 1`, "item-native-nochange").Scan(&backend)
		}); err != nil {
			t.Fatal(err)
		}
		if backend != "native" {
			t.Fatalf("run backend = %q, want native", backend)
		}
		if itemState != "closed" {
			t.Fatalf("item state after no_change = %q, want closed\nreport:\n%s", itemState, report)
		}
	})

	t.Run("provider_quota_refusal_records_backend_window", func(t *testing.T) {
		f := newNativeE2EFixture(t)
		f.applyItem(t, "item-native-quota", "Trigger quota refusal on native backend", []string{"greeting.txt"})

		resetAt := f.now.Add(2 * time.Hour).Unix()
		startFakeProvider(t, func(turn int, req native.TurnRequest, w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":    "quota window exhausted",
				"terminal": true,
				"rate_limit": map[string]any{
					"rateLimitType": "five_hour",
					"utilization":   1.0,
					"status":        "rejected",
					"resetsAt":      resetAt,
				},
			})
		})

		res, report, err := f.runLoop(t, nil)
		if err != nil {
			t.Fatalf("loop.Run: %v\nreport:\n%s", err, report)
		}
		if res.Landed != 0 {
			t.Fatalf("res.Landed = %d, want 0\nreport:\n%s", res.Landed, report)
		}
		if tip := git(t, f.root, "rev-parse", "main"); tip != f.baseSHA {
			t.Fatalf("throwaway trunk moved on quota refusal: %s != %s", tip, f.baseSHA)
		}

		db, err := store.OpenRead(context.Background(), f.storePath)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var kind, status string
		var gotResetMS int64
		if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
			return r.QueryRow(
				`SELECT kind, status, reset_at FROM backend_windows WHERE backend = ?`,
				"native",
			).Scan(&kind, &status, &gotResetMS)
		}); err != nil {
			t.Fatalf("expected backend_windows row for native backend: %v\nreport:\n%s", err, report)
		}
		if kind != "five_hour" || status != "rejected" || gotResetMS != resetAt*1000 {
			t.Fatalf("backend_windows row = (kind=%q, status=%q, reset_at=%d), want (five_hour, rejected, %d)",
				kind, status, gotResetMS, resetAt*1000)
		}
	})

	t.Run("write_outside_fence_refused", func(t *testing.T) {
		f := newNativeE2EFixture(t)
		f.applyItem(t, "item-native-fence", "Attempt write outside fence on native backend", []string{"greeting.txt"})

		escapedPath := filepath.Join(f.root, "escaped.txt")
		var mu sync.Mutex
		var fenceErrText string
		var escapeErrText string

		startFakeProvider(t, func(turn int, req native.TurnRequest, w http.ResponseWriter) {
			switch turn {
			case 1:
				// Attempt 1: write inside worktree but outside fence.txt (greeting.txt only).
				writeMockTurnResponse(w, native.TurnResponse{
					ID:         "fence-1",
					StopReason: native.StopToolUse,
					Content: []native.ContentBlock{{
						Type:  native.BlockToolUse,
						ID:    "call-write-outside",
						Name:  "write_file",
						Input: json.RawMessage(`{"path":"outside.txt","content":"should not be written\n"}`),
					}},
				})
			case 2:
				if tr, ok := lastToolResult(req); ok && tr.IsError {
					mu.Lock()
					fenceErrText = tr.Content
					mu.Unlock()
				}
				// Attempt 2: write outside the worktree into the throwaway main checkout.
				payload, _ := json.Marshal(map[string]any{
					"path":    escapedPath,
					"content": "escaped\n",
				})
				writeMockTurnResponse(w, native.TurnResponse{
					ID:         "fence-2",
					StopReason: native.StopToolUse,
					Content: []native.ContentBlock{{
						Type:  native.BlockToolUse,
						ID:    "call-write-escape",
						Name:  "write_file",
						Input: payload,
					}},
				})
			case 3:
				if tr, ok := lastToolResult(req); ok && tr.IsError {
					mu.Lock()
					escapeErrText = tr.Content
					mu.Unlock()
				}
				writeMockTurnResponse(w, native.TurnResponse{
					ID:         "fence-3",
					StopReason: native.StopToolUse,
					Content: []native.ContentBlock{{
						Type:  native.BlockToolUse,
						ID:    "call-obs-fence",
						Name:  "obstacle",
						Input: json.RawMessage(`{"kind":"owner","question":"Need fence expanded to outside.txt"}`),
					}},
				})
			default:
				writeMockTurnResponse(w, native.TurnResponse{
					ID:         fmt.Sprintf("fence-%d", turn),
					StopReason: native.StopEndTurn,
					Content: []native.ContentBlock{{
						Type: native.BlockText,
						Text: "OBSTACLE: owner\nQUESTION: Need fence expanded to outside.txt\nRESULT: blocked\n",
					}},
				})
			}
		})

		res, report, err := f.runLoop(t, []string{"greeting.txt"})
		if err != nil {
			t.Fatalf("loop.Run: %v\nreport:\n%s", err, report)
		}
		if res.Landed != 0 {
			t.Fatalf("res.Landed = %d, want 0\nreport:\n%s", res.Landed, report)
		}
		if fenceErrText == "" || !strings.Contains(strings.ToLower(fenceErrText), "fence") {
			t.Fatalf("expected write_file to outside.txt to return a fence refusal tool_result, got %q\nreport:\n%s", fenceErrText, report)
		}
		if escapeErrText == "" || !strings.Contains(strings.ToLower(escapeErrText), "refused") {
			t.Fatalf("expected write_file to %s to return a refused tool_result, got %q\nreport:\n%s", escapedPath, escapeErrText, report)
		}

		// Neither file may have been created or modified on disk.
		if _, err := os.Stat(escapedPath); !os.IsNotExist(err) {
			t.Fatalf("out-of-worktree file %s was created despite fence", escapedPath)
		}
		workOutside := filepath.Join(f.laneRoot, "lane-1", "worktree", "outside.txt")
		if raw, err := os.ReadFile(workOutside); err == nil && string(raw) != "untouched\n" {
			t.Fatalf("worktree outside.txt modified to %q, want untouched\\n", string(raw))
		}
		if trunkOutside := git(t, f.root, "show", "main:outside.txt"); trunkOutside != "untouched" {
			t.Fatalf("main:outside.txt = %q, want untouched", trunkOutside)
		}
	})

	t.Run("report_unreachable_native_gaps", func(t *testing.T) {
		if len(nativeFakeCoverageGaps) == 0 {
			t.Fatal("nativeFakeCoverageGaps must list each part of the native backend unreachable with a fake")
		}
		seen := make(map[string]bool, len(nativeFakeCoverageGaps))
		for _, g := range nativeFakeCoverageGaps {
			if strings.TrimSpace(g.Name) == "" || strings.TrimSpace(g.Files) == "" || strings.TrimSpace(g.Reason) == "" {
				t.Fatalf("incomplete gap entry: %+v", g)
			}
			if seen[g.Name] {
				t.Fatalf("duplicate gap name %q", g.Name)
			}
			seen[g.Name] = true
			t.Logf("UNREACHABLE_GAP %s (%s): %s", g.Name, g.Files, g.Reason)
		}
	})
}
