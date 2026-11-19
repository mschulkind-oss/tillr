// The URL is the only UI navigation state (design §5.2): no router library, and every
// selection round-trips through the location hash so a pasted URL opens the exact item,
// run, tab and exchange. The item is the route — `#/item/<id>` — and everything else rides
// behind it as search parameters, which is what makes a reload land back on the same item.
//
// The board's narrowing is the one piece of state that must survive with no item selected,
// so it rides in the location's own search instead: `?states=blocked,working&q=parser`.
// Keeping it out of the hash leaves "the item is the route" true, and makes a filtered
// board a link whether or not a row is open.

import { useCallback, useEffect, useState } from "react";

import type { BoardFilter } from "./board";

export type Tab = "transcript" | "exchanges" | "timeline" | "gate";

/** The tab a selection lands on: the run's own transcript, because it is the one record that
 * answers "what did this run actually do" for a live run and a finished one alike, and it is
 * the one that does not need tillr-trace to read the run's artifacts. The exchanges and the
 * timeline are the supervisor's and the run's recorded streams' views of the same run, one
 * click away. */
export const DEFAULT_TAB: Tab = "transcript";

export interface Selection {
  item: string | null;
  run: string | null;
  tab: Tab;
  exchange: string | null;
  /** The artifact kind the payload drawer has open, if any. */
  artifact: string | null;
}

export const DEFAULT_SELECTION: Selection = {
  item: null,
  run: null,
  tab: DEFAULT_TAB,
  exchange: null,
  artifact: null,
};

const ITEM_ROUTE = "#/item/";

/** The questions view is a route of its own — `#/questions` — so the one place a reader
 * can reach without first knowing an item is also a link they can send. It is a view, not
 * an item selection: opening it clears the selected item, and selecting an item leaves it. */
export const QUESTIONS_ROUTE = "#/questions";

/** History API writes do not emit native hashchange/popstate. Notify every route reader
 * after the current state update, so the shell and legacy screens see the same URL. */
export const URL_CHANGE_EVENT = "observatory:urlchange";
function notifyURLChange(): void {
  queueMicrotask(() => window.dispatchEvent(new Event(URL_CHANGE_EVENT)));
}

function parseTab(raw: string | null): Tab {
  if (raw === "timeline") return "timeline";
  if (raw === "transcript") return "transcript";
  if (raw === "exchanges") return "exchanges";
  if (raw === "gate") return "gate";
  return DEFAULT_TAB;
}

/** selectionFromSearch reads the five parameters. Anything absent stays absent: an
 * unselected item is not a default item. */
export function selectionFromSearch(search: string): Selection {
  const params = new URLSearchParams(search);
  return {
    item: params.get("item"),
    run: params.get("run"),
    tab: parseTab(params.get("tab")),
    exchange: params.get("exchange"),
    artifact: params.get("artifact"),
  };
}

/** selectionFromHash reads the route and the parameters behind it. A hash this screen does
 * not recognize is no selection at all rather than a guess at what was meant. */
export function selectionFromHash(hash: string): Selection {
  if (!hash.startsWith(ITEM_ROUTE)) return { ...DEFAULT_SELECTION };
  const rest = hash.slice(ITEM_ROUTE.length);
  const mark = rest.indexOf("?");
  const itemId = decodeURIComponent(mark === -1 ? rest : rest.slice(0, mark));
  if (itemId === "") return { ...DEFAULT_SELECTION };
  const params = new URLSearchParams(mark === -1 ? "" : rest.slice(mark));
  return {
    item: itemId,
    run: params.get("run"),
    tab: parseTab(params.get("tab")),
    exchange: params.get("exchange"),
    artifact: params.get("artifact"),
  };
}

function paramsFrom(selection: Selection): URLSearchParams {
  const params = new URLSearchParams();
  if (selection.run) params.set("run", selection.run);
  if (selection.tab !== DEFAULT_TAB) params.set("tab", selection.tab);
  if (selection.exchange) params.set("exchange", selection.exchange);
  if (selection.artifact) params.set("artifact", selection.artifact);
  return params;
}

/** searchFromSelection writes only the parameters that are set, in one fixed order, so a
 * shared URL carries the selection and nothing else. */
export function searchFromSelection(selection: Selection): string {
  const params = new URLSearchParams();
  if (selection.item) params.set("item", selection.item);
  for (const [key, value] of paramsFrom(selection)) params.set(key, value);
  const search = params.toString();
  return search ? `?${search}` : "";
}

/** hashFromSelection writes the item as the route and everything else behind it. */
export function hashFromSelection(selection: Selection): string {
  if (!selection.item) return "";
  const params = paramsFrom(selection).toString();
  const path = `${ITEM_ROUTE}${encodeURIComponent(selection.item)}`;
  return params ? `${path}?${params}` : path;
}

/** useUrlSelection holds the selection in `window.location.hash` and pushes a history
 * entry on every change, so the browser's back button walks the inspection. */
export interface UpdateOptions {
  /** Replace the current history entry instead of pushing one. The board's first render
   * uses it to land on the queue's head without leaving an entry the back button must walk
   * through before it can leave the screen. */
  replace?: boolean;
}

