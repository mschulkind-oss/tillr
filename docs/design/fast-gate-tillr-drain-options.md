---
title: "Options for what the fast gate runs before the merge when a change touches tillr-drain"
status: accepted
stage: BUILT
stage-since: 2026-11-25
updated: 2026-11-25
tags:
  - tillr
  - gate
  - drain
  - watch
  - testing
  - design
summary: "Lays out four designer options from an operator request for what pre-merge go-affected runs when a change touches tools/tillr-drain: (A) keep all of tillr-drain/drain before the merge and raise fast-gate-median-slow to 1,200 s; (B) split tillr-drain/drain so its 18 pure unit tests (2.94 s) stay before the merge and its 2,077 integration tests (3,633.40 s) move to the after-merge proof (90 s stays once tillr-view's 3 unsharded launcher-build tests are sharded or deferred, or 360 s if left unsharded); (C) prove all of tillr-drain/drain after the merge while keeping go-build and non-drain consumer tests before the merge (same 90 s threshold rule); and (D) run only intra-package tests reached by changed files (sound CHA still reaches a median of 1,666 tests / 3,432.59 s across recent drain commits, requiring 1,200 s). On 2026-11-25 (OQ-DFG1), the owner ruled to stick with Option C (already built in 5e728e5a) and decide again in the future once more data is collected."
next: "Collect operating data under Option C (already built on main in 5e728e5a) so the owner can decide again in the future; no question is open."
depends-on:
  - ../reference/gate.md
  - ../reference/gate.md#D-GR38
  - ../reference/gate.md#D-GR39
  - ../reference/gate.md#D-GR53
  - ../reference/gate.md#D-GR54
  - ../reference/gate.md#D-GR56
  - gate-test-capacity.md
  - landing-principles.md
  - tillr-watch.md
  - ../reference/authority-not-approval.md
related:
  - ../research/gate-batching.md
vantage:
  status-chip: true
---

# Options for what the fast gate runs before the merge when a change touches `tillr-drain`

