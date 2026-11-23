// The board's reading of the snapshot: the queue's order, its counts, and the words a state
// is drawn with. Pure functions over the server's payload, so the screen cannot invent an
// order or an eighth state (design §3.1 projects the seven states once, server-side).

import type { DrainSummary, ItemView, ServerCounts, SnapshotData } from "./types";

/** The seven human states, in the order the board names them. */
const STATE_LABELS: Record<string, string> = {
  in_queue: "In queue",
  working: "Working",
  blocked: "Blocked",
  landed: "Landed",
  waiting: "Waiting",
  not_started: "Not started",
  resolved_elsewhere: "Resolved elsewhere",
  closed: "Closed",
};

/** stateLabel is the screen's spelling of a projected state. A state this client has not
 * seen is printed as the server spelled it rather than dropped or mislabelled. */
export function stateLabel(state: string): string {
  return STATE_LABELS[state] ?? state.replace(/_/g, " ");
}

/** Places are counted by the server before filtering. Sorting never renumbers them. */
export function queueOrder(items: ItemView[]): ItemView[] {
  return [...items].sort((a, b) => (a.place ?? Infinity) - (b.place ?? Infinity) || a.item_id.localeCompare(b.item_id));
}

/** Which half of the store the rail draws: the queue (items still to come) or the archive
 * (items that finished). It is not a filter — it does not narrow rows, it chooses which
 * rows the filter narrows, and it rides beside the filter in the URL because a reload has
 * to land on the same view (design §4.5). */
export type BoardView = "queue" | "archive";

/** The states that mean an item is finished: it waits on nothing, and the queue is not
 * where it belongs. These are the projection's spellings, which are what the wire carries;
 * the Go side names the same terminal set in `terminalItemStates`
 * (tools/tillr-view/serve.go), where the raw store state is spelled `resolved-elsewhere`
 * and the projection renames it `resolved_elsewhere`. */
export const TERMINAL_STATES = ["landed", "resolved_elsewhere", "closed"] as const;

/** isTerminalState is the split the archive is made of: landed, resolved elsewhere and
 * closed are done; everything else is still in the queue's business. */
export function isTerminalState(state: string): boolean {
  return (TERMINAL_STATES as readonly string[]).includes(state);
}

export function isTerminal(item: ItemView): boolean {
  return isTerminalState(item.projected_state.state);
}

/** queueItems is the spine: the items that are not terminal, in the order they were given
 * (the places supplied by the server). The order is not recomputed here — the rail renders it. */
export function queueItems(items: ItemView[]): ItemView[] {
  return items.filter((item) => !isTerminal(item));
}

/** archiveItems is the other half of the same list: the terminal items, in the same order,
 * drawn with the same row (design §4.5). */
export function archiveItems(items: ItemView[]): ItemView[] {
  return items.filter(isTerminal);
}

/** finishedCount is the header's finished count: every terminal item, whatever its state,
 * which is the number that stays visible while the queue is on screen. */
export function finishedCount(items: ItemView[]): number {
  return items.filter(isTerminal).length;
}

/** recentlyFinished is the summary band's rows: the last `limit` items that reached a
 * terminal state, newest first. "Last changed" is the only clock the store records for an
 * item, so it is the one the band orders by and the one it says. */
export function recentlyFinished(items: ItemView[], limit = 5): ItemView[] {
  return items
    .filter(isTerminal)
    .sort((a, b) => b.updated_at_ms - a.updated_at_ms)
    .slice(0, limit);
}

export interface BoardCounts {
  total: number;
  /** Every projected state the store reports, keyed by the seven spellings. The header
   * renders the states it has no box for from this map, so a state the store grew (a
   * `closed` row, say) is counted rather than dropped: four numbers that do not add up to
   * the items are a header quietly lying about its own inventory. */
  states: Record<string, number>;
  inQueue: number;
  blocked: number;
  landed: number;
  running: number;
}

/** boardCounts is the header's four numbers. `running` is the drain's own count of live
 * runs, not a count of items, because one item can have a run and a lane can carry two.
 *
 * The server computes the same numbers from the same rows (`snapshot.counts`), and its
 * answer wins when it is there: two arithmetics for one headline is how a board ends up
 * disagreeing with its own CLI, and the server's is the one a `tillr` command can be
 * checked against. The client-side count below is the fallback for a server that predates
 * the field, not a second opinion. */
export function boardCounts(
  items: ItemView[],
  drain: DrainSummary | undefined,
  server: ServerCounts | undefined = undefined,
): BoardCounts {
  if (server && typeof server.by_state === "object" && server.by_state !== null) {
    return {
      total: items.length,
      states: { ...server.by_state },
      inQueue: server.by_state["in_queue"] ?? 0,
      blocked: server.by_state["blocked"] ?? server.blocked,
      landed: server.by_state["landed"] ?? 0,
      running: server.runs_live,
    };
  }
  const states: Record<string, number> = {};
  for (const item of items) {
    const state = item.projected_state.state;
    states[state] = (states[state] ?? 0) + 1;
  }
  return {
    total: items.length,
    states,
    inQueue: states["in_queue"] ?? 0,
    blocked: states["blocked"] ?? 0,
    landed: states["landed"] ?? 0,
    running: drain?.running_runs ?? 0,
  };
}

