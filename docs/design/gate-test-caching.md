---
title: "Skip test work whose result is proved unchanged"
status: in-review
stage: DESIGN
updated: 2026-11-25
tags:
  - tillr
  - gate
  - drain
  - testing
  - caching
  - design
summary: "Measures Go test cache reuse across landing gates, base replays, and trunk proofs; audits Go 1.27.3's four test-cache soundness gaps against this tree; lays out four composable options (content-keyed result store, Linux Landlock read sandboxing, duration-targeted sharding with per-shard checkpointing, and leaf-package carving) alongside build-system trade-offs; checks each option against docs/reference/gate.md; and files OQ-GC1 through OQ-GC5 for the owner."
next: "Rule OQ-GC1, OQ-GC2, OQ-GC3, OQ-GC4, and OQ-GC5 to choose the soundness bar, caching architecture, replay/pre-merge reuse scope, clock policy, and sharding/package-carving depth."
depends-on:
  - ../reference/gate.md
  - gate-test-capacity.md
  - landing-principles.md
  - fast-gate-tillr-drain-options.md
vantage:
  status-chip: true
---

# Skip test work whose result is proved unchanged

> **In short.** Across recorded landing gates in this repository, Go's test cache answered only `8.9%–10.1%` of selected package and shard runs (`0.0%` of `1,776` `tillr-drain/drain` shards) while leaving cross-module and child-process reads unverified. A shared content-keyed test result store — paired with kernel-enforced read sandboxing so undeclared inputs fail immediately instead of going stale, plus per-shard checkpointing across restarts — lets landing gates, replays, restarts, and trunk proofs skip any test unit whose inputs have not changed.