**Status:** Ruled by the owner on 2026-11-25 ([`OQ-DFG1`](#OQ-DFG1), [`D-GR56`](../reference/gate.md#D-GR56)): `"ro DFG1, let's stick wiht C. you think it's a good idea and it's not hurting. we'll collect more info and make another deciison in the future."` **Option C** (`DEFERRED_PROOF_PACKAGES` in [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py) and expanded `reads` in [`tools/gate/checks/go-test-tillr-drain.json`](../../tools/gate/checks/go-test-tillr-drain.json), already on `main` from commit `5e728e5a` after [`D-GR53`](../reference/gate.md#D-GR53) and [`D-GR54`](../reference/gate.md#D-GR54)) is the owner's standing choice, with [`config/tillr/watch/fast-gate-slow.toml`](../../config/tillr/watch/fast-gate-slow.toml) remaining at `90000` ms (`90 s`) as Option C states and no separate choice recorded on the slow-gate alert threshold beyond what Option C states; the owner will decide again in the future once more operating data is collected. Every option framing, trade-off, measurement, and conditional threshold calculation below (`D-DFG1` in the [Decision Ledger](#decision-ledger)) remains recorded from an operator request as the designer's analysis rather than an owner ruling.

> **In short.** Running `github.com/mschulkind-oss/tillr-drain/drain`'s `~2,080` subprocess-heavy tests (`~4,800 s` of summed test-wall time; `83.0%`–`99.99%` of shard time in every >`20`-minute gate in [Measured `go-affected` timings (2026-11-25)](../reference/gate.md#measured-go-affected-timings-2026-11-25)) before the merge cannot clear the `90 s` `fast-gate-median-slow` alert even without contention (`10.75`–`17.18 min` post-priority-tuning, `~150 s` theoretical `32`-core solo floor), wastes passed ~`30`-minute gates whenever the trunk moves into a squash conflict, and drives load that fails unrelated tests. Four options trade pre-merge coverage against gate latency and wasted work: **A** keeps `drain` pre-merge and raises `fast-gate-median-slow` to `1,200 s`; **B** splits `drain`'s `18` pure unit tests (`2.94 s`) from its `2,077` integration tests (`3,633.40 s`) across `428` white-box files using `1,509` unexported symbols and keeps `90 s`; **C** proves `tillr-drain/drain` after the merge while keeping `go-build` and `8` consumer packages (`219` tests) before the merge and keeps `90 s` (noting that `tillr-view`'s `21` unsharded `build_tests` add a `302.60 s` cold tail unless its `3` Observatory front-end build tests are sharded or deferred); and **D** computes intra-package test reachability, which when sound still selects a median of `1,666` tests (`3,432.59 s`) on production `drain` commits and therefore still requires raising `fast-gate-median-slow` to `1,200 s`. On 2026-11-25 ([`OQ-DFG1`](#OQ-DFG1)), the owner ruled to keep **Option C** and decide again in the future once more data is collected.

- **Start at:** [Section 1](#1-problem-standing-rules-and-measurements) for the measured cost and standing rules, [Section 2](#2-what-the-after-merge-proof-and-automatic-revert-already-do) for what already happens when a failure is first caught after the merge, [Section 3](#3-the-four-options-compared) for the four options and their `fast-gate-median-slow` thresholds, and the [Decision Ledger](#decision-ledger) ([`OQ-DFG1`](#OQ-DFG1)) for the owner's 2026-11-25 ruling.
- **Needs your ruling:** None.
- **Reads with:** [the gate reference](../reference/gate.md) ([Measured `go-affected` timings (2026-11-25)](../reference/gate.md#measured-go-affected-timings-2026-11-25), [`D-GR38`](../reference/gate.md#D-GR38), [`D-GR39`](../reference/gate.md#D-GR39), [`D-GR53`](../reference/gate.md#D-GR53), [`D-GR54`](../reference/gate.md#D-GR54), [`D-GR56`](../reference/gate.md#D-GR56)), [gate test capacity](./gate-test-capacity.md), [landing principles](./landing-principles.md), [tillr-watch](./tillr-watch.md), and [authority, not approval](../reference/authority-not-approval.md).

---

## 1. Problem, standing rules, and measurements

### 1.1 Why a `tillr-drain` gate cannot finish under 90 seconds, and what slow gates cost

In [Measured `go-affected` timings (2026-11-25)](../reference/gate.md#measured-go-affected-timings-2026-11-25), `101` ordinary pre-merge `gate.timing.jsonl` records were inspected (`68` running `go-affected`, `52` exceeding `20` minutes / `1,200,000 ms`):

1. **`tillr-drain/drain` dominates every slow gate.** Across the `4` profiled >`20`-minute runs (`1,651,843 ms` / `27.53 min` to `2,299,493 ms` / `38.32 min`), `github.com/mschulkind-oss/tillr-drain/drain` accounted for **`83.0%`–`99.99%`** of total package shard time (`20,678.80 s` to `24,208.50 s` summed across `16` concurrent shards in [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py), with `0 ms` slot wait) ([Measured `go-affected` timings (2026-11-25)](../reference/gate.md#measured-go-affected-timings-2026-11-25)).
2. **Less processor contention cuts 30 minutes to 11–17 minutes, still 7x–11x above 90 seconds.** Before the `07:43 ET` process-priority change (`gate` `nice 5`, `proof` `nice 10`, `worker` `nice 15`), the median across `63` `go-affected` gates was **`1,606,590 ms` (`26.78 min`)**. Across the `5` gates started after `07:43 ET`, wall time on the same `8`-package and `3`-package selections fell from `1,782,531 ms` (`29.71 min`) to **`1,031,027 ms` (`17.18 min`)** and from `1,782,822 ms` (`29.71 min`) to **`645,131 ms` (`10.75 min`)** (`tillr-drain/drain` shard sum fell `4.30x` from `24,208.50 s` to `5,626.00 s`, eliminating `18,212.30 s` / `76.9%` of contention wait across `2,069` common tests) ([Measured `go-affected` timings (2026-11-25)](../reference/gate.md#measured-go-affected-timings-2026-11-25)).
3. **Even after fixing the three clock-waiting tests, the ~4,800-second test sum sets a >90-second floor.** Commit `e59c6e33` (2026-11-25) replaced real-clock sleeps in the `3` clock-waiting tests recorded at `302.43 s`, `112.09 s`, and `101.65 s` in [Measured `go-affected` timings (2026-11-25)](../reference/gate.md#measured-go-affected-timings-2026-11-25) (standalone runs after `e59c6e33`: `9.41 s`, `8.79 s`, and `14.92 s`, verified with `git show -s --format=%B e59c6e33` on 2026-11-25). Even so, the remaining `~2,080` subprocess-heavy tests in `tillr-drain/drain` sum **`~4,800 s`** of test-wall time — a **`~150 s`** minimum across `32` cores for one gate in complete isolation ([Measured `go-affected` timings (2026-11-25)](../reference/gate.md#measured-go-affected-timings-2026-11-25)), and `10.75`–`17.18 min` whenever workers, sibling gates, or a trunk proof share the host.
4. **`fast-gate-median-slow` stands continuously.** In [`config/tillr/watch/fast-gate-slow.toml`](../../config/tillr/watch/fast-gate-slow.toml), `fast-gate-median-slow` opens when `event.median20_wall_ms > 90000` (`90 s`) over a `24h` window and clears only when `event.median20_wall_ms <= 90000`, while `fast-gate-run-slow` opens when `event.wall_ms > 60000` (`60 s`) over a `1h` window (verified on 2026-11-25 with `rg -n 'wall_ms|window' config/tillr/watch/fast-gate-slow.toml`). In `medianFastGateTiming` ([`tools/tillr-drain/drain/gate_timing.go`](../../tools/tillr-drain/drain/gate_timing.go), verified on 2026-11-25 with `rg -n 'LIMIT 19|window\[9\]' tools/tillr-drain/drain/gate_timing.go`), `median20_wall_ms` is `(window[9] + window[10]) / 2` across the last `20` ordinary `drain.fast-gate-timing` events — so whenever `10` or more of the last `20` candidates exceed `90,000 ms`, `fast-gate-median-slow` cannot clear.
5. **Wasted passed gates and load-induced collateral failures.** Slow pre-merge gates cost more than latency:
   - **Conflict-discarded passed gates:** Each candidate's `gate.verdict` binds `(base_sha, head_sha^{tree})`, and `Trunk.Squash` in [`tools/tillr-land/land/trunk.go`](../../tools/tillr-land/land/trunk.go) three-way merges `base_sha..head_sha` onto `trunk_before` only after the gate finishes ([landing principles §2](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch) and [§3](./landing-principles.md#3-gate-what-will-be-the-tip-once)). When sibling lanes advance `main` into an overlapping hunk during a `25.1`–`38.32 min` gate ([Measured `go-affected` timings (2026-11-25)](../reference/gate.md#measured-go-affected-timings-2026-11-25); `17` squash conflicts in `10` hours across `29` unproved landings recorded in [`D-GR55`](../reference/gate.md#D-GR55), verified with `rg -n 'D-GR55' docs/reference/gate.md` on 2026-11-25), the passed gate is thrown away: `conflictFix` (`-cfix`) must rewrite `head_sha^{tree}` and run a fresh gate.
   - **Cross-lane load failures:** During a `5`-gate burst alongside a trunk proof on the `32`-processor host on 2026-11-25 (`82` Go test runners and `81` `drain` test binaries alive at once, `~1,700` new processes/s, load average `158`–`170`, verified on 2026-11-25 in [the gate batching research record](../research/gate-batching.md#operating-session-burst-measurement-2026-11-25)), CPU-bound tests stretched `5.2x`–`8.0x` ([Measured `go-affected` timings (2026-11-25)](../reference/gate.md#measured-go-affected-timings-2026-11-25)) and load caused unrelated timing-sensitive tests to fail across sibling gates and worker runs.

### 1.2 Why this is the owner's decision, and what is in the tree at `43455fc4`

Changing what `go-affected` checks before a merge changes the repository's pre-merge contract:

- **Pre-merge rule when the measurement was taken ([`D-GR38`](../reference/gate.md#D-GR38), [`D-GR39`](../reference/gate.md#D-GR39)):** On 2026-11-21 the owner ruled `"if you think drain suite selected tests are 40 seconds, let's do that, and monitor how it goes, just make sure we have perf monitoring all over it. 10 seconds was apsirational"` ([`D-GR38`](../reference/gate.md#D-GR38)), amending [`D-GR1`](../reference/gate.md#D-GR1)'s `10 s` target (`9,000 ms` `LimitMS` in [`tools/gate/gate_budget.go`](../../tools/gate/gate_budget.go), verified on 2026-11-25 with `rg -n 'LimitMS' tools/gate/gate_budget.go`). [`D-GR39`](../reference/gate.md#D-GR39) (designer, 2026-11-22) implemented `go-affected` outside that `9,000 ms` fast cap with Go's test cache on ([`D-GR40`](../reference/gate.md#D-GR40)), selecting changed packages, reverse test-import closures, declared `builds`/`reads` edges in [`tools/gate/go-test-edges.json`](../../tools/gate/go-test-edges.json), and whole-module floors.
- **Same-day build and owner ruling ([`D-GR53`](../reference/gate.md#D-GR53), [`D-GR54`](../reference/gate.md#D-GR54), [`OQ-DFG1`](#OQ-DFG1), [`D-GR56`](../reference/gate.md#D-GR56)):** Later on 2026-11-25, the owner answered `"sounds like a good idea. do it"` ([`D-GR53`](../reference/gate.md#D-GR53)) to deferring `tillr-drain`'s integration tests to the after-merge proof, and a designer decision from a separate request ([`D-GR54`](../reference/gate.md#D-GR54), commits `8d2558e3` and `5e728e5a`) added `DEFERRED_PROOF_PACKAGES = frozenset({"github.com/mschulkind-oss/tillr-drain/drain"})` to [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py) (**Option C** below) rather than splitting `drain` (**Option B**). After this document laid out all four options (**A**–**D**) and their `fast-gate-median-slow` implications, the owner ruled [`OQ-DFG1`](#OQ-DFG1) on 2026-11-25 (`"ro DFG1, let's stick wiht C. you think it's a good idea and it's not hurting. we'll collect more info and make another deciison in the future."`, [`D-GR56`](../reference/gate.md#D-GR56)), confirming **Option C** as the standing choice and leaving a future revisit once more data is collected, without recording a choice on the slow-gate alert threshold beyond what Option C states.
- **Unchanged across every option ([`OQ-GR3`](../reference/gate.md#OQ-GR3), [`D-GR50`](../reference/gate.md#D-GR50), [`I3`](../reference/gate.md#I3)):** Gate code (`tools/tillr-gate/` and `tools/gate/`) never lands on deferred proof; none of Options A–D defers `tools/tillr-gate` or `tools/gate` suites. Likewise, exceeding a budget never kills a making-progress check ([`D-GR15`](../reference/gate.md#D-GR15), [`R-GTC4`](./gate-test-capacity.md#R-GTC4)), and alerts never fire for expected steady-state conditions ([`D-TW73`](./tillr-watch.md#D-TW73)).

### 1.3 Tree-level measurements on `43455fc4` (2026-11-25)

Every repository-structure, selection, purity, white-box, intra-package reachability, and post-`5e728e5a` consumer wall-time number in [Section 3](#3-the-four-options-compared) was measured on 2026-11-25 against commit `43455fc4` (`linux/amd64`, `go1.27.3`) with the three commands below.

**Command 1 — Module, package, duration-table, and `go-affected` selection counts (measured 2026-11-25 at `43455fc4`, `19.56 s` wall time):**

```sh
python3 - <<'PY'
import json, pathlib, subprocess, sys
sys.path.insert(0, "tools/gate")
import go_affected

root = pathlib.Path(".").resolve()
mods = go_affected.discover_tillr_modules(root)
graph = go_affected.load_tillr_graph(root)
pkg_info = graph["pkg_info"]
edges = go_affected.load_edges(root)
durs = json.loads((root / "tools/go-test-durations.json").read_text())

test_pkgs, total_scan, other_drain_pkgs, other_drain_tests = [], 0, [], 0
for ip, info in sorted(pkg_info.items()):
    ts = [t for t in go_affected.scan_package_tests(root, info) if t != "TestEdgeInputs"]
    if ts:
        test_pkgs.append(ip)
        total_scan += len(ts)
        if info["module"] == "tools/tillr-drain" and ip != "github.com/mschulkind-oss/tillr-drain/drain":
            other_drain_pkgs.append(ip)
            other_drain_tests += len(ts)

drain_scan = [t for t in go_affected.scan_package_tests(root, pkg_info["github.com/mschulkind-oss/tillr-drain/drain"]) if t != "TestEdgeInputs"]
listed = [l.strip() for l in subprocess.check_output(["go", "-C", "tools/tillr-drain", "test", "./drain", "-list", "."], text=True).splitlines() if l.strip().startswith("Test") and l.strip() != "TestEdgeInputs"]
sel_prod = go_affected.select_affected_packages(root, ["tools/tillr-drain/drain/loop.go"], edges=edges, graph=graph)
sel_test = go_affected.select_affected_packages(root, ["tools/tillr-drain/drain/loop_test.go"], edges=edges, graph=graph)
sel_prod_tests = sum(len(x["selected_tests"] if x["selected_tests"] is not None else [t for t in go_affected.scan_package_tests(root, pkg_info[x["import_path"]]) if t != "TestEdgeInputs"]) for x in sel_prod["selected"])
sel_prod_dur = sum(sum(durs.get(x["import_path"], {}).get(t, 0.0) for t in (x["selected_tests"] if x["selected_tests"] is not None else [t for t in go_affected.scan_package_tests(root, pkg_info[x["import_path"]]) if t != "TestEdgeInputs"])) for x in sel_prod["selected"])
all_dur = sum(sum(v.values()) for v in durs.values())
drain_dur = sum(durs["github.com/mschulkind-oss/tillr-drain/drain"].values())
print(f"modules={len(mods)} test_pkgs={len(test_pkgs)} scan_tests={total_scan} drain_scan={len(drain_scan)} drain_go_list={len(listed)} non_drain_scan={total_scan - len(drain_scan)}")
print(f"other_drain_pkgs={len(other_drain_pkgs)} other_drain_tests={other_drain_tests}")
print(f"durations: pkgs={len(durs)} total={all_dur:.2f}s drain={drain_dur:.2f}s ({len(durs['github.com/mschulkind-oss/tillr-drain/drain'])} entries) non_drain={all_dur - drain_dur:.2f}s")
print(f"select(drain/loop.go): pkgs={len(sel_prod['selected'])} tests={sel_prod_tests} recorded_dur={sel_prod_dur:.2f}s")
print(f"select(drain/loop_test.go): pkgs={len(sel_test['selected'])}")
PY
```

Result on 2026-11-25 at `43455fc4`:

- **`13` tillr Go modules** holding **`50` Go test packages** and **`4,073` scanned top-level tests** (excluding `TestEdgeInputs`): `2,109` in `github.com/mschulkind-oss/tillr-drain/drain` (`2,095` executable `Test*` functions in `go -C tools/tillr-drain test ./drain -list .`, plus `14` `func Test...` fixture strings matched by regex in `scan_package_tests`) and **`1,964` tests across the other `49` Go test packages** — including **`15` non-`drain` test packages (`152` tests)** inside `tools/tillr-drain` (root `tools/tillr-drain` plus `14` leaf subpackages).
- In [`tools/go-test-durations.json`](../../tools/go-test-durations.json) (`25` packages, `3,841.86 s` total), `github.com/mschulkind-oss/tillr-drain/drain` holds **`1,842` entries summing `3,695.13 s` (`96.18%`)**, while all `24` other recorded packages combined sum **`146.73 s`**.
- With `tillr-drain/drain` deferred (`DEFERRED_PROOF_PACKAGES`), editing `tools/tillr-drain/drain/loop.go` still selects **`8` pre-merge packages (`219` tests, `22.58 s` recorded duration in `tools/go-test-durations.json`)**: reverse test importers `tillr-drain` (`21` tests), `tillr-store` (`37`), and `tillr-watch` (`111`); `builds` consumers `tillr-drain/forkgate` (`26`), narrowed `tillr-item` (`1`), and narrowed `tillr-view` (`21`); and narrowed `reads` source scanners `tillr/definition` (`1`) and `tillr/store` (`1`). Editing `tools/tillr-drain/drain/loop_test.go` alone selects **`2` packages (`2` narrowed source-scanning tests, `0.00 s` recorded duration)**.

**Command 2 — `go/types` + Class Hierarchy Analysis (CHA) purity, white-box, and intra-package reachability inside `tools/tillr-drain/drain` (measured 2026-11-25 at `43455fc4`, `18.40 s` wall time):**

```sh
python3 - <<'PY'
import pathlib, subprocess, tempfile
with tempfile.TemporaryDirectory() as td:
    src = pathlib.Path(td) / "analyze.go"
    src.write_text(r'''package main
import ("bytes"; "encoding/json"; "fmt"; "go/ast"; "go/importer"; "go/parser"; "go/token"; "go/types"; "io"; "os"; "os/exec"; "path/filepath"; "sort"; "strings")
type lp struct{ ImportPath, Export string; GoFiles, TestGoFiles, XTestGoFiles []string }
type ci struct{ base types.Importer; own map[string]*types.Package }
func (c *ci) Import(p string) (*types.Package, error) { if x, ok := c.own[p]; ok { return x, nil }; return c.base.Import(p) }
func isTest(fn *ast.FuncDecl) bool {
  if fn.Recv != nil || fn.Name == nil { return false }; n := fn.Name.Name
  if n == "TestMain" || n == "TestEdgeInputs" || !strings.HasPrefix(n, "Test") { return false }
  if len(n) > 4 && n[4] >= 'a' && n[4] <= 'z' { return false }
  return fn.Type.Params != nil && len(fn.Type.Params.List) == 1
}
func unexpRecv(fl *ast.FieldList) bool {
  if fl == nil || len(fl.List) == 0 { return false }; t := fl.List[0].Type
  if s, ok := t.(*ast.StarExpr); ok { t = s.X }; id, ok := t.(*ast.Ident); return ok && !id.IsExported()
}
func main() {
  out, _ := exec.Command("go", "-C", "tools/tillr-drain", "list", "-test", "-deps", "-export", "-json", "./drain").Output()
  dec, exp := json.NewDecoder(bytes.NewReader(out)), map[string]string{}
  for { var p lp; if dec.Decode(&p) != nil { break }; if p.Export != "" { exp[p.ImportPath] = p.Export } }
  var bp lp; out2, _ := exec.Command("go", "-C", "tools/tillr-drain", "list", "-json", "./drain").Output(); _ = json.Unmarshal(out2, &bp)
  fset, fb := token.NewFileSet(), map[*ast.File]string{}
  parse := func(ns []string) (fs []*ast.File) { for _, n := range ns { f, _ := parser.ParseFile(fset, filepath.Join("tools/tillr-drain/drain", n), nil, 0); fb[f] = n; fs = append(fs, f) }; return }
  pf, wf, xf := parse(bp.GoFiles), parse(bp.TestGoFiles), parse(bp.XTestGoFiles)
  imp := &ci{base: importer.ForCompiler(fset, "gc", func(p string) (io.ReadCloser, error) { return os.Open(exp[p]) }), own: map[string]*types.Package{}}
  iw := &types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
  pkgW, err := (&types.Config{Importer: imp}).Check("github.com/mschulkind-oss/tillr-drain/drain", fset, append(append([]*ast.File{}, pf...), wf...), iw); if err != nil { panic(err) }
  imp.own["github.com/mschulkind-oss/tillr-drain/drain"] = pkgW
  ix := &types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
  if _, err := (&types.Config{Importer: imp}).Check("github.com/mschulkind-oss/tillr-drain/drain_test", fset, xf, ix); err != nil { panic(err) }
  var allD map[string]map[string]float64; raw, _ := os.ReadFile("tools/go-test-durations.json"); _ = json.Unmarshal(raw, &allD); durs := allD["github.com/mschulkind-oss/tillr-drain/drain"]
  of, po, uo := map[types.Object]string{}, map[types.Object]bool{}, map[types.Object]bool{}
  rec := func(fs []*ast.File, inf *types.Info, pr bool) {
    for _, f := range fs { fn := fb[f]; for _, d := range f.Decls { switch x := d.(type) {
      case *ast.FuncDecl: if o := inf.Defs[x.Name]; o != nil { of[o] = fn; if pr { po[o] = true; if !x.Name.IsExported() || unexpRecv(x.Recv) { uo[o] = true } } }
      case *ast.GenDecl: for _, sp := range x.Specs { switch s := sp.(type) {
        case *ast.TypeSpec: if o := inf.Defs[s.Name]; o != nil { of[o] = fn; if pr { po[o] = true; if !s.Name.IsExported() { uo[o] = true } } }
          if st, ok := s.Type.(*ast.StructType); ok && st.Fields != nil { for _, fl := range st.Fields.List { for _, id := range fl.Names { if fo := inf.Defs[id]; fo != nil { of[fo] = fn; if pr { po[fo] = true; if !id.IsExported() || !s.Name.IsExported() { uo[fo] = true } } } } } }
        case *ast.ValueSpec: for _, id := range s.Names { if o := inf.Defs[id]; o != nil { of[o] = fn; if pr { po[o] = true; if !id.IsExported() { uo[o] = true } } } }
      } }
    } } }
  }
  rec(pf, iw, true); rec(wf, iw, false); rec(xf, ix, false)
  inits, pvars, ifaces, ffields := 0, 0, 0, 0
  for _, f := range pf { for _, d := range f.Decls { switch x := d.(type) {
    case *ast.FuncDecl: if x.Recv == nil && x.Name.Name == "init" { inits++ }
    case *ast.GenDecl: if x.Tok == token.VAR { for _, s := range x.Specs { pvars += len(s.(*ast.ValueSpec).Names) } }
      if x.Tok == token.TYPE { for _, s := range x.Specs { switch st := s.(*ast.TypeSpec).Type.(type) {
        case *ast.InterfaceType: ifaces++
        case *ast.StructType: if st.Fields != nil { for _, fl := range st.Fields.List { if _, ok := fl.Type.(*ast.FuncType); ok { if len(fl.Names) > 0 { ffields += len(fl.Names) } else { ffields++ } } } }
      } } }
  } } }
  edges, impD := map[types.Object]map[types.Object]bool{}, map[types.Object]bool{}
  addE := func(a, b types.Object) { if a != nil && b != nil && a != b && of[b] != "" { if edges[a] == nil { edges[a] = map[types.Object]bool{} }; edges[a][b] = true } }
  markI := func(o types.Object, u types.Object) {
    if u == nil || u.Pkg() == nil { return }; pp := u.Pkg().Path()
    if pp == "os/exec" || pp == "net" || pp == "net/http" || pp == "net/http/httptest" || pp == "database/sql" || strings.HasSuffix(pp, "/store") || strings.HasSuffix(pp, "/land") || strings.HasSuffix(pp, "/run") || (pp == "os" && (u.Name() == "MkdirTemp" || u.Name() == "Create" || u.Name() == "WriteFile" || u.Name() == "OpenFile" || u.Name() == "RemoveAll")) || (pp == "testing" && u.Name() == "TempDir") { impD[o] = true }
  }
  walk := func(fs []*ast.File, inf *types.Info) {
    for _, f := range fs { for _, d := range f.Decls {
      visit := func(o types.Object, root ast.Node) { if o == nil { return }; ast.Inspect(root, func(n ast.Node) bool {
        if id, ok := n.(*ast.Ident); ok { markI(o, inf.Uses[id]); addE(o, inf.Uses[id]) }
        if se, ok := n.(*ast.SelectorExpr); ok { if s := inf.Selections[se]; s != nil { markI(o, s.Obj()); addE(o, s.Obj()) } }
        return true
      }) }
      switch x := d.(type) {
      case *ast.FuncDecl: visit(inf.Defs[x.Name], x)
      case *ast.GenDecl: for _, sp := range x.Specs { switch s := sp.(type) {
        case *ast.ValueSpec: for _, id := range s.Names { visit(inf.Defs[id], s) }
        case *ast.TypeSpec: visit(inf.Defs[s.Name], s)
      } }
      }
    } }
  }
  walk(pf, iw); walk(wf, iw); walk(xf, ix)
  var nts []*types.Named; var ims []*types.Func
  for _, nm := range pkgW.Scope().Names() { if tn, ok := pkgW.Scope().Lookup(nm).(*types.TypeName); ok { if nt, ok := tn.Type().(*types.Named); ok {
    if ifc, ok := nt.Underlying().(*types.Interface); ok { for i := 0; i < ifc.NumMethods(); i++ { m := ifc.Method(i); if of[m] == "" { of[m] = of[tn] }; ims = append(ims, m) } } else { nts = append(nts, nt) }
  } } }
  cha := 0
  for _, im := range ims { for _, nt := range nts { for _, t := range []types.Type{nt, types.NewPointer(nt)} {
    if o, _, _ := types.LookupFieldOrMethod(t, false, pkgW, im.Name()); o != nil { if fn, ok := o.(*types.Func); ok && types.Identical(fn.Type(), im.Type()) { addE(im, fn); cha++ } }
  } } }
  wbUF, wbUS := map[string]bool{}, map[types.Object]bool{}
  for _, f := range wf { fn := fb[f]; ast.Inspect(f, func(n ast.Node) bool {
    if id, ok := n.(*ast.Ident); ok && uo[iw.Uses[id]] { wbUF[fn] = true; wbUS[iw.Uses[id]] = true }
    if se, ok := n.(*ast.SelectorExpr); ok && iw.Selections[se] != nil && uo[iw.Selections[se].Obj()] { wbUF[fn] = true; wbUS[iw.Selections[se].Obj()] = true }
    return true
  }) }
  type tr struct{ name, file, pkg string; obj types.Object }
  var ts []tr
  for _, f := range wf { for _, d := range f.Decls { if fn, ok := d.(*ast.FuncDecl); ok && isTest(fn) { ts = append(ts, tr{fn.Name.Name, fb[f], "drain", iw.Defs[fn.Name]}) } } }
  for _, f := range xf { for _, d := range f.Decls { if fn, ok := d.(*ast.FuncDecl); ok && isTest(fn) { ts = append(ts, tr{fn.Name.Name, fb[f], "drain_test", ix.Defs[fn.Name]}) } } }
  ft, impF := map[string][]int{}, map[string]bool{}
  for i, t := range ts {
    vis, q, pfs, im := map[types.Object]bool{t.obj: true}, []types.Object{t.obj}, map[string]bool{}, impD[t.obj]
    for len(q) > 0 { c := q[0]; q = q[1:]; if impD[c] { im = true }; if po[c] { pfs[of[c]] = true }; for nx := range edges[c] { if !vis[nx] { vis[nx] = true; q = append(q, nx) } } }
    if im { impF[t.file] = true }; for p := range pfs { ft[p] = append(ft[p], i) }
  }
  pureF := map[string]bool{}; for _, t := range ts { if !impF[t.file] { pureF[t.file] = true } }
  pTests, iTests, wbIF, wbIUF := 0, 0, 0, 0; pDur, iDur := 0.0, 0.0
  for _, t := range ts { if pureF[t.file] { pTests++; pDur += durs[t.name] } else { iTests++; iDur += durs[t.name] } }
  for _, f := range wf { fn := fb[f]; if impF[fn] { wbIF++; if wbUF[fn] { wbIUF++ } } }
  var dlist []float64; var tlist []int; over90, over500 := 0, 0
  for _, f := range pf { idx := ft[fb[f]]; s := 0.0; for _, i := range idx { s += durs[ts[i].name] }; dlist = append(dlist, s); tlist = append(tlist, len(idx)); if s > 90 { over90++ }; if len(idx) >= 500 { over500++ } }
  sort.Float64s(dlist); sort.Ints(tlist)
  lo, _ := exec.Command("git", "log", "-n", "200", "--format=%h", "43455fc4", "--", "tools/tillr-drain/drain/").Output()
  var cd []float64; var ct []int; over800 := 0
  for _, sha := range strings.Fields(string(lo)) {
    if len(cd) >= 20 { break }
    do, _ := exec.Command("git", "diff-tree", "--no-commit-id", "--name-only", "-r", sha, "--", "tools/tillr-drain/drain/").Output()
    u, hit := map[int]bool{}, false
    for _, p := range strings.Fields(string(do)) { b := filepath.Base(p); if strings.HasSuffix(b, ".go") && !strings.HasSuffix(b, "_test.go") { if _, ok := ft[b]; ok { hit = true; for _, i := range ft[b] { u[i] = true } } } }
    if !hit { continue }; s := 0.0; for i := range u { s += durs[ts[i].name] }; cd = append(cd, s); ct = append(ct, len(u)); if len(u) >= 837 { over800++ }
  }
  sort.Float64s(cd); sort.Ints(ct)
  fmt.Printf("files: prod=%d wb=%d bb=%d tests=%d | hazards: init=%d vars=%d ifaces=%d func_fields=%d cha=%d\n", len(pf), len(wf), len(xf), len(ts), inits, pvars, ifaces, ffields, cha)
  fmt.Printf("purity: pure_files=%d (%d tests, %.2fs) integ_files=%d (%d tests, %.2fs) wb_integ=%d wb_integ_unexp=%d unexp_syms=%d\n", len(pureF), pTests, pDur, len(impF), iTests, iDur, wbIF, wbIUF, len(wbUS))
  fmt.Printf("per_file: over90s=%d/%d over500t=%d/%d med_tests=%d med_dur=%.2fs p75_dur=%.2fs max_dur=%.2fs\n", over90, len(pf), over500, len(pf), tlist[len(pf)/2], dlist[len(pf)/2], dlist[len(pf)*75/100], dlist[len(pf)-1])
  fmt.Printf("last20_commits: over837=%d/20 min=%dt/%.2fs next=%dt/%.2fs med=%dt/%.2fs max=%dt/%.2fs\n", over800, ct[0], cd[0], ct[1], cd[1], (ct[9]+ct[10])/2, (cd[9]+cd[10])/2, ct[19], cd[19])
}
''')
    print(subprocess.check_output(["go", "run", str(src)], text=True).strip())
PY
```

Result on 2026-11-25 at `43455fc4`:

- **Files and tests (`linux/amd64`):** `249` production `GoFiles`, `471` `package drain` `TestGoFiles`, and `108` `package drain_test` `XTestGoFiles` (`579` `*_test.go` files total: `571` containing top-level `Test*` functions and `8` helper-only files), holding **`2,095` executable top-level `Test*` functions** whose current names sum **`3,636.34 s`** in [`tools/go-test-durations.json`](../../tools/go-test-durations.json) (the remaining `58.79 s` of the `3,695.13 s` package table belongs to moved or renamed historical entries such as `TestForkBuildCommandContracts`, moved to `forkgate` in `f17d0c36`).
- **Pure unit vs. integration split (including CHA interface edges):** Only **`12` test files (`18` tests, `2.94 s` recorded duration — `0.08%` of `3,636.34 s`)** are transitively pure in-memory unit-test files; the other **`559` test files (`2,077` tests, `3,633.40 s` — `99.92%`)** transitively invoke subprocesses (`os/exec`), SQLite/store/land/run packages, network sockets, or temporary directories (`t.TempDir` / `os.MkdirTemp`). Of those `559` integration files, **`455` (`1,718` tests)** are white-box `package drain` files, and **`428` of those `455`** directly reference unexported `package drain` symbols (**`1,509` distinct unexported `package drain` declarations** across functions, methods, types, struct fields, variables, and constants across `435` white-box files).
- **Intra-package reachability and soundness hazards:** The `249` production `GoFiles` declare **`48` `init()` functions**, **`155` package-level `var`s**, **`17` interfaces** (`1,867` CHA interface-to-concrete method edges), and **`34` `func`-typed struct fields**. Even before accounting for `init()`, `var` initializers, `func`-typed struct callbacks, reflection, or subprocess `execve` CLI calls, static `go/types` + CHA reachability connects **`240` of `249` production files (`96.4%`)** to `> 90 s` of recorded `drain` test time and **`231` of `249` (`92.8%`)** to `>= 500` tests (median file: `1,197` tests / `3,112.49 s`; p75: `3,323.61 s`; max: `3,511.00 s`). Across the **last `20` commits on `main` touching non-test `tools/tillr-drain/drain/*.go`**, that graph selects a **median of `1,666` tests and `3,432.59 s` (`94.4%` of `3,636.34 s`)**, with `19` of `20` commits (`95%`) selecting `>= 837` tests and `>= 2,875.95 s` (min `223` tests / `715.43 s` in `c0442249`; second-smallest `837` tests / `2,875.95 s` in `d7595176`; max `1,770` tests in `f9b24d2c` and `3,511.00 s` in `f206384d`).

**Command 3 — Post-`5e728e5a` consumer `go-affected` and `go-build` wall times (measured 2026-11-25 at `43455fc4`):**

```sh
python3 - <<'PY'
import subprocess, time
for label, cmd in [
    ("go-affected(drain/loop.go)", ["python3", "tools/gate/go_affected.py", "--changed", "tools/tillr-drain/drain/loop.go"]),
    ("go-affected(drain/loop_test.go)", ["python3", "tools/gate/go_affected.py", "--changed", "tools/tillr-drain/drain/loop_test.go"]),
    ("go-build", ["tools/gate/repo-gate", "go-build"]),
]:
    t0 = time.monotonic()
    res = subprocess.run(cmd, capture_output=True, text=True)
    print(f"{label}: rc={res.returncode} wall={time.monotonic() - t0:.2f}s")
PY
```

Result on 2026-11-25 at `43455fc4` (with `tillr-drain/drain` in `DEFERRED_PROOF_PACKAGES`):

- `go-affected` on `tools/tillr-drain/drain/loop_test.go`: **`4.55 s`** (`2` narrowed source-scanning tests).
- `tools/gate/repo-gate go-build`: **`2.49 s`** with remembered module builds.
- `go-affected` on `tools/tillr-drain/drain/loop.go`: **`19.02 s` when Go's test cache hits** (`7,740 ms` selection + `8` cached packages), and **`302.60 s` (`5.04 min`) on a cold `TILLR_GO_TEST_EDGE_DIGEST`**. In that `302.60 s` uncached run, `7` of the `8` consumer packages finished within **`53.00 s`** (`tillr-store` `53.00 s` sum, `forkgate` `42.60 s`, `tillr-watch` `5` shards `<= 20.00 s` each, `tillr-drain` `9.50 s`, `tillr-item` `4.35 s`, `tillr/definition` `2.08 s`, `tillr/store` `0.94 s`), while `github.com/mschulkind-oss/tillr-view` ran **`21` narrowed `build_tests` serially in one shard** (`21 < SHARD_THRESHOLD = 100` in [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py), verified on 2026-11-25 with `rg -n 'SHARD_THRESHOLD' tools/gate/go_affected.py`), `15` of which have no entry in [`tools/go-test-durations.json`](../../tools/go-test-durations.json) — led by three npm/Vite Observatory launcher-build tests (`TestShippedLauncherBuildsObservatoryWhenToolchainPresent`: **`81.44 s`**, `TestViewRestartThroughTheShippedLauncherMovesOntoChangedSource`: **`76.72 s`**, and `TestShippedLauncherWithoutNodeWarnsAtBuildTimeAndKeepsNoFrontEndNote`: **`23.74 s`** — `181.90 s` across `3` tests). That explains why in [Measured `go-affected` timings (2026-11-25)](../reference/gate.md#measured-go-affected-timings-2026-11-25) `da2e75c68f-a1-n1` (`3` packages, a `drain/*_test.go` edit) had only `3.28 s` of non-`drain` shard time (`99.99%` `drain`), whereas `7e8ca65b86-a1-n1` (`8` packages, a `drain/*.go` edit) had `17.0%` non-`drain` shard time.

---

## 2. What the after-merge proof and automatic revert already do

Whenever an option defers part or all of `tillr-drain/drain` out of pre-merge `go-affected` (Options **B**, **C**, and any narrowed variant of **D**), five built mechanisms in [After-merge proof and the green pointer](../reference/gate.md#after-merge-proof-and-the-green-pointer), [A red proof](../reference/gate.md#a-red-proof), and [The trunk fixer's gate](../reference/gate.md#the-trunk-fixers-gate) already govern any failure that lands on `main`:

1. **Coalesced after-merge execution (`go-test-tillr-drain`).** Under `landing_strategy: after-merge` ([`config/tillr/gate-policy.json`](../../config/tillr/gate-policy.json), [`D-GR4`](../reference/gate.md#D-GR4)), `HeavyRunner` runs `tillr-gate checks --heavy-tree` in a detached checkout of the trunk tip at `proof` priority (`nice 5` / `cpu.weight 200` under [`OQ-GTC3`](./gate-test-capacity.md#OQ-GTC3), as amended by [`OQ-GR7`](../reference/gate.md#OQ-GR7)). [`tools/gate/checks/go-test-tillr-drain.json`](../../tools/gate/checks/go-test-tillr-drain.json) runs the sharded `tools/tillr-drain` Go suite (`go-test-shards.py --cache`, [`D-GR49`](../reference/gate.md#D-GR49)), and its `reads` cover `tools/tillr-drain/`, every sibling `builds` module and transitive `replace` dependency, and every non-Go `reads` rule declared for `tillr-drain/drain` in [`tools/gate/go-test-edges.json`](../../tools/gate/go-test-edges.json) so result memory ([`D-GR8`](../reference/gate.md#D-GR8)) never reuses a stale pass. Landings that arrive while a proof is running coalesce into one proof on the newest tip.
2. **Broken `tillr-drain` code never deploys into the live drain ([`D-GR5`](../reference/gate.md#D-GR5)).** `refs/tillr/green` moves only when the entire after-merge proof passes, and `tillr` builds, restarts, and deploys exclusively from `refs/tillr/green`. A `tillr-drain/drain` regression that slips past the fast gate onto `main` never replaces the running `tillr-drain loop` binary.
3. **Single-landing culprit search and automatic revert ([`D-GR6`](../reference/gate.md#D-GR6), [`D-GR31`](../reference/gate.md#D-GR31)).** When `go-test-tillr-drain` fails on the tip, `HeavyRunner` searches the landings since `refs/tillr/green` with `--heavy-tree --only go-test-tillr-drain` (skipping focused single-test replay only when the first proof recorded a Go timeout panic or runtime deadlock per [`D-GR31`](../reference/gate.md#D-GR31)). Because `auto_revert: true` is in force ([`config/tillr/gate-policy.json`](../../config/tillr/gate-policy.json), [`D-GR6`](../reference/gate.md#D-GR6)), the drain automatically commits a revert of the offending landing on `main` and returns the failure log and triage cases to the worker session that wrote the candidate.
4. **Trunk fixer fallback when a revert cannot settle the red ([`D-GR6`](../reference/gate.md#D-GR6), [`D-FG1`](../reference/gate.md#D-FG1)).** If a later landing on `main` modified overlapping lines so the automatic revert conflicts, if reverting does not turn `go-test-tillr-drain` green, or if the failure cannot be attributed to one commit (including timeout/deadlock runs under [`D-GR31`](../reference/gate.md#D-GR31)), the drain files one `fixer-main-<sha>` item at `repair` priority (highest processor share under [`OQ-GTC3`](./gate-test-capacity.md#OQ-GTC3)). Unlike ordinary candidates, a trunk fixer's pre-merge gate (`settleFixerGate` in [`tools/tillr-drain/drain/fixer_gate.go`](../../tools/tillr-drain/drain/fixer_gate.go)) runs `fixer-gate --only go-test-tillr-drain` **before** the fixer merges, while unrelated candidates whose diffs do not touch `go-test-tillr-drain`'s read paths continue landing with the failure recorded as `inherited` ([`OQ-GR1`](../reference/gate.md#OQ-GR1)).
5. **Durable heavy-miss accounting and residual exposure window ([`D-GR26`](../reference/gate.md#D-GR26), [`D-GR55`](../reference/gate.md#D-GR55)).** `tillr view heavy-misses` records every `confirmed` and `offered` fast-gate pass later caught by the heavy proof. The residual cost of catching a `tillr-drain/drain` failure after the merge rather than before is bounded to the window of one proof + culprit search: during that window under `lane_base: tip` ([`D-GR55`](../reference/gate.md#D-GR55)), newly dispatched lanes cut from the unproved tip (with the brief's unproved-base warning to report unrelated red checks as `OBSTACLE: trunk`), and if another `tools/tillr-drain` landing touches the same lines before `auto_revert` runs, recovery escalates from a clean revert to `fixer-main-<sha>`. Conversely, a contradicted check is automatically `disabled` with a `flaky-<check-id>` item ([`D-GR21`](../reference/gate.md#D-GR21)).

---

## 3. The four options compared

*Provenance: Options **A**, **B**, **C**, and **D** below were prepared as the designer's options from an operator request (`D-DFG1`); on 2026-11-25 the owner ruled [`OQ-DFG1`](#OQ-DFG1) to stick with **Option C** and decide again in the future once more data is collected, recording no choice on the slow-gate alert threshold beyond what Option C states.*

| Option | What runs before the merge on a `tools/tillr-drain` change | Measured pre-merge gate cost | What is **first seen after the merge** instead of before | What `fast-gate-median-slow` (`90 s` today) becomes |
| :--- | :--- | :--- | :--- | :--- |
| **A. Keep `D-GR38`/`D-GR39` (`drain` pre-merge) and raise the alert threshold** | Fast checks + `go-build` + full `go-affected` including all `2,095` `tillr-drain/drain` tests (`16` shards) + consumer packages | **`10.75`–`17.18 min`** post-`07:43 ET` (`26.78 min` median pre-`07:43 ET`; `~150 s` solo `32`-core floor) ([Measured `go-affected` timings (2026-11-25)](../reference/gate.md#measured-go-affected-timings-2026-11-25)); discards `25.1`–`38.32 min` gates on squash conflict | Nothing newly deferred relative to `D-GR38`/`D-GR39` (only existing heavy tree checks outside `go-affected`, such as `tillr-docs`) | **Raises from `90 s` to `1,200 s` (`1,200,000 ms` / `20 min`)** (and raises or scopes `fast-gate-run-slow` from `60 s`) |
| **B. Split `tillr-drain/drain`: pure unit tests pre-merge, integration tests after-merge** | Fast checks + `go-build` (compiles all unit + integration tests) + `18` pure `drain` unit tests (`2.94 s`) + `8` consumer packages (`219` tests) | **`19.02 s` cached / `302.60 s` cold** on `drain/*.go` (`<= 53.00 s` across `7` consumer packages + `2.94 s` unit tests once `tillr-view`'s `3` `81.44 s`/`76.72 s`/`23.74 s` launcher tests are sharded/deferred; [Command 2 & 3, 2026-11-25](#13-tree-level-measurements-on-43455fc4-2026-11-25)), plus exporting `<= 1,509` symbols across `428` white-box files | Behavioral regressions in `drain`'s **`559` integration test files (`2,077` tests, `3,633.40 s`)** — subprocesses, Git worktrees, SQLite WAL/locks, detached supervision, restarts, multi-lane coordination | **Stays at `90 s` (`90,000 ms`)** (paired with sharding/deferring `tillr-view`'s `3` launcher-build tests; `360 s` / `360,000 ms` if `tillr-view` stays unsharded during `>= 10`-of-`20` `drain/*.go` bursts) |
| **C. Prove all of `tillr-drain/drain` after the merge (`DEFERRED_PROOF_PACKAGES`)** *(chosen in [`OQ-DFG1`](#OQ-DFG1); built in `5e728e5a` via `D-GR54`)* | Fast checks + `go-build` (compiles all `249` `drain/*.go` and `579` `drain/*_test.go` files) + `8` non-`drain` consumer packages (`219` tests on `drain/*.go`; `2` tests on `drain/*_test.go`) + any touched leaf subpackage (`15` pkgs, `152` tests) | **`4.55 s`** on `drain/*_test.go`; **`19.02 s` cached / `302.60 s` cold** on `drain/*.go` (`<= 53.00 s` across `7` consumer packages once `tillr-view`'s `3` launcher tests are sharded/deferred; [Command 3, 2026-11-25](#13-tree-level-measurements-on-43455fc4-2026-11-25)), zero `drain` source refactor | Everything in Option B (`2,077` integration tests, `3,633.40 s`) **plus** `drain`'s **`12` pure unit-test files (`18` tests, `2.94 s`)** when neither `go-build` nor the `219` consumer tests fail | **Stays at `90 s` (`90,000 ms`)** (paired with sharding/deferring `tillr-view`'s `3` launcher-build tests; `360 s` / `360,000 ms` if `tillr-view` stays unsharded during `>= 10`-of-`20` `drain/*.go` bursts) |
| **D. Intra-package reachability inside `tillr-drain/drain`** | Fast checks + `go-build` + `8` consumer packages + only `Test*` functions in `drain` transitively reaching changed declarations | **Sound (`go/types` + CHA, `18.40 s` analysis):** median **`1,666` tests (`3,432.59 s`, `94.4%` of `drain`)** across last `20` `drain/*.go` commits (`0%` `drain` test-cache reuse; `19/20` commits `>= 2,875.95 s`, [Command 2, 2026-11-25](#13-tree-level-measurements-on-43455fc4-2026-11-25)) -> `79.1%`–`96.6%` of Option A's `drain` shard work | **Sound:** only unmodeled reflection/SQL/env/CLI couplings if startup/`builds` paths widen to all `2,095` tests.<br>**Unsound cutoff:** regressions across `17` interfaces (`1,867` CHA edges), `34` `func` fields, `48` `init()`s, `155` `var`s, and `Loop` callers beyond cutoff | **Sound: raises from `90 s` to `1,200 s` (`1,200,000 ms`)** (`95%` of recent `drain/*.go` commits select `>= 2,875.95 s`).<br>*(`90 s` holds only if narrowed unsoundly)* |

### 3.1 Option A — Keep `D-GR38`/`D-GR39` (`tillr-drain/drain` before the merge) and raise the alert threshold

- **Mechanism:** Revert `5e728e5a` (`DEFERRED_PROOF_PACKAGES` in [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py)) so `select_affected_packages` again selects `github.com/mschulkind-oss/tillr-drain/drain` on direct changes, reverse imports, `builds`/`reads` edges, and `go.mod` floors, and raise the slow-gate thresholds in [`config/tillr/watch/fast-gate-slow.toml`](../../config/tillr/watch/fast-gate-slow.toml).
- **Measured cost:**
  - **Wall time per `tillr-drain` gate:** **`645,131 ms` (`10.75 min`) to `1,031,027 ms` (`17.18 min`)** post-`07:43 ET` priority weighting, **`1,606,590 ms` (`26.78 min`)** median pre-`07:43 ET` (`52` of `101` gates over `20 min`), and a **`~150 s`** theoretical minimum on an otherwise idle `32`-core host for `~4,800 s` of test-wall time ([Measured `go-affected` timings (2026-11-25)](../reference/gate.md#measured-go-affected-timings-2026-11-25)). Under a `5`-gate burst beside a trunk proof, gates take `25.1`–`38.32 min` at load average `158`–`170` ([gate batching research record](../research/gate-batching.md#operating-session-burst-measurement-2026-11-25)).
  - **Throughput and wasted work:** Because a `10.75`–`26.78 min` gate keeps the candidate's base aging while sibling lanes merge, overlapping edits discard already-passed gates at `Trunk.Squash` (`-cfix` must re-gate from scratch), and concurrent `16`-shard `drain` runs saturate CPU and spawn `~1,700` processes/s ([gate batching research record](../research/gate-batching.md#operating-session-burst-measurement-2026-11-25)). Against [landing principle §3](./landing-principles.md#3-gate-what-will-be-the-tip-once) and [`D-GR2`](../reference/gate.md#D-GR2), pre-merge cost scales poorly with concurrent trunk activity.
- **What failure is first seen after the merge instead of before:** None beyond checks already in the heavy tier (`tillr-docs`, heavy Python suites). Every `tillr-drain/drain` unit and integration failure is caught before the merge; the after-merge proof (`go-test-tillr-drain`) mostly hits remembered passes or confirms cross-candidate merge interactions (`gated_tree != landed_tree` after three-way squash).
- **What `fast-gate-median-slow` (`90 s`) becomes:** Must rise from `90 s` (`90000 ms`) to **`1,200 s` (`1200000 ms`, `20 minutes`)** (`match = 'event.median20_wall_ms > 1200000'`, `clear_when = 'event.median20_wall_ms <= 1200000'`), sitting above the `10.75`–`17.18 min` post-`07:43 ET` operating range so normal `tillr-drain` work does not trigger constant expected-condition alerts ([`D-TW73`](./tillr-watch.md#D-TW73)) while still catching regression back to `25.1`–`38.32 min` contention stalls. (`fast-gate-run-slow`, currently `60 s` / `60000 ms`, would also have to rise to `1,200 s` or exclude `slowest_check == "go-affected"` so it does not fire on every single `tillr-drain` run; raising the global median threshold to `1,200 s` blinds `fast-gate-median-slow` to non-`drain` fast-gate regressions between `90 s` and `20 min`.)

### 3.2 Option B — Split `tillr-drain/drain` so fast unit tests stay before the merge and subprocess integration tests are proved after it

- **Mechanism:** Keep pure in-memory unit tests in `tools/tillr-drain/drain` (selected by pre-merge `go-affected`), move subprocess/Git/SQLite/filesystem integration tests into a separate package (for example `tools/tillr-drain/draininteg`), exclude `github.com/mschulkind-oss/tillr-drain/draininteg` via `DEFERRED_PROOF_PACKAGES` in [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py) (required because `select_affected_packages` otherwise still selects `draininteg` via reverse test imports from `drain`, declared edges, and `go.mod` floors), and prove `draininteg` after the merge in `go-test-tillr-drain`.
- **Measured cost ([Command 1, 2 & 3, 2026-11-25](#13-tree-level-measurements-on-43455fc4-2026-11-25)):**
  - **Pre-merge gate cost:** `go-affected` on a `drain/*.go` edit runs `drain`'s `18` pure-file unit tests (`2.94 s` recorded duration) concurrently alongside the `8` consumer packages (`219` tests: `19.02 s` cached, or `302.60 s` uncached today where `7` packages finish within `53.00 s` and `tillr-view`'s `21` unsharded `build_tests` add `181.90 s` across `3` Observatory launcher-build tests), plus `2.49 s` remembered `go-build` (which still compiles `draininteg` via `go test -exec true ./...` in [`tools/gate/go_build.go`](../../tools/gate/go_build.go)) and `<= 9.00 s` fast checks.
  - **Refactor and API-surface cost:** In `tools/tillr-drain/drain` at `43455fc4`, **`559` of `571` test-bearing files (`2,077` of `2,095` tests, `3,633.40 s` — `99.92%` of recorded duration)** are integration test files and only **`12` files (`18` tests, `2.94 s`)** are pure unit-test files ([Command 2, 2026-11-25](#13-tree-level-measurements-on-43455fc4-2026-11-25)). **`455` of those `559` integration files (`1,718` tests)** are white-box `package drain` files, and **`428` files directly reference `1,509` distinct unexported `package drain` symbols**. Moving all `559` integration files into a separate Go package requires exporting up to `1,509` internal functions, methods, types, struct fields, and variables of `package drain` (widening `drain`'s public API across the module), moving `559` files (plus `just pack-sync` copies), and rewriting [`tools/gate/go-test-edges.json`](../../tools/gate/go-test-edges.json) and [`tools/go-test-durations.json`](../../tools/go-test-durations.json) — all to retain **`18` tests (`2.94 s`)** before the merge (`1,982` vs. `1,964` pre-merge Go tests across the repository, a `+0.92%` difference). (Moving only the `108` black-box `package drain_test` files avoids exporting internals, but leaves all `455` white-box integration files — `1,718` tests — before the merge, failing to clear `90 s`.)
- **What failure is first seen after the merge instead of before:** Any behavioral regression caught only by the `559` integration test files (`2,077` tests, `3,633.40 s`) — subprocess execution, Git worktree/branch/landing plumbing, SQLite WAL/store transactions, detached gate/heavy supervision, restart state handoffs, and end-to-end loop passes — that compiles cleanly in `go-build` and passes both the `18` pure `drain` unit tests and the `219` consumer tests. When such a regression merges, [Section 2](#2-what-the-after-merge-proof-and-automatic-revert-already-do) holds `refs/tillr/green` back so the broken code never deploys, runs `go-test-tillr-drain` in the coalesced after-merge proof, auto-reverts the offending commit (`auto_revert: true`) and returns the failure to the worker session, or files `fixer-main-<sha>` if a later overlapping commit prevents a clean revert.
- **What `fast-gate-median-slow` (`90 s`) becomes:** **Stays at `90 s` (`90000 ms`)** (matching the `65.4 s` ten-run baseline on which [`D-TW43`](./tillr-watch.md#D-TW43) set `90 s`, verified on 2026-11-25 with `rg -n '65\.4' docs/design/tillr-watch.md`). Note from [Command 3](#13-tree-level-measurements-on-43455fc4-2026-11-25) that keeping `median20_wall_ms <= 90,000 ms` even during a `>= 10`-of-`20` streak of non-test `drain/*.go` edits also requires sharding `tillr-view`'s `21` `build_tests` or moving `tillr-view`'s `3` Observatory front-end build tests (`81.44 s`, `76.72 s`, `23.74 s`) to after-merge `go-test-tillr-view`; if `tillr-view` is left unsharded, a `drain/*.go`-heavy `20`-gate window hits `302.60 s` and would require `360 s` (`360000 ms`).

### 3.3 Option C — Prove the whole `tillr-drain/drain` package after the merge

- **Mechanism:** Keep `DEFERRED_PROOF_PACKAGES = frozenset({"github.com/mschulkind-oss/tillr-drain/drain"})` in [`tools/gate/go_affected.py`](../../tools/gate/go_affected.py) and the expanded `reads` list on [`tools/gate/checks/go-test-tillr-drain.json`](../../tools/gate/checks/go-test-tillr-drain.json) (built on `main` in commit `5e728e5a` under [`D-GR54`](../reference/gate.md#D-GR54) and confirmed by the owner on 2026-11-25 in [`OQ-DFG1`](#OQ-DFG1) / [`D-GR56`](../reference/gate.md#D-GR56)). Non-test edits under `tools/tillr-drain/drain/` still add `github.com/mschulkind-oss/tillr-drain/drain` to `changed_pkgs`, so all non-deferred reverse test importers and `builds`/`reads` edge consumers still run before the merge, and [`tools/gate/go_build.go`](../../tools/gate/go_build.go) (`go test -exec true ./...`) still compiles every production and test file in `tools/tillr-drain/drain/` before the merge.
- **Measured cost:**
  - **Pre-merge gate cost:** Removing `tillr-drain/drain` eliminates `83.0%`–`99.99%` of shard time from the profiled slow gates in [Measured `go-affected` timings (2026-11-25)](../reference/gate.md#measured-go-affected-timings-2026-11-25). At `43455fc4` ([Command 1 & 3, 2026-11-25](#13-tree-level-measurements-on-43455fc4-2026-11-25)), a `drain/*_test.go`-only edit runs `go-affected` in **`4.55 s`** (`2` narrowed tests), a cached `drain/*.go` edit runs in **`19.02 s`**, and an uncached `drain/*.go` edit runs in **`302.60 s`** (`<= 53.00 s` across `7` consumer packages once `tillr-view`'s `3` `81.44 s`/`76.72 s`/`23.74 s` Observatory launcher-build tests are sharded or deferred), plus **`2.49 s`** remembered `go-build`. Edits to any of the **`15` other Go test packages in `tools/tillr-drain` (`152` tests)** — including leaf subpackages `workflow` (`7`), `remotesync` (`9`), `fixer` (`9`), `heavyrun` (`34`), `forkgate` (`26`), and `keep` (`21`) — still run their own fast package tests before the merge (`1,964` of `1,982` non-integration Go tests across `49` of `50` Go test packages stay pre-merge, **`99.09%`**).
  - **Engineering and maintenance cost:** Zero files moved or exported under `tools/tillr-drain/drain/`.
- **What failure is first seen after the merge instead of before:** Every test failure inside `github.com/mschulkind-oss/tillr-drain/drain` (`2,095` tests across `571` test-bearing files: the `2,077` integration tests / `3,633.40 s` also deferred under Option B, **plus** the `18` pure unit tests / `2.94 s` kept pre-merge under Option B) whenever the candidate compiles cleanly in `go-build` (including all `579` `drain/*_test.go` files) and passes the `8` pre-merge consumer packages (`219` tests). As detailed in [Section 2](#2-what-the-after-merge-proof-and-automatic-revert-already-do), `refs/tillr/green` prevents deployment of any broken commit ([`D-GR5`](../reference/gate.md#D-GR5)), the coalesced `go-test-tillr-drain` proof catches the failure and culprits the landing, `auto_revert: true` reverts the commit automatically and returns the failure to the worker ([`D-GR6`](../reference/gate.md#D-GR6)), `fixer-main-<sha>` handles non-revertable overlaps before any unproved deploy ([`D-FG1`](../reference/gate.md#D-FG1)), and `tillr view heavy-misses` tracks whether pure-unit misses actually occur in practice ([`D-GR26`](../reference/gate.md#D-GR26)) — if a pure logic helper later warrants pre-merge unit testing, extracting that loop-free code into a leaf subpackage under [`D-GR47`](../reference/gate.md#D-GR47) automatically puts its tests back in pre-merge `go-affected` without exporting `drain` internals.
- **What `fast-gate-median-slow` (`90 s`) becomes:** **Stays at `90 s` (`90000 ms`)** (with the same [Command 3](#13-tree-level-measurements-on-43455fc4-2026-11-25) finding as Option B: `fast-gate-median-slow` clears at `90 s` whenever `< 10` of the last `20` ordinary gates hit uncached `tillr-view` `build_tests`, and sharding or deferring `tillr-view`'s `3` Observatory launcher-build tests keeps even `100%` `drain/*.go` streaks below `90 s`, whereas leaving `tillr-view` unsharded would require `360 s` / `360000 ms` during `>= 10`-of-`20` `drain/*.go` streaks).

### 3.4 Option D — Run before the merge only the tests that changed files reach inside `tillr-drain/drain`

- **Can intra-package reachability in `package drain` be computed soundly?** Only by adding conservative whole-package widening rules that defeat most of the narrowing, because Go packages share a single initialization and symbol namespace ([Command 2, 2026-11-25](#13-tree-level-measurements-on-43455fc4-2026-11-25)):
  1. **Package startup (`48` `init()` functions, `155` package-level `var`s, and `TestMain`):** Every test invocation in `package drain` executes `TestMain` (`main_test.go`) and all `48` `init()` functions and `155` package-level `var` initializers across `drain`'s `249` `GoFiles`. Any edit reaching an `init()` or non-constant `var` initializer soundly reaches all `2,095` tests.
  2. **Dynamic dispatch, callbacks, reflection, SQL, and CLI `execve` (`17` interfaces, `34` `func`-typed struct fields):** Syntactic symbol graphs miss callers through interfaces (`1,867` CHA implementation edges), `func`-typed struct hooks (`34` fields across config and step structs), JSON/SQLite schema reflection, and subprocess tests invoking the compiled `tillr-drain` binary across `execve` (no Go call edge links `exec.Command(drainBin, ...)` to `Loop.Run`). Soundness requires full `go/types` + CHA plus widening whenever CLI entrypoints or shared SQL/JSON schemas change.
  3. **Zero Go test-cache hits on `package drain` edits:** When *any* `.go` file in `tools/tillr-drain/drain/` changes, Go recompiles the `drain.test` binary and invalidates cached test results for the package; every selected `Test*` in `-run` executes cold.
- **Measured cost when computed with `go/types` + CHA ([Command 2, 2026-11-25](#13-tree-level-measurements-on-43455fc4-2026-11-25)):**
  - Even *before* startup, callback-field, reflection, or `execve` widening, `Loop` (`NewLoop`, `Loop.Run`, `Loop.pass`, `Loop.verify`, `Plan`, `ProcessSteps`) forms a single strongly connected hub: **`240` of `249` production `GoFiles` (`96.4%`)** reach `> 90 s` of recorded `drain` test time, **`231` of `249` (`92.8%`)** reach `>= 500` tests, and the median single production file reaches **`1,197` tests (`3,112.49 s`, `85.6%` of `3,636.34 s`)**.
  - Across the **last `20` commits on `main` touching production `tools/tillr-drain/drain/*.go`** (`f206384d..43455fc4`), `go/types` + CHA (`18.40 s` analysis wall time in [Command 2](#13-tree-level-measurements-on-43455fc4-2026-11-25)) selects a **median of `1,666` of `2,095` tests (`79.5%`) and `3,432.59 s` (`94.4%` of `3,636.34 s`)** — ranging from `223` tests / `715.43 s` (`c0442249`) and `837` tests / `2,875.95 s` (`d7595176`) to `1,770` tests (`f9b24d2c`) and `3,511.00 s` (`f206384d`), with **`19` of `20` commits (`95%`) selecting `>= 837` tests and `>= 2,875.95 s` (`79.1%`–`96.6%` of `3,636.34 s`)**. Only `drain/*_test.go`-only edits shrink to single-file tests. On production `drain` commits, Option D therefore still executes `79.1%`–`96.6%` of Option A's `drain` test-duration load (`645,131`–`1,031,027 ms` / `10.75`–`17.18 min` for `100%` of `drain` post-`07:43 ET` in [Measured `go-affected` timings (2026-11-25)](../reference/gate.md#measured-go-affected-timings-2026-11-25)), plus `18.40 s` for `go/types` + CHA planning, while preserving Option A's conflict-discard and CPU-contention cost.
- **What failure is first seen after the merge instead of before:**
  - *Under conservative `go/types` + CHA + startup/`builds` widening:* only a cross-component behavioral dependency invisible to Go types and call edges (for instance, two unlinked helpers sharing an SQLite column invariant, JSON field name, or filesystem path convention) is first seen in after-merge `go-test-tillr-drain` (and handled by `refs/tillr/green` hold + `auto_revert` / `fixer-main-<sha>` per [Section 2](#2-what-the-after-merge-proof-and-automatic-revert-already-do)).
  - *Under an unsound hop cutoff or edited-test-only rule (the only way to make Option D finish under `90 s`):* any regression reaching callers across `17` interfaces (`1,867` CHA edges), `34` `func`-typed struct fields, `48` `init()` functions, `155` package `var`s, `execve` CLI boundaries, or `Loop.pass` beyond the hop limit is first seen after the merge — giving Option C's deferred-proof profile while maintaining a complex custom static analyzer whose pre-merge guarantee has silent holes (previously rejected in [`D-GR39`](../reference/gate.md#D-GR39) for edited-test-only and coverage-map selection).
- **What `fast-gate-median-slow` (`90 s`) becomes:** Under **sound** intra-package reachability, **cannot stay at `90 s` — must rise to `1,200 s` (`1200000 ms`, `20 minutes`)** for the same reason as Option A (`19` of the last `20` production `drain` commits still run `79.1%`–`96.6%` of `package drain` with `0%` test-cache reuse). Keeping `90 s` (`90000 ms`) would hold only if Option D gave up soundness and ran `0`-hop / edited-test-only selection.

---

## Open Questions

None. [`OQ-DFG1`](#OQ-DFG1) was answered by the owner on 2026-11-25 and is recorded in the [Decision Ledger](#decision-ledger); the owner will decide again in the future once more operating data is collected.

---

## Decision Ledger

Two tables, so the owner's words are never presented as the designer's choices.

### Settled by the owner

| ID | Ruling | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-DFG1"></a>[`OQ-DFG1`](#OQ-DFG1) | **Stick with Option C — prove the whole `github.com/mschulkind-oss/tillr-drain/drain` package after the merge (`DEFERRED_PROOF_PACKAGES`), keeping `fast-gate-median-slow` at `90 s` (`90000 ms`) as Option C states, and decide again in the future once more data is collected (question closed, option C).** `"ro DFG1, let's stick wiht C. you think it's a good idea and it's not hurting. we'll collect more info and make another deciison in the future."` Confirms the [`D-GR54`](../reference/gate.md#D-GR54) `go-affected` selection already on `main` in commit `5e728e5a` ([`D-GR56`](../reference/gate.md#D-GR56)) while keeping `tools/tillr-gate/` and `tools/gate/` suites ([`OQ-GR3`](../reference/gate.md#OQ-GR3), [`D-GR50`](../reference/gate.md#D-GR50), [`I3`](../reference/gate.md#I3)), `go-build` test compilation of all `tools/tillr-drain/...` files, the `15` non-`drain` `tools/tillr-drain` test packages, and the `8` consumer packages before the merge; records no choice on the slow-gate alert threshold beyond what Option C states (`fast-gate-median-slow` stays at `90 s` / `90000 ms` in [`config/tillr/watch/fast-gate-slow.toml`](../../config/tillr/watch/fast-gate-slow.toml)). | 2026-11-25, owner | [Section 1.2](#12-why-this-is-the-owners-decision-and-what-is-in-the-tree-at-43455fc4), [Section 3.3](#33-option-c--prove-the-whole-tillr-draindrain-package-after-the-merge) | Yes (`5e728e5a`) |

### The designer's decisions

None of these is an owner ruling, and the owner may overturn any of them.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-DFG1"></a>`D-DFG1` | **From an operator request; the designer's framing and measurements, not an owner ruling (the owner may overturn any part not settled in [`OQ-DFG1`](#OQ-DFG1)).** Frame the four pre-merge `go-affected` options (**A**–**D**), their tree-level purity/white-box/CHA reachability measurements at `43455fc4`, and their conditional `fast-gate-median-slow` threshold notes (including the observation that `tillr-view`'s `3` unsharded Observatory launcher-build tests account for `181.90 s` of the `302.60 s` uncached `drain/*.go` consumer run) in [Section 1](#1-problem-standing-rules-and-measurements) through [Section 3](#3-the-four-options-compared), without settling any `tillr-view` sharding/deferral or slow-gate threshold change beyond Option C. | 2026-11-25, designer, from an operator request | [Section 1](#1-problem-standing-rules-and-measurements), [Section 3](#3-the-four-options-compared) | Measurements only; Option C built in `5e728e5a` ([`D-GR54`](../reference/gate.md#D-GR54)) |
