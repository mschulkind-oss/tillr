// The queue, as the queue: one row per item, in the order the store will dispatch them.
// `rank` is a sparse lexical token, so the gutter's numbers are the board's own statement
// of priority rather than an accident of the order rows came back in.
//
// The order is one list with two kinds of row in it, and the rail must not draw them as
// one continuum: a ranked row shows the store's token, and the items the store never
// ranked — every blocked, landed and closed row in this seed, 16 of 65 — are drawn under
// their own heading, because an id-ordered tail continuing the list reads as a recorded
// priority that was simply low. `UnrankedHeading` names the count and the one command that
// corrects it; `rankProvenance` catches the case where the payload carries no rank at all,
// which is a server older than the field rather than a store that never ranked anything.

import { FinishedBand } from "./FinishedBand";
import { NowBand } from "./NowBand";
import { RefusalState } from "./RefusalState";
import type { RefusalSentence } from "./RefusalState";
import { StateGlyph } from "./StateGlyph";
import { hasRank, rankProvenance, splitByRank, stateLabel, unrankedCount } from "../models/board";
import type { BoardFilter, BoardView, RankProvenance } from "../models/board";
import type { RefObject } from "react";
import type { BuildStamp, ItemView, SnapshotData } from "../models/types";

/** The store's own refusals, which are not an empty queue: each names a condition with its
 * own remedy, so the rail prints the server's code and message rather than a drawn board
 * that would read as a finished drain. A code that is not here falls through to the shared
 * component's `the face refused: <code>` branch, which is the point of `RefusalState`. */
const STORE_REFUSALS: Record<string, RefusalSentence> = {
  "store-absent": {
    title: "The store is not there",
    body: "The screen has no rows to draw, so it says so rather than showing an empty queue.",
    tone: "notice--accent",
    retry: true,
  },
  "store-unreadable": {
    title: "The board could not read the store",
    body: "The screen has no rows to draw, so it says so rather than showing an empty board.",
    tone: "notice--blocked",
    retry: true,
  },
  "schema-too-new": {
    title: "The store's schema is newer than this face",
    body: "This store was written by a schema version this face will not read. Reading it anyway would show numbers that look current and are not.",
    tone: "notice--blocked",
    retry: true,
  },
  "schema-uninitialized": {
    title: "The store's schema is not initialized",
    body: "The store has no schema this face can read, so it says so rather than showing an empty board.",
    tone: "notice--blocked",
    retry: true,
  },
  "table-absent": {
    title: "The store is missing a table this face reads",
    body: "The store answered, and a table it points at is not there — a defect, not an empty board.",
    tone: "notice--blocked",
    retry: true,
  },
};

export interface QueueRailProps {
  /** The queue's rows, already ordered by `queueOrder`, or undefined while the first read
   * is in flight. */
  items: ItemView[] | undefined;
  total: number;
  /** How many of the whole board's rows carry no rank, counted before the filter. The
   * unranked heading names the rows it is drawing and, when a narrowing hides some, the
   * number hiding behind it — a block that reported only its visible rows would shrink
   * when the operator typed in the filter box. */
  unrankedTotal?: number;
  selectedItem: string | null;
  onSelectItem: (itemId: string) => void;
  /** The board's narrowing: the text needle the box draws, and the states the header's
   * counts select. The box renders the text half; the state half is the header's control. */
  filter: BoardFilter;
  onFilterChange: (text: string) => void;
  /** Clears every filter, not just this box: one control, whichever half is narrowing. */
  onClearFilter: () => void;
  /** Rows the narrowing hides, so the rail's own line cannot disagree with the header's. */
  hidden: number;
  filterRef: RefObject<HTMLInputElement | null>;
  /** Closes the rail where it is a drawer (narrow windows). */
  onCloseRail?: () => void;
  /** The store the board reads could not be found, which is not the same as an empty
   * queue: an empty board says so instead of looking finished. */
  storeAbsent: boolean;
  /** The store refused the read outright, in the rail's place (design §5.2). It carries the
   * server's code, so an unknown one is printed rather than folded into an empty queue. */
  refusal?: { code: string; message: string } | null;
  onRetry?: () => void;
  /** The snapshot the now band reads its live runs from (design §4.4). Undefined while the
   * first read is in flight, and the band is absent with it. */
  snapshot?: SnapshotData;
  /** The build that answered, from the response cursor (`snapshot.build` on the envelope,
   * not on the board's data). The unranked heading names it when the payload carries no
   * `rank` at all, because that server's identity is the whole diagnosis. */
  build?: BuildStamp;
  /** The viewer's clock. A test pins it, the band ticks it, and a paused board hands it the
   * moment of the pause so the band's elapsed and lease stop where the reader left them. */
  now?: number;
  /** Which half of the store this rail draws (design §4.5). Absent means the queue, so a
   * caller that predates the archive still gets the spine. */
  view?: BoardView;
  /** Switches views. The header's finished count and this rail's own link call it. */
  onSelectView?: (view: BoardView) => void;
  /** Every terminal item, for the header's "N finished →" link and the empty queue's one
   * line about where the work went. */
  finished?: number;
  /** Every item the snapshot carries, so an empty view can say "of N in the store" rather
   * than reading as an empty store. */
  storeTotal?: number;
  /** How many rows the other view's filter matches, so a filtered-empty rail can say the
   * item is one click away instead of dead-ending. */
  otherMatches?: number;
  /** The newest terminal items, for the summary band (design §4.5). */
  recent?: ItemView[];
}

