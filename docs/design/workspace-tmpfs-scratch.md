---
title: "RAM-backed /tmp for tillr's workspace, with /tmp use measured and shrunk to fit"
status: in-review
stage: DESIGN
updated: 2026-11-25
tags:
  - tillr
  - workspace
  - tmpfs
  - gate
  - drain
  - watch
  - design
summary: "Moving ephemeral storage to tmpfs in tillr's separate repository stops /tmp churn from hitting btrfs, provided concurrent /tmp occupancy and leaks stay small enough for host RAM. Measuring all 15 candidate-gate checks and all 28 trunk-proof checks in an isolated mount namespace shows a 1,142,629,379-byte serial proof peak (go-test-tillr-drain), a 137,276,802-byte cold-gate peak (go-build), and 13,307,888 bytes across 2,117 files leaked per clean proof pass (plus 1,034,861,473 bytes across 44,151 entries accumulated on the live ten-lane drain). Per-execution /tmp directories with lifeline cleanup, single-binary package compilation in tillr-gate shards, NODE_DISABLE_COMPILE_CACHE=1, and notify lockfile cleanup cut ten-lane /tmp bursts below 1.3 GiB with zero steady-state leaks, within a 2 GiB (2,147,483,648-byte) ceiling watched on drain.tmp-usage. Nothing is built."
next: "Rule OQ-WT1 (the 2 GiB ten-lane /tmp ceiling and whether tmpfs also carries a hard mount cap) and OQ-WT2 (whether ephemeral check and suite scratch under the workspace's durable root moves to RAM-backed /tmp or stays on disk), and review D-WT1 through D-WT7 in the Decision Ledger. Slices 1 through 4 of Section 6 wait on neither question."
depends-on:
  - gate-test-capacity.md
  - tillr-utility-contracts.md
  - tillr-watch.md
  - landing-principles.md
  - ../reference/authority-not-approval.md
vantage:
  status-chip: true
---

# RAM-backed /tmp for tillr's workspace, with /tmp use measured and shrunk to fit

**Status:** Nothing is built: `rg -n 'drain\.tmp-usage|NODE_DISABLE_COMPILE_CACHE|TILLR_NOTIFY_DIR' tools config` returns nothing, and the enclosing workspace's `yolo-jail.toml` sets no `ephemeral_storage` key (`git check-ignore -q yolo-jail.toml` holds in this checkout, and `/workspace/yolo-jail.toml` omits `ephemeral_storage`). `R-WT1` and `R-WT2` record dated verbatim statements by the owner ([§1.1](#11-standing-owner-rulings)); `D-WT1` through `D-WT7` are the designer's decisions and await the owner's review in the [Decision Ledger](#decision-ledger); [`OQ-WT1`](#OQ-WT1) and [`OQ-WT2`](#OQ-WT2) await the owner's ruling. The companion move of tillr into its own repository and workspace has no design document under `docs/` yet (`fd 'repo-move|separate-repo|own-repo' docs` returns nothing).

> **In short.** Before flipping `ephemeral_storage = "tmpfs"` in tillr's separate repository, tillr bounds every check and run to a short private `/tmp` subdirectory reaped on exit or `SIGKILL`, compiles one stripped test binary per multi-shard Go package instead of one per shard, disables Node's unbounded `/tmp/node-compile-cache` in ephemeral runs, fixes `notify.Serve`'s `.sock.lock` leak, caps accepted ten-lane `/tmp` occupancy at **2 GiB (`2,147,483,648` bytes)**, and alerts on `drain.tmp-usage` whenever `tmp_used_bytes` exceeds that ceiling.

**Why it matters.** On 2026-11-24 heavy file churn across `/tmp` and workspace scratch degraded the host's btrfs filesystem (`R-WT1`, `R-WT2`). On 2026-11-25 the live ten-lane workspace held `1,034,861,473` bytes across `44,151` entries in `/tmp` (`670.30 MB` of orphaned `/tmp/go-build*` trees, `318.88 MB` across `4,496` entries in `/tmp/node-compile-cache`, and `36,656` leaked `.sock.lock` files in `/tmp/tillr-0`), while a single clean trunk proof peaked at `1,142,629,379` bytes and leaked `13,307,888` bytes across `2,117` files ([§2](#2-measured-tmp-use-across-one-gate-and-one-trunk-proof)). Mounting `/tmp` on `tmpfs` without fixing those leaks and duplicate shard links would trade btrfs metadata churn for unbounded RAM growth.

