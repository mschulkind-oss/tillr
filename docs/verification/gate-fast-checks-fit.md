---
title: "Documents and Go build checks — budget measurements"
status: accepted
stage: CURRENT
verified: 2026-11-17
tags: [gate, verification, performance]
summary: "Measured causes, conservative remembered-pass boundaries, and five-run before/after evidence for the unchanged fast-check budgets."
---

# Documents and Go build checks — budget measurements

## What was measured

On 2026-11-17, the manual audit ran each check serially, without another gate started
by this lane. It compared the baseline implementation with the candidate, using the
real tillr roadmap, its named local targets, and the ten Go tool modules' source
inputs in a private fixture repository. A Go module is the collection of packages
under one `go.mod` file. The standard shared Go build cache was left unchanged.

```sh
python3 tools/gate/benchmark-fast-checks.py --baseline 3ee0202cadada629839ebe7b54d0b926cdfb3fc3
```

Every measured document run appended different prose to the roadmap's last section.
Every measured Go run changed a global variable in the profiler command, so it was
not an exact-repeat tree pass. Go module passes were primed before the after runs;
the first after document run still checked all sections. There is no elapsed-time
assertion in the audit or regression tests.

| Check | Before: five runs, ms | Before median | After: five runs, ms | After median | Unchanged budget |
| :--- | :--- | ---: | :--- | ---: | ---: |
| Documents | 2643, 2975, 2810, 2390, 2510 | 2643 | 2294, 609, 610, 700, 656 | 656 | 2000 |
| Go build | 1283, 1246, 1227, 1232, 1219 | 1232 | 975, 974, 931, 962, 921 | 962 | 2700 |

These are measurements, not promises about every cold run. A new checker version,
changed headings, changed linked inputs, or unsupported cross-section syntax can
require more rendering. A change to the shared Go library must rebuild its local
consumers. Neither budget nor the 9000 ms cap changed.

## Documents: local target resolution dominates

The command below, counted by filename on 2026-11-17, found the roadmap in 20 of the
last 30 documentation-touching commits; the gate design appeared in three and the
usage-recording request in three. This identifies the frequently changed large page,
not a claim that every one of those commits ran the same gate.

```sh
git log -30 --format= --name-only -- docs/
```

Five serial direct renderer runs per page in the lane checkout measured:

| Command | Median, ms |
| :--- | ---: |
| `uvx vantage-check check --format json docs/tillr/roadmap.md` | 2576 |
| `uvx vantage-check check --format json docs/design/gate-rework.md` | 813 |
| `uvx vantage-check check --format json docs/design/run-wall-ruling.md` | 433 |
| `uvx vantage-check version` | 278 |

