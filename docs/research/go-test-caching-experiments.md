---
title: "Go test caching experiments: baseline gate hit rates and three prototypes across worktrees and restarts"
status: accepted
stage: CURRENT
tags:
  - tillr
  - gate
  - go-test
  - caching
  - research
summary: "Empirical record for the follow-on Go test caching design item: baseline Go test cache hit rates from 213 recorded gate.timing.jsonl files (0% on go-build, 19.51% on go-affected summaries), verification of -trimpath binary identity across worktrees, and measured results from three prototypes (content-addressed result records, Landlock input confinement, and duration-weighted fine shards) across repeated runs, second worktrees, restarted-partway runs, and wrong-skip tests."
vantage:
  status-chip: true
---

# Go test caching experiments: baseline gate hit rates and three prototypes across worktrees and restarts

This document records the empirical measurements requested on 2026-11-25 to evaluate ways of
skipping proved-unchanged Go test work across [landing gates](../reference/gate.md#the-landing-gate),
restarts, and separate worktrees. It is the data input for the follow-on design item (*Design: skip
test work whose result is proved unchanged, across gates, restarts and worktrees*). No gate
declaration, check configuration, or owner policy was changed in this work, and no owner design
decision is settled here.

All prototype code and its unit tests were recorded in ancestor commit `b9a403d7` (read with `git show b9a403d7:docs/research/go-test-caching-experiments/<file>.py`) and later retired once the production Go implementations landed:

- `docs/research/go-test-caching-experiments/baseline_timings.py` (`b9a403d7`) — scans recorded
  `gate.timing.jsonl` files and companion `go-affected` logs.
- `docs/research/go-test-caching-experiments/proto_content_cache.py` (`b9a403d7`) — Prototype 1
  (content-addressed result records keyed by compiled test binary, arguments, and declared inputs).
- `docs/research/go-test-caching-experiments/proto_sandbox_inputs.py` (`b9a403d7`) — Prototype 2
  (restricted filesystem view probing Landlock, `unshare`, `bwrap`, and `strace`).
- `docs/research/go-test-caching-experiments/proto_fine_shards.py` (`b9a403d7`) — Prototype 3
  (duration-weighted fine shards backed by per-shard content-addressed result records).
- `docs/research/go-test-caching-experiments/test_prototypes.py` (`b9a403d7`) — unit tests for the
  prototypes.

Experiments were run in `podman-self` using two byte-identical checkouts (`wt-a` and `wt-b`)
extracted at separate paths under a durable scratch directory (`<scratch>/exp-run/wt-a` and
`<scratch>/exp-run/wt-b`). The original runs did not record `uname -r` or `go version` directly;
on 2026-11-26 on this container, checking the environment prints:

```bash
uname -r; uname -m; go version
```

- `uname -r`: `7.3.5-zen1-1-zen`
- `uname -m`: `x86_64`
- `go version`: `go version go1.27.3 linux/amd64`

---

## 1. Baseline from recorded gate timings

To measure how often Go's built-in test cache currently answers selected packages or shards in
practice, we ran `baseline_timings.py` (`b9a403d7`)
read-only across `/workspace/.tillr/loop`:

```bash
python3 docs/research/go-test-caching-experiments/baseline_timings.py --root /workspace/.tillr/loop
```

- **Scope scanned:** 213 `gate.timing.jsonl` files, 287 timing records (0 malformed).
- **`go-build` test cache performance (from `gate.timing.jsonl`):**
  - Hits: **0 / 3,835 (0.00%)**
  - Misses: **3,835 (100.00%)**
  - Not applicable: **1,289**
- **`go-affected` cache performance (from companion `change-logs/*go-affected*.log` files):**
  - Individual package lines recorded in logs: **5,481** (0 / 5,481 direct cache hits, **0.00%**)
  - Summary lines across 163 runs: **363 / 1,861 selected packages cached (19.51%)**

Top packages by total recorded executions in `go-affected` companion logs
(`change-logs/*go-affected*.log`):

| Package | Cached | Total executions | Hit rate |
|---|---:|---:|---:|
| `github.com/mschulkind-oss/tillr-drain/drain` | 0 | 1,744 | 0.00% |
| `github.com/mschulkind-oss/tillr-watch` | 0 | 762 | 0.00% |
| `github.com/mschulkind-oss/tillr-view` | 0 | 462 | 0.00% |
| `github.com/mschulkind-oss/tillr-gate` | 0 | 270 | 0.00% |
| `github.com/mschulkind-oss/tillr-item` | 0 | 260 | 0.00% |
| `github.com/mschulkind-oss/tillr-land` | 0 | 254 | 0.00% |
| `github.com/mschulkind-oss/tillr-run/run` | 0 | 252 | 0.00% |
| `github.com/mschulkind-oss/tillr/store` | 0 | 187 | 0.00% |
| `github.com/mschulkind-oss/tillr/definition` | 0 | 137 | 0.00% |

In recorded gate runs, Go's built-in test cache never answered a primary package execution (0% hit
rate across 1,744 `drain` runs, 762 `watch` runs, and 254 `land` runs) because each lane or gate
checkout lives at a different filesystem path and runs with fresh scratch.

