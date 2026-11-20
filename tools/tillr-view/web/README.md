# The tillr observatory front end

A browser for the loop's items, runs, passes and landings, read-only by default.
With `tillr-view serve --write`, eligible Items-page rows offer “Move to top”;
the server delegates placement to the item command, as described in the
[write-route reference](../README.md#opt-in-item-placement). The current
page layout follows the [observatory reference](../../../docs/reference/observatory-redesign.md#entities-and-urls).
It reads the `/api/v1/*` routes and posts placement only when writes are enabled;
the browser never writes the store directly.

## Running and checking

- `just observatory` builds the browser, embeds it in `tillr-view`, and serves both
  the API and browser at `http://127.0.0.1:4318`.
- `just observatory-dev` runs Vite and the Go API under the development process
  supervisor. Vite proxies API requests and websocket upgrades to that API.
- In this directory, `npm ci`, `npm run typecheck`, `npm test`, and `npm run build`
  install, check, test, and build the browser.

Pack delivery is a derived copy: run `just pack-sync` after editing this tree.
On the host, deploy the pack with `yolo host apply --assert`, then relaunch any
running jail. A browser build alone does not update a running server's embedded assets.

## Item and run pages

`#/item/<id>` opens the item's definition, criteria, document link, fence, latest
run and timeline. Consecutive decisions with identical action and reason fold into
one row, with the pass count and earliest timestamp. That row links to the item's
pass ledger. Runs and landing attempts link to their own pages.

`#/run/<id>` opens one run and combines three existing read faces on one time axis:

- The run route's staging, execution, boundary, fence, gate, landing and verdict records.
- The timeline route's recorded model turns, tool calls and other observations.
- The gate route's plan and check spans, including skipped checks with their reasons.

Legacy query-only item links redirect to the item page. Legacy item links carrying
`run` redirect, by replacing the history entry, to the run page and retain their tab
and artifact parameters. The old three-pane board is not a selectable layout.

The run page also opens the existing transcript, exchanges, claims and artifact
readers. Selected exchanges load even when the exchanges tab is not open; an unknown
exchange is named as missing. Transcript pagination still reads bounded pages, not
the entire log. Turn and tool details link to the run's transcript, but the existing
timeline route supplies no transcript entry identifier, so the viewer does not guess
one from the observation ID. Check links carry the selected run and span to highlight
that execution.

## Recording queue priority

The Items page shows [places](../../../docs/reference/observatory-actions.md#queue-order-and-places),
counted from 1 across the whole order. Filtering keeps those numbers, including
any gaps. Parked and finished items have no place. Stored ordering keys are never
sent to the page, and there is no separate unranked block.

Use `tillr item place` to move an item without changing its state, criteria,
fence or runs. The page offers the same command when the server enables writes.

`tillr item sync` fills empty keys in its own transaction after the last keyed
item, in item id order. Until sync runs, readers count those items in the same
order, so filling the keys does not change their places.

## Reading the spans

Rows nest by recorded parent. Expand or collapse a row to control its subtree.
Search matches names and recorded attributes, including tool and check names;
the count is the number of matching spans, not the number of mounted rows. Search
is held in the URL's `q` parameter, so a shared selection retains its filter.
Only rows around the visible portion of the list are mounted, even for large runs.

- Mouse wheel zooms; horizontal wheel or Shift-wheel pans. Drag the axis to pan.
- Focus the time axis: `+` and `-` zoom, Left and Right pan, and `f` fits all clocks.
- The buttons provide the same controls. A live run retains its scale until Fit.
- Select a span to inspect its start, duration, recorded attributes and entity links.
  The `span` URL parameter reopens that selection and scrolls to the row.
- The ten spans with the most time not covered by children link directly to their rows.
  That time subtracts the union of direct children's intervals, clipped to the parent:
  overlapping children do not get subtracted twice. A missing duration remains unknown.

The marked **end-time chain** *(coined here)* follows the latest finishing recorded
root and only descendants whose finish reaches their parent's end. A child that
finishes earlier has spare time before that end and is not marked as determining it.
This is an explanation from recorded parent relationships and clocks, not a claim
about unrecorded dependencies or a prediction of how much parallel work could be
shortened. Missing clocks limit the explanation.

## Missing and derived clocks

A missing clock draws an explained dashed row, never a zero-width bar. An open run
with a recorded start grows using the explicitly labeled viewer's clock; it never
acquires a fabricated recorded finish. Its scale stays fixed and its clock freezes
when paused. Recorded instants have a minimum visible mark, distinct from missing
measurements. Derived clocks are hatched.
Every row retains the server's provenance badge: recorded, derived, unobserved,
unreadable or absent. A route refusal is shown alongside the surviving records,
not disguised as an empty successful read.

The shell's pause disables both socket invalidations and fallback polling. Resuming
refreshes the reads. Navigation never adopts a newer run under the reader: the run ID
is the route, and the item page pins its latest-run summary when it first loads.

## Code and verification

| Location | Responsibility |
| :--- | :--- |
| [App](src/App.tsx) | Page routing, legacy redirects, shell and pause |
| [ItemRunPages](src/components/ItemRunPages.tsx) | Existing read faces and entity pages |
| [SpanViewer](src/components/SpanViewer.tsx) | Nested, searchable, virtualized time axis and details |
| [Span models](src/models/spans.ts) | Source composition and interval calculations |
| [API](src/models/api.ts) and [queries](src/models/queries.ts) | Typed reads and shared refresh keys |
| [Span tests](tests/span-viewer.test.tsx) | Missing clocks, nested rows, interval union, selection, controls and 5,000 rows |
| [Page tests](tests/app-selection.test.tsx) | Cold links, redirects, folding and existing routes |

The retained queue components are tested in isolation with the
[queue test composition](tests/queue-harness.tsx); that composition is not shipped as
an application route. The three replaced timing components are removed, and their
clock/provenance tests run against the unified viewer.
