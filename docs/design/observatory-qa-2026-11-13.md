# Observatory QA — 2026-11-13

> **History note, 2026-11-21.** This is a dated record. It names `docs/design/agent-runtime.md` as that file stood when the record was written; the file
> was deleted on 2026-11-21, and the retirement record says what became of it.

## Verdict

The observatory is honest about the *inside* of a run and wrong about the *shape of the board*
(the observatory's queue screen, never the retired work board):
the queue it draws is sorted alphabetically instead of by the store's rank, its tick count is the
number of dispatch decisions, and a store file that no longer exists is still reported `present`
while the page's own timestamp advances. Every defect below was found by reading the live system
(UI on 4318, API on 4319, a copy of the store served on a scratch port), not by reading source;
where a claim rests on source, the live behaviour that source produces is what is quoted.

The UI was being rewritten by another lane while this audit ran. Findings are stated against the
API responses and the store, which did not move; where a UI detail is cited, the time it was seen
is given, because the surface changed twice during the session (a list of rows with a drain line
in the header at 20:58 UTC, a numbered `queue-row` list with In queue/Blocked/Landed/Running stats
at 21:04 UTC). Anything that only a restyle could fix is deliberately absent.

**Status, 2026-11-13:** re-checked at 21:55 on the worktree's own build. The alphabetical order named
above, the duplicate startup refusal and the routing refusal's null moment are no longer the board's
behaviour; the refused-store-after-start, the tick count, the stop reason and the working-without-a-run
projection still are. The section below says what moved, and every finding carries the measurement that
decided it.

## What changed under this document

This audit was run at 16:05 and re-checked at 21:55 on 2026-11-13. Four surfaces moved between the
two moments, and the store moved with them (41 items, now 25 in queue / 13 blocked / 2 landed /
1 closed, where the audit measured 24 / 16 / 1 and no `closed` at all). A finding reads differently
because of the row it appears in; each finding's own `Status, 2026-11-13` line carries the
measurement that decided it.

| Surface | What it is now | Findings it touches |
|---|---|---|
| The queue's order | `/api/v1/snapshot` returns items sorted by `rank` and carries a `rank` field per item; the rail sorts by it (`queueOrder`) and prints it in the gutter instead of the row's index | F1 (fixed), F14 (the row's name carries the store's rank, not a position) |
| The server's counts | `data.counts` — `by_state`, `dispatchable`, `blocked`, `runs_live` — computed server-side; the header prefers it to its own arithmetic over the items | F10 (a payload now carries them, still no CLI face), F9 (a store state with no run is counted as `working`), F15 (new: the four stats no longer sum to the items) |
| The error vocabulary | every code is kebab-case and every refusal shares one body: `item-not-found`, `run-not-found`, `no-design-doc`, `design-doc-missing`, `design-doc-outside-root`, `design-doc-unreadable`, `unknown-kind`, `unknown-route`, `store-absent`, `store-unreadable` | F12 (the moment is real now; the cursor is still `0`), the error-contract bullet under "What works" |
| The document anchor | the item panel opens a design document at the heading the item's `body_ref` names — `docs/design/tillr-observatory.md#6-the-first-screen-two-item-drain-acceptance-walkthrough`, scrolled into view inside the pane and never in `window.location.hash` | F5 (opening an item now lands on its questions, which shortens the per-item path and changes nothing about the projection) |

## Findings

### F1 — The queue is drawn in alphabetical order, and numbered as if that were its order

**Severity:** blocker

**What I did.** Read the store's rank for every item through the CLI, without touching SQLite:

```
tillr-item show --store /workspace/.tillr/state.db --item <id>   # 41 times, one per item
```

then compared that order with the order `GET /api/v1/snapshot` returns and with the order the
page draws.

**What happened.** 24 of the 41 items carry a rank, and those 24 are exactly the 24 in-queue
items. The store's order begins at rank `0` = `item-pipe-real-registry` and ends at rank `n` =
`item-agent-runtime-backend`. The API returns `item-agent-runtime-backend` first — the last item
in the store's order — and the page numbers it "1"; `item-pipe-real-registry`, the item the loop
would pick next, is row 30 of 41. The API's item object carries no `rank` field at all, so no
client can recover the real order from what it is given.

**What I expected and why.** The drain's own planner orders by rank — its query reads
`SELECT item_id, state, COALESCE(rank, '') FROM items ORDER BY COALESCE(rank, '') = '', COALESCE(rank, ''), item_id`
— so the operator's first question ("what is in the queue, in what order") has a right answer, and
a numbered board that answers it backwards is worse than an unnumbered one. A restyle cannot fix
this: the field is missing from the API.

**Evidence.**

```
$ curl -s http://localhost:4319/api/v1/snapshot | jq -r '.data.items[0:3][].item_id'
item-agent-runtime-backend
item-bar-ironbar-config
item-bell-coalesce

$ tillr-item show --store /workspace/.tillr/state.db --item item-agent-runtime-backend | jq .payload.rank
"n"
$ tillr-item show --store /workspace/.tillr/state.db --item item-pipe-real-registry | jq .payload.rank
"0"
```

and in the page at 21:04 UTC, `button.queue-row` texts begin
`"1 | agent-runtime.md's backend boundary … | item-agent-runtime-backend"` … with
`"30 | The composed-pipeline case judges a candidate … "` for `item-pipe-real-registry`
(screenshot `board-top.png` / `pos-*`).

**Status, 2026-11-13: fixed.** The snapshot carries `rank` and returns items in that order:
`curl -s .../snapshot | jq -r '.data.items[0:3][] | "\(.rank) \(.item_id)"'` answers
`0 item-pipe-real-registry`, `1 item-drain-question-hold`, `2 item-lane-branch-scope` — the item the loop
would pick first is row one, and `item-agent-runtime-backend` (`rank: "n"`) is the 24th of the ranked rows
instead of the first. The rail draws that order and that number: `queueOrder` in
`tools/tillr-view/web/src/models/board.ts` sorts by `rank` then id, and `QueueRail` prints `rank` in the
gutter. Measured against the API and the component that renders it, not in a browser — this lane had no
Chrome DevTools tool.

### F2 — "10 ticks" is the number of dispatch decisions; the store holds two ticks

**Severity:** major

**What I did.** Compared the header's drain line with the store's own decisions and with the loop's
result file:

```
curl -s http://localhost:4319/api/v1/items/item-seed-02 | jq '.data.decisions'
ls /workspace/.tillr/loop/drain-*.json
```

**What happened.** `drain_summary` reports `ticks: 10`, `dispatched: 2`, `landed: 1`. The store's
decisions are 10 rows over **five** items, two rows each, at exactly two timestamps
(`...062156Z` and `...065219Z`) — so two ticks wrote ten decisions. The loop's own result file
`drain-20261113T065219Z.json` says `"ticks": 1`, and the other one `drain-20261113T062059Z.json`
says `"ticks": 0`. `ticks` is assigned `len(dispatches)`, which is the count of decisions.

**What I expected and why.** "10 ticks" is the number an operator uses to judge how hard the loop
has been working and whether it is spinning. It is inflated fivefold here, and it will be *deflated*
in the same way: a tick that decides nothing writes no dispatch row, so a loop that ticks and idles
reports no ticks at all.

**Evidence.**

```
$ curl -s http://localhost:4319/api/v1/snapshot | jq -c '.data.drain_summary | {ticks,dispatched,landed}'
{"ticks":10,"dispatched":2,"landed":1}
$ curl -s http://localhost:4319/api/v1/items/item-seed-04 | jq -c '.data.decisions[] | {action,reason,ms:.dispatched_at_ms}'
{"action":"dispatch","reason":"ready","ms":1794550916192}
{"action":"hold","reason":"run-link-missing","ms":1794552739087}
$ head -c 400 /workspace/.tillr/loop/drain-20261113T065219Z.json
{… "stop": "queue-drained", "ticks": 1, "dispatched": 2, "landed": 1, "rejected": 0, …}
```

**Status, 2026-11-13: open.** Still `ticks: 10` for a store whose dispatch rows are ten over
two moments: `curl -s .../snapshot | jq -c '.data.drain_summary | {ticks,dispatched,landed}'` answers
`{"ticks":10,"dispatched":2,"landed":1}`, and a read-only count of the store's `dispatches`, grouped by
`dispatched_at`, answers two groups of five (`1794550916192: 5`, `1794552739087: 5`). The loop's own
files still say `"ticks": 1` (`drain-20261113T065219Z.json`) and `"ticks": 0`
(`drain-20261113T062059Z.json`). What would close it: the count read from those files rather than from
the dispatch rows.

### F3 — A store that is not there is reported `present`, and the page keeps advancing its clock

**Severity:** major

**What I did.** Copied the store, served the copy on a scratch port with its own `tillr-view`,
loaded the page, then deleted the file and wrote 200 KB of random bytes in its place:

```
cp /workspace/.tillr/state.db ./scratch/vanish.db
tillr-view serve --store ./scratch/vanish.db --root /workspace --addr 127.0.0.1:4331
mv ./scratch/vanish.db ./scratch/vanish.db.away ; head -c 200000 /dev/urandom > ./scratch/vanish.db
curl -s http://127.0.0.1:4331/api/v1/snapshot
```

**What happened.** The API kept answering `200` with all 41 items, `event_head_seq` frozen at 54,
and `"store": {"path": "./scratch/vanish.db", "state": "present"}` — for a path that no longer
holds a database. The page's header read

```
Store: ./scratch/vanish.db (v5 · seq #54)
Snapshot: 2026-11-13T21:03:33.156118658Z OBSERVED
```

— a timestamp generated at that second over data read from a file that had been replaced.

**What I expected and why.** `state` is documented as "one read source as this response found it",
and `absent` is already a value it uses for `loop_dir` when the directory is missing. A frozen
board that says `present` forever is the worst failure this face can have: the operator sees a
healthy screen showing the last good moment, with no staleness anywhere, and the one number that
changes — `Snapshot:` — is the one that says "as of now".

**Evidence.** The three commands above; the response was
`{"store":{"path":"./scratch/vanish.db","state":"present"}}` with `41` items and
`GET /api/v1/items/item-seed-02` answering `200`, and the header quoted above
(screenshot `vanish-vanished-store.png`).

**Status, 2026-11-13: open.** Reproduced on a copy served on a scratch port: after moving the file
away and writing 200 KB of random bytes in its place, `GET /api/v1/snapshot` still answers `200` with 41
items and `.data.sources.store` still reads `{"path":"./vanish.db","state":"present"}`, and the timestamp
advanced across the swap (`21:55:15.306712487Z` → `21:55:15.345654969Z`); `/api/v1/items/item-seed-02`
also answered `200`. A store that is unreadable when the process *starts* is refused now (F13), so the
refusal exists and only the replaced-under-a-running-server case is unmeasured. What would close it:
measuring the store per read, so a file swapped under a running server reports its condition instead of
`present`.

### F4 — The stop reason is on disk and the board says another slice owes it

**Severity:** major

**What I did.** Asked the board why the loop stopped, then read the loop's result files.

**What happened.** `last_stop_reason` is `null`, and the page prints the server's note verbatim:
"a stop reason is written by the loop's own result file, which OBS-1 adds", with an `ABSENT` badge.
The file it names is already there and already says `"stop": "queue-drained"`. The same file also
carries `ticks`, `dispatched`, `landed`, `spend_usd`, `tokens`, and a `detail` sentence naming what
the last tick held.

**What I expected and why.** The board is given `--loop-dir` (its snapshot already reports
`loop_dir: present`), so the answer is in a source it has open. Telling the operator a fact is owed
by a future slice, when the fact is one file read away, is a statement about the software that is
*false today* — and it is the answer to "what did the last tick do", which is question four of five.

**Evidence.**

```
$ curl -s http://localhost:4319/api/v1/snapshot | jq -c '.data.drain_summary | {last_stop_reason,stop_note}'
{"last_stop_reason":null,"stop_note":"a stop reason is written by the loop's own result file, which OBS-1 adds"}
$ python3 -c 'import json;print(json.load(open("/workspace/.tillr/loop/drain-20261113T065219Z.json"))["stop"])'
queue-drained
$ python3 -c 'import json;print(json.load(open("/workspace/.tillr/loop/drain-20261113T065219Z.json"))["detail"])'
a full tick planned nothing dispatchable: 5 held item(s): item-seed-01 (blocked), item-seed-02 (not-ready), item-seed-03 (blocked), item-seed-04 (run-link-missing), item-seed-05 (blocked)
```

That `detail` sentence — the only account of what the last tick did — reaches no screen.

**Status, 2026-11-13: open.** `last_stop_reason` is still `null` and the note still promises a future
slice: `curl -s .../snapshot | jq -c '.data.drain_summary | {last_stop_reason,stop_note}'` answers
`{"last_stop_reason":null,"stop_note":"a stop reason is written by the loop's own result file, which OBS-1 adds"}`.
The file it names is there and says it: `jq -c '.payload|{stop,ticks,dispatched,landed,spend_usd}'
/workspace/.tillr/loop/drain-20261113T065219Z.json` → `{"stop":"queue-drained","ticks":1,"dispatched":2,"landed":1,"spend_usd":0.16299}`.
What would close it: reading that file in the snapshot assembly.

### F5 — Six queued items are waiting on unanswered design questions, and nothing in the queue says so

**Severity:** major

**What I did.** Fetched every item's dossier and looked for items whose design document still
carries open questions:

```
for id in $(curl -s .../snapshot | jq -r '.data.items[].item_id'); do
  curl -s .../items/$id | jq -c 'select(.data.open_questions != []) | {id:.data.item.item_id,n:(.data.open_questions|length)}'
done
```

**What happened.** Six queued items — `item-decided-by-autoqa`, `item-land-attempt-event`,
`item-obs-verify`, `item-tillr-layerb-index`, `item-tillr-stop-reason`, `item-trace-correlation` —
each name a design document with **four** open questions (four in
[`autoqa-tillr-seams.md`](./autoqa-tillr-seams.md#OQ-AQA1), four in
[`tillr-observatory.md`](./tillr-observatory.md#10-open-questions)), and one
blocked item, `item-oq-mqr-retirement`, carries four more. All six project `in_queue`, the same
badge as the rest of the queue. The item
panel does render an OPEN QUESTIONS section once an item is selected (seen at 21:04 UTC), so the
data is reachable — but only by opening items one at a time, and nothing in the projection
(`projected_state`) distinguishes "ready to dispatch" from "needs a ruling first".

**What I expected and why.** An item whose own design document asks four questions the build cannot
answer is stuck for a reason the operator can act on, and the planner is meant to hold exactly this
case (`item-drain-question-hold` names it as `question-open`). The board is where that distinction
would be seen across the queue; showing 24 identical `in_queue` rows hides six blockers behind the
one state that means "nothing is wrong here".

**Evidence.** The item's own questions come back from its document, and the projection does
not notice them:

```
$ curl -s .../api/v1/items/item-obs-verify | jq -c '.data.open_questions[0], .data.questions_source'
{"question_id":"OQ-OBS1","text":"Should the Go backend and web/ tree live inside as tillr-view serve, …"}
"docs/design/tillr-observatory.md"

$ curl -s .../api/v1/snapshot | jq -r '.data.items[] | select(.item_id=="item-obs-verify") | .projected_state.state'
in_queue
```

That is the same state every other queued row answers.

**Status, 2026-11-13: open.** Same six queued items, four open questions each, measured one item at a
time against a fresh snapshot:

```
$ for id in $(curl -s .../snapshot | jq -r '.data.items[].item_id'); do
    curl -s .../items/$id | jq -c 'select((.data.open_questions // []) != []) | {id:.data.item.item_id,n:(.data.open_questions|length),state:.data.item.projected_state.state}'
  done
{"id":"item-tillr-stop-reason","n":4,"state":"in_queue"}
{"id":"item-trace-correlation","n":4,"state":"in_queue"}
{"id":"item-land-attempt-event","n":4,"state":"in_queue"}
{"id":"item-tillr-layerb-index","n":4,"state":"in_queue"}
{"id":"item-obs-verify","n":4,"state":"in_queue"}
{"id":"item-decided-by-autoqa","n":4,"state":"in_queue"}
{"id":"item-oq-mqr-retirement","n":4,"state":"blocked"}
```

`projected_state` still distinguishes nothing, so the six read as ordinary queue rows. What would close
it: the projection naming `question-open` for an item whose design document still asks.

### F6 — Sixteen blocked items are told the same sentence, and it says nothing

**Severity:** major

**What I did.** Listed the projection of every blocked item, and asked each blocked item for its
`blocked_by` edges.

**What happened.** All 16 blocked items carry
`{"state":"blocked","raw_state":"blocked","underlying":"the store marks it blocked"}` and
`blocked_by: null` (via `tillr-item show`). The page therefore shows sixteen rows whose only
explanation is that the store says so. Their titles are the actual reasons — "The owner rules …",
"A determination: …" — but that is prose, not data, and the detail panel repeats the same
"the store marks it blocked" line where a cause would go.

**What I expected and why.** "What is stuck and why" is one of the five questions, and a constant
string is not an answer to the second half. The store has a place for the cause (`item_edges`, read
by the same projection) and none of these sixteen uses it; a blocked item that records no edge is
a state the board should distinguish from one that is waiting on a named item — otherwise the
operator cannot tell "waiting for X" from "parked by a person, reason unrecorded".

**Evidence.** Every one of the 16 blocked items' `projected_state.underlying` is the same string;
e.g. `curl -s .../items/item-seed-05 | jq '.data.blocked_by'` → `[]` while
`curl -s .../items/item-seed-05 | jq '.data.item.projected_state'` →
`{"state":"blocked","raw_state":"blocked","underlying":"the store marks it blocked"}`.

**Status, 2026-11-13: open**, and the population has moved from sixteen blocked items to thirteen
(25 in queue, 13 blocked, 2 landed, 1 closed). Every one of the thirteen still answers the same string
and records no cause: `.projected_state.underlying` is `"the store marks it blocked"` for all thirteen,
`.data.blocked_by` is `[]` for all thirteen, and the store's `item_edges` table holds 0 rows. What would
close it: a cause on the projection, or the store writing the edge that names it.

### F7 — A 30-minute timeout leaves the header reading "0 rejected"

**Severity:** major

**What I did.** Followed the one run whose state is not `exited`, through the item row and the run
page, then compared it with the drain summary.

**What happened.** `run-item-seed-04-20261113T062156Z` is `timed-out`, ran `1800.00s`, spent
`$0.0530`, and its item is back to `in_queue`: the next tick held it with reason `run-link-missing`,
which the row does show. The header reads `1 landed · 0 rejected`, and `rejected` is
`byResult["isolated"] + byResult["refused"]` — a timed-out or failed run is counted nowhere. On the
run page the timeout is visible as `run / timed-out / 1800.00s / OBSERVED` and nothing more: no
record distinguishes "hit the run's cap" from "died", and the drain summary has no field for either.

**What I expected and why.** A run that burned thirty minutes of lane time and half a cent is
exactly what the operator is watching for. `rejected: 0` is a true statement about landing
attempts wearing a label that reads as "nothing went wrong", and the drain summary has no field
for timed-out, failed or abandoned runs at all.

**Evidence.**

```
$ curl -s .../runs/run-item-seed-04-20261113T062156Z | jq -c '.data.run | {state,exit_code,ms:(.finished_at_ms-.started_at_ms)}'
{"state":"timed-out","exit_code":null,"ms":1800000}
$ curl -s .../snapshot | jq -c '.data.drain_summary | {landed,rejected}'
{"landed":1,"rejected":0}
```

**Status, 2026-11-13: open.** The run is still `{"state":"timed-out","exit_code":null,"ms":1800000,"cost_usd":0.052996}`
and the header still reads `{"landed":1,"rejected":0,"infra_failures":null}`. What would close it: a
count of the runs that ended without landing — timed out, failed or abandoned — in the drain summary.

### F8 — "Inter-Tool Exchanges (1)" for a run that made none

**Severity:** minor

**What I did.** Opened the timed-out run's tabs and read the API behind each.

**What happened.** The run's exchanges tab is counted `1`, and the row is not an exchange: it is
the seam placeholder
`{exchange_id: "run-item-seed-04-…/seam/index", layer: "harness", caller: "tillr-trace",
callee: "run-item-seed-04-…", channel: "subprocess", epistemic_state: "unobserved_seam",
summary: "tillr-trace has not indexed this run: every Layer B row is unknown, not empty"}`.
Its body text is honest — the row is badged UNOBSERVED SEAM — but the count is not: it says the run
exchanged one call with one tool. The timeline tab, the same run, counts `0` while
`/timeline` returns 2 spans, one of them a `gap` placeholder; `0` is what an unloaded tab also
shows, so "nothing there" and "not fetched yet" are the same number.

**What I expected and why.** The exchange count is the fastest available answer to "what did this
run actually do", and a placeholder counted as an exchange makes a run with no evidence look like a
run with evidence. A gap row and an observation must not share a count.

**Evidence.** The JSON above, plus
`curl -s .../runs/run-item-seed-04-…/timeline | jq '.data.spans | length'` → `2` while the page
shows `Timeline Waterfall (0)`, and the run page rendering
`# TIME(UTC) LAYER CALLER -> CALLEE … 1 no clock harness tillr-trace -> run-item-seed-04-… subprocess UNOBSERVED SEAM`.

**Status, 2026-11-13: open.** `/runs/run-item-seed-04-…/exchanges` returns exactly one row and it is the
seam placeholder — `"exchange_id":"…/seam/index"`, `"epistemic_state":"unobserved_seam"`,
`"ts_source":"none"` — while `/timeline` returns `2` spans. The counts are still the length of the rows
returned. What would close it: counting observed rows, and giving the tab a state for "not fetched yet"
that is not `0`.

### F9 — An item with no run can project `working`

**Severity:** minor

**What I did.** On the scratch copy, moved one item to `in-progress` with a write face against my
own copy and watched the page:

```
tillr-item state --store ./scratch/state.db --item item-trace-correlation --state in-progress --now <now>
```

**What happened.** `projected_state` became `{"state":"working","raw_state":"in-progress"}` with
`latest_run: null`, both lanes `idle`, and `running_runs: 0`. The page rendered the row as
`working | raw: in-progress | no tick decision ABSENT | no run ABSENT`, next to a stats group
reading `IN QUEUE 24 BLOCKED 16 LANDED 1 RUNNING 0`.

**What I expected and why.** `working` is the word that answers "what is running right now", and it
is applied to a store state rather than to the existence of a run. `running_runs` (the measured
number) and the `working` projection can disagree on one screen, and the reader has no way to tell
which one to believe.

**Evidence.** `curl -s http://127.0.0.1:4325/api/v1/snapshot | jq -c '.data.items[] | select(.item_id=="item-trace-correlation").projected_state'`
→ `{"state":"working","raw_state":"in-progress"}`, with `.data.drain_summary.running_runs` → `0`.
Screenshot `live-*` from that port.

**Status, 2026-11-13: open.** Reproduced on a copy: `tillr-item state --store ./copy.db --item
item-trace-correlation --state in-progress`, then the snapshot answers
`{"projected_state":{"state":"working","raw_state":"in-progress"},"latest_run":null}` with
`.data.counts.runs_live` at `0` in the same body — and the server's own `counts.by_state` adds
`"working":1`, so the disagreement is now inside one payload. What would close it: `working` requiring a
live run, or a second word for a store state that has none.

### F10 — The board's headline numbers exist in no command

**Severity:** minor

**What I did.** Tried to check the header's numbers against the CLI the brief names.

**What happened.** `tillr-view status --store /workspace/.tillr/state.db` returns
`generated_at, snapshot, item, items, questions, questions_source, candidates, runs` — no drain
summary, no lanes, no dispatch counts, no spend. Items (41), states (24 in-queue / 16 blocked /
1 landed) and runs (2) agree with the API exactly; the ticks, dispatched, landed, rejected, cost,
lanes and stop reason agree with nothing, because no other face publishes them. They are assembled
in the snapshot handler from rows no report exposes.

**What I expected and why.** "Is the thing I am looking at telling me the truth" is answerable for
the item list and not for the numbers in the header — which are the ones an unattended loop's
operator actually watches. Everything I could cross-check matched; the point is that every header
figure except the item counts and the per-run cost cannot be cross-checked at all.

**Evidence.** `tillr-view status --store … | jq '.payload | keys'` →
`["candidates","generated_at","item","items","questions","questions_source","runs","snapshot"]`.

**Status, 2026-11-13: open.** `tillr-view status --store /workspace/.tillr/state.db --root /workspace |
jq -c '.payload|keys'` still answers `["candidates","generated_at","item","items","questions","questions_source","runs","snapshot"]`
— no drain summary. The header's four numbers now come from `snapshot.counts`, which is new since the
audit, but it reaches no CLI face either. What would close it: the drain summary and `counts` on
`tillr-view status`.

### F11 — The first tick's result file records zeros for work the store shows it did

**Severity:** minor

**What I did.** Read both of the loop's result files.

**What happened.** `drain-20261113T062059Z.json` reads `"stop": ""`, `"ticks": 0`,
`"dispatched": 0`, `"landed": 0`, `"spend_usd": 0` — while the store holds the two decisions that
tick made at `...062156Z` (`dispatch ready`, lanes 1 and 2), the run row that landed, and
`$0.16299` of spend. The later file, for the tick that dispatched nothing, is the one that
reports work.

**What I expected and why.** This matters because of F4: the fix for the stop reason is to read
these files, and one of the two is false about the tick it names. An operator asking "what did the
last tick do" through whichever file happens to be newest can be told the loop did nothing while it
dispatched two items and landed one.

**Evidence.** The two JSON bodies quoted in F2 and F4; the store's matching decisions are
`curl -s .../items/item-seed-02 | jq -c '.data.decisions[] | {action,ms:.dispatched_at_ms}'` →
`{"action":"dispatch","ms":1794550916192}` and `{"action":"hold","ms":1794552739087}`.

**Status, 2026-11-13: open.** The file is unchanged: `jq -c '.payload|{stop,ticks,dispatched,landed,spend_usd}'
/workspace/.tillr/loop/drain-20261113T062059Z.json` → `{"stop":"","ticks":0,"dispatched":0,"landed":0,"spend_usd":0}`,
while the store holds the two dispatch rows at `1794550916192` — the second this file's `created_at`
(`06:20:59.947723202Z`) was written, and one tick later than the moment it claims to describe. What
would close it: the tick's file written when the tick ends, or not written at all.

### F12 — The routing 404 carries a zero cursor while every other refusal carries the real one

**Severity:** minor

**What I did.** Asked each route for something that is not there.

**What happened.** `GET /api/v1/items/nope` answers
`{"snapshot":{…"event_head_seq":51,"generated_at":"2026-11-13T20:57:32Z"},"error":{"code":"not_found",…}}`
— the real cursor. `GET /api/v1/nonsense` answers `{"code":"unknown_route",…}` with
`"store_path":"", "schema_version":0, "event_head_seq":0, "generated_at":"0001-01-01T00:00:00Z"`.
The error shape is otherwise identical, which is what makes this one dangerous: a client that reads
`generated_at` to order two answers sees the clock jump back two thousand years on a typo'd URL.

**What I expected and why.** The envelope exists so a client can tell two responses apart by the
moment they were read at; a refusal that carries a null moment is a refusal a client must special-case.

**Evidence.** The two bodies above.

**Status, 2026-11-13: open, narrowed.** Every code is kebab-case now — measured: `item-not-found`,
`run-not-found`, `no-design-doc`, `unknown-route`; read from `tools/tillr-view/apierror.go`: `store-absent`,
`store-unreadable`, `design-doc-missing`, `design-doc-outside-root`, `design-doc-unreadable`,
`unknown-kind` — and the routing refusal carries a real moment: `GET /api/v1/nonsense` answers
`"generated_at":"2026-11-13T21:47:29.748997978Z"` with `store_path` and `schema_version` filled, where it
used to answer `0001-01-01T00:00:00Z`. The cursor is still zero: `"event_head_seq":0` in that same body.
The harm the finding named — a clock that jumps back two thousand years — is gone. What would close it:
publishing the head on the one refusal that makes no store read.

### F13 — Startup refusals print the same fault twice

**Severity:** nit

**What I did.** Started the server against a missing store, an empty file and a non-database file.

```
tillr-view serve --store ./scratch/nope.db --addr 127.0.0.1:4330
```

**What happened.** Exit code 2 (correct, refused) with the line
`tillr-view: store: stat ./scratch/nope.db: no such file or directory` printed **twice**; likewise
for `no such table: schema_migrations` and `file is not a database`. There is no rendered state for
"no store" in the browser at all: the process never starts, so the page cannot exist.

**What I expected and why.** Refusing to start is the right behaviour for a read-only face; printing
the reason twice reads like two faults and is the first thing an operator sees when the store path is
wrong.

**Evidence.** The command above; `echo $?` → `2`, stderr quoted.

**Status, 2026-11-13: fixed.** One line, once: `timeout 5 ./tillr-view serve --store ./nope.db
--addr 127.0.0.1:4461` prints `tillr-view: the store cannot be read: store: stat ./nope.db: no such file
or directory`, then `tillr-view serve: every API route answers 503 store-absent` — the duplicate is gone.
The second half is fixed too: the process no longer refuses to start, so a browser gets a rendered window
over a `503`, and a non-database file answers `503 store-unreadable` (`timeout 5 … --store ./junk.db` →
`store: opening ./junk.db read-only: file is not a database (26)`). Both commands exit `124` because
`timeout` killed a server that was still running, which is the point.

### F14 — The board has no `h1`, and its row names begin with a position glyph

**Severity:** nit

**What I did.** Read the accessibility tree (`Accessibility.getFullAXTree`) and walked the page with
Tab and Enter.

**What happened.** `<html lang="en">` is present, the item panel renders its own `H1`, and the board's
sections are `H2`s with no `H1` above them; rows are real `<button>`s, reachable in DOM order and
activated by Enter (Tab, Tab, Enter navigated to `#/item/item-bell-coalesce`). The state chips and
muted text pass contrast comfortably (`#2f63c9` on `#e8eefb` = 4.82, `#b3261e` on `#fdecea` = 5.72,
`#16723d` on `#e5f5eb` = 5.30, muted text 16.60) and the focused row draws `solid 2px` — so the
keyboard path works. The row's accessible name, though, is `"2 | The bar is ironbar's config …| item-bar-ironbar-config"`:
a screen reader announces the row number the alphabet gave it.

**What I expected and why.** Everything here is decorative except the row number: it is the same
wrong order as F1, announced aloud, and a restyle is what should carry the fix rather than a
separate change.

**Evidence.** The AX tree names listed above; the Tab/Enter sequence returned
`location.href` = `http://localhost:4318/#/item/item-bell-coalesce`.

**Status, 2026-11-13: open, narrowed.** The row's accessible name no longer begins with a position: the
gutter prints the store's `rank` when the item has one, and marks the ones that do not
(`queue-row__rank`, `data-rank="absent"|"recorded"`), so a name now reads
`0 | The composed-pipeline case … | item-pipe-real-registry`. The `h1` is still missing — the front end's
only `<h1>` is the detail pane's title, and the board's sections are still `H2`s. What would close it: a
board heading, `h1`, above the rail. (This lane had no Chrome DevTools tool, so the row name above is the
component's composition rather than a re-read `Accessibility.getFullAXTree`.)

### F15 — The header's four numbers no longer add up to the board's item count

**Severity:** minor

**What I did.** Compared the store's own counts with the four stat values the header renders, on a
board whose store gained a `closed` item after this audit was written.

**What happened.** The store answers 41 items, `{"by_state":{"blocked":13,"closed":1,"in_queue":25,"landed":2},"dispatchable":25,"blocked":13,"runs_live":0}`,
while the header renders In queue 25, Blocked 13, Landed 2, Running 0 — forty of forty-one. `closed`
is a state the front end names (`STATE_LABELS.closed`) and the rail draws, and no stat counts it, so
one item is on the board and in none of the headline numbers. "The counts that are checkable are
right" was true of the audit's store and is false by one of this one.

**Status, 2026-11-13: fixed.** The header now renders a stat for every state the store reports and
one for the item count itself, so its numbers add up and a state nobody wrote a box for still shows:

```
$ curl -s .../snapshot | jq -c '.data.counts.by_state'
{"blocked":13,"closed":1,"in_queue":25,"landed":2}

header, read from the running page:
In queue 25 · Blocked 13 · Landed 2 · Closed 1 · Running 0 · Items 41
```

25 + 13 + 2 + 1 = 41, and the group's `aria-label` names the total ("Board counts, 41 items").

**What I expected and why.** Four numbers over a list of items read as a partition of that list. A
state the board draws but the header does not count is how "nothing is missing" becomes untrue
quietly, and the operator's check — the stats add up — fails without anything looking wrong.

**Evidence.**

```
$ curl -s .../api/v1/snapshot | jq -c '.data.counts'
{"by_state":{"blocked":13,"closed":1,"in_queue":25,"landed":2},"dispatchable":25,"blocked":13,"runs_live":0}

$ curl -s .../api/v1/snapshot | jq '.data.items | length'
41
```

and `Stats` in `tools/tillr-view/web/src/components/ObservatoryHeaderBar.tsx` renders exactly four:
In queue, Blocked, Landed, Running.

**Status, 2026-11-13: open.** What would close it: a stat for every state the rail draws, or a total
beside them, so the four (or five) numbers can be added up against the queue's own length.

## What works

- **The error contract.** Five distinct refusal codes where five situations deserve them
  (`not_found`, `no_design_doc`, `design_doc_missing`/`design_doc_unreadable`,
  `unknown_kind`, `unknown_route`), all `application/json; charset=utf-8`, all with the same
  `{snapshot, error:{code,message}}` body — F12 is the one exception. A bad id is cleanly
  distinguishable from a good id with nothing in it: `/items/item-seed-01` (no design document)
  answers `no_design_doc` while the item itself is readable.
  *Status, 2026-11-13: still true, and wider* — the vocabulary is ten codes and every one is
  kebab-case now (`item-not-found`, `run-not-found`, `no-design-doc`, `design-doc-missing`,
  `design-doc-outside-root`, `design-doc-unreadable`, `unknown-kind`, `unknown-route`,
  `store-absent`, `store-unreadable`), still one `{snapshot, error:{code,message}}` body with
  `application/json; charset=utf-8`, and the routing refusal now carries the real moment as well
  (F12).
- **The design-document route works.** `GET /api/v1/items/item-seed-02/doc` answers 200 with
  `path: /workspace/docs/design/seat-profiles.md`, `bytes: 121266`, `truncated: false` and the
  Markdown itself; pointing the same server at an empty root turns that into
  `design_doc_unreadable` rather than an empty document.
- **The live channel is real.** A `WebSocket` on `/api/v1/ws` receives
  `{"type":"snapshot.invalidated","event_head_seq":53,"changed":["items:item-trace-correlation","snapshot"]}`
  after a write to the served copy, and the page re-rendered the row's badge from `in_queue` to
  `working` without a reload within the poll that followed, with the header's sequence number
  advancing `#53 → #54`.
- **The run pipeline is honest about what it did not see.** The timed-out run's page reads
  `STAGE PIPELINE — (1 OF 7 STAGES OBSERVED)`, with `boundary` at `UNOBSERVED SEAM`, `gate`,
  `land` and `verdict` at `ABSENT`, `Evidence streams: log unindexed · proc unindexed · git
  unindexed · brief derived_clock`, and the missing trace index named as the cause rather than
  rendered as emptiness.
- **The counts that are checkable are right.** 41 items, 24 in-queue / 16 blocked / 1 landed and
  2 runs agree item-for-item and number-for-number between `GET /api/v1/snapshot` and
  `tillr-view status --store /workspace/.tillr/state.db`; every item's projected state matched
  the CLI's raw state through the `in-queue → in_queue` spelling.
  *Status, 2026-11-13: still true of the items and the runs, no longer true of the header* — the
  store's 41 items are now 25 in-queue / 13 blocked / 2 landed / 1 closed, they still agree between
  the two faces, and the header shows four of those five numbers, which is F15.
- **Cost and duration are on the board per run**, as `run-item-seed-04-… · timed-out · 1800.0s ·
  $0.0530` on the row and `1800.00s` on the run page; the header totals the drain's spend as
  `$0.1630`, which matches both the store's run rows and the loop's `spend_usd`.

## The five operator questions

| Question | Answered? | Findings | Status, 2026-11-13 |
|---|---|---|---|
| What is in the queue, in what order? | no — sorted alphabetically and numbered as if that were the order | F1, F14 | **yes** — the payload carries `rank` and arrives in rank order, and the rail prints the rank in its gutter; a row's name no longer announces a position (F1 fixed; the missing `h1` is what F14 keeps) |
| What is running right now? | partly — lanes and a running count are shown, but an item with no run can read `working` | F9, F10 | **still partly** — reproduced: an item moved to `in-progress` projects `working` with `latest_run: null` and `runs_live: 0` in one payload (F9); the counts are server-computed now but reach no CLI face (F10) |
| What is stuck and why? | partly — the state is right; the cause is a constant string, and open design questions are invisible in the queue | F5, F6, F7 | **still partly** — thirteen blocked items, one constant string and no edge between them (F6); six queued items carry four open questions each and the projection does not say so (F5); a thirty-minute timeout still counts nowhere (F7) |
| What did the last tick do? | no — the tick count is a decision count, and the tick's own account is unread | F2, F4, F11 | **still no** — `ticks` is still ten dispatch rows over two moments, `last_stop_reason` is still `null` while the file holds `queue-drained`, and the first tick's file still reports zeros (F2, F4, F11) |
| Is this telling me the truth? | partly — every cross-checkable number matches its CLI; the header's numbers cannot be cross-checked, a vanished store reads `present`, and one result file is false | F3, F10, F11, F12, F15 | **still partly** — a store replaced under the running server still reads `present` while its timestamp advances (F3, reproduced); the routing refusal's moment is real now but its cursor is still `0` (F12); the header's four stats no longer sum to the items (F15) |

## Order to fix

Read at 16:05; F1, F13 and half of F14 have since been closed, so the list below is the order the
remaining work should be taken in — F3 first.

1. **F1** — it is the only finding that makes an operator act on the wrong item, and the fix is a
   column in a query plus a field in the payload. *(2026-11-13: fixed.)*
2. **F3** — a frozen board that reports itself healthy is the failure this whole face exists to
   prevent; `present` must be measured, not asserted, and a failure to read must be rendered.
3. **F4** — it is a one-file read of a source the server already holds, and its absence is the only
   place the board makes a claim about itself that is false by construction.
4. **F2** — the same read as F4 fixes the tick count, and the number is the loop's headline.
5. **F7** — a timed-out run must appear in the drain's account; until it does, `0 rejected` is a
   reassurance nobody should take.
6. **F5** — needs a projection decision (does an unanswered question make an item "held"?) before a
   surface change, which is why it follows the four that are pure reads.
7. **F6** — the same shape as F5 for the sixteen blocked items; a cause field beats a constant.
8. **F8** — replace the count with "observed" versus "unobserved", so a gap row stops being an
   exchange.
9. **F9** — decide whether `working` means "the store says in-progress" or "a run is live"; today it
   means both.
10. **F10** — publish the drain summary through a CLI face so the header can be checked by the
    command an operator already trusts.
11. **F11** — the loop's first result file must be finalized or not written; it is a trap for F4's fix.
12. **F12** — a null cursor on the one refusal that has no store read; small, and it closes the only
    hole in the error contract.
13. **F13** — a duplicate stderr line.
14. **F14** — an `h1` and a row name without a position glyph; carry it with the restyle.
