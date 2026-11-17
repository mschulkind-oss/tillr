# Gate stall watchdog verification

Verified on 2026-11-17 for the lane candidate; this record does not claim a trunk
landing or host deployment. The behavior contract is
[preventing a progressing check from being stopped](../design/gate-rework.md#prevent-a-gate-check-from-being-stopped-while-it-makes-progress).

## Repairs and verification

Independent review found three blocking defects: exited-child CPU could disappear
from process snapshots, a crash could separate the first stalled judgment from its
waiting transition, and a rerun could import remaining-check measurements from a
previous execution. The persistence and stale-measurement regressions failed
before repair and passed afterward. The CPU snapshot guard also failed the default
SIGCHLD disposition with `SA_NOCLDWAIT`
([discard terminated-child status](https://man7.org/linux/man-pages/man2/sigaction.2.html));
it was replaced rather than waived.

Linux now uses an [inherited kernel task clock](https://man7.org/linux/man-pages/man2/perf_event_open.2.html),
installed before the check can create children. The counter retains exited-child
usage independently of exit notifications. Injected counters cover discarded
children, including `SIG_DFL` (default signal handling) with `SA_NOCLDWAIT`, and
sub-millisecond progress.
The final CPU-only independent re-check found no blocker or major issue.

Successful native counter collection **was not verified**: this jail denies
`perf_event_open`. The live unavailable path was exercised and explicitly reported
`watchdog unavailable: kernel task-clock counter: permission denied`; it does not
substitute incomplete process snapshots or infer a stall from bytes alone.

| Command | Observed result |
| --- | --- |
| `cd tools/tillr-gate && go test ./...` | Passed, 9.602 seconds after the final fixture repair |
| `cd tools/tillr-gate && go test -race -run 'TestWatchdog\|TestProgressing\|TestStalled\|TestStallIs\|TestDiscarded\|TestDisappearing\|TestGateWrapper\|TestTaskClock\|TestDenied\|TestSubMillisecond\|TestMissingKernel' .` | Passed, 1.521 seconds |
| `cd tools/tillr-drain && go test ./...` | Passed; CLI package 94.122 seconds, drain package 106.152 seconds |
| `cd tools/tillr-drain && go test -race ./drain -run 'Stall\|FastGate\|GateSpan'` | Passed, 9.254 seconds |
| `cd tools/tillr-view && go test ./...` | Passed, 88.903 seconds |
| `python3 tools/gate/change-gate.test.py` | One adapter regression passed |
| `python3 tools/gate/checks/gate-budget.test.py` | Five admission tests passed |
| `python3 tools/tillr-loop/test.py` | Adapter mappings and argument contracts passed |
| `npm --prefix tools/tillr-view/web run typecheck` | Passed |
| `npm --prefix tools/tillr-view/web test` | 33 files, 303 tests passed |
| `python3 tests/tillr-docs.test.py` | Passed; 59 documented face invocations over nine documents checked |
| `just pack-sync` and `python3 tests/pack-sync.test.py` | Passed; 11 modules, 670 files, matching contents and modes |
| `yolo pack lint yolo-packs/tillr` | Installed jail binary accepted the pack; host rendering was not checked |
| `uvx vantage-check` with each modified Markdown file named | Eight files, including this record, passed |
| `git diff --check` | Passed |

Case 53 of `tests/fork-check.test.py` was executed without modification. Its output
was `fork-check timing rule: 2 elapsed-time assertion(s) declared (2 backlog, 0 liveness), no check starts load`.
The observer tests use injected clocks and samples, not sleeps, elapsed-time
threshold assertions, or artificial CPU load. Early watchdog fixtures used the
system temporary-directory default; the permanent tests now use the owned scratch
helper, and final verification also set `TMPDIR` to the authorized scratch root.

The check declaration JSON files are unchanged: the fast budgets sum to 8,950 ms
and the admission cap remains 9,000 ms. `FastTierCapMS` occurs only in its
admission comment and constant declaration, not in the runner's interruption logic. Whole-gate wrapper clocks remain unchanged by this task.

## Ten concurrent real fast-tier runs

At **2026-11-17 08:24:18 UTC**, ten processes each invoked:

```sh
tillr-gate checks --fast-tree --base "$BASE" --head "$HEAD" \
  --worktree "$FIXTURE" --checks "$DECLARATIONS" --logs "$LOGS"
```

The isolated Git fixture contained the candidate files, with read-only access to
the source repository's historical objects. Its refs and writes were private.
Declarations kept their budgets and commands, with remembering disabled so every
check ran. Go used the standard shared caches. All ten gates overlapped for
**13,685 ms** and exited **0**: **110 check passes, zero stopped checks**.

This is gate zero's recorded data; every row finished with its own passing verdict.
Each row also explicitly reported the kernel-counter permission denial above.

| Check | Target ms | Actual ms | Over target ms | Stopped |
| --- | ---: | ---: | ---: | --- |
| citations | 1,000 | 364 | 0 | No |
| commit-identity | 200 | 101 | 0 | No |
| design-citations | 500 | 104 | 0 | No |
| documents | 2,000 | 3,401 | 1,401 | No |
| file-size | 200 | 218 | 18 | No |
| gate-budget | 300 | 95 | 0 | No |
| go-build | 2,700 | 8,389 | 5,689 | No |
| pack-sync | 500 | 465 | 0 | No |
| scratch | 500 | 204 | 0 | No |
| seed-freeze | 200 | 159 | 0 | No |
| structural | 850 | 222 | 0 | No |

Earlier measurements failed solely on `pack-sync`: first the delivery mirrors
had not yet been synchronized, then the archive-only fixture lacked historical
documentation blobs. Synchronizing the mirrors and adding read-only historical
object access repaired those failures; no check was removed or failure waived.
The initial `go-build` observation was 20,561 ms against its 2,700 ms target and
still finished with its own passing verdict.

## Remaining limits

The measured documents, file-size, and Go-build overruns are cost defects against
unchanged targets; the table retains them rather than raising budgets or converting
working checks into candidate failures. This task does not optimize those checks.
Successful kernel-counter collection needs an environment that permits it; only
the explicit unavailable path and injected retained-child accounting were verified
here. Host pack rendering and deployment remain the owner's work after landing.