---

## 2. Verification of background claims

Before building the prototypes, we tested two background claims across the two byte-identical
worktrees `wt-a` and `wt-b`.

### 2.1 Test binary identity under `-trimpath`

We compiled test binaries with `go test -trimpath -c` in both `wt-a` and `wt-b` and compared their
SHA-256 digests:

```bash
WTA="<scratch>/exp-run/wt-a"
WTB="<scratch>/exp-run/wt-b"
SCRATCH="<scratch>/exp-run/scratch"

cd "$WTA/tools/tillr-drain" && go test -trimpath -c -o "$SCRATCH/spend_a.test" ./spend
cd "$WTB/tools/tillr-drain" && go test -trimpath -c -o "$SCRATCH/spend_b.test" ./spend
sha256sum "$SCRATCH/spend_a.test" "$SCRATCH/spend_b.test"

cd "$WTA/tools/tillr-item" && go test -trimpath -c -o "$SCRATCH/item_a.test" .
cd "$WTB/tools/tillr-item" && go test -trimpath -c -o "$SCRATCH/item_b.test" .
sha256sum "$SCRATCH/item_a.test" "$SCRATCH/item_b.test"

cd "$WTA/tools/tillr-drain" && go test -trimpath -c -o "$SCRATCH/drain_a.test" ./drain
cd "$WTB/tools/tillr-drain" && go test -trimpath -c -o "$SCRATCH/drain_b.test" ./drain
sha256sum "$SCRATCH/drain_a.test" "$SCRATCH/drain_b.test"
```

| Package | SHA-256 in `wt-a` | SHA-256 in `wt-b` | Identical? |
|---|---|---|---|
| `tools/tillr-drain/spend` | `0fdc03a0e65843f49e2013d9f9e09470aca950a7e2ae24456d5f4da6eb49e76a` | `0fdc03a0e65843f49e2013d9f9e09470aca950a7e2ae24456d5f4da6eb49e76a` | Yes |
| `tools/tillr-item` | `131b2e1f5b58915750534e98630e95465e3fb14c42ae4ddad12824b7abca8772` | `131b2e1f5b58915750534e98630e95465e3fb14c42ae4ddad12824b7abca8772` | Yes |
| `tools/tillr-drain/drain` | `662ac99670e169c5737c03ad9a807c45d6e8ca691f9401279b8fd9017ac9a832` | `662ac99670e169c5737c03ad9a807c45d6e8ca691f9401279b8fd9017ac9a832` | Yes |

The `-trimpath` claim holds: all three packages produced byte-identical test binaries across
separate worktree paths.

### 2.2 Go built-in test cache across worktrees

Using a shared `GOCACHE` directory across `wt-a` and `wt-b`, we tested a package with no `os` file
reads (`tools/tillr-drain/spend`) and a test that opens files inside its module
(`tools/tillr/watch`, `TestWatchDocumentedRules` reading `testdata/*.toml`):