function QueueRow({
  item,
  selected,
  onSelect,
}: {
  item: ItemView;
  selected: boolean;
  onSelect: () => void;
}) {
  const state = item.projected_state.state;
  // The loop's own reason for a hold, when its latest decision was a question. The state
  // stays the seven-state reading; the reason is what a reader needs to know the wait is a
  // ruling rather than a lane (serve.go's itemProjection.Hold).
  const hold = item.projected_state.hold ?? "";
  const rank = hasRank(item) ? item.rank : null;
  const open = typeof item.open_criteria === "number" ? item.open_criteria : null;
  return (
    <li>
      <button
        type="button"
        className="queue-row"
        data-state={state}
        data-rank={rank === null ? "absent" : "recorded"}
        aria-current={selected ? "true" : "false"}
        onClick={onSelect}
      >
        {/* The state's shape, in the row's leading position (design §7.2): greyscale and a
         * colour-blind reader lose the border's hue, never the silhouette. */}
        <span className="queue-row__glyph">
          <StateGlyph state={state} />
        </span>
        {/* A rank is the store's own token and the operator quotes it verbatim. An item
         * the store never ranked shows `—`, never a number: a count in this gutter is
         * indistinguishable from a priority, which is the confusion the unranked heading
         * exists to end. */}
        <span
          className="queue-row__rank"
          title={
            rank === null
              ? "the store records no rank for this item; the queue falls back to item id after the ranked items"
              : `queue rank ${rank}`
          }
        >
          {rank ?? "—"}
        </span>
        <span className="queue-row__title" title={item.title}>
          {item.title}
        </span>
        <span className="queue-row__meta">
          <span className="queue-row__id">{item.item_id}</span>
          <span className="queue-row__state" data-hold={hold === "" ? undefined : hold}>
            {hold === "" ? stateLabel(state) : hold}
          </span>
        </span>
        {open === null ? null : (
          <span className="queue-row__count" title={`${open} open criteria`}>
            {open}
          </span>
        )}
      </button>
    </li>
  );
}

/** The correction acts only on one item already in the store. */
function howToRank(): string {
  return "Record a priority with `tillr item rank --store <store> --item <id> --rank <token>`.";
}
/** UnrankedHeading heads the block of items the store never ranked.
 *
 * The queue is one order, and its tail is the store's silence: `unrankedCount` items sort
 * after every ranked one, by id. Drawn as a continuation of the list, that tail reads as a
 * priority nobody recorded — the failure measured on 2026-11-14, where `item-seed-01`
 * (blocked) sorted after rank `z` and nothing on screen distinguished it from a recorded
 * priority. So the heading names the block, its count, and the one command that corrects
 * it.
 *
 * The `server-unranked` variant is a different fact with no remedy in the store: the
 * payload carries no `rank` field at all, so the order is item id and the board will not
 * present it as priority. It names the build that answered, because a board that cannot
 * say which build served the page cannot be vouched for, and it says "unverified" rather
 * than guessing when the server sends no build stamp. */