/** storeState answers how the store itself is doing, which is the difference between an
 * empty board and a board that could not be read. */
export function storeState(snapshot: SnapshotData | undefined): string {
  return snapshot?.sources.store.state ?? "unknown";
}

/** matchesFilter is the queue filter's text rule: a case-insensitive substring of the
 * item's id or title. Deliberately not a query language — the filter box is for finding a
 * thing you can already name. */
export function matchesFilter(item: ItemView, filter: string): boolean {
  const needle = filter.trim().toLowerCase();
  if (needle === "") return true;
  return (
    item.item_id.toLowerCase().includes(needle) || item.title.toLowerCase().includes(needle)
  );
}

/** The states the board offers as a filter, in the order the design names its counts
 * (design §4.4: "Five counts … and each one is a filter. The operator who wants the 16
 * blocked items clicks 16"). A state the store grew but this list does not name is still
 * offered from the store's own counts, so the control's vocabulary cannot drift out of step
 * with the rows the rail is drawing. */
export const FILTER_STATES = ["in_queue", "working", "blocked", "landed", "closed"] as const;

/** The board's narrowing: the projected states the operator selected, and the text needle.
 * Both ride in the URL, so a narrowed board is a link (design §4.4). An empty `states`
 * means every state — the absence of a narrowing is not a narrowing to nothing. */
export interface BoardFilter {
  states: string[];
  text: string;
  /** The view the narrowing applies to. Absent means the queue, so a URL written before
   * the archive existed still lands on the spine. */
  view?: BoardView;
}

export const EMPTY_FILTER: BoardFilter = { states: [], text: "" };

/** matchesStateFilter is the state half: the item's projected state is one the operator
 * selected, or nothing is selected and every state passes. Liveness rides on the same
 * field — `in_queue`, `working` and `blocked` are in flight, `landed` and `closed` are
 * terminal — so one control narrows both questions the operator has. */
export function matchesStateFilter(item: ItemView, states: string[]): boolean {
  return states.length === 0 || states.includes(item.projected_state.state);
}

/** matchesBoardFilter is the rail's whole narrowing rule: the text needle and the state
 * selection, both of which have to pass. Combining them is the point — "the blocked items
 * whose title says parser" is one question, not two screens. */
export function matchesBoardFilter(item: ItemView, filter: BoardFilter): boolean {
  return matchesFilter(item, filter.text) && matchesStateFilter(item, filter.states);
}

/** filterActive says whether any narrowing is on, which is what makes the header's one
 * line appear and the clear control reachable. */
export function filterActive(filter: BoardFilter): boolean {
  return filter.states.length > 0 || filter.text.trim() !== "";
}

/** hiddenCount is how many of the board's rows the narrowing hides. It is counted from the
 * whole board rather than from the narrowed list, because the number the operator needs is
 * the one that says the board is still all there ("+31 hidden"), never a total that
 * silently shrank to the view. */
export function hiddenCount(items: ItemView[], filter: BoardFilter): number {
  return items.filter((item) => !matchesBoardFilter(item, filter)).length;
}

/** toggleState adds or removes one state from the selection, keeping the order the operator
 * clicked so the URL is stable rather than re-sorted. */
export function toggleState(states: string[], state: string): string[] {
  return states.includes(state)
    ? states.filter((selected) => selected !== state)
    : [...states, state];
}

/** filterStatesLabel is the selection in the screen's words, for the header's one line. */
export function filterStatesLabel(states: string[]): string {
  return states.map(stateLabel).join(", ");
}

/** relativeTime is how long ago a recorded instant was, in the coarsest useful unit. The
 * board is a live surface, so an age is more useful than a timestamp, and an unrecorded
 * one says so rather than showing 1970. */
export function relativeTime(ms: number | null | undefined, now: number = Date.now()): string {
  if (ms === null || ms === undefined || Number.isNaN(ms)) return "no clock";
  const delta = now - ms;
  if (delta < 0) return "in the future";
  const minutes = Math.floor(delta / 60_000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

/** localTime is a recorded instant in the browser's own time zone, for the places that
 * need an exact moment rather than an age. Never the raw UTC string. */
export function localTime(ms: number): string {
  return new Date(ms).toLocaleString();
}

/** money is a price the store reported, or the named absence of one. Never $0.0000. */
export function money(amount: number | null | undefined): string {
  return amount === null || amount === undefined ? "not reported" : `$${amount.toFixed(4)}`;
}

/** seconds is a millisecond duration as the header reads it: one decimal place, so a 71.6s
 * gate and an 83.0s gate are distinguishable numbers rather than one rounded to 72 and 83.
 * A missing measurement is `null`, never zero: the board says the gate was not measured
 * rather than drawing a zero-length bar, which would read as a fast gate. */
export function seconds(ms: number | null | undefined): string {
  return ms === null || ms === undefined ? "not measured" : `${(ms / 1000).toFixed(1)}s`;
}

/** shortId shortens an id for a dense surface without hiding which ids it is: the head and
 * tail of a long token, and the whole token when it is already short. */
export function shortId(id: string, keep = 30): string {
  if (id.length <= keep) return id;
  return `${id.slice(0, keep - 8)}…${id.slice(-7)}`;
}