```bash
GOCACHE_TEST="$SCRATCH/gocache_trimpath"
rm -rf "$GOCACHE_TEST" && mkdir -p "$GOCACHE_TEST"

# Case A: no module file reads
(cd "$WTA/tools/tillr-drain" && GOCACHE="$GOCACHE_TEST" go test -trimpath ./spend)
(cd "$WTA/tools/tillr-drain" && GOCACHE="$GOCACHE_TEST" go test -trimpath ./spend)
(cd "$WTB/tools/tillr-drain" && GOCACHE="$GOCACHE_TEST" go test -trimpath ./spend)

# Case B: opens files in module testdata/
GOCACHE_WATCH="$SCRATCH/gocache_watch_rules"
rm -rf "$GOCACHE_WATCH" && mkdir -p "$GOCACHE_WATCH"
(cd "$WTA/tools/tillr" && GOCACHE="$GOCACHE_WATCH" go test -trimpath ./watch -run "^TestWatchDocumentedRules$")
(cd "$WTA/tools/tillr" && GOCACHE="$GOCACHE_WATCH" go test -trimpath ./watch -run "^TestWatchDocumentedRules$")
(cd "$WTB/tools/tillr" && GOCACHE="$GOCACHE_WATCH" go test -trimpath ./watch -run "^TestWatchDocumentedRules$")
```

- **`tools/tillr-drain/spend`:** `wt-a` cold took `0.022s`; `wt-a` repeat reported `(cached)`;
  `wt-b` cross-worktree reported `(cached)` (cache hit across worktrees).
- **`tools/tillr/watch` (`TestWatchDocumentedRules`):** `wt-a` cold took `0.018s`; `wt-a` repeat
  reported `(cached)`; `wt-b` cross-worktree took `0.114s` (cache miss across worktrees).

This confirms the second background claim: Go's built-in cache keys opened files by absolute path,
size, and mtime, so any test opening a file in its module misses when run from another worktree.

---

## 3. Prototype 1: Content-addressed result record (`proto_content_cache.py`)

### What was built

`proto_content_cache.py` (`b9a403d7`) compiles each
package's test binary with `go test -trimpath -c`, computes a SHA-256 cache key from:

```text
SHA-256(test_binary_sha256 + normalized_test_args + declared_edge_digest + relevant_env)
```

where `declared_edge_digest` is computed from [`tools/gate/go-test-edges.json`](../../tools/gate/go-test-edges.json)
via `tools/gate/go_affected.py`, and `relevant_env` captures `GOOS`, `GOARCH`, and `CGO_ENABLED`.
Passing runs write `<key>.json` in the cache directory; subsequent runs with a matching key skip
executing the test binary.

### Measured results: `tools/tillr-drain/workflow` and `tools/tillr-land/land`

```bash
P1_CACHE="<scratch>/exp-run/p1-cache"
rm -rf "$P1_CACHE" && mkdir -p "$P1_CACHE"

# Cold (wt-a), Repeat (wt-a), Cross-worktree (wt-b)
python3 docs/research/go-test-caching-experiments/proto_content_cache.py \
    --repo-root "$WTA" --cache-dir "$P1_CACHE" --scratch-dir "$SCRATCH" \
    --pkg tools/tillr-drain/workflow --pkg tools/tillr-land/land --json

python3 docs/research/go-test-caching-experiments/proto_content_cache.py \
    --repo-root "$WTA" --cache-dir "$P1_CACHE" --scratch-dir "$SCRATCH" \
    --pkg tools/tillr-drain/workflow --pkg tools/tillr-land/land --json

python3 docs/research/go-test-caching-experiments/proto_content_cache.py \
    --repo-root "$WTB" --cache-dir "$P1_CACHE" --scratch-dir "$SCRATCH" \
    --pkg tools/tillr-drain/workflow --pkg tools/tillr-land/land --json
```