The staged roadmap is 201586 bytes, larger than the earlier measurement in the
[gate design](../design/gate-rework.md#prevent-the-documents-and-go-build-checks-from-overrunning-their-budgets).
Individual diagnostic rule-family exclusions did not isolate one dominant rule.
A separate diagnostic probe replaced local Markdown destinations with remote URLs,
removing local target and anchor reads while retaining the body. Three runs each
measured 543 ms for 198757 bytes, 333 ms for 34544 bytes, and 303 ms for 15006 bytes.
These probes intentionally changed link meaning and produced findings: they were
profiling experiments, not verification or deployed rule exclusions. They show that
startup and body size alone do not explain the full roadmap's cost; local target
resolution accounts for most of the difference.

The previous document key included the entire page's exact blob. An ordinary edit
changed that key, so the unchanged majority of the roadmap was rendered again.
Moving context to three linked pages without moving item blocks was tried and
rejected: its five-run median was 3695 ms. It added local targets without removing
the repeated work. Those edits were discarded.

The candidate remembers independent second-level roadmap sections instead. A
remembered pass is a previous successful check of the same inputs, reused without
running that check again. Each section's key binds its content, frontmatter,
checker and config bytes, sibling names, named target objects, and all original
headings and explicit anchors. Self-line links also bind the page's line count.
Findings identify the original roadmap and line, not a temporary fragment.
Cross-section math, reference definitions, fences, unsupported HTML, noncanonical
headings, header-only pages, Vantage directives, and unrouted planning use the
original full-file renderer with an explicit diagnostic. No roadmap text, item block, ordering, or source reference
moved, so item synchronization was not required.

## Go: unnecessary modules and test setup

At the baseline, `python3 tools/gate/checks/go-build.py` runs `go list`, command
builds where appropriate, and `go test -run '^$'` for every module beneath:

- `tools/pi-profiler`, `tools/tillr`, `tools/tillr-store`, `tools/tillr-item`;
- `tools/tillr-run`, `tools/tillr-gate`, `tools/tillr-view`, `tools/tillr-land`;
- `tools/tillr-drain`, `tools/tillr-cli`.

The declaration read `.`: a Markdown-only commit changed the whole-tree key and
compiled every module. Filtering test cases still ran `TestMain`, the Go test
binary's setup entry point; several tools build another binary there. A failing
regression demonstrated that setup ran even with no test cases selected.

The candidate's outer remembered pass binds the module directories, imported
checker code, workspace declarations, tool identities, and inherited Go build
options. The fast-tree face still reports this check on a Markdown-only change,
now as remembered, and the regression records no compile starting. Inside a
necessary build run, each module binds its entire source tree, including tests
and embedded assets, local replacement trees, effective Go environment, and
checker bytes. Binding is checked again before publication. A one-module profiler
edit compiled that module and reported the other nine as remembered.

Command builds still validate real main entry points. `go test -exec true` compiles
and links test binaries without executing setup or cases. Go's build cache location,
explicit owner overrides, and trimmed-source-path behavior are preserved.
Workspaces, including ones auto-discovered below the repository root, file-valued
or custom compiler build flags, symlinked inputs, and local replacement inputs
outside the outer declaration run normally but are explicitly not remembered. External local
replacements also build without inner remembrance, rather than refusing judgment.

## Verification and repair record

The first independent review found stale-pass risks in cross-section math,
self-line targets, header dependency targets, checker imports, workspace and
file-valued Go options, and publication after sources change during compilation.
Each has a permanent regression or explicit conservative binding rule. The review
also found that external local replacements incorrectly refused judgment; that
path now builds without remembrance. The second review found four additional
boundary cases: header-only pages, indented headings, nested auto-discovered
workspaces, and symlinked replacement roots. Focused regressions now cover each;
their conservative fixes were localized, so no third review round was requested.
A final regression reproduced a mutable symlink target bypassing an unchanged
roadmap section. Named symlink targets now prevent document-pass reuse as well.
The compiler-options fixture also tried to commit without a file change; it now
keeps that existing fixture commit rather than asking Git for a nonexistent change.

Applicable verification commands are the change-check suite, tree-check suite,
`go test ./...` in `tools/tillr-gate`, the budget check and its suite, the timing
audit in case 53 of `tests/fork-check.test.py`, pack synchronization, pack lint,
and the pack synchronization suite. The full landing gate was deliberately not
run in the lane. The pack shape was linted using the installed in-jail binary;
a host render was not verified. Deployment needs `yolo host apply --assert` and
relaunching affected running jails, not `rcup`.

Final results observed on 2026-11-17:

| Command | Result |
| :--- | :--- |
| `python3 tools/gate/checks/change_checks_test.py` | 39 tests, OK |
| `python3 tools/gate/checks/tree_checks_test.py` | 16 tests, OK |
| `cd tools/tillr-gate && go test ./...` | package suite passed |
| `python3 tools/gate/checks/gate-budget.py` | fast budget sum 8950 ms within 9000 ms |
| `python3 tools/gate/checks/gate-budget.test.py` | 5 tests, OK |
| Case 53 extracted from `tests/fork-check.test.py` and executed with its repository globals | 2 declared elapsed-time assertions, 2 backlog, no check starts load |
| `just pack-sync` then `python3 tests/pack-sync.test.py` | 11 modules, 665 files; source/copy sets, bytes, executable modes and contracts passed |
| `yolo pack lint yolo-packs/tillr` | pack OK, 716 files staged |
| `uvx vantage-check check --format json docs/verification/gate-fast-checks-fit.md` | 0 errors, 0 warnings, 0 failures |