export function useQuestionsRoute(): [boolean, (open: boolean, options?: UpdateOptions) => void] {
  const [open, setOpen] = useState(() => window.location.hash === QUESTIONS_ROUTE);

  useEffect(() => {
    const onPop = () => setOpen(window.location.hash === QUESTIONS_ROUTE);
    window.addEventListener("popstate", onPop);
    window.addEventListener("hashchange", onPop);
    window.addEventListener(URL_CHANGE_EVENT, onPop);
    return () => {
      window.removeEventListener("popstate", onPop);
      window.removeEventListener("hashchange", onPop);
      window.removeEventListener(URL_CHANGE_EVENT, onPop);
    };
  }, []);

  const update = useCallback((next: boolean, options?: UpdateOptions) => {
    setOpen(next);
    const { pathname, search, hash } = window.location;
    const target = next ? `${pathname}${search}${QUESTIONS_ROUTE}` : `${pathname}${search}`;
    if (target !== `${pathname}${search}${hash}`) {
      // Opening or closing the view is a history step a reader expects the back button to
      // undo. A selection that leaves the view passes `replace`, so the item's own route is
      // the entry the back button returns to rather than the view it was opened from.
      if (options?.replace) window.history.replaceState(null, "", target);
      else window.history.pushState(null, "", target);
      notifyURLChange();
    }
  }, []);

  return [open, update];
}

export function useUrlSelection(): [
  Selection,
  (next: Partial<Selection>, options?: UpdateOptions) => void,
] {
  const [selection, setSelection] = useState<Selection>(() =>
    selectionFromHash(window.location.hash),
  );

  useEffect(() => {
    const onPop = () => setSelection(selectionFromHash(window.location.hash));
    window.addEventListener("popstate", onPop);
    window.addEventListener("hashchange", onPop);
    window.addEventListener(URL_CHANGE_EVENT, onPop);
    return () => {
      window.removeEventListener("popstate", onPop);
      window.removeEventListener("hashchange", onPop);
      window.removeEventListener(URL_CHANGE_EVENT, onPop);
    };
  }, []);

  const update = useCallback((next: Partial<Selection>, options?: UpdateOptions) => {
    setSelection((current) => {
      const merged = { ...current, ...next };
      const hash = hashFromSelection(merged);
      if (hash !== window.location.hash) {
        // The search is the board's narrowing, which a selection does not touch: dropping
        // it here would clear the operator's filter every time they opened a row.
        const url = `${window.location.pathname}${window.location.search}${hash}`;
        if (options?.replace) window.history.replaceState(null, "", url);
        else window.history.pushState(null, "", url);
        notifyURLChange();
      }
      return merged;
    });
  }, []);

  return [selection, update];
}

/** filterFromSearch reads the narrowing back out of the location's search. A parameter the
 * screen does not recognize is ignored rather than guessed at, and `states` is a plain
 * comma-separated list of projected-state tokens: 41 rows do not need a query language. */
export function filterFromSearch(search: string): BoardFilter {
  const params = new URLSearchParams(search);
  const states = (params.get("states") ?? "")
    .split(",")
    .map((state) => state.trim())
    .filter((state) => state !== "");
  const view = params.get("view");
  return {
    states,
    text: params.get("q") ?? "",
    // The view is the one navigation bit that must survive with no item selected, and it
    // is not a narrowing: an absent `view` is the queue, so an old link still works.
    ...(view === "archive" ? { view: "archive" as const } : {}),
  };
}

/** searchFromFilter writes only the parameters that narrow something, so an unfiltered
 * board carries no leftover `?states=` and a cleared filter leaves a clean link. */
export function searchFromFilter(filter: BoardFilter): string {
  const params = new URLSearchParams();
  if (filter.states.length > 0) params.set("states", filter.states.join(","));
  if (filter.text.trim() !== "") params.set("q", filter.text);
  if (filter.view === "archive") params.set("view", "archive");
  const search = params.toString();
  return search ? `?${search}` : "";
}

/** useUrlFilter holds the narrowing in `window.location.search`. It replaces rather than
 * pushes: a narrowed board is one screen, and the back button is already the selection's
 * (design §5.2), so a chip click must not bury an inspection step behind it. */
export function useUrlFilter(): [BoardFilter, (next: BoardFilter) => void] {
  const [filter, setFilter] = useState<BoardFilter>(() => filterFromSearch(window.location.search));

  useEffect(() => {
    const onPop = () => setFilter(filterFromSearch(window.location.search));
    window.addEventListener("popstate", onPop);
    window.addEventListener(URL_CHANGE_EVENT, onPop);
    return () => {
      window.removeEventListener("popstate", onPop);
      window.removeEventListener(URL_CHANGE_EVENT, onPop);
    };
  }, []);

  const update = useCallback((next: BoardFilter) => {
    setFilter(next);
    const search = searchFromFilter(next);
    const { pathname, hash } = window.location;
    if (search !== window.location.search) {
      window.history.replaceState(null, "", `${pathname}${search}${hash}`);
      notifyURLChange();
    }
  }, []);

  return [filter, update];
}