| Stage | Hits / Total | Wall time | Compile time (`workflow` / `land`) | Test run time (`workflow` / `land`) |
|---|---:|---:|---:|---:|
| Cold (`wt-a`) | 0 / 2 | 7,678 ms | 1,555 ms / 911 ms | 440 ms / 4,148 ms |
| Repeat (`wt-a`) | 2 / 2 | 1,958 ms | 713 ms / 823 ms | 0 ms / 0 ms |
| Second worktree (`wt-b`) | 2 / 2 | 3,271 ms | 1,936 ms / 826 ms | 0 ms / 0 ms |

- **Time saved on repeated run:** 5,720 ms (74.5% wall-clock reduction; 100% of test execution
  eliminated).
- **Time saved on second worktree (`wt-b`):** 4,407 ms (57.4% wall-clock reduction; 100% of test
  execution eliminated).
- **Matched cache keys across worktrees:** `workflow` (`6068960f7afa80f7a30207f7329ab297223796264e481f317234cda42ea9672f`)
  and `land` (`901aed51cbc173bd61f31879b3a112332ec58a6bc6d0fd960db720320a072647`).

### Measured results: `tools/tillr-drain/drain` (`^Test(Fixer|Obstacle)`, 15 tests)

```bash
python3 docs/research/go-test-caching-experiments/proto_content_cache.py \
    --repo-root "$WTA" --cache-dir "$P1_CACHE" --scratch-dir "$SCRATCH" \
    --pkg tools/tillr-drain/drain --run '^Test(Fixer|Obstacle)' --json
# Repeated in $WTA and cross-worktree in $WTB with identical flags
```

| Stage | Hits / Total | Wall time | Compile time | Test run time |
|---|---:|---:|---:|---:|
| Cold (`wt-a`) | 0 / 1 | 52,140 ms | 2,333 ms | 46,638 ms |
| Repeat (`wt-a`) | 1 / 1 | 2,007 ms | 1,250 ms | 0 ms |
| Second worktree (`wt-b`) | 1 / 1 | 2,105 ms | 1,249 ms | 0 ms |

- **Time saved on repeated run:** 50,133 ms (96.1% reduction).
- **Time saved on second worktree (`wt-b`):** 50,035 ms (96.0% reduction).
- **Cache key in all three runs:** `6bb6fd9539ea1adac5169f4b51b2eee90f18f0ebcee69d7270396582e01498d5`.

### Restarted-partway measurement

```bash
P1_RESTART_CACHE="<scratch>/exp-run/p1-restart-cache"
rm -rf "$P1_RESTART_CACHE" && mkdir -p "$P1_RESTART_CACHE"

python3 docs/research/go-test-caching-experiments/proto_content_cache.py \
    --repo-root "$WTA" --cache-dir "$P1_RESTART_CACHE" --scratch-dir "$SCRATCH" \
    --pkg tools/tillr-drain/workflow --pkg tools/tillr-land/land \
    --stop-after 1 --json

python3 docs/research/go-test-caching-experiments/proto_content_cache.py \
    --repo-root "$WTA" --cache-dir "$P1_RESTART_CACHE" --scratch-dir "$SCRATCH" \
    --pkg tools/tillr-drain/workflow --pkg tools/tillr-land/land --json
```

- **Partway run (`--stop-after 1`):** 788 ms wall time; `workflow` compiled (561 ms), executed
  (75 ms), and recorded; `land` was not started.
- **Resumed run:** 3,358 ms wall time; `workflow` hit the cache (compiled 530 ms, run 0 ms), and
  `land` missed (compiled 566 ms, executed 1,986 ms). **0 completed packages were repeated.**

### Wrong-skip test and limitations

1. **Synthetic input mutation (`--test-wrong-skip`):**
   `python3 docs/research/go-test-caching-experiments/proto_content_cache.py --cache-dir "$P1_CACHE" --scratch-dir "$SCRATCH" --test-wrong-skip --json`
   returned `cold_hit: false`, `repeat_hit: true`, `mutated_hit: false`, `mutated_exit_code: 1`,
   `key_changed: true`.