function UnrankedHeading({
  provenance,
  shown,
  total,
  build,
}: {
  provenance: RankProvenance;
  shown: number;
  total: number;
  build?: BuildStamp;
}) {
  const count = shown === total ? `${shown}` : `${shown} of ${total}`;
  const noun = total === 1 && shown === total ? "item" : "items";
  if (provenance === "server-unranked") {
    const buildName = build
      ? build.revision
        ? `build ${build.revision.slice(0, 12)}${build.modified ? "+dirty" : ""}`
        : `build ${build.executable || "unnamed"}`
      : "build unverified — this server names no build";
    return (
      <header className="queue-unranked__head" data-provenance={provenance}>
        <h3 className="queue-unranked__title" id="queue-unranked-title">
          No priority in this payload — {count} {noun}
        </h3>
        <p className="queue-unranked__note">
          This server sends no <code>rank</code> field, so it predates the queue&rsquo;s rank
          and the order below is item id, not priority ({buildName}). The board will not
          present an order it was not given as a priority it can vouch for.
        </p>
      </header>
    );
  }
  return (
    <header className="queue-unranked__head" data-provenance={provenance}>
      <h3 className="queue-unranked__title" id="queue-unranked-title">
        No priority recorded — {count} {noun}
      </h3>
      <p className="queue-unranked__note">
        {provenance === "store-unranked"
          ? "This store has never ranked an item, so the whole queue is in item id order. "
          : "The store carries no rank for these, so the queue falls back to item id after every ranked item. "}
        {howToRank()}
      </p>
    </header>
  );
}