**The shape.** Five parts, matching the five questions of the request:
- **Measured `/tmp` writers** ([§2](#2-measured-tmp-use-across-one-gate-and-one-trunk-proof)): exact mount-namespace measurement command and complete peak and residual numbers across all 15 candidate-gate checks and all 28 trunk-proof checks.
- **Disposition per writer** ([§3](#3-what-happens-to-each-tmp-writer)): short-lived build and test files stay in RAM-backed `/tmp` rather than moving back onto workspace btrfs, but shrink by ~5x and leave `0 B` behind (`D-WT1`–`D-WT3`); Node's compile cache is disabled in ephemeral runs (`D-WT4`); daemon sockets prefer `$XDG_RUNTIME_DIR/tillr` and stop leaking `.sock.lock` (`D-WT5`).
- **Ten-lane `/tmp` ceiling and alert fact** ([§4](#4-accepted-ten-lane-peak-and-the-recorded-alert-fact)): **`2,147,483,648` bytes (`2 GiB`)** at ten lanes (`D-WT7`, [`OQ-WT1`](#OQ-WT1)), sampled once per minute into `drain.tmp-usage` and watched by `drain-tmp-usage-high.toml` on `event.tmp_used_bytes > 2147483648`.
- **Workspace-backed scratch** ([§5](#5-what-happens-to-scratch-kept-under-the-workspace)): durable state, worktrees, run transcripts, remembered gate verdicts, and content-addressed binaries stay on disk (`D-WT6`), while [`OQ-WT2`](#OQ-WT2) asks the owner whether ephemeral check and suite scratch under the workspace's durable root moves to `tmpfs` or stays on disk.
- **First slice as one behavior** ([§6](#6-the-first-slice-and-the-build-order)): `tillr-gate` gives each check execution its own `/tmp/tc-<pid>-<seq>` (`TMPDIR` + `GOTMPDIR`) with `NODE_DISABLE_COMPILE_CACHE=1` and removes it unconditionally when the check ends.

**Cost.** Zero model calls; one `statfs` plus a shallow `/tmp` scan once per minute in `tillr-drain`, and fewer linker invocations per sharded Go suite because multi-shard packages link one test binary instead of $K$.

**Start at** [§2](#2-measured-tmp-use-across-one-gate-and-one-trunk-proof), then [§3](#3-what-happens-to-each-tmp-writer), [§4](#4-accepted-ten-lane-peak-and-the-recorded-alert-fact), [§5](#5-what-happens-to-scratch-kept-under-the-workspace), and [§6](#6-the-first-slice-and-the-build-order).

**Needs your ruling:** [`OQ-WT1`](#OQ-WT1) (confirming or adjusting the `2 GiB` ten-lane `/tmp` ceiling and whether `tmpfs` also gets a hard mount size limit) and [`OQ-WT2`](#OQ-WT2) (whether ephemeral test and gate scratch currently written under the workspace's durable root moves to RAM-backed `/tmp` or stays on disk).

**Awaits your review:** the designer's decisions `D-WT1` through `D-WT7` in the [Decision Ledger](#decision-ledger). You may overturn any of them.

**Reads with:** [gate test capacity](./gate-test-capacity.md) (whose process-concurrency and memory-pressure rulings bound simultaneous gate and proof runs on the 125 GiB host), [utility contracts](./tillr-utility-contracts.md) (whose `D-UC18` ports `go-test-shards.py` into `tillr-gate shards`), [tillr-watch](./tillr-watch.md) (whose standing rule keeps watch alerts observational), [landing principles](./landing-principles.md), and [authority, not approval](../reference/authority-not-approval.md).

**Words.** Lane, candidate, gate, and trunk proof keep their meanings in [the vocabulary](./tillr-vocabulary.md#5-the-words). Two measurement terms are used with exact meanings here:

- **Peak bytes** *(measured here)*: the maximum sum of apparent file sizes (`st_size` across all non-directory entries under `/tmp`) observed on a 25 ms sampling cadence during a check's execution or immediately upon process exit.
- **Residual bytes** *(measured here)*: non-directory bytes still present in the check's private `/tmp` after the check process and its waited children exit.

---

## 1. Standing rulings, jail mechanics, and host baseline

### 1.1 Standing owner rulings

Two verbatim statements by the owner on 2026-11-24 set the goal and boundary of this design:

- **`R-WT1` (2026-11-24, owner):** *"we're chrning a ton of files that is causing btrfs issues, and we can eithe rget our own subvlume, or fix this churn, or both."*
- **`R-WT2` (2026-11-24, owner):** *"when we move to the separate repo, you can set ephemeral storage to tmpfs so we don't destroy btrfs with our /tmp usage and if we shrink /tmp usage enough we won't destroy ram either."*

In addition, the owner's 2026-11-25 rulings in [gate test capacity](./gate-test-capacity.md) ([`OQ-GTC1`](./gate-test-capacity.md#OQ-GTC1), [`OQ-GTC2`](./gate-test-capacity.md#OQ-GTC2), and [`OQ-GTC3`](./gate-test-capacity.md#OQ-GTC3), recorded in commit `1f8eafdd`) keep per-gate shard ceilings without cross-run slot locks, leave memory enforcement to kernel and container limits without swap polling, and weight CPU share across four process classes (`repair > gate > proof > worker`). Six Go test suites also carry a founding comment attributing to the owner the rule that suite scratch lives under the workspace's durable root rather than `/tmp` so failure evidence survives `/tmp` cleanup (`main_test.go` in `tillr-drain`, `tillr-land`, `tillr-item`, and `tillr-run`; `plan_test.go` in `tillr-gate`; `conformance_test.go` in `tillr`). Under [the provenance review](../tillr/decision-provenance-review.md#decision-ledger), only the owner may decide how `R-WT1`–`R-WT2` compose with that earlier suite-scratch rule ([`OQ-WT2`](#OQ-WT2)).

### 1.2 Jail ephemeral storage and live 2026-11-25 host baseline

Running `yolo config-ref` inside the environment documents `ephemeral_storage`:

- `"volume"` (the default when omitted, as in `/workspace/yolo-jail.toml` today): backs `/tmp`, `/var/tmp`, and `/var/lib/containers` with a persistent host directory on the root btrfs filesystem (`/dev/mapper/root`). Every temporary compiler object, test binary, SQLite write-ahead log, and socket lockfile hits btrfs metadata and extent trees.
- `"tmpfs"`: mounts `/tmp`, `/var/tmp`, and `/var/lib/containers` as RAM-backed `tmpfs` inside the jail; `/workspace` remains on the host filesystem. As `yolo config-ref` notes, `tmpfs` eliminates disk I/O and btrfs metadata churn for those paths, counts against host free memory, and is wiped on jail restart.

Measured on the 32-core, 125 GiB RAM host (`go1.27.3 linux/amd64`, `node v24.19.0`) on 2026-11-25 while ten worker lanes (`lane-1`..`lane-10`), `express-1`..`express-2`, `tillr-drain loop`, `tillr-view serve`, and `tillr-watch wait` were active:

- **Host memory:** `125 GiB` total RAM; `83–101 GiB` `MemAvailable` during normal ten-lane operation on 2026-11-25, compared with `125 GB` total, `52 GB` available, `21 GB` swap in use, and `4.1%` five-minute `some` memory pressure at 10:50 ET on 2026-11-24 ([gate test capacity §1](./gate-test-capacity.md)).
- **Daemon and adapter environment (`/proc/<pid>/environ`):** `tillr-drain loop`, `tillr-view serve`, `tillr-watch wait`, `--tillr-gate-supervisor`, and `--tillr-heavy-supervisor` all run with `TMPDIR`, `GOTMPDIR`, `NODE_COMPILE_CACHE`, and `XDG_RUNTIME_DIR` **unset**. Commit `a0eacfea` (2026-11-24) gave `go_affected.py` per-slot `GOTMPDIR` under the workspace's durable root, and commit `28cef00c` (2026-11-25) pointed `run-pi`'s `TMPDIR` at `<lane>/durable/<run_id>/tmp` on disk (leaving `run-claude`, `run-muse`, and `command` runs with `TMPDIR` unset).
- **Live `/tmp` accumulation (`1,034,861,473` bytes across `44,151` entries):**
  - `/tmp/go-build*`: `670,301,914` bytes across `10` directories (`2,702` entries) — orphaned `37.4 MB` `drain.test` binaries, `_pkg_.a` archives up to `74.4 MB`, and `27–40 MB` `testlog.txt` files left behind when `go test` or `go build` children were killed without a parent-reaped `GOTMPDIR`.
  - `/tmp/node-compile-cache`: `318,882,041` bytes across `4,496` entries — Node v24's `typescript/lib/tsc.js` calls `module.enableCompileCache()`, which defaults to `/tmp/node-compile-cache` when `NODE_COMPILE_CACHE` is unset and writes ~`1.36 MB` V8 bytecode cache files keyed by absolute script path for every temporary copy of `tools/tillr-view/web`.
  - `/tmp/tillr-0`: `36,656` leaked `<hash>.sock.lock` files (`1.89 MB` of directory entries, `0` file payload bytes) — `Serve` in [`tools/tillr/notify/notify.go`](../../tools/tillr/notify/notify.go) unlinks `<sock>` on return but never unlinks `<sock>.lock`.
  - Residual test and worker artifacts: `45,677,518` bytes across `297` entries (`tillr-trace-test-*` `23.61 MB`, `tillr-run-ssKyAJ` `13.42 MB`, `tillr-gate` `6.19 MB`, `pi-*` `2.11 MB`, `claude*` `0.24 MB`, three leaked `TestRestartWhenNoLoopIsRunningUsesRecordedLaunchAndSucceeds*` directories, and `mise-cache` `887 B`).

---

## 2. Measured /tmp use across one gate and one trunk proof

### 2.1 Exact measurement command and environment

Every check declared in [`tools/gate/checks/*.json`](../../tools/gate/checks/) was measured on 2026-11-25 at base `95d7043eb6a9163e4efadf1e2a73d8a64a294869` without mutating the shared `/tmp` or `<git-common-dir>/tillr-gate`. Each check ran inside an unshared Linux mount namespace (`CLONE_NEWNS` with `MS_REC | MS_PRIVATE`) that bind-mounted a fresh empty directory onto `/tmp` and a fresh empty directory onto `<git-common-dir>/tillr-gate` (so cold tree checks executed instead of short-circuiting on cached verdicts), with `TMPDIR`, `TEMP`, `TMP`, `GOTMPDIR`, `NODE_COMPILE_CACHE`, `NODE_DISABLE_COMPILE_CACHE`, `XDG_RUNTIME_DIR`, and `TILLR_RUN_ID` unset to match `tillr-drain`:

```sh
python3 -c '
import ctypes, os, subprocess, sys, tempfile, threading, time
from pathlib import Path

MS_BIND, MS_REC, MS_PRIVATE = 4096, 16384, 1 << 18
STRIP = {"TMPDIR","TEMP","TMP","GOTMPDIR","NODE_COMPILE_CACHE",
         "NODE_DISABLE_COMPILE_CACHE","XDG_RUNTIME_DIR","TILLR_RUN_ID"}
argv = sys.argv[1:]
git_gate = Path(subprocess.check_output(
    ["git", "rev-parse", "--git-common-dir"], text=True).strip()) / "tillr-gate"

def scan(root):
    files = entries = size = 0
    stack = [root]
    while stack:
        for e in os.scandir(stack.pop()):
            st = e.stat(follow_symlinks=False)
            entries += 1
            if (st.st_mode & 0o170000) == 0o040000:
                stack.append(e.path)
            else:
                files += 1
                size += st.st_size
    return files, entries, size

with tempfile.TemporaryDirectory() as probe:
    tmp_d, gate_d = Path(probe) / "tmp", Path(probe) / "gate"
    tmp_d.mkdir(); gate_d.mkdir()
    env = {k: v for k, v in os.environ.items() if k not in STRIP}
    env["YOLO_DURABLE_DIR"] = "/workspace/.yolo/durable"
    r_fd, w_fd = os.pipe()
    pid = os.fork()
    if pid == 0:
        os.close(w_fd); os.read(r_fd, 1)
        libc = ctypes.CDLL("libc.so.6", use_errno=True)
        os.unshare(os.CLONE_NEWNS)
        libc.mount(b"none", b"/", None, MS_REC | MS_PRIVATE, None)
        libc.mount(str(tmp_d).encode(), b"/tmp", None, MS_BIND, None)
        if git_gate.exists():
            libc.mount(str(gate_d).encode(), str(git_gate).encode(), None, MS_BIND, None)
        os._exit(subprocess.call(argv, env=env))
    os.close(r_fd)
    peak = [0, 0, 0]; stop = threading.Event()
    def loop():
        while not stop.is_set():
            f, e, b = scan(tmp_d)
            peak[:] = [max(peak[0], f), max(peak[1], e), max(peak[2], b)]
            stop.wait(0.025)
    t = threading.Thread(target=loop); t.start()
    os.write(w_fd, b"1"); os.close(w_fd)
    _, status = os.waitpid(pid, 0)
    stop.set(); t.join()
    rf, re, rb = scan(tmp_d)
    peak[:] = [max(peak[0], rf), max(peak[1], re), max(peak[2], rb)]
    print({"rc": os.waitstatus_to_exitcode(status),
           "peak_files": peak[0], "peak_entries": peak[1], "peak_bytes": peak[2],
           "residual_files": rf, "residual_entries": re, "residual_bytes": rb})
' -- <check-command...>
```

### 2.2 Candidate gate: all 15 checks and whole-gate runs

Change-scoped checks were measured across real repository commits (`e87855fa..95d7043e` for Go/code/workflow diffs, `8ef9ed2c..95d7043e` for Markdown diffs, and four affected Go packages including `tillr-view` for `go-affected`):

| Check ID | Program / command | Peak regular files | Peak `/tmp` entries | Peak bytes | Residual files / bytes | Writers observed in `/tmp` |
| :--- | :--- | ---: | ---: | ---: | ---: | :--- |
| `go-build` | `python3 tools/gate/checks/go-build.py` (`go` across 11 modules) | 53 | 749 | `137,276,802` (137.28 MB) | 6 / `99` B | `/tmp/go-build*` (`137,276,703` B across 47 files / 731 entries from parallel `go test -exec true` + `go build`); `mise-cache` (`99` B) |
| `workflows` | `python3 tools/gate/checks/workflows.py <base> <head>` | 8 | 23 | `17,242,238` (17.24 MB) | 6 / `99` B | `/tmp/go-build*` (`17,242,139` B from `go run . workflow check` and `go build` of `tillr-watch`); `mise-cache` (`99` B) |
| `go-affected` | `python3 tools/gate/go_affected.py --changed ...` (4 Go pkgs incl. `tillr-view`) | 606 | 621 | `6,416,299` (6.42 MB) | **606 / `6,416,299` B** | `/tmp/node-compile-cache` (**`6,416,200` B / 600 files leaked** by `tsc` + `vite` in `restart_test.go`); `/tmp/esbuild-*` (`1,537,830` B transient); `/tmp/tillr-<uid>` (`4` socket/lock files, `0` B); `mise-cache` (`99` B). Go test binaries stayed out of `/tmp` because `go_affected.py` sets per-slot `TMPDIR`/`GOTMPDIR` under the workspace's durable root |
| `format` | `just format --check` | 7 | 20 | `7,257` (7.26 KB) | 6 / `99` B | `/tmp/just-*` recipe script (`7,158` B); `mise-cache` (`99` B) |
| `go-module-inventory` | `python3 tools/gate/checks/go-module-inventory.py <base> <head>` | 6 | 19 | `99` | 6 / `99` B | Empty `/tmp/go-module-inventory-*` fallback dir (`0` B); `mise-cache` (`99` B) |
| `citations`, `commit-identity`, `design-citations`, `documents`, `elapsed-time`, `file-size`, `gate-budget`, `go-edges`, `pack-sync`, `scratch` (10 checks) | `python3 tools/gate/checks/<id>.py` / `python3 tests/pack-sync.test.py` (`documents` runs `uvx vantage-check`, caching under `$HOME/.cache/uv`) | 6 each | 18 each | `99` each | 6 / `99` B each | `mise-cache` (`99` B across 6 shim cache files) |
| **Whole gate (cold fast-tree)** | `tillr-gate checks --fast-tree --base 606b4330 --head 95d7043e ...` | **53** | **840** | **`137,276,802`** (137.28 MB) | 6 / `99` B | Dominated by `go-build` (`go-affected` skipped when `--changed` is not passed; if `go-affected` also runs `tillr-view`, add `6.42 MB` peak and `6.42 MB` leaked) |
| **Whole gate (warm change-scoped)** | `tillr-gate checks --fast-tree --scope-to-change --base 606b4330 --head 95d7043e ...` | **7** | **19** | **`148`** | 6 / `99` B | `mise-cache` (`148` B peak, `99` B residual) when `go-build` hits its remembered tree verdict and no Node-building Go package changed |

### 2.3 Trunk proof: all 28 heavy checks

`JudgeHeavy` (`heavy_steps.go` in [`tools/tillr-drain/drain/`](../../tools/tillr-drain/drain/)) executes the 28 `tier: "heavy"` checks **serially** on `.tillr/lanes/.rolling`. Consequently, the serial trunk proof's peak live `/tmp` bytes equals the maximum across the 28 checks (`1,142,629,379` B during `go-test-tillr-drain`), while leaked residuals **accumulate** across all 28 checks (`13,307,888` B across `2,117` regular files per proof pass):

| Heavy check ID | Program / command | Peak regular files | Peak `/tmp` entries | Peak bytes | Residual files / bytes | Dominant `/tmp` writers |
| :--- | :--- | ---: | ---: | ---: | ---: | :--- |
| `go-test-tillr-drain` | `go-test-shards.py tools/tillr-drain -k 6 --cache` | 982 | 4,840 | `1,142,629,379` (1,142.63 MB) | **943 / `4,135,635` B** | `/tmp/go-build*` (`1,139.88 MB`: `--cache` omits `GOTMPDIR`/`TMPDIR`, linking `37.5 MB` `drain.test` binaries across concurrent shards); `/tmp/Test*` (`37.5 MB` x 2 + `30.1 MB` copied binaries); `/tmp/tillr-keep-*` (`30.1 MB`); `/tmp/node-compile-cache` (**`4.14 MB` / 582 files leaked**); `/tmp/tillr-<uid>` (**354 `.sock.lock` files leaked**) |
| `go-test-tillr-watch` | Unsharded `go test -parallel 1 ./...` in `tools/tillr-watch` | 66 | 392 | `258,375,174` (258.38 MB) | **32 / `99` B** | `/tmp/TestWatchWaitIdleUnderStoreWritesAndDeliversAlert*` (`242.71 MB` SQLite WAL burst in `t.TempDir()`); `/tmp/go-build*` (`32.05 MB`); `/tmp/tillr-<uid>` (**26 `.sock.lock` files leaked**) |
| `go-test-shards-test` | `python3 tests/go-test-shards.test.py` | 1,533 | 1,929 | `206,054,547` (206.05 MB) | 6 / `99` B | `/tmp/go-build*` (`206.05 MB` across 1,527 files from nested `go test` fixtures); Python `tempfile` (`220` B) |
| `go-test-tillr-item` | `go-test-shards.py tools/tillr-item -k 6 --cache` | 54 | 3,006 | `166,649,523` (166.65 MB) | 6 / `99` B | `/tmp/go-build*` (`166.65 MB`); `/tmp/TestVersion*` (`18.95 MB`) |
| `go-test-tillr-view` | `go-test-shards.py tools/tillr-view -k 6 --cache` *(note 1)* | 678 | 1,633 | `146,358,593` (146.36 MB) | **606 / `6,416,107` B** | `/tmp/go-build*` (`145.15 MB`); `/tmp/Test*` (`22–30.8 MB` SQLite fixtures); `/tmp/node-compile-cache` (**`6.42 MB` / 600 files leaked**) |
| `go-test-tillr-land` | `go-test-shards.py tools/tillr-land -k 6 --cache` *(note 1)* | 40 | 1,232 | `137,905,017` (137.91 MB) | 6 / `99` B | `/tmp/go-build*` (`137.90 MB`); `/tmp/tillr-land-*` / `tillr-fork-*` / `tillr-recovery-*` (`786` B) |
| `tillr-loop` | `python3 tools/tillr-loop/test.py` | 53 | 341 | `127,850,406` (127.85 MB) | 6 / `99` B | `/tmp/go-build*` (`127.85 MB`); Python `tempfile` `/tmp/tmp*` (`143.9 KB`); `/tmp/args*` (`63.6 KB`) |
| `tillr-docs` | `tillr-docs.test.py && tillr-build.test.py && tillr-scratch.test.py` | 26 | 67 | `107,094,621` (107.09 MB) | 6 / `99` B | `/tmp/go-build*` (`107.08 MB` from `tillr-build.test.py` and `tillr-scratch.test.py`); `/tmp/just-*` (`11.6 KB`) |
| `go-test-tillr-store` | `go-test-shards.py tools/tillr-store -k 6 --cache` | 77 | 407 | `90,454,993` (90.45 MB) | **9 / `99` B** | `/tmp/Test*` (`66–79.3 MB` schema/migration SQLite DBs in `t.TempDir()`); `/tmp/go-build*` (`41.28 MB`); `3` leaked `.sock.lock` files |
| `go-test-tillr-run` | `go-test-shards.py tools/tillr-run -k 6 --cache` | 83 | 1,139 | `64,666,961` (64.67 MB) | **10 / `637` B** | `/tmp/go-build*` (`64.67 MB`); `/tmp/TestVersion*` (`13.29 MB`); `/tmp/land-*` (`70.9 KB`); `/tmp/jiti` (**`538` B / 4 files leaked**) |
| `go-test-tillr` | Unsharded `go test -parallel 1 ./...` in `tools/tillr` | 77 | 313 | `45,039,024` (45.04 MB) | 6 / `99` B | `/tmp/go-build*` (`34.87 MB`); `/tmp/Test*` WAL checkpoint fixtures (`18.4–23.0 MB`) |
| `go-test-tillr-trace` | Unsharded `go test -parallel 1 ./...` in `tools/tillr-trace` | 20 | 190 | `42,298,303` (42.30 MB) | 6 / `99` B | `/tmp/TestALargeSourceIsStreamed*` (`33.57 MB` in `t.TempDir()`); `/tmp/go-build*` (`8.74 MB`) |
| `go-test-tillr-gate` | `go-test-shards.py tools/tillr-gate -k 6 --cache` | 119 | 1,042 | `34,421,620` (34.42 MB) | 6 / `99` B | `/tmp/go-build*` (`34.39 MB`); `/tmp/Test*` (`30–32 KB`) |
| `tillr-roll` | `python3 tests/tillr-roll.test.py` | 146 | 244 | `28,640,746` (28.64 MB) | 6 / `99` B | Python `tempfile` `/tmp/tmp*` (`28.64 MB` built binaries & git repos); `/tmp/go-build*` (`1.91 MB`) |
| `change-checks-test` | `python3 tools/gate/checks/change_checks_test.py` | 44 | 146 | `17,242,238` (17.24 MB) | 6 / `99` B | `/tmp/go-build*` (`17.24 MB`); `/tmp/just-*` (`7.2 KB`) |
| `go-test-tillr-cli` | Unsharded `go test -parallel 1 ./...` in `tools/tillr-cli` | 21 | 163 | `9,477,102` (9.48 MB) | 6 / `99` B | `/tmp/go-build*` (`9.48 MB`) |
| `observatory-web` | `npm run typecheck && npm run test` in `tools/tillr-view/web` | 679 | 684 | `6,071,919` (6.07 MB) | **545 / `2,755,508` B** | Vitest temp dir `/tmp/uUlNaVZaw-*` (`3.32 MB` transient); `/tmp/node-compile-cache` (**`2.76 MB` / 545 files leaked**) |
| `go-affected-test` | `python3 tests/go-affected.test.py` | 22 | 204 | `6,028,274` (6.03 MB) | **7 / `99` B** | `/tmp/go-build*` (`6.03 MB`); Python `tempfile` (`81.9 KB`); `1` leaked `.sock.lock` |
| `go-shard-helpers-test` | `shards_test.py && cache_test.py` *(note 1)* | 14 | 101 | `4,647,207` (4.65 MB) | 6 / `99` B | `/tmp/go-build*` (`4.65 MB`) |
| `tree-checks-test` | `python3 tools/gate/checks/tree_checks_test.py` | 16 | 103 | `1,896,023` (1.90 MB) | 6 / `99` B | `/tmp/go-build*` (`1.90 MB`) |
| `observatory-dev` | `python3 tests/observatory-dev.test.py` | 422 | 639 | `576,887` (0.58 MB) | 6 / `99` B | Python `tempfile` `/tmp/observatory*` (`576.8 KB`) |
| `hooks-install` | `sh tests/hooks-install.test.sh` | 262 | 404 | `147,713` (0.15 MB) | 1 / `49` B | POSIX `mktemp -d` `/tmp/tmp.*` (`147.7 KB`) |
| `gate-adapters-test` | `change-gate.test.py && classification-scan.test.py && done.test.py` | 41 | 80 | `30,217` (30.2 KB) | 6 / `99` B | Python `tempfile` `/tmp/tmp*` (`30.1 KB`) |
| `format-test`, `commit-identity-test`, `gate-budget-test`, `tillr-backends`, `pack-sync-test` (5 checks) | Python test scripts under `tests/` and `tools/gate/checks/` | 7–9 | 19–23 | `112`–`397` | 6–7 / `99`–`397` B | `mise-cache` (`99`–`397` B) and tiny Python tempfiles (`13`–`49` B) |
| **Serial 28-check trunk proof** | All 28 `heavy` checks run one at a time | **1,533** (`go-test-shards-test`) | **4,840** (`go-test-tillr-drain`) | **`1,142,629,379`** (1.06 GiB) | **2,117 / `13,307,888` B** | `1,142.63 MB` peak during `go-test-tillr-drain`; `13.31 MB` (`node-compile-cache` + `.sock.lock` + `jiti`) leaked per pass |

*Note 1:* Under concurrent host load from the live ten-lane drain during measurement, `TestShippedLauncherBuildsObservatoryWhenToolchainPresent` in `go-test-tillr-view` failed after completing its `tsc` + `vite build` (`✓ built in 3.17s`, so all `/tmp` writes ran), and `TestShardRunnerLiveness` in `go-test-tillr-land` / `go-shard-helpers-test` hit its 2-second fixture stall timer; neither affects the dominant peak (`go-test-tillr-drain` at `1,142.63 MB`).

### 2.4 Summary by writer program

Across the gate, trunk proof, daemons, and worker harnesses, every `/tmp` byte comes from nine writer families:

| # | Writer program | Where invoked | Peak `/tmp` footprint | Residual leak per pass / live host |
| ---: | :--- | :--- | :--- | :--- |
| 1 | `go` toolchain (`/tmp/go-build*` when `GOTMPDIR` is unset) | `go-test-shards.py --cache` (only sets `TMPDIR` when `--scratch` is passed), 4 unsharded heavy Go suites, `go-build.py`, `workflows.py`, and Python-spawned `go test` / `go build` | `1,139.88 MB` in `go-test-tillr-drain`; `137.28 MB` in `go-build` | `0 B` on clean exit; **`670.30 MB` on live host** when children receive `SIGKILL` |
| 2 | Go test binaries (`t.TempDir()` `/tmp/Test*` and `os.MkdirTemp("", ...)` `/tmp/tillr-*`, `/tmp/args*`, `/tmp/land-*`) | All Go test suites when `TMPDIR` is unset | `242.71 MB` in `go-test-tillr-watch`; `79.28 MB` in `go-test-tillr-store`; `37.51 MB` x 2 copied binaries in `go-test-tillr-drain` | `0 B` on clean exit; **`~37 MB` + 3 daemon dirs on live host** when child daemons outlive `t.TempDir()` or tests are killed |
| 3 | Node v24 `module.enableCompileCache()` (`/tmp/node-compile-cache`) | `tsc` in `tools/tillr-view/web` (`go-affected`, `go-test-tillr-view`, `go-test-tillr-drain`, `observatory-web`) with `NODE_COMPILE_CACHE` unset | `6.42 MB` (600 files) per `tillr-view` build | **`13.31 MB` / ~1,727 files leaked per proof**; **`318.88 MB` / 4,496 entries on live host** (never evicted) |
| 4 | Vitest, `esbuild`, and `jiti` (`/tmp/uUlNaVZaw-*`, `/tmp/esbuild-*`, `/tmp/jiti`) | `observatory-web`, `go-affected`, `go-test-tillr-run` | `3.32 MB` (`vitest`), `1.54 MB` (`esbuild`), `538 B` (`jiti`) | `jiti` leaks `538 B` / 4 files; `vitest` and `esbuild` clean up on normal exit |
| 5 | `tillr/notify` Unix sockets and lockfiles (`/tmp/tillr-<uid>/<hash>.sock` + `.sock.lock`) | `SocketPath` and `Serve` in [`tools/tillr/notify/notify.go`](../../tools/tillr/notify/notify.go) whenever `XDG_RUNTIME_DIR` is unset | `366` files (`0 B` payload) in `go-test-tillr-drain` | **`384` `.sock.lock` files leaked per proof**; **`36,656` leaked `.sock.lock` files on live host** |
| 6 | Hardcoded `/tmp` notify test dirs (`os.MkdirTemp("/tmp", "tillr-notify-*")`) | `examine_coordinator_test.go` (`tillr-drain`) and `notify_exited_test.go` (`tillr-run`), written to stay under Linux's 108-byte `AF_UNIX` `sun_path` limit | `2` files (`0 B` sockets) | `0 B` on clean exit; bypasses `TMPDIR` if killed |
| 7 | Python `tempfile` and POSIX `mktemp -d` (`/tmp/tmp*`, `/tmp/observatory*`, `/tmp/tmp.*`, `/tmp/go-module-inventory-*`) | `tillr-roll.test.py`, `tillr-loop/test.py`, `observatory-dev.test.py`, `hooks-install.test.sh`, `gate-adapters-test`, `go-module-inventory.py` | `28.64 MB` (`tillr-roll`), `576.8 KB` (`observatory-dev`), `147.7 KB` (`hooks-install`) | `0 B` on clean exit |
| 8 | `just` recipe scripts and `mise` shim cache (`/tmp/just-*`, `/tmp/mise-cache`) | `format`, `tillr-docs`, `change-checks-test`, and every `mise`-shimmed binary | `11.6 KB` (`just-*`), `397 B` (`mise-cache`) | `mise-cache` retains `6–7` reusable files (`99–397 B` bounded singleton) |
| 9 | Worker harnesses and ad-hoc worker builds (`/tmp/pi-*`, `/tmp/claude*`, `/tmp/tillr-*`) | `tillr-run` worker processes running with `TMPDIR` unset | `~15–40 MiB` per Go-testing worker | **`~39.4 MB` across 130 entries on live host** after multi-day uptime |

---

## 3. What happens to each /tmp writer

### 3.1 Why short-lived scratch stays in RAM-backed /tmp instead of moving to /workspace

Once the separate workspace sets `ephemeral_storage = "tmpfs"` (`R-WT2`), `/tmp` is RAM-backed `tmpfs` while `/workspace` remains on the host's btrfs volume. Moving high-churn compiler objects, SQLite test databases, or `git init` test fixtures out of `/tmp` onto `/workspace` would put file churn back onto btrfs — the exact failure `R-WT1` and `R-WT2` set out to stop. Conversely, leaving `/tmp` unshrunk and leaking on `tmpfs` wastes gigabytes of host RAM.

The rule across [§3.2](#32-writer-by-writer-disposition-d-wt1-through-d-wt5) is therefore:
- **Short-lived ephemeral build and test scratch stays in `/tmp` (`tmpfs`), scoped per execution and shrunk** so peak RAM stays small and zero bytes survive exit or `SIGKILL` (`D-WT1`–`D-WT3`).
- **Unbounded cross-run caches in `/tmp` are disabled** in ephemeral runs (`D-WT4`).
- **Daemon sockets prefer `$XDG_RUNTIME_DIR/tillr`** and stop leaking lockfiles (`D-WT5`).

### 3.2 Writer-by-writer disposition (`D-WT1` through `D-WT5`)

| # | Writer | Action | How it shrinks or where it moves | Peak & residual in `/tmp` after change |
| ---: | :--- | :--- | :--- | :--- |
| 1 | `go` toolchain (`/tmp/go-build*`) | **Shrinks in `/tmp`** (`D-WT1`, `D-WT2`) | (a) Every check and worker run gets a short private `/tmp/tc-<pid>-<seq>` (or `/tmp/th-*`, `/tmp/tr-*`) exported as `TMPDIR` and `GOTMPDIR`, reaped on exit and by the lifeline supervisor on `SIGKILL`. (b) `tillr-gate shards` ([`D-UC18`](./tillr-utility-contracts.md#decision-ledger)) compiles **one** stripped binary (`go test -c -ldflags="-s -w"`) per multi-shard package instead of $K$ concurrent `go test` links, and unlinks it as soon as that package's shards finish. (c) `go-build.py` passes `-ldflags="-s -w"` and caps concurrent module links at `4`. | `go-test-tillr-drain` compile scratch drops from `1,139.88 MB` to **`<= 160 MiB`**; cold `go-build` drops from `137.28 MB` to **`<= 50 MiB`**; residual: **`0 B`** |
| 2 | Go `t.TempDir()` and `os.MkdirTemp("", ...)` (`/tmp/Test*`, `/tmp/tillr-keep-*`, etc.) | **Shrinks in `/tmp`** (`D-WT1`, `D-WT3`) | Inherits per-check `TMPDIR` (`D-WT1`), reaping any child daemon dirs on check teardown; `tillr-drain` restart/keep fixtures hardlink (`os.Link` within `/tmp`, falling back to copy across mounts) read-only binaries instead of duplicating `30–37.5 MB` files; `TestWatchWaitIdleUnderStoreWritesAndDeliversAlert` bounds its synthetic WAL loop (`242.7 MB` -> `< 32 MiB`). | `go-test-tillr-watch` drops from `258.38 MB` to **`<= 60 MiB`**; residual: **`0 B`** |
| 3 | Node v24 `/tmp/node-compile-cache` | **Moves out of `/tmp` (disabled in ephemeral runs)** (`D-WT4`) | `tillr-gate`, `JudgeHeavy`, `tillr-run`, and Observatory build scripts export `NODE_DISABLE_COMPILE_CACHE=1`. (Interactive developers who want a persistent cache can set `NODE_COMPILE_CACHE` under `$XDG_CACHE_HOME` outside `/tmp`.) | **`0 B`** peak, **`0 B`** residual (eliminates `318.88 MB` live leak and `13.31 MB`/proof leak) |
| 4 | `vitest`, `esbuild`, and `jiti` temp files | **Stays in `/tmp` (scoped)** (`D-WT1`) | Honor per-check `TMPDIR=/tmp/tc-<pid>-<seq>`; `jiti`'s `538 B` cache dir is wiped with `/tmp/tc-*` when the check exits. | `<= 3.5 MiB` transient during web checks; residual: **`0 B`** |
| 5 | `tillr/notify` sockets & `.sock.lock` (`/tmp/tillr-<uid>`) | **Moves (daemon) & shrinks to zero leak (tests)** (`D-WT5`) | (a) Daemon `SocketPath` uses `$XDG_RUNTIME_DIR/tillr` when set (`/run/user/<uid>` `tmpfs`), falling back to `/tmp/tillr-<uid>` (`1` live socket + `1` lockfile). (b) `Serve` in [`notify.go`](../../tools/tillr/notify/notify.go) unlinks `<sock>.lock` alongside `<sock>` before releasing the lock fd. (c) Check runners export `TILLR_NOTIFY_DIR=/tmp/tc-<pid>-<seq>/n` (`< 36` bytes, safely under the 108-byte `AF_UNIX` limit) so all test sockets vanish on check exit. | Live daemon: `2` zero-byte entries; tests: **`0` leaked `.sock.lock` files** (down from `36,656`) |
| 6 | Hardcoded `os.MkdirTemp("/tmp", "tillr-notify-*")` in 2 tests | **Shrinks in `/tmp` (scoped)** (`D-WT1`, `D-WT5`) | Replace `"tmp"` literal `"/tmp"` with `""` (`os.TempDir()`) in `examine_coordinator_test.go` and `notify_exited_test.go`; short `/tmp/tc-<pid>-<seq>` keeps `sun_path` `< 65` bytes while guaranteeing parent cleanup. | `2` zero-byte sockets during test; residual: **`0 B`** |
| 7 | Python `tempfile` and POSIX `mktemp -d` | **Stays in `/tmp` (scoped)** (`D-WT1`) | Honor per-check `TMPDIR`; any leaked subdir on timeout/`SIGKILL` is removed with `/tmp/tc-*` / `/tmp/th-*`. | `<= 29 MiB` peak (`tillr-roll`); residual: **`0 B`** |
| 8 | `just` `/tmp/just-*` and `mise` `/tmp/mise-cache` | **Stays in `/tmp`** (`D-WT1`) | `just` honors `TMPDIR`; `mise-cache` stays as a single bounded `< 1 KiB` metadata directory shared across shims. | `< 12 KiB` peak; residual: **`<= 1 KiB`** (`mise-cache` singleton) |
| 9 | Worker harnesses (`tillr-run` child `TMPDIR`) | **Shrinks in `/tmp` (scoped)** (`D-WT1`, `D-WT4`) | `tillr-run` (`exec.go`) creates `/tmp/tr-<short-run-id>` (`TMPDIR` + `NODE_DISABLE_COMPILE_CACHE=1`, leaving `GOTMPDIR` unset so `go` uses `$TMPDIR`) and removes `/tmp/tr-<short-run-id>` when the run lease closes; `tillr-drain loop` sweeps dead-owner `/tmp/tc-*`, `/tmp/th-*`, and `/tmp/tr-*` on startup. | `~15–40 MiB` per active Go-testing worker; residual after run exit: **`0 B`** |

---

## 4. Accepted ten-lane peak and the recorded alert fact

### 4.1 Accepted ten-lane peak: `2,147,483,648` bytes (`2 GiB`) (`D-WT7`)

**Designer's decision (`D-WT7`, subject to owner ruling on [`OQ-WT1`](#OQ-WT1)).** At ten worker lanes plus one serial trunk proof, the design accepts a peak `/tmp` occupancy of **`2,147,483,648` bytes (`2 GiB` / `2,048 MiB`)**.

Component-by-component simultaneous worst case after `D-WT1` through `D-WT5` (including [`OQ-WT2`](#OQ-WT2) Option A, where ephemeral check and suite scratch also uses `/tmp`):

| Concurrent consumer at 10 lanes | Typical steady state | Simultaneous burst ceiling | Basis |
| :--- | ---: | ---: | :--- |
| Daemons (`tillr-drain`, `tillr-view`, `tillr-watch`) + `mise-cache` | `< 1 MiB` | `1 MiB` | `1` notify socket + `mise-cache` (`< 1 KiB`) |
| 1 serial trunk proof (`JudgeHeavy` on `.rolling`) | `40–180 MiB` | `256 MiB` | Single-binary `-ldflags="-s -w"` package shards (`D-WT2`), hardlinked fixtures & bounded WAL (`D-WT3`), `0 B` Node compile cache (`D-WT4`) |
| Concurrent candidate gates (bounded by [gate test capacity](./gate-test-capacity.md)) | `10–120 MiB` | `640 MiB` | Warm gate `< 1 MiB`; cold `go-build` `<= 50 MiB`; active Go-testing `go-affected` slots `~40–80 MiB` each across concurrent gates |
| Up to 10 active worker runs (`/tmp/tr-<id>`) | `50–200 MiB` | `400 MiB` | Worker bash logs already live on disk under `<lane>/durable/<run_id>/tmp`; targeted `go test` in lanes uses `~15–40 MiB` per active Go worker |
| **Total across the workspace** | **`~150–450 MiB`** | **`~1,297 MiB` (`1.27 GiB`)** | **`0 B` cumulative leak across completed checks and runs** |

Setting the accepted ceiling at **`2,147,483,648` bytes (`2 GiB`)** provides `1.58x` headroom above the simultaneous burst ceiling (`1.27 GiB`) so concurrent landings do not false-alarm, while capping accepted `/tmp` RAM at **1.6% of the host's 125 GiB RAM** and **3.8% of the 52 GiB 2026-11-24 `MemAvailable` low-water mark**.

### 4.2 Recorded fact (`drain.tmp-usage`) and watch alert (`D-WT7`)

Modeled directly on `drain.process-cpu` ([`process_cpu.go`](../../tools/tillr-drain/drain/process_cpu.go)), `tillr-drain loop` samples `/tmp` once every 60 seconds (`tmp_usage.go`) and appends a **`drain.tmp-usage`** event to `.tillr/state.db`:

```go
const TmpUsageKind = "drain.tmp-usage"

type TmpWriterEntry struct {
	Name       string `json:"name"`
	Files      int    `json:"files"`
	Entries    int    `json:"entries"`
	Bytes      int64  `json:"bytes"`
	AllocBytes int64  `json:"alloc_bytes"`
}

type TmpUsageFact struct {
	SessionID      string           `json:"session_id"`
	WindowMS       int64            `json:"window_ms"`
	TmpPath        string           `json:"tmp_path"`
	FSType         string           `json:"fstype"`
	TmpUsedBytes   int64            `json:"tmp_used_bytes"`
	TreeBytes      int64            `json:"tree_bytes"`
	TreeAllocBytes int64            `json:"tree_alloc_bytes"`
	FSUsedBytes    int64            `json:"fs_used_bytes"`
	FSTotalBytes   int64            `json:"fs_total_bytes"`
	FileCount      int              `json:"file_count"`
	EntryCount     int              `json:"entry_count"`
	TopWriter      string           `json:"top_writer"`
	TopWriterBytes int64            `json:"top_writer_bytes"`
	TopWriters     []TmpWriterEntry `json:"top_writers"`
}
```

**Why both `statfs` and a directory scan are recorded:**
- When `/tmp` is mounted as `tmpfs` (`f_type == 0x01021994`, `fstype == "tmpfs"`), `FSUsedBytes` (`(Blocks - Bfree) * Bsize`) is an $O(1)$ kernel accounting figure that **includes open-but-unlinked files** holding RAM pages in `/tmp`.
- Before `ephemeral_storage = "tmpfs"` is enabled (`fstype == "btrfs"`), `statfs("/tmp")` would report the entire host root volume's hundreds of gigabytes. Setting `FSUsedBytes = 0` when `fstype != "tmpfs"` and defining **`tmp_used_bytes = max(tree_alloc_bytes, fs_used_bytes)`** makes the single predicate `event.tmp_used_bytes > 2147483648` accurate both before and after the workspace move, while `top_writers` names the exact `/tmp/<prefix>` culprit (or reveals an unlinked open file whenever `fs_used_bytes > tree_alloc_bytes`).

**Watch rule (`config/tillr/watch/drain-tmp-usage-high.toml`):**

```toml
[[rule]]
id = "drain-tmp-usage-high"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.tmp-usage"]

[rule.when]
group_by = "global"
pattern = "predicate_match"
match = "event.tmp_used_bytes > 2147483648"
clear_when = "event.tmp_used_bytes <= 2147483648"
window = "5m"
```

When `event.tmp_used_bytes` exceeds `2,147,483,648`, `tillr-watch` opens one `drain-tmp-usage-high` episode naming `event.tmp_used_bytes`, `event.fstype`, `event.file_count`, `event.top_writer`, and `event.top_writer_bytes`, and clears automatically on the next 60-second sample at or below `2,147,483,648` bytes. Per [tillr-watch's standing ruling](./tillr-watch.md#32-standing-ruling-an-alert-never-fails-benches-or-caps-work-d-tw2), the alert never kills work.

---

## 5. What happens to scratch kept under the workspace

Tillr keeps two fundamentally different classes of paths under `/workspace` today: **durable workspace state and artifacts** (which must survive restarts or serve audit/worktree roles) and **ephemeral check and suite scratch** (created and `rm -rf`'d within seconds during gates and tests, yet churning btrfs extents because of the founding `TestMain` rule noted in [§1.1](#11-standing-owner-rulings)):

| Workspace path | Live size (2026-11-25) | Role | Disposition |
| :--- | ---: | :--- | :--- |
| `.tillr/state.db` (`-wal`, `-shm`, `.loops`, `.watch-attached`, `keeper.*`) | `53.0 MB` | Canonical SQLite store, WAL, and loop coordination locks | **Left on disk (`D-WT6`)** — persistent state |
| `.tillr/loop/<run-id>/` (`brief.md`, `node-report.md`, `progress.json`) | `~30K` entries | Staged briefs and run reports read by the judge and `tillr-view` | **Left on disk (`D-WT6`)** — durable audit records |
| `.tillr/lanes/<lane>/worktree` and `.tillr/lanes/.rolling` | 11 Git worktrees | Persistent lane and trunk-proof Git checkouts | **Left on disk (`D-WT6`)** — persistent worktrees; putting 11 checkouts in `tmpfs` would waste multi-GiB of RAM |
| `.tillr/lanes/<lane>/durable/<run-id>/` (`events.jsonl`, `landing-state.json`, `node-report.md`) | Per-run logs | Worker transcripts and landing call state read by `tillr-trace` and crash recovery (note: `run-pi`'s ephemeral `tmp/` subdir added in `28cef00c` already deletes itself on exit and moves to `/tmp/tr-<id>` under `D-WT1`) | **Left on disk (`D-WT6`)** — post-run evidence (ephemeral `tmp/` moves to `/tmp/tr-<id>`) |
| `.tillr/lanes/.requests`, `.fixers`, `.replay`, `.revert`, and `<git-common-dir>/tillr-gate/` | Small JSON + logs | Detached check requests/verdicts and remembered gate verdicts | **Left on disk (`D-WT6`)** — crash recovery and cross-run verdict cache |
| `<durable-root>/bin/` and `<durable-root>/tillr-observatory/<key>/` | `145 MB` | Content-addressed built binaries (`tillr-roll`) and Observatory web bundles | **Left on disk (`D-WT6`)** — low-churn, reused across runs |
| `<durable-root>/.go-affected-scratch/slot-*`, `go-test-shards-slots/slot-*`, `sibling-cache-*`, `tillr-roll-*`, and `TestMain` `scratch()` (`tillr-*-bin-*`) | **`~1.80 GiB` on btrfs** (`646 MB` `.go-affected-scratch` + `737 MB` `sibling-cache-*` + `189 MB` `tillr-roll-*`) | Short-lived per-test / per-slot compiler & fixture directories that call `os.RemoveAll` in `t.Cleanup` (or leak on `SIGKILL`) | **Owner's call in [`OQ-WT2`](#OQ-WT2)** (designer leaning: Option A — move ephemeral check/suite scratch to per-check `/tmp` on `tmpfs` and copy bounded failure logs to disk on red) |

Why [`OQ-WT2`](#OQ-WT2) matters: today `go_affected.py` sets `TMPDIR` and `GOTMPDIR` to `<durable-root>/.go-affected-scratch/slot-<N>/tmp` on `/workspace`, and six `TestMain` suites build binaries and SQLite stores under `<durable-root>` because of the founding `TestMain` comment (`never /tmp: a suite that writes to /tmp loses its evidence when the box is cleaned, and the owner refuses it`). Because `t.Cleanup` already deletes those directories on both pass and fail, keeping them on `/workspace` preserves no post-mortem evidence on test failure while continuing to churn hundreds of megabytes of temporary binaries and SQLite WALs on btrfs during every candidate gate. Because scoping that founding rule belongs to the owner ([§1.1](#11-standing-owner-rulings)), [`OQ-WT2`](#OQ-WT2) asks the owner directly.

---

## 6. The first slice and the build order

### 6.1 The first slice as one behavior

**Slice 1 (one behavior, `D-WT1` + `D-WT4` in `tillr-gate`):** When `tillr-gate` spawns a check process (`runCheck` in [`tools/tillr-gate/run.go`](../../tools/tillr-gate/run.go)), it creates a private directory `/tmp/tc-<pid>-<seq>`, sets `TMPDIR=/tmp/tc-<pid>-<seq>`, `GOTMPDIR=/tmp/tc-<pid>-<seq>`, and `NODE_DISABLE_COMPILE_CACHE=1` in the check's environment, and removes `/tmp/tc-<pid>-<seq>` unconditionally when the check finishes, times out, or is canceled.

- **What single test proves it:** one Go test in `tools/tillr-gate` runs a fast check that writes files to `$TMPDIR` and `$GOTMPDIR`, invokes `node -e 'require("node:module").enableCompileCache()'` (verifying `/tmp/node-compile-cache` is untouched), and runs a second check killed by context cancellation, asserting zero `/tmp/tc-*` or `/tmp/node-compile-cache` entries remain afterward.
- **Why it lands first and waits on nothing:** it touches one file group in `tools/tillr-gate`, waits on neither [`OQ-WT1`](#OQ-WT1) nor [`OQ-WT2`](#OQ-WT2), immediately eliminates the `6.42 MB` / `600`-file `/tmp/node-compile-cache` leak on every `tillr-view` candidate gate, and prevents killed `go-build` or `workflows` gate checks from orphaning `/tmp/go-build*`.

### 6.2 Subsequent slices

| Slice | What lands | Why it is useful alone | Follows |
| ---: | :--- | :--- | :--- |
| 1 | Per-check `/tmp/tc-<pid>-<seq>` (`TMPDIR` + `GOTMPDIR`) + `NODE_DISABLE_COMPILE_CACHE=1` + unconditional teardown in `tillr-gate` (`run.go`) | Zero `/tmp/node-compile-cache` or `/tmp/go-build*` leaks from candidate gates | — |
| 2 | `notify.Serve` ([`notify.go`](../../tools/tillr/notify/notify.go)) unlinks `<sock>.lock` alongside `<sock>` on exit; `SocketPath` honors `TILLR_NOTIFY_DIR` (set to `<check-tmp>/n` by `tillr-gate`) and `$XDG_RUNTIME_DIR/tillr`; `examine_coordinator_test.go` and `notify_exited_test.go` use `os.TempDir()` instead of `"/tmp"` | Stops the `36,656`-file `.sock.lock` leak in `/tmp/tillr-<uid>` | 1 |
| 3 | `JudgeHeavy` (`heavy_steps.go` + heavy supervisor lifeline) and `tillr-run` (`exec.go` + worker lease teardown) allocate `/tmp/th-<pid>-<seq>` and `/tmp/tr-<short-id>` with `TMPDIR`, `GOTMPDIR`, `TILLR_NOTIFY_DIR`, and `NODE_DISABLE_COMPILE_CACHE=1`, plus dead-owner `/tmp/t[chr]-*` sweep at `tillr-drain loop` startup | Eliminates the `13.31 MB` / `2,117`-file leak per trunk proof and `670.30 MB` orphaned `/tmp/go-build*` buildup from killed proofs and worker runs | 1, 2 |
| 4 | `go-test-shards.py` / `tillr-gate shards` ([`D-UC18`](./tillr-utility-contracts.md#decision-ledger)) compiles one stripped binary (`go test -c -ldflags="-s -w"`) per multi-shard package and unlinks it when that package's shards finish; `go-build.py` uses `-ldflags="-s -w"` with `max_workers=4`; `tillr-drain` restart/keep fixtures hardlink binaries in `/tmp` and `tillr-watch` bounds its synthetic WAL burst test | Drops `go-test-tillr-drain` `/tmp` peak from `1,142.63 MB` to `<= 256 MiB`, `go-test-tillr-watch` from `258.38 MB` to `<= 60 MiB`, and cold `go-build` from `137.28 MB` to `<= 50 MiB` | 3 |
| 5 | `drain.tmp-usage` 60-second sampler in `tools/tillr-drain/drain/tmp_usage.go` and `config/tillr/watch/drain-tmp-usage-high.toml` at the threshold ruled in [`OQ-WT1`](#OQ-WT1) (`2,147,483,648` B proposed) | Continuous recorded `/tmp` occupancy and immediate watch alert on regressions, working on both `btrfs` and `tmpfs` | [`OQ-WT1`](#OQ-WT1) |
| 6 | Apply [`OQ-WT2`](#OQ-WT2)'s ruling to workspace-backed ephemeral scratch (`.go-affected-scratch`, `go-test-shards-slots`, `TestMain` `scratch()`), and set `ephemeral_storage = "tmpfs"` in the separate repository's `yolo-jail.toml` once Slices 1–5 are green | Completes the move of ephemeral churn off btrfs onto bounded `tmpfs` | 1–5, [`OQ-WT1`](#OQ-WT1), [`OQ-WT2`](#OQ-WT2), separate-repo move |

---

## 7. Edge cases, alternatives considered, and scope boundaries

### 7.1 Edge cases

| Case | What happens |
| :--- | :--- |
| Linux `AF_UNIX` 108-byte `sun_path` limit on Unix sockets | `/tmp/tc-1234567-99/n/<16-hex>.sock.lock` is `46` bytes — well below `108` bytes — whereas deep workspace paths exceed `108` bytes |
| Detached gate or heavy proof supervisor receives `SIGKILL` | The lifeline guard process (`lifeline` in `tools/tillr-drain` / `tillr-run`) removes `/tmp/tc-*` / `/tmp/th-*` when its parent pipe closes; if the whole container or power dies, `tmpfs` clears on restart and `tillr-drain loop` sweeps any `/tmp/t[chr]-<pid>-*` whose `<pid>` is dead at startup |
| A child process opens a `1 GiB` file in `/tmp`, unlinks it, and keeps the fd open | `readdir` sees `0` bytes, but on `tmpfs` `statfs` (`FSUsedBytes`) still counts the `1 GiB` of pinned RAM pages; `tmp_used_bytes = max(tree_alloc_bytes, fs_used_bytes)` catches it and `fs_used_bytes > tree_alloc_bytes` diagnoses an unlinked open fd immediately |
| `drain.tmp-usage` runs before `ephemeral_storage = "tmpfs"` is enabled | `fstype` is `"btrfs"`, so `fs_used_bytes` is `0` and `tmp_used_bytes` uses `tree_alloc_bytes`, avoiding false alerts from whole-disk btrfs occupancy |
| Fixture hardlink (`os.Link`) crosses mounts (e.g., binary on `/workspace` disk, `t.TempDir()` on `/tmp` `tmpfs`) | Helper tries `os.Link` first (zero-copy when both paths are in `/tmp` under Option A of [`OQ-WT2`](#OQ-WT2)), then read-only symlink where path-relative lookup allows, falling back to `io.Copy` only when an executable requires its own directory |

### 7.2 Alternatives considered

| Alternative | Why rejected |
| :--- | :--- |
| **Redirect all `/tmp` writers (`TMPDIR`, `GOTMPDIR`) into `/workspace`** | Puts compiler, SQLite WAL, and test churn directly back onto the host's btrfs filesystem, defeating `R-WT1` and `R-WT2` |
| **Flip `ephemeral_storage = "tmpfs"` immediately without shrinking or fixing leaks** | Leaks `13.31 MB` per trunk proof and `6.42 MB` per web-touching gate into RAM forever (`318.88 MB` `node-compile-cache` + `670.30 MB` orphaned `go-build*` observed on the live box), plus `1.14 GB` single-proof spikes alongside ten lanes |
| **Point `NODE_COMPILE_CACHE` at a shared directory in `/tmp` and keep it enabled** | Keyed by absolute script path: every test copying `web/` into a fresh temp directory writes ~`600` unique cache files that no future run ever hits |
| **Have `tillr-watch` delete `/tmp` files when `drain-tmp-usage-high` fires** | Violates [tillr-watch's standing ruling](./tillr-watch.md#32-standing-ruling-an-alert-never-fails-benches-or-caps-work-d-tw2) and risks deleting live check scratch mid-run; bounded lifecycle cleanup belongs in `tillr-gate`, `tillr-drain`, and `tillr-run` |

### 7.3 Out of scope here

Per the request boundaries, three adjacent topics are intentionally left to their own items:
- Moving tillr into its own repository and workspace (which has no design document under `docs/` yet, and is the place where `ephemeral_storage = "tmpfs"` is enabled in `yolo-jail.toml` once Slices 1–5 land).
- Provisioning a separate btrfs subvolume for the previous shared workspace (`R-WT1`).
- Changing worker lane counts or test shard counts (owned by [gate test capacity](./gate-test-capacity.md)).

---

## Open Questions

1. 💬 **OQ-WT1: Does `2 GiB` (`2,147,483,648` bytes) stand as the accepted ten-lane `/tmp` ceiling, and should `tmpfs` rely on the watch alert alone or also carry a hard mount size cap?**

   After Slices 1–4 (`D-WT1`–`D-WT5`), ten worker lanes plus one serial trunk proof have a simultaneous worst-case `/tmp` burst of `~1.27 GiB` (`150–450 MiB` typical steady state) and `0 B` cumulative leak across completed checks ([§4.1](#41-accepted-ten-lane-peak-2147483648-bytes-2-gib-d-wt7)). On the 125 GiB host (`52 GiB` 2026-11-24 `MemAvailable` low-water mark), `yolo`'s `ephemeral_storage = "tmpfs"` mounts `tmpfs` without a custom small `size=` cap by default, so an unchecked runaway writer could grow past `2 GiB` until `drain-tmp-usage-high` is answered unless the kernel mount itself also caps `/tmp`.

   - **A — Accept `2 GiB` (`2,147,483,648` bytes) watched by `drain.tmp-usage`, over `yolo`'s default `ephemeral_storage = "tmpfs"` mount.** `drain-tmp-usage-high` alerts whenever `event.tmp_used_bytes > 2147483648`; no kernel `ENOSPC` kills in-flight lanes during a transient spike above `2 GiB`.
   - **B — Accept `2 GiB` (`2,147,483,648` bytes) for the watch alert, AND cap the jail's `/tmp` `tmpfs` mount at `4 GiB` (`4,294,967,296` bytes).** Alert at `2 GiB` and fail a runaway `/tmp` writer with `ENOSPC` at `4 GiB` (`3.2%` of host RAM) so `/tmp` can never OOM the host overnight (requires `yolo` `tmpfs` size support if not already exposed in `yolo-jail.toml`).
   - **C — Set a tighter `1 GiB` (`1,073,741,824`-byte) alert ceiling.** Requires keeping workspace-backed suite scratch on disk ([`OQ-WT2`](#OQ-WT2) Option B) or serializing cold `go-build` across lanes so simultaneous bursts never pass `1 GiB`.

   <!-- vantage: question id=OQ-WT1 leaning="A — 2 GiB (1.6% of 125 GiB RAM) gives headroom over the ~1.27 GiB ten-lane + proof burst without false alarms, works with yolo's existing ephemeral_storage = tmpfs setting today, and catches any leak regression on the next 60-second sample." -->

   _Leaning:_ A — `2 GiB` (`1.6%` of `125 GiB` RAM) gives headroom over the `~1.27 GiB` ten-lane + proof burst without false alarms, works with `yolo`'s existing `ephemeral_storage = "tmpfs"` setting today, and catches any leak regression on the next 60-second sample.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-WT2: What happens to ephemeral check and suite scratch currently written under the workspace's durable root on disk (`.go-affected-scratch`, `go-test-shards-slots`, and `TestMain` `scratch()`)?**

   Durable workspace state — `.tillr/state.db*`, `.tillr/loop/`, `.tillr/lanes/<lane>/worktree`, `.tillr/lanes/<lane>/durable/`, `.tillr/lanes/.rolling`, `<git-common-dir>/tillr-gate/`, and content-addressed `bin/` + `tillr-observatory/` — stays on disk (`D-WT6`). By contrast, `go_affected.py` (`/.go-affected-scratch/slot-*`, `646 MB` on disk on 2026-11-25), `go-test-shards-slots`, `sibling-cache-*` (`737 MB`), and six `TestMain` suites (`scratch(t, ...)`) create and `rm -rf` short-lived compiler objects, binaries, and SQLite stores under `/workspace` because of the founding `TestMain` rule (`never /tmp: a suite that writes to /tmp loses its evidence when the box is cleaned, and the owner refuses it`). Because `t.Cleanup` already deletes those directories on both pass and fail, keeping them on `/workspace` preserves no failure evidence today while continuing heavy btrfs file churn on every gate.

   - **A — Make ephemeral check and suite scratch part of this change: move it into per-check `/tmp` (`tmpfs`), and copy failure logs/artifacts to disk only when a check fails.** `.go-affected-scratch` slots, `go-test-shards` slots, Python check `TemporaryDirectory`s, and `TestMain` `scratch(t, ...)` use the check's private `/tmp/tc-*` / `/tmp/th-*` directory on `tmpfs` (already included in the `~1.27 GiB` burst math under `2 GiB`), while a failing check copies bounded diagnostic logs/artifacts (`<= 16 MiB`) into `.tillr/lanes/.gates/` or `<git-common-dir>/tillr-gate/` on disk before `/tmp/tc-*` is deleted — eliminating steady-state btrfs churn without losing red-check evidence.
   - **B — Leave all workspace-backed scratch on disk.** Keep `.go-affected-scratch`, `go-test-shards-slots`, and `TestMain` `scratch()` under the workspace's disk durable root unchanged, scoping this change strictly to writers that currently target `/tmp` (Slices 1–5).
   - **C — Split: move `go-affected` and `go-test-shards` compiler/runtime slots (`TMPDIR` + `GOTMPDIR`) to `/tmp`, while keeping `TestMain` helper binaries cached by tree hash on disk under `<durable-root>/bin/`.** Stops per-gate `go-build` churn under `.go-affected-scratch` while keeping Go test helper binaries on disk.

   <!-- vantage: question id=OQ-WT2 leaning="A — moving ephemeral slot and suite scratch onto tmpfs eliminates ~1.8 GiB and thousands of files per gate of btrfs churn on /workspace while preserving red-check evidence on disk when a check actually fails." -->

   _Leaning:_ A — moving ephemeral slot and suite scratch onto `tmpfs` eliminates `~1.8 GiB` and thousands of files per gate of btrfs churn on `/workspace` while preserving red-check evidence on disk when a check actually fails.

   **Answer:**
   > _(empty — fill in when decided)_

---

## Decision Ledger

**Who decided each row.** `R-WT1` and `R-WT2` record dated verbatim statements by the owner; inherited rulings on test concurrency and process priority remain in [gate test capacity](./gate-test-capacity.md). Every `D-WT*` row below is the designer's decision, never an owner ruling, and the owner may overturn any of them.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `R-WT1` | **Owner ruling (verbatim).** *"we're chrning a ton of files that is causing btrfs issues, and we can eithe rget our own subvlume, or fix this churn, or both."* | 2026-11-24, owner | [§1.1](#11-standing-owner-rulings) | — |
| `R-WT2` | **Owner ruling (verbatim).** *"when we move to the separate repo, you can set ephemeral storage to tmpfs so we don't destroy btrfs with our /tmp usage and if we shrink /tmp usage enough we won't destroy ram either."* | 2026-11-24, owner | [§1.1](#11-standing-owner-rulings) | — |
| `D-WT1` | **Designer's decision; the owner may overturn it.** Every gate check (`tillr-gate`), heavy trunk-proof check (`JudgeHeavy`), and worker run (`tillr-run`) allocates a short private `/tmp` directory (`/tmp/tc-<pid>-<seq>`, `/tmp/th-<pid>-<seq>`, `/tmp/tr-<short-id>`) exported as `TMPDIR` (and `GOTMPDIR` for checks), removes it unconditionally on completion/timeout/cancellation and via the lifeline guard on `SIGKILL`, and sweeps dead-owner `/tmp/t[chr]-*` directories on `tillr-drain loop` startup. **Rejected:** redirecting `TMPDIR`/`GOTMPDIR` onto `/workspace` btrfs. | 2026-11-25, designer | [§3.1](#31-why-short-lived-scratch-stays-in-ram-backed-tmp-instead-of-moving-to-workspace), [§3.2](#32-writer-by-writer-disposition-d-wt1-through-d-wt5), [§6](#6-the-first-slice-and-the-build-order) | — |
| `D-WT2` | **Designer's decision; the owner may overturn it.** In `go-test-shards.py` / `tillr-gate shards` ([`D-UC18`](./tillr-utility-contracts.md#decision-ledger)), packages with multiple shards compile **one** stripped test binary (`go test -c -ldflags="-s -w"`) shared read-only across that package's `-test.run` shards and unlinked as soon as those shards finish, instead of linking $K$ copies concurrently; `go-build.py` likewise passes `-ldflags="-s -w"` and caps concurrent module links at `4`. | 2026-11-25, designer | [§3.2](#32-writer-by-writer-disposition-d-wt1-through-d-wt5), [§6.2](#62-subsequent-slices) | — |
| `D-WT3` | **Designer's decision; the owner may overturn it.** Fixture tests in `tillr-drain` hardlink (`os.Link` within `/tmp` with copy fallback across mounts) read-only helper binaries instead of copying `30–37.5 MB` executables per test, and `TestWatchWaitIdleUnderStoreWritesAndDeliversAlert` in `tillr-watch` bounds its synthetic WAL write burst so its `t.TempDir()` stays under `32 MiB` (down from `242.71 MB`). | 2026-11-25, designer | [§3.2](#32-writer-by-writer-disposition-d-wt1-through-d-wt5), [§6.2](#62-subsequent-slices) | — |
| `D-WT4` | **Designer's decision; the owner may overturn it.** `tillr-gate`, `JudgeHeavy`, `tillr-run`, and Observatory build scripts export `NODE_DISABLE_COMPILE_CACHE=1` so Node v24's `module.enableCompileCache()` writes zero files into `/tmp/node-compile-cache` during ephemeral runs. **Rejected:** keeping `/tmp/node-compile-cache` enabled across temp-copied `web/` trees. | 2026-11-25, designer | [§3.2](#32-writer-by-writer-disposition-d-wt1-through-d-wt5), [§6.1](#61-the-first-slice-as-one-behavior) | — |
| `D-WT5` | **Designer's decision; the owner may overturn it.** `Serve` in [`tools/tillr/notify/notify.go`](../../tools/tillr/notify/notify.go) unlinks `<sock>.lock` alongside `<sock>` before closing its flock fd; `SocketPath` honors `TILLR_NOTIFY_DIR` (set to `<check-tmp>/n` by check runners, keeping `sun_path` `< 50` bytes) and prefers `$XDG_RUNTIME_DIR/tillr` for live daemons; `examine_coordinator_test.go` and `notify_exited_test.go` pass `""` (`os.TempDir()`) instead of hardcoded `"/tmp"`. | 2026-11-25, designer | [§3.2](#32-writer-by-writer-disposition-d-wt1-through-d-wt5), [§6.2](#62-subsequent-slices) | — |
| `D-WT6` | **Designer's decision; the owner may overturn it.** Durable workspace paths — `.tillr/state.db*`, `.tillr/loop/`, `.tillr/lanes/<lane>/worktree`, `.tillr/lanes/<lane>/durable/`, `.tillr/lanes/.rolling`, `.tillr/lanes/.requests` / `.fixers` / `.replay` / `.revert`, `<git-common-dir>/tillr-gate/`, and content-addressed `<durable-root>/bin/` and `<durable-root>/tillr-observatory/` — stay on disk; ephemeral check and suite scratch currently under `<durable-root>` follows [`OQ-WT2`](#OQ-WT2). | 2026-11-25, designer | [§5](#5-what-happens-to-scratch-kept-under-the-workspace), [`OQ-WT2`](#OQ-WT2) | — |
| `D-WT7` | **Designer's decision; the owner may overturn it.** Accept a ten-lane `/tmp` peak ceiling of **`2,147,483,648` bytes (`2 GiB`)** (subject to [`OQ-WT1`](#OQ-WT1)), record `/tmp` occupancy every 60 seconds in `drain.tmp-usage` (`tmp_used_bytes = max(tree_alloc_bytes, fs_used_bytes)` with `fs_used_bytes` populated only when `fstype == "tmpfs"`), and alert via `config/tillr/watch/drain-tmp-usage-high.toml` when `event.tmp_used_bytes > 2147483648`, delivered in the six slices of [§6](#6-the-first-slice-and-the-build-order) starting with Slice 1's per-check `/tmp` isolation in `tillr-gate`. | 2026-11-25, designer | [§4](#4-accepted-ten-lane-peak-and-the-recorded-alert-fact), [§6](#6-the-first-slice-and-the-build-order), [`OQ-WT1`](#OQ-WT1) | — |