2. **Real package source mutation (`tools/tillr-drain/workflow` in a scratch worktree):**

   ```bash
   MUT_WT="<scratch>/exp-run/wt-mut"
   # After appending '// mutation comment', then 'var MutatedVariable = 42', to a .go file in $MUT_WT/tools/tillr-drain/workflow:
   python3 docs/research/go-test-caching-experiments/proto_content_cache.py \
       --repo-root "$MUT_WT" --cache-dir "$P1_CACHE" --scratch-dir "$SCRATCH" \
       --pkg tools/tillr-drain/workflow --json
   ```

   - Adding a non-functional comment (`// mutation comment`) left the compiled binary SHA-256
     unchanged (`0b9edeef...`) and produced a safe cache hit.
   - Adding `var MutatedVariable = 42` changed the compiled binary SHA-256 from
     `0b9edeefcf9548b6...` to `1762033898d54f6e...`, changed the cache key from
     `6068960f7afa80f7...` to `4b6c764c3a485083...`, and forced a rerun (`cache_hit: false`, 82 ms).

**What could not be avoided or guaranteed here:**

- **Compile cost remains on cache hits:** Because the key depends on the compiled test binary's
  SHA-256, every cache hit still pays `go test -trimpath -c` (713 ms to 1,936 ms per package).
- **No runtime enforcement of undeclared inputs:** Prototype 1 trusts
  [`tools/gate/go-test-edges.json`](../../tools/gate/go-test-edges.json); if a test or child
  process reads an undeclared file outside the module, the cache key does not change.

---

## 4. Prototype 2: Restricted filesystem view (`proto_sandbox_inputs.py`)

### What was built and container mechanism probe

`proto_sandbox_inputs.py` (`b9a403d7`) probes which
filesystem-restriction or syscall-tracing mechanisms work inside this container (`podman-self`) and
applies a Landlock Linux Security Module (LSM) allowlist permitting only system runtime paths, the
Go toolchain/cache, scratch `TMPDIR`, the package's module directory, and declared inputs from
[`tools/gate/go-test-edges.json`](../../tools/gate/go-test-edges.json).

```bash
python3 docs/research/go-test-caching-experiments/proto_sandbox_inputs.py --probe
```

| Mechanism | Available? | Exact probe result / refusal |
|---|---|---|
| Landlock LSM (`SYS_landlock_create_ruleset = 444`, `SYS_landlock_restrict_self = 446`) | **Yes** | ABI version 11 (`errno: 0`); unprivileged restriction with `PR_SET_NO_NEW_PRIVS` succeeds. |
| `unshare -rm` | **No** | `unshare not found in PATH` |
| `bwrap` (Bubblewrap) | **No** | `bwrap not found in PATH` |
| `strace` | **Yes** | `/bin/strace` exits 0 on `strace -f -e trace=file true`. |

### Wrong-skip / undeclared-read test

```bash
python3 docs/research/go-test-caching-experiments/proto_sandbox_inputs.py --test-undeclared-read
```

Result: `declared_exit: 0`, `undeclared_exit: 1`, `undeclared_eacces: true`
(`open ../../undeclared/secret.txt: permission denied`). Landlock blocks undeclared reads with
`EACCES` rather than letting a test pass on untracked inputs.

### Real package tests under Landlock restriction

We ran six real repository packages under Landlock with only declared edges and system runtime
paths allowlisted:

```bash
for PKG in tools/tillr-drain/workflow tools/tillr-land/land tools/tillr-item tools/tillr-store tools/tillr-gate tools/tillr-drain/drain; do
    python3 docs/research/go-test-caching-experiments/proto_sandbox_inputs.py \
        --repo-root "$WTA" --scratch-dir "$SCRATCH" --pkg "$PKG" --json
done
```