export function QueueRail({
  items,
  total,
  unrankedTotal,
  selectedItem,
  onSelectItem,
  filter,
  onFilterChange,
  onClearFilter,
  hidden,
  filterRef,
  storeAbsent,
  refusal,
  onRetry,
  onCloseRail,
  snapshot,
  build,
  now,
  view = "queue",
  onSelectView,
  finished = 0,
  storeTotal,
  otherMatches = 0,
  recent = [],
}: QueueRailProps) {
  // The one order, cut into the two blocks the rail draws. The cut is taken from `queueOrder`'s
  // own answer, so concatenating the two lists is the client's whole order rule — the split
  // is a heading, never a second sort.
  const rows = items ?? [];
  const { ranked, unranked } = splitByRank(rows);
  const provenance = rankProvenance(rows);
  const unrankedShown = unranked.length;
  const unrankedAll = unrankedTotal ?? unrankedCount(rows);
  const atStoreTotal = storeTotal ?? total;
  const viewWord = view === "archive" ? "finished" : "queue";
  const otherWord = view === "archive" ? "queue" : "finished";
  return (
    <aside className="pane pane--rail" aria-label={view === "archive" ? "Archive" : "Queue"}>
      <div className="pane__head">
        <h2 className="pane__title">{view === "archive" ? "Archive" : "Queue"}</h2>
        {/* Where finished work went, in one control. On the queue it is the way into the
         * archive; on the archive it is the way back (design §4.5). */}
        {onSelectView === undefined ? null : view === "archive" ? (
          <button type="button" className="btn btn--quiet" onClick={() => onSelectView("queue")}>
            ← Queue
          </button>
        ) : finished > 0 ? (
          <button type="button" className="btn btn--quiet" onClick={() => onSelectView("archive")}>
            {finished} finished →
          </button>
        ) : null}
        {onCloseRail ? (
          <button type="button" className="btn btn--quiet" onClick={onCloseRail}>
            Close ✕
          </button>
        ) : null}
        <span className="pane__count">
          {/* A refused store has no count to report: `—`, never `0` (design §5.2). */}
          {refusal
            ? "—"
            : items === undefined
              ? "reading…"
              : `${items.length}${items.length === total ? "" : ` / ${total}`}`}
        </span>
      </div>
      {/* The live run, copied above the list and outside its order (design §4.4): the row
       * keeps its rank position, and the band is absent when no lease is live. It sits
       * above the filter because it is a summary of the board, not a member of the
       * narrowed set. */}
      <NowBand snapshot={snapshot} now={now} onSelectItem={onSelectItem} />
      {/* The summary of what closed, on the queue only: the archive's own list is the
       * full answer, and a band over it would repeat the top of the screen. */}
      {view === "queue" ? (
        <FinishedBand
          items={recent}
          now={now}
          onSelectItem={onSelectItem}
          onOpenArchive={() => onSelectView?.("archive")}
        />
      ) : null}
      {/* There is nothing to narrow while the store refuses: the box would filter a
       * refusal, which is not a set of rows. */}
      {refusal ? null : (
        <div className="queue-filter">
          <input
            ref={filterRef}
            className="queue-filter__input"
            type="search"
            value={filter.text}
            placeholder="Filter by id or title"
            aria-label={view === "archive" ? "Filter the archive" : "Filter the queue"}
            onChange={(event) => onFilterChange(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Escape") {
                onFilterChange("");
                event.currentTarget.blur();
              }
            }}
          />
          <span className="queue-filter__hint">/</span>
        </div>
      )}
      {hidden > 0 ? (
        <div className="queue-filter__hidden">
          <span>{hidden} hidden by the filter</span>
          <button type="button" className="btn btn--quiet" onClick={onClearFilter}>
            Clear filters
          </button>
        </div>
      ) : null}
      <div className="pane__scroll">
        {refusal ? (
          <RefusalState
            code={refusal.code}
            message={refusal.message}
            sentences={STORE_REFUSALS}
            onRetry={onRetry}
            retryLabel="Read the store again"
          />
        ) : items === undefined ? (
          <div className="skeleton" aria-label="reading the queue">
            {[0, 1, 2, 3, 4, 5].map((row) => (
              <div className="skeleton__row" key={row}>
                <span className="skeleton__line" />
                <span className="skeleton__line" style={{ width: `${70 - row * 6}%` }} />
                <span className="skeleton__line" />
              </div>
            ))}
          </div>
        ) : items.length === 0 ? (
          <div className="queue-empty">
            {storeAbsent ? (
              <p>the store is not there, so the queue has nothing to read</p>
            ) : atStoreTotal === 0 ? (
              <p>the store carries no items</p>
            ) : total === 0 ? (
              // The view itself is empty, not the filter. A drained queue must not read
              // as a drained store: it says where the work went and offers the one click
              // (design §4.5, §5.2).
              view === "archive" ? (
                <>
                  <p>the archive is empty — no item in the store has finished</p>
                  <button type="button" className="btn" onClick={() => onSelectView?.("queue")}>
                    Back to the queue
                  </button>
                </>
              ) : (
                <>
                  <p>
                    the queue is empty — {finished} finished item
                    {finished === 1 ? " is" : "s are"} in the archive
                  </p>
                  <button type="button" className="btn" onClick={() => onSelectView?.("archive")}>
                    Open the archive
                  </button>
                </>
              )
            ) : (
              // A rail that emptied because of a filter must not read as an empty store:
              // it names the view it is hiding and offers the one way back. When the
              // filter matches the other view, it says that too: search has to reach a
              // landed item (design §4.5).
              <>
                <p>
                  no item matches the filter — {total} {viewWord} item
                  {total === 1 ? "" : "s"}, none shown
                </p>
                {otherMatches > 0 ? (
                  <button
                    type="button"
                    className="btn"
                    onClick={() => onSelectView?.(view === "archive" ? "queue" : "archive")}
                  >
                    {otherMatches} {otherWord} item{otherMatches === 1 ? " matches" : "s match"} —
                    open the {otherWord === "queue" ? "queue" : "archive"}
                  </button>
                ) : null}
                <button type="button" className="btn" onClick={onClearFilter}>
                  Clear filters
                </button>
              </>
            )}
          </div>
        ) : (
          <div className="queue-list">
            {ranked.length > 0 ? (
              <ul className="queue">
                {ranked.map((item) => (
                  <QueueRow
                    key={item.item_id}
                    item={item}
                    selected={item.item_id === selectedItem}
                    onSelect={() => onSelectItem(item.item_id)}
                  />
                ))}
              </ul>
            ) : null}
            {unranked.length > 0 ? (
              /* One heading for the block, not one per row: the fact is the same for every
               * row under it, and a repeated line would bury the rows it explains. */
              <section className="queue-unranked" aria-labelledby="queue-unranked-title">
                <UnrankedHeading
                  provenance={provenance}
                  shown={unrankedShown}
                  total={unrankedAll}
                  build={build}
                />
                <ul className="queue">
                  {unranked.map((item) => (
                    <QueueRow
                      key={item.item_id}
                      item={item}
                      selected={item.item_id === selectedItem}
                      onSelect={() => onSelectItem(item.item_id)}
                    />
                  ))}
                </ul>
              </section>
            ) : null}
          </div>
        )}
      </div>
    </aside>
  );
}