**Needs your ruling:** [`OQ-GC1`](#OQ-GC1), [`OQ-GC2`](#OQ-GC2), [`OQ-GC3`](#OQ-GC3), [`OQ-GC4`](#OQ-GC4), [`OQ-GC5`](#OQ-GC5) — jump straight to [§6](#6-open-questions) once the context in [§1](#1-owner-direction-and-framing)–[§5](#5-checking-every-option-against-gate-rulings) is clear.

---

## 1. Owner direction and framing

The owner stated the governing bar for test skipping on 2026-11-25:

> `"if we know it's correct, we can be just as aggressive as possible."`

That ruling sets the boundary between settled authority and open design choices in this document. The owner has settled the goal — skip every test run whose outcome is proved unchanged, across gates, replays, restarts, and worktrees — and the condition under which aggressive skipping is welcome: proof that the skipped run would produce the same verdict, rather than heuristics that silently go stale.

Everything else in how this document slices the problem is a designer or operator framing that the owner may override:

- **Decomposing the design into four composable mechanisms ([§4](#4-options-correctness-arguments-and-build-system-trade-offs)) rather than one monolithic rewrite** is a designer framing; the owner may pick one, combine several, or replace the decomposition.
- **Keeping Go's toolchain rather than migrating all `14` Go modules to Bazel, Buck2, Pants, or Nix ([§4.5](#45-build-system-replacement-and-dynamic-test-selection-tools))** is a designer recommendation grounded in repository measurements and primary-source verification, not a pre-settled constraint ([`OQ-GC2`](#OQ-GC2)).
- **Storing shared test verdicts in the repository's common Git directory alongside existing check-result memory** and **enforcing input declarations with Linux Landlock** are proposed mechanisms awaiting the owner's ruling ([`OQ-GC1`](#OQ-GC1), [`OQ-GC2`](#OQ-GC2), [`OQ-GC3`](#OQ-GC3)).

On `HEAD` (`0e57a73d96ea`), the repository already carries three partial pieces of reuse machinery:

1. **Whole-check tree memory ([`tools/tillr-gate/memory.go`](../../tools/tillr-gate/memory.go)).** Tree-scoped checks store pass records under `.git/tillr-gate/results/` keyed by check declaration, declared `reads` file digests, tool versions, and needed refs ([`D-GR8`](../reference/gate.md#D-GR8)). Change-scoped `go-affected` sets `"remember": false` in [`tools/gate/checks/go-affected.json`](../../tools/gate/checks/go-affected.json), and heavy `go-test-*` checks declare coarse module-wide `reads` (for example, [`tools/gate/checks/go-test-tillr-drain.json`](../../tools/gate/checks/go-test-tillr-drain.json) lists `11` Go modules plus `config/tillr/` and `yolo-packs/tillr/`), so nearly every code landing invalidates the whole check.
2. **Declared cross-module edge digests ([`tools/gate/go-test-edges.json`](../../tools/gate/go-test-edges.json)).** For `14` of the repository's `53` Go test packages, `tools/gate/go_affected.py` and `tools/go-test-shards.py` (`--cache`) hash declared `reads` and `builds` closures into `TILLR_GATE_EDGE_INPUTS`, which generated `TestEdgeInputs` tests read via `os.Getenv` so Go's own test cache sees changes ([`D-GR40`](../reference/gate.md#D-GR40), [`D-GR49`](../reference/gate.md#D-GR49)).
3. **Unwired package-verdict flags in `tools/gate/go_affected.py`.** `compute_package_verdict_digest`, `package_verdict_reusable`, and `--verdicts-in` / `--verdicts-out` (`TILLR_GO_VERDICTS_IN` / `TILLR_GO_VERDICTS_OUT`) already compute content digests for selected packages, yet `rg -n 'TILLR_GO_VERDICTS|verdicts-in' tools/` matches only `tools/gate/go_affected.py` itself: neither [`tools/gate/checks/go-affected.json`](../../tools/gate/checks/go-affected.json), [`tools/gate/change_gate.go`](../../tools/gate/change_gate.go), [`tools/tillr-drain/drain/replay_go_affected.go`](../../tools/tillr-drain/drain/replay_go_affected.go), nor `tools/go-test-shards.py` passes those flags today.

Earlier on 2026-11-25, [`D-GR53`](../reference/gate.md#D-GR53) and [`D-GR54`](../reference/gate.md#D-GR54) moved `github.com/mschulkind-oss/tillr-drain/drain` out of pre-merge `go-affected` into the after-merge `go-test-tillr-drain` proof (also weighed in [`OQ-DFG1`](./fast-gate-tillr-drain-options.md#OQ-DFG1)) after `drain` accounted for `83.0%–99.99%` of >20-minute landing gates. That deferral relieved pre-merge latency without reducing total test work: rolling trunk proofs, cancelled proofs on superseded tips, restarted gates, and base or culprit replays still re-execute unchanged Go tests.

---

## 2. Recorded cache hits and test runs today

At snapshot `seq <= 44,215` (`2026-11-25T22:00:28Z`) in `/workspace/.tillr/state.db` and the live loop and rolling directories, recorded Go test cache reuse splits sharply across the three gate contexts.

| Context | Recorded scope | Answered from cache | Ran (uncached) | Unclassified in logs | Why |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Landing gate** (`go-affected` check level) | `285` gate records across `211` `gate.timing.jsonl` files | `0` (`0.0%` reused; `44` `not-run` when out of scope) | `167` (`220,491.2 s` total wall; median `1,393.9 s`, p90 `2,855.5 s`, max `4,357.2 s`) | `74` early records before check breakdown | `go-affected.json` sets `"remember": false` |
| **Landing gate** (unsharded packages in `go-affected`) | `1,389` package runs across `170` `*go-affected*.log` files | `233` definite (`16.8%`) to `300` ceiling (`21.6%`) | `1,089` definite (`78.4%`: `1,081` pass + `8` fail) to `1,156` (`83.2%`) | `67` sub-`5 ms` `(0.00s)` passes | Distinct lane worktree paths and fresh file mtimes miss Go's cache whenever a test opens an in-module file |
| **Landing gate** (sharded packages in `go-affected`) | `4,206` shard runs across `521` package selections (`170` logs) | `267` shards (`6.3%`); `0 / 1,776` (`0.0%`) in `tillr-drain/drain`; all shards cached in `2 / 521` (`0.4%`) packages | `3,939` shards (`93.7%`: `3,874` pass + `65` fail) | `0` (sharded binaries exceed `5 ms` when uncached) | Large packages open fixtures and temp git repos inside per-lane worktrees |
| **Base replay** (candidate acquittal on `base_sha`) | `47` `drain.base-replay-ended` events (`36` naming `go-affected`; `1,423.7 s` wall, median `24.7 s`) | `0` recorded (`0%` by construction on named-test replays) | Not broken out per package/shard | All `47` ended `"unknown"` before `replayGoCheck` landed; non-red replay stdout is discarded | `runNamedGoTestsInReplay` passes `-count=1`; fallback `go_affected.py` runs in a fresh `.replay/<sha>-*` worktree |
| **Trunk proof** (whole-check memory in `drain.heavy-progress`) | `194` `go-test-*` module check evaluations across `24` started / `23` ended proofs (`6` green, `9` red, `8` cancelled) | `85` (`43.8%` answered without entering `running`, via `.git/tillr-gate/results/` or sub-poll completion) | `109` (`56.2%`: `97` pass, `9` fail, `3` error) | — | Whole-check memory reuses untouched leaf modules; wide `reads` invalidate core modules |
| **Trunk proof** (`6` unsharded `go test ./...` checks) | `222` package lines in `.rolling/logs/<sha>/0*-go-test-*.log` | `149` (`67.1%`, led by `126 / 163` in `go-test-tillr`) | `73` (`32.9%`: `71` pass + `2` fail; `18 / 19` ran in `go-test-tillr-watch`) | `0` | `Rolling` reuses one path (`.rolling/worktree`), so pure in-memory leaf packages hit Go's cache |
| **Trunk proof** (`8` sharded `go-test-shards.py --cache` checks) | `144` module log files (`25` full proofs + `14` culprit-replay dirs in `.rolling/logs/`) | Not emitted to stdout | `22` failing shards across `22` failing module runs | `122` passing module runs (`121` proof + `1` culprit replay) | `go-test-*.json` omits `TILLR_GO_SHARDS_EVIDENCE_OUT`; stdout prints test-event counts that replay identically on cache hits |

### How each figure was measured and why two blind spots exist

Two telemetry omissions explain the unclassified cells above and are straightforward to close regardless of which caching option the owner selects:

1. **`go_affected.py` parses `(cached)` into `rec["cached"]` inside `parse_go_test_json`, yet [`tools/gate/checks/go-affected.json`](../../tools/gate/checks/go-affected.json) does not pass `--evidence-out`, and `gateCheckMeasurement` in [`tools/tillr-drain/drain/gate_timing.go`](../../tools/tillr-drain/drain/gate_timing.go) omits `go-affected` package records.** Consequently, `change-logs/*-go-affected.log` contains only `format_progress_event` lines (`go-affected: pass <pkg> (<elapsed:.2f>s)`). In Go 1.27.3 (`cmd/go/internal/test/test.go` and `cmd/internal/test2json/test2json.go`), a cached `go test -json` run replays buffered `--- PASS: TestX (D.DDs)` lines in memory and closes `test2json.Converter` in under `1 ms`, emitting package `Elapsed: 0` (`(0.00s)`). When the replayed top-level test lines inside an unsharded package sum to `>= 0.01s` while the enclosing package line prints `(0.00s)`, the run is **provably** a Go test cache hit (`233` packages). When both test lines and package line print `(0.00s)` (`67` packages), a sub-`5 ms` pure-in-memory binary execution (`3–4 ms` observed on Linux) cannot be separated from a cache hit without `--evidence-out`. Combined across all `5,595` package and shard invocations (`1,389 + 4,206`), `500–567` (`8.9%–10.1%`) hit Go's cache and `5,028–5,095` (`89.9%–91.1%`) executed.
2. **`tools/go-test-shards.py` (`--cache`) computes per-package cache status (`hit`, `partial`, `miss`) inside `_run_cached`, yet `tools/gate/checks/go-test-*.json` never sets `TILLR_GO_SHARDS_EVIDENCE_OUT`.** Because `go test -json` replays per-test `"Action": "pass"` events from Go's cached output buffer, `go-test-shards.py`'s summary line (`<N> tests passed, 6 shards per package`) prints the same count on a cache hit and an uncached run.

Every figure in the table above comes from the following read-only inspection script run against `/workspace/.tillr`:

```sh
python3 - <<'PY'
import glob, json, os, re, sqlite3
from collections import Counter, defaultdict

con = sqlite3.connect("file:/workspace/.tillr/state.db?mode=ro", uri=True)
max_seq = 44215  # frozen snapshot at 2026-11-25T22:00:28Z

# 1. Landing gate: gate.timing.jsonl + change-logs/*go-affected*.log
timing_files = sorted(glob.glob("/workspace/.tillr/loop/*/gate.timing.jsonl"))
timing_rows = [json.loads(l) for tf in timing_files for l in open(tf) if l.strip()]
exec_counts = Counter(r.get("checks", {}).get("go-affected", {}).get("execution", "missing") for r in timing_rows)
walls = sorted(r["checks"]["go-affected"]["wall_ms"] for r in timing_rows
               if r.get("checks", {}).get("go-affected", {}).get("execution") == "ran"
               and r["checks"]["go-affected"].get("wall_ms") is not None)
print("gate.timing.jsonl:", len(timing_files), len(timing_rows), dict(exec_counts),
      "sum_s=", round(sum(walls) / 1000, 1), "p50_ms=", walls[len(walls) // 2], "p90_ms=", walls[int(len(walls) * 0.9)])

ga_logs = sorted(glob.glob("/workspace/.tillr/loop/*/change-logs/*go-affected*.log"))
prog_re = re.compile(r"^go-affected: (pass|fail|skip) (\S+?)(?: ([^(]\S*))?(?: \(([\d.]+)s\))?$")
unsharded, sharded_shards, sharded_pkgs, drain_shards = Counter(), Counter(), Counter(), Counter()
for lf in ga_logs:
    pkg_evs, pkg_test_sum = defaultdict(list), Counter()
    for ln in open(lf, errors="replace"):
        m = prog_re.match(ln.rstrip("\n"))
        if not m:
            continue
        act, pkg, test, dur = m.group(1), m.group(2), m.group(3), float(m.group(4) or 0.0)
        if test:
            if "/" not in test:
                pkg_test_sum[pkg] += dur
        else:
            pkg_evs[pkg].append((act, dur))
    for pkg, evs in pkg_evs.items():
        if len(evs) == 1:
            act, dur = evs[0]
            key = ("ran_fail" if act == "fail" else
                   "definite_cached" if dur == 0.0 and pkg_test_sum[pkg] >= 0.01 else
                   "ambig_0.00s" if dur == 0.0 else "ran_pass")
            unsharded[key] += 1
        else:
            z = sum(1 for a, d in evs if a == "pass" and d == 0.0)
            nz = sum(1 for a, d in evs if a == "pass" and d > 0.0)
            fl = sum(1 for a, d in evs if a == "fail")
            sharded_shards["cached_0.00s"] += z
            sharded_shards["ran_pass"] += nz
            sharded_shards["ran_fail"] += fl
            if pkg == "github.com/mschulkind-oss/tillr-drain/drain":
                drain_shards["cached_0.00s"] += z
                drain_shards["ran"] += nz + fl
            sharded_pkgs["all_cached" if z == len(evs) else "partial_cached" if z else "none_cached"] += 1
print("go-affected logs:", len(ga_logs), dict(unsharded), dict(sharded_pkgs), dict(sharded_shards), dict(drain_shards))

# 2. Base replay: drain.base-replay-ended
br = [json.loads(p) for (p,) in con.execute(
    "SELECT payload FROM events WHERE kind='drain.base-replay-ended' AND seq<=?", (max_seq,))]
br_el = sorted(r["elapsed_ms"] for r in br)
print("base-replay:", len(br), Counter(r["outcome"] for r in br),
      Counter(c for r in br for c in r.get("checks", [])),
      "p50_ms=", br_el[len(br_el) // 2], "sum_s=", round(sum(br_el) / 1000, 1))

# 3. Trunk proof: drain.heavy-progress + .rolling/logs/*
hp = [json.loads(p) for (p,) in con.execute(
    "SELECT payload FROM events WHERE kind='drain.heavy-progress' AND seq<=?", (max_seq,))]
by_tip_check = defaultdict(list)
for r in hp:
    cid = r.get("check_id", "")
    if r.get("phase") == "progress" and cid.startswith("go-test-") and cid != "go-test-shards-test":
        by_tip_check[(r.get("tip_sha"), cid)].append(r.get("outcome"))
print("heavy-progress:", Counter(r.get("phase") for r in hp),
      Counter(r.get("outcome") for r in hp if r.get("phase") == "ended"),
      Counter(([o for o in outs if o != "running"][-1], "running" in outs)
              for outs in by_tip_check.values() if any(o != "running" for o in outs)))

unsharded_roll, sharded_roll = Counter(), Counter()
for d in sorted(glob.glob("/workspace/.tillr/lanes/.rolling/logs/*")):
    files = os.listdir(d)
    bucket = "full_proof" if len(files) > 3 else "culprit_replay"
    for fn in files:
        if not re.match(r"^\d+-go-test-(?!shards-test)", fn):
            continue
        txt = open(os.path.join(d, fn), errors="replace").read()
        for ln in txt.splitlines():
            if ln.startswith("ok  \t"):
                unsharded_roll[(bucket, "pkg_cached" if "(cached)" in ln else "pkg_ran")] += 1
            elif ln.startswith("FAIL\t"):
                (sharded_roll if "go-test-shards:" in txt else unsharded_roll)[(bucket, "pkg_fail")] += 1
            elif ln.startswith("go-test-shards: tools/"):
                sharded_roll[(bucket, "module_pass_unclassified")] += 1
            elif re.match(r"^go-test-shards: \S+ shard \d+ failed", ln):
                sharded_roll[(bucket, "shard_fail")] += 1
print("rolling logs:", dict(unsharded_roll), dict(sharded_roll))
PY
```

---

## 3. Soundness gaps in Go's test cache and this tree's exposure

In Go 1.27.3 (`$GOROOT/src/cmd/go/internal/test/test.go`, functions `tryCacheWithID`, `computeTestInputsID`, `hashOpen`, `hashStat`, and `saveOutput`), `go test` caches passing runs in two stages:

1. `testID` hashes the compiled test binary's `BuildActionID` (or `BuildContentID`) together with cacheable test command-line flags (`-run`, `-parallel`, `-short`, `-timeout`). Looking up `testID` returns the previous run's `testlog.txt`, written via hooks in Go's `os` package (`internal/testlog`) whenever the test process called `os.Getenv`, `os.Chdir`, `os.Stat`, or `os.OpenFile`.
2. `computeTestInputsID` replays `testlog.txt` against the current host environment and filesystem to compute `testInputsID`, and `tryCacheWithID` looks up `(testID, testInputsID)`.

All four known gaps of that protocol appear in this repository's measurements or test suites today:

| Gap in Go's test cache | Exposed in this tree today? | Direction of exposure today | Concrete specimen in `tools/` |
| :--- | :--- | :--- | :--- |
| **1. Inputs keyed by path, size, and mtime + `2 s` cutoff** | **Yes** (miss direction on every cross-worktree and fresh-checkout gate; unhit today on same-stat stale content) | Forces cache misses across `lane-*`, `.replay/*`, and `.rolling/worktree`, and drops cache writes within `2 s` of checkout | `loadTestContext` in [`tools/gate/change_checks_test.go`](../../tools/gate/change_checks_test.go); `11` `testdata/` directories across `tools/`; `detachedWorktreeAt` in [`tools/tillr-drain/drain/steps.go`](../../tools/tillr-drain/drain/steps.go) |
| **2. Files outside `a.Package.Root`** | **Yes** (`14` packages mitigated via `go-test-edges.json`; unmitigated packages remain live) | Stale green cache hits (and missed `go-affected` selection) when repo-root or sibling files change | `TestRepositoryEnablesRollAndPushAndExplainsTheInstall` and `TestShardedModulesMatchTheGate` in [`tools/tillr-roll/roll/roll_test.go`](../../tools/tillr-roll/roll/roll_test.go); `TestWorktreeAutoDiscovery` in [`tools/tillr-loop/loop/loop_test.go`](../../tools/tillr-loop/loop/loop_test.go) |
| **3. Child-process reads (plus `os.Environ()`)** | **Yes** (`286` test files in `32` packages call `exec.Command*`; `20` packages lack `go-test-edges.json` entries; `125` files call `os.Environ()`) | Stale green cache hits even inside a single Go module when a child `go build`, `git`, `python3`, or `sh` reads unimported files | `newHookFixture` in [`tools/tillr/gitidentity/precommit_test.go`](../../tools/tillr/gitidentity/precommit_test.go) (`go build ./cmd/pre-commit-hook`); `buildDrainBinary` in [`tools/tillr-drain/keep/keep_test.go`](../../tools/tillr-drain/keep/keep_test.go) |
| **4. Network and wall-clock time** | **Network: loopback/Unix sockets only** (`0` external hosts). **Time: yes** (`168` test files call `time.Now()`, `8` format/truncate calendar dates) | Cached passes never re-evaluate `time.Now()` or ephemeral port collisions | [`tools/tillr-view/sync_residue_test.go`](../../tools/tillr-view/sync_residue_test.go), [`tools/tillr-run/run/seat_record_test.go`](../../tools/tillr-run/run/seat_record_test.go) (`time.Now()`); `21` `httptest` and `7` `net.Listen*` test files |

### 3.1 Inputs keyed by path, size, and mtime, plus the two-second cutoff

**Exposure today: yes on cache invalidation across worktrees and fresh checkouts; latent (not triggered today) on same-stat content edits.**

Even though `go_affected.py` and `go-test-*.json` pass `GOFLAGS=-trimpath` so test binaries compile to the same `BuildActionID` across worktrees, `computeTestInputsID` initializes `pwd := a.Package.Dir` (the real worktree directory) and resolves every relative `stat` or `open` in `testlog.txt` with `filepath.Join(pwd, name)` before writing `fmt.Fprintf(h, "open %s %x\n", name, fh)`. That hashes **both** the absolute worktree path (`/workspace/.tillr/lanes/lane-1/worktree/...` versus `lane-2`, `.replay/<sha>-*`, or `.rolling/worktree`) **and** `hashWriteStat` (`Size()`, `Mode()`, `ModTime()`, `IsDir()`). Every package reading any file inside its own module — including the `11` `testdata/` directories under `tools/` and source/config readers such as `loadTestContext` in [`tools/gate/change_checks_test.go`](../../tools/gate/change_checks_test.go) — therefore misses Go's test cache whenever a second worktree runs the exact same commit tree.

Furthermore, `hashOpen` rejects any regular file with `time.Since(info.ModTime()) < modTimeCutoff` (`const modTimeCutoff = 2 * time.Second`, returning `errFileTooNew`), causing `saveOutput` to discard the cache write altogether. Whenever `detachedWorktreeAt` ([`tools/tillr-drain/drain/steps.go`](../../tools/tillr-drain/drain/steps.go)) checks out a fresh replay or rolling worktree and launches a fast test within two seconds, Go refuses to save the result.

In the opposite (unsound) direction — mutating a file in place while preserving exact byte length and nanosecond `ModTime()` — this tree is not triggered today: `rg -n 'os\.Chtimes' tools/` matches only `5` test files (`alarm_why_test.go`, `start_view_watcher_test.go`, `heavy_recovery_test.go`, `lanes_test.go`, `launcher_observatory_test.go`), all touching private `t.TempDir()` fixtures rather than tracked inputs.

```sh
# Prove both the <2 s cutoff and cross-worktree path miss on an in-module reader (`tools/gate`):
python3 - <<'PY'
import os, shutil, subprocess, tempfile, time
with tempfile.TemporaryDirectory() as tmp:
    gc = os.path.join(tmp, "gocache")
    wt1, wt2 = os.path.join(tmp, "wt1"), os.path.join(tmp, "wt2")
    shutil.copytree("tools/tillr", os.path.join(tmp, "tillr"))
    shutil.copytree("tools/gate", wt1)
    shutil.copytree("tools/gate", wt2)
    env = {**os.environ, "GOCACHE": gc, "CGO_ENABLED": "0", "GOFLAGS": "-trimpath", "GODEBUG": "gocachetest=1"}
    r0 = subprocess.run(["go", "test", "-run", "^TestChangeChecksGitIdentity$", "."], cwd=wt1, env=env, capture_output=True, text=True)
    print("fresh (<2s):", [l for l in r0.stderr.splitlines() if "too new" in l or "do not save" in l])
    time.sleep(2.1)
    r1 = subprocess.run(["go", "test", "-run", "^TestChangeChecksGitIdentity$", "."], cwd=wt1, env=env, capture_output=True, text=True)
    r2 = subprocess.run(["go", "test", "-run", "^TestChangeChecksGitIdentity$", "."], cwd=wt2, env=env, capture_output=True, text=True)
    print("wt1 (>2s) stdout:", r1.stdout.strip())
    print("wt2 (same bytes, different path) stdout:", r2.stdout.strip())
PY
```

### 3.2 Files outside the package's module

**Exposure today: yes.**

Because this repository has `14` standalone `go.mod` files under `tools/` and no `go.work`, `a.Package.Root` for each module package equals that single module's directory (for example, `.../tools/tillr-roll`). In `computeTestInputsID`, any `stat` or `open` where `search.InDir(name, a.Package.Root) == ""` hits `break` (`// Do not recheck files outside the module, GOPATH, or GOROOT root.`).

[`tools/gate/go-test-edges.json`](../../tools/gate/go-test-edges.json) closes this gap for `14` declared packages via `TILLR_GATE_EDGE_INPUTS`, and [`tools/gate/go_edges.go`](../../tools/gate/go_edges.go) verifies source-scanning modules against `missingSourceScanningEdges`. However, packages omitted from `go-test-edges.json` remain exposed today:

1. **`github.com/mschulkind-oss/tillr-roll/roll` ([`tools/tillr-roll/roll/roll_test.go`](../../tools/tillr-roll/roll/roll_test.go))** is absent from `tools/gate/go-test-edges.json`, and [`tools/gate/checks/go-test-tillr-roll.json`](../../tools/gate/checks/go-test-tillr-roll.json) runs plain `go test -parallel 1 -timeout 0 ./...` without `TILLR_GATE_EDGE_INPUTS`. Yet `TestRepositoryEnablesRollAndPushAndExplainsTheInstall` opens `.tillr.toml` and `docs/` at the repository root via `repoRootForRollTest`, and `TestShardedModulesMatchTheGate` globs and reads `tools/gate/checks/go-test-*.json`. Editing `.tillr.toml` or `tools/gate/checks/go-test-*.json` does not invalidate Go's test cache for `tillr-roll/roll` (nor select it in `go_affected.py`).
2. **`github.com/mschulkind-oss/tillr-loop/loop` ([`tools/tillr-loop/loop/loop_test.go`](../../tools/tillr-loop/loop/loop_test.go))** is absent from `tools/gate/go-test-edges.json`, and [`tools/gate/checks/go-test-tillr-loop.json`](../../tools/gate/checks/go-test-tillr-loop.json) runs plain `go test ./...`. Yet `TestWorktreeAutoDiscovery` reads `yolo-packs/tillr/programs/tillr-item/run` via `repoRoot(t)`, and `TestGatePlansAgainstCandidateDeclarations` and `TestSelfCheckAndMissingJudge` call `t.Chdir(repoRoot(t))` to inspect `tools/gate/` and `tools/tillr-gate/`.

```sh
# Reproduce on HEAD: `tillr-roll/roll` reads `.tillr.toml` outside `tools/tillr-roll` and still caches with an unchanged input ID:
tmp_gc="$(mktemp -d)"
GOCACHE="$tmp_gc" CGO_ENABLED=0 GOFLAGS=-trimpath GODEBUG=gocachetest=1 \
  go -C tools/tillr-roll test -run '^TestRepositoryEnablesRollAndPushAndExplainsTheInstall$' ./roll
GOCACHE="$tmp_gc" CGO_ENABLED=0 GOFLAGS=-trimpath GODEBUG=gocachetest=1 \
  go -C tools/tillr-roll test -run '^TestRepositoryEnablesRollAndPushAndExplainsTheInstall$' ./roll
rm -rf "$tmp_gc"
```

### 3.3 Anything a child process reads (and raw process environments)

**Exposure today: yes — including inside a single Go module.**

Go's `internal/testlog` hooks run only inside the parent test binary's Go runtime. When a test spawns a child process (`exec.Command` or `exec.CommandContext` appears in `286` `*_test.go` files across `32` of `53` test packages, `20` of which have no entry in `tools/gate/go-test-edges.json`), `-test.testlogfile` is not inherited and non-Go children (`git`, `python3`, `sh`, `just`) could not write to it anyway. Likewise, `os.Environ()` (`125` `*_test.go` files) reads `syscall.Environ()` directly without calling `testlog.Getenv`, so environment variables inspected via `os.Environ()` or inherited by child processes are invisible to `computeTestInputsID`.

Three live tests demonstrate that this gap affects both intra-module and cross-module child builds today:

1. **`newHookFixture` in [`tools/tillr/gitidentity/precommit_test.go`](../../tools/tillr/gitidentity/precommit_test.go)** (`github.com/mschulkind-oss/tillr/gitidentity`, absent from `go-test-edges.json`): `gitidentity` does not import `./cmd/pre-commit-hook`, yet `newHookFixture` runs `exec.Command("go", "build", "-o", binPath, "./cmd/pre-commit-hook")`. Editing `tools/tillr/cmd/pre-commit-hook/main.go` leaves `gitidentity`'s test binary and `testlog.txt` byte-for-byte unchanged, so `go test ./gitidentity` returns `(cached)` against stale hook code.
2. **`buildDrainBinary` in [`tools/tillr-drain/keep/keep_test.go`](../../tools/tillr-drain/keep/keep_test.go)** (`github.com/mschulkind-oss/tillr-drain/keep`, absent from `go-test-edges.json`): `keep` does not import root `github.com/mschulkind-oss/tillr-drain`, yet `buildDrainBinary` runs `exec.Command("go", "build", "-o", bin, ".")` in `tools/tillr-drain`. Editing `tools/tillr-drain/main.go` or any sibling module imported by `tillr-drain` but not `keep` leaves `keep`'s Go test cache entry valid.
3. **`TestHookDispatchMatchesDrainFlagCases` in [`tools/tillr-loop/loop/loop_test.go`](../../tools/tillr-loop/loop/loop_test.go)**: runs `exec.Command("go", "test", "./drain", "-run", "^TestTillrLoopDefinitionHookFlagParity$")` inside `tools/tillr-drain`, completely invisible to `tillr-loop/loop`'s `testlog.txt`.

```sh
# Inspect intra-module and cross-module child builds in packages outside go-test-edges.json:
rg -n 'exec\.Command\("go", "(build|test)"' \
  tools/tillr/gitidentity/precommit_test.go \
  tools/tillr-drain/keep/keep_test.go \
  tools/tillr-loop/loop/loop_test.go
```

### 3.4 Network and wall-clock time

**Exposure today: network is confined to loopback and local Unix sockets; wall-clock `time.Now()` is exposed across `168` test files.**

- **Network:** Scanning all `1,182` `*_test.go` files under `tools/` finds `21` files starting `httptest.NewServer` or `NewUnstartedServer` on `127.0.0.1:0` and `7` files calling `net.Listen*` on `127.0.0.1:0` or temporary Unix domain sockets (such as [`tools/tillr/notify/notify_test.go`](../../tools/tillr/notify/notify_test.go) and [`tools/tillr-view/launcher_observatory_test.go`](../../tools/tillr-view/launcher_observatory_test.go)). Checking literal dial and HTTP targets (`rg -n '\b(http\.(Get|Post|Head)|net\.Dial\w*)\s*\(\s*"[^"]+"' tools/ -g '*_test.go'`) returns only loopback `http://` URLs constructed from local test listeners in [`tools/tillr-view/restart_serving_internal_test.go`](../../tools/tillr-view/restart_serving_internal_test.go) — zero external internet hosts.
- **Wall-clock time:** `rg -l '\btime\.Now\s*\(' tools/ -g '*_test.go' | wc -l` counts `168` test files calling `time.Now()`, including `8` files formatting or truncating calendar dates (`rg -l 'time\.Now\(\)\.(UTC\(\)\.)?(Format|Year|Month|Day|Truncate)' tools/ -g '*_test.go'`, such as [`tools/tillr-view/sync_residue_test.go`](../../tools/tillr-view/sync_residue_test.go) and [`tools/tillr-run/run/seat_record_test.go`](../../tools/tillr-run/run/seat_record_test.go)). Go's test cache hashes no clock reading, so any test that only fails across a calendar boundary or embedded timestamp stays green when cached.

---

## 4. Options, correctness arguments, and build-system trade-offs

The four mechanisms below attack orthogonal failure modes:

- **Option 1** makes cache keys **worktree- and mtime-independent** across gates, replays, restarts, and rolling proofs.
- **Option 2** makes the input key **sound by construction**, turning undeclared file or child-process reads into immediate test failures instead of stale green verdicts.
- **Option 3** shrinks the **unit of checkpointed work** so cancelled proofs, stalled gates, and narrowed `read_tests` / `build_tests` runs do not repeat whole modules or giant shards.
- **Option 4** shrinks the **compile and import-closure blast radius** so edits to leaf logic stop selecting unrelated suites.

The four options compose cleanly as layers, though each carries distinct engineering cost and assumptions for the owner to weigh in [§6](#6-open-questions).

### 4.1 Option 1: Content-keyed test result store

**Mechanism.** Store per-package and per-shard test verdicts in a shared machine-local content-addressed directory under the repository's common Git directory (`.git/tillr-gate/test-verdicts/`, alongside `.git/tillr-gate/results/`), rather than relying on Go's mtime-and-absolute-path `testInputsID`. A **test unit key** *(coined here)* hashes:

1. **Test code and transitive Go dependencies:** either the trimmed test binary's `BuildContentID` (`CGO_ENABLED=0` and `GOFLAGS=-trimpath`) or the content SHA-256 of every `.go`, `go.mod`, `go.sum`, and `embed` file in the package's transitive import closure, plus Go toolchain version (`GOVERSION`) and architecture.
2. **In-module non-Go test inputs:** relative path plus content SHA-256 (never absolute path or `mtime`) of non-`.go` files inside the package directory (such as `testdata/`), eliminating both cross-worktree path misses and Go's `2 s` `modTimeCutoff`.
3. **Declared cross-module `reads` and `builds`:** content SHA-256 of every path matched by the unit's `go-test-edges.json` `reads` (narrowed to `read_tests` when running a subset) plus the content digest of every `builds` target (narrowed to `build_tests` under [`D-GR51`](../reference/gate.md#D-GR51)).
4. **Execution contract:** normalized test flags, sorted top-level test-name list for that shard or narrowed invocation, scrubbed environment digest (excluding per-slot scratch paths `TMPDIR`, `GOTMPDIR`, and `GIT_CONFIG_GLOBAL`), and sandbox policy version.

Writes use atomic temp-file-and-rename (matching [`tools/tillr-gate/memory.go`](../../tools/tillr-gate/memory.go)), require zero cross-lane locks ([`D-GR2`](../reference/gate.md#D-GR2)), and never reuse a recorded `fail` — while recording a `fail` alongside an existing `pass` for the identical unit key triggers contradiction disabling under [`D-GR21`](../reference/gate.md#D-GR21) and [`D-GR22`](../reference/gate.md#D-GR22). Wiring the existing `--verdicts-in` / `--verdicts-out` design in `go_affected.py` and `go-test-shards.py` onto this shared store immediately spans **landing gates**, **restarted runs**, **rolling trunk proofs**, and (if ruled in [`OQ-GC3`](#OQ-GC3)) **base and culprit replays**.

- **Correctness argument:** When test outcome `F(B, I, E)` is a deterministic function of compiled test binary `B`, input file bytes `I`, and scrubbed environment `E`, and every byte read by the test or its children is included in `(B, I, E)`, SHA-256 collision resistance guarantees that equal unit keys imply byte-identical `(B, I, E)` regardless of worktree path, checkout timestamp, or prior restarts.
- **Assumptions required for soundness:**
  1. *Completeness of declared inputs:* the test and all child processes read no repo or host file outside the hashed set (untrue on `HEAD` for `tillr-roll/roll`, `tillr-loop/loop`, `tillr/gitidentity`, and `tillr-drain/keep` unless paired with Option 2 or exhaustive static audits).
  2. *Path-invariance of test logic:* a test's pass or fail verdict does not depend on the spelling of `os.Getwd()` or `TMPDIR`.
  3. *Determinism:* the test does not depend on wall-clock calendar date, external network state, or unseeded randomness that flips verdicts without input changes.

### 4.2 Option 2: Enforced input boundary so undeclared reads fail loud

**Mechanism.** Run each cached Go test binary under a small Linux Landlock wrapper (`go test -exec`) that restricts filesystem access **before** `execve` of the test binary. Because Landlock rulesets are inherited across `fork` and `execve` in the Linux kernel, the restriction binds **both the Go test binary and every child `go`, `git`, `python3`, or `sh` process it spawns**.

Probing this host environment (`Linux 7.3.5-zen1-1-zen`) confirms unprivileged **Landlock ABI v11** (`landlock_create_ruleset` syscall `444` returns a valid file descriptor without `CAP_SYS_ADMIN`, whereas `unshare`, `bwrap`, and `fusermount3` are absent or unprivileged-restricted in container environments). For each test unit, the Landlock ruleset grants:

- **Read + write + execute:** the invocation's private scratch directory (`TMPDIR` and `GOTMPDIR`, plus a private or declared `GOCACHE` if child `go build` is declared) and `/dev/null`, `/dev/urandom`, and `/proc`.
- **Read + execute (read-only):** the Nix and system toolchain paths (`/nix/store`, `/usr`, `/bin`, `/lib`, `/etc`), the package's own directory, and **only** the exact repository paths declared in `go-test-edges.json` `reads` and pre-built `builds` outputs.
- **Denied (`EACCES`):** every other directory and file in the repository worktree (sibling `tools/*` modules, `.tillr.toml`, `docs/`, `config/`, `yolo-packs/`, `.git/`) and parent `/workspace/.tillr/` state.

Under Landlock ABI v4+, TCP `bind` and `connect` rules can likewise deny all non-loopback ports.

- **Correctness argument:** Option 1's primary soundness risk is human or agent omission in `go-test-edges.json` (Gaps 2 and 3 in [§3](#3-soundness-gaps-in-gos-test-cache-and-this-trees-exposure)). With kernel-enforced read rules derived from the **exact same input list** hashed into Option 1's unit key, any undeclared read inside the repository returns `EACCES` on the first run — failing the test immediately instead of writing a stale pass record into the cache.
- **Assumptions required for soundness:**
  1. *Linux Landlock availability:* Linux kernel `>= 5.13` (ABI v1+ for file reads; ABI v11 is live on this host). On macOS developer machines (`just done`), the launcher either uses `sandbox-exec` or runs without writing to a shared drain store.
  2. *Directory-hierarchy path rules:* Landlock attaches rules to open directory and file descriptors (`LANDLOCK_ACCESS_FS_READ_FILE` and `LANDLOCK_ACCESS_FS_READ_DIR`), so the worktree root itself is left unlisted while allowed subdirectories and files beneath it are explicitly added before `landlock_restrict_self`.
  3. *Clock and kernel state:* Landlock does not virtualize `CLOCK_REALTIME` (`time.Now()`), process IDs, or CPU scheduling load.

### 4.3 Option 3: Finer duration-balanced shards and per-shard result checkpointing

**Mechanism.** Current sharding loses work on interruptions and pays repeated `go` tool overhead if made finer without pre-compilation:

1. **Coarse shards lose completed work on cancellation or stall:** [`tools/gate/checks/go-test-tillr-drain.json`](../../tools/gate/checks/go-test-tillr-drain.json) runs `go-test-shards.py tools/tillr-drain -k 6 --cache`, packing `1,842` recorded tests (`3,695.1 s` single-core duration in [`tools/go-test-durations.json`](../../tools/go-test-durations.json)) into `6` shards of `~615.9 s` (`10.3 min` ideal, `25.5 min` under contention in `.rolling/logs/f3c48d25.../00-go-test-tillr-drain.log`). When a newer landing cancels an in-flight trunk proof (`8` of `23` ended proofs in `state.db` were `cancelled`) or a gate re-runs after a stall (`9` run directories in `.tillr/loop/` accounted for `74` extra gate executions), every completed shard that missed Go's cache repeats from zero.
2. **Repeated `go test` startup overhead if shards are shrunk naively:** in `go-test-shards.py --cache` and `go_affected.py`, each shard invokes `go test -json -run <pattern>`, paying `go` package-graph loading and linking per shard.

Option 3 compiles each sharded package's test binary **once** per content key (`go test -c -trimpath`), partitions tests into target-duration buckets (for example, `20–30 s` of recorded runtime per shard, bounded by the same concurrent worker limit as today), and writes **each shard's content-keyed verdict immediately upon shard completion** into `.git/tillr-gate/test-verdicts/`. Whenever a trunk proof is cancelled for a landing that did not touch `tools/tillr-drain` (or a gate restarts after a stall or loop replacement), the next run skips every shard already checkpointed under that binary's content key; likewise, narrowed `read_tests` and `build_tests` runs execute and cache only the affected shard buckets.

- **Correctness argument:** With `-parallel 1` and fresh per-shard private scratch (`TMPDIR`, `GOTMPDIR`, `GIT_CONFIG_GLOBAL`), shards share no mutable filesystem or process state. Recording a shard's pass verdict as soon as its subprocess exits `0` preserves exact equivalence with running all shards in one uninterrupted invocation, provided tests within a package do not rely on cross-test package-global mutation order (already required by [`D-GR49`](../reference/gate.md#D-GR49)'s `6`-shard partition today).
- **Assumptions required for soundness:**
  1. *Order-independence across top-level tests:* a test that passes in one shard bucket would not fail in full-package order (`TestMain` runs once per shard binary invocation).
  2. *Per-shard edge scoping:* Option 1's unit key for a shard includes the union of declared `reads` and `builds` digests for the tests assigned to that shard.

### 4.4 Option 4: Carving leaf packages out of monolithic suites

**Mechanism.** Extract cohesive, loop-free domain logic and its unit tests out of oversized packages into leaf Go subpackages that do not import the parent orchestrator package, enforced by import-ban tests (continuing [`D-GR46`](../reference/gate.md#D-GR46) and [`D-GR47`](../reference/gate.md#D-GR47)).

Measured package concentration on `HEAD` shows why selection fanout remains wide even with a sound cache:

| Package | Prod `.go` files | `*_test.go` files | Top-level `Test*` | Summed duration (`go-test-durations.json`) | Selected in `170` `go-affected` logs |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `tools/tillr-drain/drain` | `251` | `585` | `2,144` | `3,695.1 s` (`96.6%` of repo total) | `111 / 170` (`65.3%`, before [`D-GR53`](../reference/gate.md#D-GR53)) |
| `tools/tillr-view` | `77` | `134` | `414` | `38.2 s` | `130 / 170` (`76.5%`) |
| `tools/tillr-land` | `2` | `26` | `158` | `38.4 s` | `48 / 170` (`28.2%`) |
| `tools/tillr-item` | `9` | `43` | `183` | `17.2 s` | `110 / 170` (`64.7%`) |
| `tools/tillr-watch` | `9` | `54` | `112` | `—` (unsharded `~35 s` wall) | `127 / 170` (`74.7%`) |
| `tools/tillr-gate` | `22` | `32` | `145` | `14.6 s` | `46 / 170` (`27.1%`) |

Editing any single `.go` file in `tools/tillr-view` recompiles all `211` `.go` files in `tillr-view` (`77` prod + `134` test), changes `tillr-view.test`'s `BuildContentID`, and invalidates every cached shard of `tillr-view` even under Option 1. Carving pure leaf packages (such as `tillr-view`'s projection and rendering helpers, or loop-free state-classification helpers in `tools/tillr-drain`) means an edit to one leaf package leaves sibling leaf packages' `BuildContentID`s untouched.

At the same time, [`D-GR54`](../reference/gate.md#D-GR54)'s measurement bounds how far Option 4 reaches inside `tools/tillr-drain/drain`: **`436` of `535` integration test files in `drain` are white-box `package drain` tests referencing `942` unexported declarations across `457` files**, exercising the full `Loop` state machine end to end. Carving leaf helpers eliminates recompilation on leaf edits, while edits to `Loop` itself continue to rely on Options 1–3.

- **Correctness argument:** Go's package import graph is acyclic and compiler-enforced. If package `A` does not transitively import package `B` and declares no `reads` or `builds` edge on `B` (enforced by Option 2's Landlock sandbox), editing `B` cannot alter `A`'s compiled test binary or runtime inputs.
- **Assumptions required for soundness:** Leaf packages must not reach back into parent packages via `exec.Command("go", "build", ...)` without a declared `builds` edge (the exact `keep_test.go` exposure shown in [§3.3](#33-anything-a-child-process-reads-and-raw-process-environments)).

### 4.5 Build-system replacement and dynamic test-selection tools

Migrating to a hermetic build system — Bazel, Buck2, Pants, or Nix — or adopting an off-the-shelf Go test-impact analyzer was evaluated against primary sources:

1. **Bazel (`rules_go` + Gazelle + `linux-sandbox`):**
   - *What it buys:* content-addressed action caching (`--disk_cache`) keyed by SHA-256 of declared `srcs` and `data`, built-in `shard_count` test sharding, and per-action sandboxing (`src/main/tools/linux-sandbox-pid1.cc`).
   - *What it costs and misses:* Bazel's default `linux-sandbox` bind-mounts the host root `/` read-only unless `--experimental_use_hermetic_linux_sandbox` (still experimental and default `false`) or explicit `--sandbox_block_path` flags are configured — meaning reads of `/workspace` outside the execroot are not blocked by default when user namespaces are unavailable. Furthermore, `rules_go` + Gazelle requires maintaining parallel `BUILD.bazel` files across all `14` `go.mod` modules (and manually curating `data = [...]` for every cross-module non-Go read — the identical declaration burden as `go-test-edges.json`), plus wrapping Python, `uvx vantage-check`, and `just` checks, and running a JVM server per worktree or contending on a shared server lock. Kubernetes removed Bazel in KEP-2420 specifically because maintaining dual `go.mod` and `BUILD.bazel` graphs caused persistent build-file drift.
2. **Buck2, Pants, and Nix:**
   - **Buck2** (`prelude` Go rules + `gobuckify`) has limited Go multi-module `replace`-directive support across `14` sibling local modules.
   - **Pants** (`pants.backend.experimental.go`) marks its Go backend experimental/beta, with documented multi-`go.mod` and `testdata` limitations.
   - **Nix** (`buildGoModule`) sandboxes derivations hermetically via the Nix daemon, yet caches at **whole-module** granularity (rebuilding and re-testing all of `tools/tillr-drain` on any one-line edit) and requires a privileged Nix daemon unavailable inside unprivileged lane containers.
3. **Published Go dynamic test-selection tools (`Datadog TIA`, `gorts`, `gotestimpact`, `testless`):**
   - Each records per-test statement or block coverage on prior runs and skips tests whose covered lines did not change. None observes child processes across `exec.Command` without custom instrumentation across all `14` binaries, none tracks reflection or `init()` registration side effects without conservative widening, and [`D-GR39`](../reference/gate.md#D-GR39) explicitly rejected coverage-map test selection.

**Why a build-system switch is offered as an alternative in [`OQ-GC2`](#OQ-GC2) rather than bundled into Options 1–4 (designer framing, overridable by the owner):** [`D-GR39`](../reference/gate.md#D-GR39) previously rejected adopting a build system, and a compact content-digest + Linux Landlock wrapper in `tools/tillr-gate` achieves stronger intra-repository read isolation on this container host (kernel Landlock ABI v11 works unprivileged without `unshare`) at zero `BUILD.bazel` maintenance cost across `14` modules.

---

## 5. Checking every option against gate rulings

[`docs/reference/gate.md`](../reference/gate.md) governs three areas touched by this design: **Go's cache and result classification**, **test selection and gate-code safety**, and **per-gate capacity and independence**. Every option is checked against those rulings below:

| Option | Go's cache & classification ([`D-GR8`](../reference/gate.md#D-GR8), [`D-GR21`](../reference/gate.md#D-GR21)–[`D-GR23`](../reference/gate.md#D-GR23), [`D-GR40`](../reference/gate.md#D-GR40), [`D-GR49`](../reference/gate.md#D-GR49)) | Test selection & gate fence ([`I3`](../reference/gate.md#I3), [`OQ-GR3`](../reference/gate.md#OQ-GR3), [`D-GR39`](../reference/gate.md#D-GR39), [`D-GR44`](../reference/gate.md#D-GR44), [`D-GR50`](../reference/gate.md#D-GR50)–[`D-GR54`](../reference/gate.md#D-GR54)) | Per-gate capacity & independence ([`D-GR2`](../reference/gate.md#D-GR2), [`D-GR15`](../reference/gate.md#D-GR15)–[`D-GR17`](../reference/gate.md#D-GR17), [`OQ-GTC1`](./gate-test-capacity.md#OQ-GTC1), [`D-GR30`](../reference/gate.md#D-GR30), [`D-GR43`](../reference/gate.md#D-GR43)) |
| :--- | :--- | :--- | :--- |
| **1. Content-keyed result store** | **Consistent** when failures are never reused ([`D-GR8`](../reference/gate.md#D-GR8)), pass+fail on one content key disables the unit and files repair ([`D-GR21`](../reference/gate.md#D-GR21)), classification flows through `tillr-gate classify` ([`D-GR23`](../reference/gate.md#D-GR23)), and new `tillr-gate` faces land before callers ([`D-GR32`](../reference/gate.md#D-GR32)). **Amends** `replayGoCheck`'s `-count=1` only if the owner picks Option A or C in [`OQ-GC3`](#OQ-GC3). | **Consistent:** keeps `go-affected` selection, `read_tests` ([`D-GR44`](../reference/gate.md#D-GR44)), and `build_tests` ([`D-GR51`](../reference/gate.md#D-GR51)). Because the verdict store is **gate code**, any change to its key computation or lookup lands pre-merge audited under [`I3`](../reference/gate.md#I3), [`OQ-GR3`](../reference/gate.md#OQ-GR3), and [`D-GR50`](../reference/gate.md#D-GR50). | **Consistent:** lock-free atomic rename writes under `.git/tillr-gate/` create zero cross-lane wait ([`D-GR2`](../reference/gate.md#D-GR2)); cached skips cut CPU demand under [`OQ-GTC1`](./gate-test-capacity.md#OQ-GTC1); `gate.timing.jsonl` ([`D-GR30`](../reference/gate.md#D-GR30)) records ran vs content-cached per package and shard. |
| **2. Enforced input boundary (Landlock)** | **Strengthens [`D-GR40`](../reference/gate.md#D-GR40) and [`D-GR49`](../reference/gate.md#D-GR49):** turns `go-test-edges.json` from an advisory hash input into a kernel-enforced read allowlist. Any `EACCES` classifies as a normal test failure (`fail`), never masked by retries ([`D-GR17`](../reference/gate.md#D-GR17)). | **Strengthens [`D-GR39`](../reference/gate.md#D-GR39), [`D-GR44`](../reference/gate.md#D-GR44), and [`D-GR51`](../reference/gate.md#D-GR51):** a narrowed `read_tests` or `build_tests` rule that omits a file actually opened by that test fails immediately in pre-merge `go-affected`. Launcher code in `tools/tillr-gate` obeys [`I3`](../reference/gate.md#I3) and [`D-GR32`](../reference/gate.md#D-GR32). | **Consistent:** Landlock setup is three in-process syscalls (`< 0.2 ms`, zero daemon or lock), ties lifetime to the shard subreaper ([`D-GR17`](../reference/gate.md#D-GR17)), and preserves `GOMAXPROCS=1` and CPU watchdog sampling ([`D-GR16`](../reference/gate.md#D-GR16)). |
| **3. Finer shards + per-shard checkpointing** | **Amends [`D-GR49`](../reference/gate.md#D-GR49) shard-count constants** (replacing fixed `-k 6` and `len // 125` with duration-targeted buckets plus single `go test -c` compile) while keeping `--cache` explicit and refusing `--cache` inside lane workers (`TILLR_RUN_ID` set). | **Consistent** with [`D-GR53`](../reference/gate.md#D-GR53) and [`D-GR54`](../reference/gate.md#D-GR54); also gives the owner the option in [`OQ-GC3`](#OQ-GC3) to revisit [`D-GR53`](../reference/gate.md#D-GR53) once unchanged `drain` shards skip in milliseconds. | **Consistent with [`OQ-GTC1`](./gate-test-capacity.md#OQ-GTC1), [`D-GR15`](../reference/gate.md#D-GR15), and [`D-GR16`](../reference/gate.md#D-GR16):** concurrent worker count stays capped (never spawning dozens of simultaneous binaries per gate), only sequential shard granularity shrinks; no shard timeout is added ([`D-GR15`](../reference/gate.md#D-GR15), [`D-GR17`](../reference/gate.md#D-GR17)). |
| **4. Carving leaf packages** | **Consistent:** smaller packages fall below `SHARD_THRESHOLD = 100` and skip compilation altogether on Go cache hits ([`D-GR49`](../reference/gate.md#D-GR49)) or skip execution on Option 1 content hits. | **Continues [`D-GR46`](../reference/gate.md#D-GR46) and [`D-GR47`](../reference/gate.md#D-GR47)** while respecting [`D-GR54`](../reference/gate.md#D-GR54)'s rejection of moving white-box `package drain` integration tests into a separate integration-only package. | **Consistent:** reduces `go-build` and `go-affected` compile and link CPU per lane ([`D-GR2`](../reference/gate.md#D-GR2), [`OQ-GTC1`](./gate-test-capacity.md#OQ-GTC1)). |
| **Alt: Build-system migration (Bazel)** | **Conflicts with [`D-GR39`](../reference/gate.md#D-GR39)** (which rejected adopting a build system) unless overturned by the owner in [`OQ-GC2`](#OQ-GC2); requires bridging Bazel exit codes into `tillr-gate classify` ([`D-GR23`](../reference/gate.md#D-GR23)) and [`D-GR21`](../reference/gate.md#D-GR21). | **Conflicts with single-declaration check simplicity ([`D-GR10`](../reference/gate.md#D-GR10))** unless Bazel wraps or replaces `tools/gate/checks/`; Bazel rule changes become gate-fenced ([`I3`](../reference/gate.md#I3)). | **Risks violating [`D-GR2`](../reference/gate.md#D-GR2)** if lanes share a local Bazel server JVM lock, or multiplies memory (`~1–2 GB` per server) if each lane runs its own Bazel server. |

---

## 6. Open questions

1. 💬 **OQ-GC1: What proof bar is required before skipping a test across gates, restarts, and worktrees?**

   Today, `14` Go test packages declare cross-module inputs in a tracked manifest, while `39` other test packages rely on Go's test cache noticing everything they read — an assumption already broken in packages that read repository root files or build sibling binaries in child processes ([§3.2](#32-files-outside-the-packages-module), [§3.3](#33-anything-a-child-process-reads-and-raw-process-environments)). A content-keyed store is only as sound as its input list ([§4.1](#41-option-1-content-keyed-test-result-store), [§4.2](#42-option-2-enforced-input-boundary-so-undeclared-reads-fail-loud)).

   Suppose a lane edits `.tillr.toml` or a helper command's `main.go`, and a test in another package reads that file or builds that binary via a child `go build` without listing the dependency. Under a declaration-only cache, the content key for the test package does not change, the gate skips the test from cache, and a broken change lands green. Under kernel-enforced read sandboxing, the undeclared file open or child `go build` receives `Permission denied` the first time the test runs, forcing the dependency into the manifest before any pass record can ever be written.

   - **A — Content-keyed store backed by kernel-enforced read sandboxing.** Close the existing undeclared cross-module and child-build reads in the tree first, then run every cached Go test under an unprivileged Linux Landlock ruleset allowing reads only from the package's own inputs, its declared cross-module reads and pre-built binaries, private scratch, and the system toolchain. Only runs that pass inside that enforced boundary write or reuse shared content-keyed verdicts.
   - **B — Content-keyed store backed by manifest declarations and static lints only.** Compute content keys from Go's import graph plus declared manifest edges and expand static source-scanning checks, without intercepting filesystem reads at runtime.
   - **C — Two-stage rollout: enforce the Landlock read boundary first, turn cross-worktree skipping on after a proving window.** Land the Landlock read boundary on trunk proofs and landing gates while keeping current cache behavior for one soak window, then enable shared content-keyed skipping once every existing Go suite passes cleanly inside the boundary.

   <!-- vantage: question id=OQ-GC1 leaning="A — content-keyed store backed by kernel-enforced Linux Landlock read sandboxing (fixing existing undeclared reads first, landing the Landlock wrapper with tests, and turning shared content-keyed skipping on once suites pass under enforcement), because B leaves child-process reads sound by discipline rather than proof." -->

   _Leaning:_ A (rolled out cleanly under [`D-GR32`](../reference/gate.md#D-GR32): fix the undeclared reads identified in [§3](#3-soundness-gaps-in-gos-test-cache-and-this-trees-exposure), land the Landlock wrapper with tests, and turn shared content-keyed skipping on once suites pass under enforcement). Option B leaves child-process reads sound by discipline rather than proof, falling short of `"if we know it's correct, we can be just as aggressive as possible."` Option C reaches the same end state as A if you want an explicit soak window before enabling skips.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-GC2: Should tillr build content-keyed caching and Landlock sandboxing natively or adopt a build system?**

   Both a native content cache with a Landlock launcher and a hermetic build system aim to hash declared inputs by content and block undeclared reads ([§4.1](#41-option-1-content-keyed-test-result-store), [§4.2](#42-option-2-enforced-input-boundary-so-undeclared-reads-fail-loud), [§4.5](#45-build-system-replacement-and-dynamic-test-selection-tools)).

   Suppose an agent lane adds a new Go package under one of the `14` modules and writes a test reading a fixture in a sibling directory. With native caching and Landlock, `go.mod` and standard `go test` remain the single source of truth; if the test crosses a module boundary, Landlock names the blocked path and points to the single edge manifest. With Bazel (`rules_go` + Gazelle), every Go file move, import change, or new package across all `14` modules also requires regenerating and committing `BUILD.bazel` files, configuring hermetic sandbox mounts on container hosts without user namespaces, and running per-worktree Bazel JVM servers alongside lanes.

   - **A — Build content-keyed test verdicts and Landlock sandboxing natively in the gate and Go test runners.** Keep `go.mod` and `go test` as the sole build graph across all `14` modules, store lock-free content-addressed shard and package verdicts under `.git/tillr-gate/`, and enforce read boundaries with a small Go Landlock wrapper.
   - **B — Migrate the repository's Go builds and tests to Bazel (`rules_go` + Gazelle).** Generate and maintain `BUILD.bazel` files across all `14` modules, configure hermetic sandbox mounts and a shared `--disk_cache`, and route gate Go checks through `bazel test`.
   - **C — Implement Go's `GOCACHEPROG` protocol only, without gate-level shard or package skipping.** Plug a custom cache binary into Go's `GOCACHEPROG` while leaving `cmd/go`'s own input-hashing logic (`mtime`, absolute worktree path, and `2 s` cutoff) in charge of test input keys.

   <!-- vantage: question id=OQ-GC2 leaning="A — build content-keyed verdicts and Landlock sandboxing natively on top of go.mod and go test, avoiding dual go.mod/BUILD.bazel drift and per-lane JVM overhead under B, and avoiding cmd/go's built-in absolute-path and mtime input hashing under C." -->

   _Leaning:_ A. Option C cannot fix cross-worktree misses or cross-module soundness because `cmd/go` computes `testInputsID` from absolute worktree paths and mtimes before calling `GOCACHEPROG`. Option B imposes continuous dual-build-file churn on every agent lane and per-lane JVM overhead without providing stronger read isolation on this Linux host than unprivileged Landlock ABI v11.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-GC3: Where should proved-unchanged verdicts be reused — including base replays and `tillr-drain/drain`?**

   Once a test unit's pass verdict is bound to an enforced content key ([`OQ-GC1`](#OQ-GC1)), four execution sites can consult that store: pre-merge landing gates, restarted runs, rolling trunk proofs, and base or red-proof culprit replays ([§2](#2-recorded-cache-hits-and-test-runs-today)). Today, named-test base replays force `-count=1` to avoid trusting Go's unsound cache, and `tillr-drain/drain` is deferred out of pre-merge `go-affected` ([`D-GR53`](../reference/gate.md#D-GR53), [`OQ-DFG1`](./fast-gate-tillr-drain-options.md#OQ-DFG1)).

   Suppose candidate lane 1 fails on a pre-existing broken or flaky test on its base commit, and candidate lane 2 (cut from the same base commit) hits the same check ten minutes later. If base replays consult and populate the content-keyed store — while keeping contradiction detection active so any test that both passes and fails on identical content is immediately disabled as flaky and filed for repair — unaffected tests skip immediately and a genuinely flaky test is caught on the contradiction rather than masked. Separately, while unchanged `tillr-drain/drain` shards will skip in milliseconds on unrelated landings, editing any production file inside `tools/tillr-drain/drain` still recompiles the `drain` test binary and invalidates all `drain` shards.

   - **A — Reuse across landing gates, restarts, trunk proofs, and base or culprit replays; keep `tillr-drain/drain` after-merge until shard hit rates are measured.** Allow every gate context (including base and culprit replays, dropping unconditional `-count=1` once Landlock enforcement and pass/fail contradiction disabling are active) to reuse proved-unchanged pass verdicts, while leaving `tillr-drain/drain` in the after-merge trunk proof until measured shard hit rates show what pre-merge cost remains on real `drain` edits.
   - **B — Reuse across landing gates, restarts, and trunk proofs, while keeping base and culprit replays uncached (`-count=1`).** Treat base acquittal and culprit bisection as fresh live-execution probes that never read cached pass verdicts, accepting repeated replay CPU when multiple lanes share an older base.
   - **C — Reuse everywhere in Option A, and also restore `tillr-drain/drain` to pre-merge `go-affected` as soon as content-keyed shard caching lands.** Return `drain` to the pre-merge gate immediately alongside content-keyed shard caching and Landlock enforcement, accepting pre-merge execution of invalidated `drain` shards whenever `drain` itself is edited.

   <!-- vantage: question id=OQ-GC3 leaning="A — reuse proved-unchanged pass verdicts across landing gates, restarts, trunk proofs, and base/culprit replays (backed by pass/fail contradiction disabling), while keeping tillr-drain/drain in the after-merge proof until measured shard hit rates confirm pre-merge latency." -->

   _Leaning:_ A. Under `"if we know it's correct, we can be just as aggressive as possible"`, skipping unchanged tests in base replays is sound once pass/fail contradiction disabling ([`D-GR21`](../reference/gate.md#D-GR21)) catches non-deterministic flips, whereas editing any file in `package drain` recompiles `drain.test` and invalidates its shards — so keeping `drain` after-merge (consistent with [`OQ-DFG1`](./fast-gate-tillr-drain-options.md#OQ-DFG1)'s leaning) avoids re-introducing 10–25 minute pre-merge gates on `drain` edits.

   **Answer:**

   > _(empty — fill in when decided)_

4. 💬 **OQ-GC4: How should cached verdicts treat `time.Now()` and wall-clock progression?**

   Landlock proves which files and binaries a test reads, yet `168` test files in `tools/` call `time.Now()`, including `8` files formatting or truncating calendar dates ([§3.4](#34-network-and-wall-clock-time)). Go's own test cache ignores wall-clock time completely and keeps cached passes indefinitely until inputs change.

   Suppose a test formats today's UTC date with `time.Now().UTC().Format("2006-01-02")` and compares it against an event written during the same test run: that test is date-invariant on every day of the year unless it hardcodes a calendar literal. Expiring every cached verdict after 24 hours forces untouched packages across the repository to re-run every morning even when zero bytes changed, while `LD_PRELOAD` fake-clock shims cannot intercept static `CGO_ENABLED=0` Go binaries that call the kernel clock directly.

   - **A — Treat wall-clock time as benign by default, backed by contradiction disabling.** Do not expire content-keyed verdicts on a wall-clock timer; if a date-sensitive test later fails on an input key that previously passed (or vice versa), contradiction detection disables it as flaky and files an item to inject an explicit clock.
   - **B — Expire content-keyed test verdicts after a fixed wall-clock window (such as 24 hours).** Re-run cached test units once per day when selected so calendar-dependent regressions surface within 24 hours, accepting daily cache-miss bursts on untouched packages.
   - **C — Include the UTC calendar date in the cache key for packages with uninjected `time.Now()` calendar calls.** Reuse cached verdicts freely across gates, replays, restarts, and worktrees throughout each UTC day, and cache across UTC midnight indefinitely for packages without calendar-formatting `time.Now()` calls.

   <!-- vantage: question id=OQ-GC4 leaning="A — do not expire content-keyed verdicts on a wall-clock timer, relying on pass/fail contradiction disabling to isolate any date-sensitive test that flips (or C if a UTC-midnight guard on the 8 calendar-formatting test files is preferred)." -->

   _Leaning:_ A (or C if you want an automatic UTC-midnight guard on the `8` calendar-formatting test files without expiring pure packages). Option B scales test work with elapsed time rather than repository changes, and static `CGO_ENABLED=0` Go binaries rule out transparent `LD_PRELOAD` clock virtualization.

   **Answer:**

   > _(empty — fill in when decided)_

5. 💬 **OQ-GC5: How aggressively should sharding and package carving be reshaped alongside content caching?**

   Even with a sound content-keyed cache ([`OQ-GC1`](#OQ-GC1)), two granularity bottlenecks remain ([§4.3](#43-option-3-finer-duration-balanced-shards-and-per-shard-result-checkpointing), [§4.4](#44-option-4-carving-leaf-packages-out-of-monolithic-suites)): the `tillr-drain` heavy suite packs `3,695.1 s` of test duration into `6` shards of `~10+ min` each (discarding in-flight work across `8` of `23` cancelled trunk proofs), and editing any one file in `tools/tillr-view` (`211` `.go` files) or `tools/tillr-drain/drain` (`836` `.go` files) changes the whole package's test binary and invalidates all of its shards.

   Suppose a trunk proof is three minutes into proving a commit when a new landing arrives on `docs/` or `tools/tillr-view` and cancels the proof in favor of the new tip. If `drain` was compiled once and run in `~20–30 s` duration-balanced shards that write each shard's content-keyed verdict as soon as it finishes, the restarted proof on the new tip immediately skips every `drain` shard completed during those three minutes instead of starting over. Meanwhile, carving loop-free helpers out of `tillr-view` and `drain` stops leaf edits from invalidating the parent binary, without rewriting `drain`'s `436` white-box loop integration files.

   - **A — Duration-targeted shards (`~20–30 s`) with single binary compile, immediate per-shard verdict checkpointing, and opportunistic leaf-package carving.** Compile each sharded package's test binary once per content key, split into `~20–30 s` duration-balanced shards (keeping concurrent worker count bounded by existing capacity rules), write each shard's content verdict immediately on completion so cancelled proofs and restarted gates resume from completed shards, and carve loop-free leaf subpackages out of `tillr-view` and `drain` where internal loop state is not needed.
   - **B — Keep current `6`-shard and `100`-test thresholds and refactor `tillr-drain/drain` internals into exported subpackages.** Leave shard sizing unchanged and export internal `drain` types and functions (`942` unexported declarations referenced by tests today) so `drain`'s integration suite splits into smaller Go packages.
   - **C — One subprocess invocation per top-level test function.** Compile each package binary once and execute every `Test*` function (`2,144` in `drain`) in its own fresh Landlock subprocess with an individual per-test verdict record.

   <!-- vantage: question id=OQ-GC5 leaning="A — compile each sharded package binary once, run ~20-30 s duration-targeted shards with immediate per-shard content-verdict checkpointing across cancellations and restarts, and carve loop-free leaf packages out of tillr-view and drain without exporting internal loop state." -->

   _Leaning:_ A. Option C pays `2,144` subprocess startups, temp-directory setups, and `TestMain` runs per `drain` sweep (multiplying cost in packages whose `TestMain` builds helper binaries once per process). Option B exports hundreds of internal loop invariants solely for tests ([`D-GR54`](../reference/gate.md#D-GR54)), whereas Option A delivers restart resumability immediately with zero API distortion.

   **Answer:**

   > _(empty — fill in when decided)_

---

## 7. What this design leaves untouched

Unless the owner directs otherwise in [§6](#6-open-questions), five parts of the system stay as built:

- **Lander squash and verdict binding (`tools/tillr-land`).** `gate.verdict` continues to bind `(base_sha, head_sha^{tree})` onto `landings` under [landing principles](./landing-principles.md).
- **Four-class kernel CPU and I/O scheduling ([`OQ-GTC1`](./gate-test-capacity.md#OQ-GTC1)) and `GOMAXPROCS=1`.** Content-keyed skipping reduces how many test binaries run; when a binary does run, its scheduling class, `GOMAXPROCS=1`, and `60 s` CPU-stall watchdog ([`D-GR16`](../reference/gate.md#D-GR16)) are unchanged.
- **Contradicted tests are disabled, never retried ([`D-GR21`](../reference/gate.md#D-GR21), [`D-GR22`](../reference/gate.md#D-GR22)).** A test unit that records both `pass` and `fail` under the same content key is disabled and files a repair item rather than re-running to fish for green.
- **Gate-code pre-merge fence ([`I3`](../reference/gate.md#I3), [`OQ-GR3`](../reference/gate.md#OQ-GR3), [`D-GR50`](../reference/gate.md#D-GR50)).** All content-key, Landlock-sandbox, and verdict-store code lives under the gate fence and never lands on deferred proof.
- **Non-Go tree checks ([`tools/tillr-gate/memory.go`](../../tools/tillr-gate/memory.go)).** Existing `.git/tillr-gate/results/` check-level pass memory continues to govern non-Go tree checks (`tillr-docs`, `pack-sync`, `documents`).