| Package | Passed? | Exit code | `EACCES`? | Why it failed under restriction |
|---|---|---:|---|---|
| `tools/tillr-drain/workflow` | No | 1 | Yes | `TestWorkflowWatchCheckWorkflows` runs `git init -q`; Git fails reading `/home/agent/.gitconfig` and `/home/agent/.config/git/config` (`Permission denied`). |
| `tools/tillr-land/land` | No | 1 | Yes | `TestSquashOnAConflict...` runs `git add`; Git fails reading `/home/agent/.config/git/ignore` (`Permission denied`). |
| `tools/tillr-item` | No | 1 | Yes | Test harness invokes `mise`, which traverses up to `/workspace/mise.toml` and `~/.config/mise/config.toml` (`Permission denied (os error 13)`). |
| `tools/tillr-store` | No | 1 | Yes | Same `mise` failure reading `/workspace/mise.toml` and `~/.config/mise/config.toml`. |
| `tools/tillr-gate` | No | 1 | Yes | Same `mise` failure reading `/workspace/mise.toml` and `~/.config/mise/config.toml`. |
| `tools/tillr-drain/drain` | No | 1 | Yes | Subprocess `git` commands fail initializing test repositories (`unable to access '/home/agent/.config/git/ignore': Permission denied`). |

### What could not work here and why

- **No repeated, second-worktree, or restarted-partway timings for real packages:** Every real
  package suite tested failed with `EACCES` because tests invoke `git` and `mise` subprocesses that
  read ambient configuration outside the module (`/home/agent/.gitconfig`,
  `/home/agent/.config/git/ignore`, `/workspace/mise.toml`, `~/.config/mise/config.toml`).
- **Not a standalone cache:** Landlock enforces access control at runtime; it does not store or
  replay test results. Even if the allowlist were widened to ambient tool configs (or those tools
  were isolated with `GIT_CONFIG_GLOBAL=/dev/null` and similar), skipping unchanged tests would
  still require a result store such as Prototype 1.

---

## 5. Prototype 3: Finer shards (`proto_fine_shards.py`)

### What was built

`proto_fine_shards.py` (`b9a403d7`) discovers top-level
`Test*` functions in a package, partitions them into configurable shard counts via Longest
Processing Time (LPT) bin-packing weighted by [`tools/go-test-durations.json`](../../tools/go-test-durations.json),
and runs uncached shards with bounded concurrency (`workers = 4`). Each shard writes its own
content-addressed result record keyed as in Prototype 1, using `-test.run=<shard_pattern>` and
per-shard active tests for the edge digest.

### Wrong-skip test (shard-scoped invalidation)

```bash
python3 docs/research/go-test-caching-experiments/proto_fine_shards.py \
    --cache-dir "$P3_CACHE" --scratch-dir "$SCRATCH" --test-wrong-skip --json
```

Result: `cold_misses: 2`, `mutated_hits: 1`, `mutated_misses: 1`, `mutated_failed_shards: 1`.
Mutating an input read by one shard invalidated only that shard (1 miss, 1 failure) while the
unrelated shard remained a cache hit.

### Measured results: `tools/tillr-land/land` (16 tests, 4 workers)

```bash
for SHARDS_STOP in "2:1" "4:2" "8:4"; do
    S="${SHARDS_STOP%%:*}"
    STOP="${SHARDS_STOP##*:}"
    CACHE="<scratch>/exp-p3/cache-land-$S"
    RCACHE="<scratch>/exp-p3/rcache-land-$S"

    # Cold (wt-a), Repeat (wt-a), Second worktree (wt-b)
    python3 docs/research/go-test-caching-experiments/proto_fine_shards.py \
        --repo-root "$WTA" --cache-dir "$CACHE" --scratch-dir "$SCRATCH" \
        --pkg tools/tillr-land/land --shard-count "$S" --workers 4
    python3 docs/research/go-test-caching-experiments/proto_fine_shards.py \
        --repo-root "$WTA" --cache-dir "$CACHE" --scratch-dir "$SCRATCH" \
        --pkg tools/tillr-land/land --shard-count "$S" --workers 4
    python3 docs/research/go-test-caching-experiments/proto_fine_shards.py \
        --repo-root "$WTB" --cache-dir "$CACHE" --scratch-dir "$SCRATCH" \
        --pkg tools/tillr-land/land --shard-count "$S" --workers 4

    # Partway (50% shards) and Resumed
    python3 docs/research/go-test-caching-experiments/proto_fine_shards.py \
        --repo-root "$WTA" --cache-dir "$RCACHE" --scratch-dir "$SCRATCH" \
        --pkg tools/tillr-land/land --shard-count "$S" --stop-after-shards "$STOP" --workers 4
    python3 docs/research/go-test-caching-experiments/proto_fine_shards.py \
        --repo-root "$WTA" --cache-dir "$RCACHE" --scratch-dir "$SCRATCH" \
        --pkg tools/tillr-land/land --shard-count "$S" --workers 4
done
```

| Shard size | Shards | Cold (`wt-a`) | Repeat (`wt-a`) | Second WT (`wt-b`) | Partway run (50% shards) | Resumed run | Tests repeated on restart |
|---|---:|---:|---:|---:|---:|---:|---:|
| Coarse | 2 | 6,254 ms (2 misses) | 2,339 ms (2 hits) | 3,667 ms (2 hits) | 6,074 ms (1 shard, 6 tests) | 3,838 ms (1 hit, 1 miss, 10 tests) | **0** |
| Medium | 4 | 5,405 ms (4 misses) | 2,431 ms (4 hits) | 2,544 ms (4 hits) | 4,599 ms (2 shards, 6 tests) | 3,736 ms (2 hits, 2 misses, 10 tests) | **0** |
| Fine | 8 | 4,674 ms (8 misses) | 2,451 ms (8 hits) | 2,040 ms (8 hits) | 2,847 ms (4 shards, 8 tests) | 4,794 ms (4 hits, 4 misses, 8 tests) | **0** |

### Measured results: `tools/tillr-drain/drain` (60 tests, 4 workers)

We ran the first 60 discovered tests of `tools/tillr-drain/drain` (`--max-tests 60`) across
coarse (3 shards), medium (12 shards), and fine (30 shards) configurations:

```bash
for SHARDS_STOP in "3:1" "12:6" "30:15"; do
    S="${SHARDS_STOP%%:*}"
    STOP="${SHARDS_STOP##*:}"
    CACHE="<scratch>/exp-p3/cache-drain-$S"
    RCACHE="<scratch>/exp-p3/rcache-drain-$S"

    # Cold (wt-a), Repeat (wt-a), Second worktree (wt-b)
    python3 docs/research/go-test-caching-experiments/proto_fine_shards.py \
        --repo-root "$WTA" --cache-dir "$CACHE" --scratch-dir "$SCRATCH" \
        --pkg tools/tillr-drain/drain --max-tests 60 --shard-count "$S" --workers 4
    python3 docs/research/go-test-caching-experiments/proto_fine_shards.py \
        --repo-root "$WTA" --cache-dir "$CACHE" --scratch-dir "$SCRATCH" \
        --pkg tools/tillr-drain/drain --max-tests 60 --shard-count "$S" --workers 4
    python3 docs/research/go-test-caching-experiments/proto_fine_shards.py \
        --repo-root "$WTB" --cache-dir "$CACHE" --scratch-dir "$SCRATCH" \
        --pkg tools/tillr-drain/drain --max-tests 60 --shard-count "$S" --workers 4

    # Partway (~50% shards) and Resumed
    python3 docs/research/go-test-caching-experiments/proto_fine_shards.py \
        --repo-root "$WTA" --cache-dir "$RCACHE" --scratch-dir "$SCRATCH" \
        --pkg tools/tillr-drain/drain --max-tests 60 --shard-count "$S" --stop-after-shards "$STOP" --workers 4
    python3 docs/research/go-test-caching-experiments/proto_fine_shards.py \
        --repo-root "$WTA" --cache-dir "$RCACHE" --scratch-dir "$SCRATCH" \
        --pkg tools/tillr-drain/drain --max-tests 60 --shard-count "$S" --workers 4
done
```

| Shard size | Shards | Tests / shard | Cold (`wt-a`) | Repeat (`wt-a`) | Second WT (`wt-b`) | Partway (~50% stop) | Resumed run | Shards / tests repeated |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Coarse | 3 | ~20 | 51,058 ms (3 misses) | 5,352 ms (3 hits) | 18,423 ms (3 hits) | 13,237 ms (1/3 shards, 14 tests) | 15,168 ms (1 hit, 2 misses, 46 tests) | **0 / 0** |
| Medium | 12 | ~5 | 14,343 ms (12 misses) | 6,693 ms (12 hits) | 6,746 ms (12 hits) | 10,378 ms (6/12 shards, 27 tests) | 9,335 ms (6 hits, 6 misses, 33 tests) | **0 / 0** |
| Fine | 30 | 2 | 21,903 ms (30 misses) | 13,165 ms (30 hits) | 13,393 ms (30 hits) | 12,343 ms (15/30 shards, 15 tests) | 15,579 ms (15 hits, 15 misses, 45 tests) | **0 / 0** |

At every shard size, **zero completed shards were repeated** on restart:

- **Coarse (3 shards):** High cold variance (51,058 ms) because heavy tests pack into fewer
  shards, and stopping after 1 shard leaves 2 large shards (46 tests, 76.7% of the selection)
  uncached on resume.
- **Medium (12 shards):** Balances 4-worker parallelism and restart granularity (cold 14,343 ms;
  repeat 6,693 ms; `wt-b` 6,746 ms; stopping at 6/12 shards leaves 33 tests to run in 9,335 ms).
- **Fine (30 shards):** Checkpoints every 2 tests (3.3% of the selection), but incurs higher
  per-shard subprocess and edge-digest overhead (13,165 ms on repeat; 13,393 ms on `wt-b`).

---

## 6. Comparison and open questions for the design item

| Dimension | Go built-in cache (baseline) | Prototype 1 (content cache) | Prototype 2 (Landlock sandbox) | Prototype 3 (fine shards) |
|---|---|---|---|---|
| Repeated run (`wt-a`) | 0% in gate logs; hits simple packages | 100% hits (`workflow`, `land`, `drain` slice) | N/A (access control only) | 100% hits across all shards |
| Second worktree (`wt-b`) | Misses when opening module files (`testdata/`) | 100% hits | N/A | 100% hits across all shards |
| Restarted partway | Reruns uncompleted packages | Skips completed packages | N/A | 0 completed shards repeated |
| Enforces input hygiene | No | Relies on `go-test-edges.json` | Yes (`EACCES` on undeclared reads) | Relies on `go-test-edges.json` |
| Real repo compatibility | Passes, low gate hit rate | Passes cleanly | Fails on ambient `git` / `mise` configs | Passes cleanly |
| Wrong-skip behavior | Invalidates on source/mtime | Invalidates on binary/input change; comments hit | Blocks undeclared reads immediately | Invalidates only affected shard |

Open questions left for the owner in the follow-on design item (not settled here):

1. **Where result records live and how they are shared:** per-machine cache under durable scratch
   versus a shared store across gates and trunk proofs, and retention/eviction rules.
2. **Whether to pair Prototype 1 or 3 with input-hygiene enforcement:** whether to isolate `git` and
   `mise` ambient configs so Landlock can run as a hygiene check, or rely on
   [`tools/gate/go-test-edges.json`](../../tools/gate/go-test-edges.json) alone.
3. **Target shard granularity:** choosing between package-level records (lowest overhead), medium
   shards (~5–10 tests/shard, balanced parallelism and restart recovery), or fine shards (2
   tests/shard).
4. **Whether to cache before or after compilation:** accepting `go test -trimpath -c` cost per hit
   versus keying on source + dependency digests before invoking the compiler.
