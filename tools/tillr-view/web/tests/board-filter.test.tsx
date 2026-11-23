// The board's narrowing: the state filter, the text filter, the URL that carries both, and
// the one line that says what is hidden. Two failures are what this suite exists to catch:
// a board that reads as an empty store when it is only filtered, and a filtered board that
// cannot be pasted to the next person. The counts are the controls (design §4.4), so the
// state half is driven through the header an operator actually clicks.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test } from "vitest";

import { QueueHarness as App } from "./queue-harness";
import {
  EMPTY_FILTER,
  filterActive,
  filterStatesLabel,
  hiddenCount,
  matchesBoardFilter,
  matchesStateFilter,
  toggleState,
} from "../src/models/board";
import { filterFromSearch, searchFromFilter } from "../src/models/url";
import { docData, envelope, filterSnapshotData } from "./fixtures";
import type { ApiEnvelope, ItemData } from "../src/models/types";

const items = filterSnapshotData.items;

/** A socket that never opens: the screen falls back to its own reads, and no reconnect
 * timer fires behind the assertions. */
class SilentSocket {
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: unknown }) => void) | null = null;
  onclose: (() => void) | null = null;
  close() {}
}

const originalFetch = globalThis.fetch;
const originalWebSocket = globalThis.WebSocket;

function itemDataFor(itemId: string): ItemData {
  const row = items.find((candidate) => candidate.item_id === itemId) ?? items[0];
  return {
    item: {
      item_id: row.item_id,
      title: row.title,
      body_ref: "",
      design_doc: "",
      allowed_paths: [],
      projected_state: row.projected_state,
    },
    criteria: [],
    blocked_by: [],
    decisions: [],
    attempts: [],
    runs: [],
    owner_decisions: [],
    state_moves: [],
    verdicts: [],
    closed_blockers: [],
  };
}

function stubFetch() {
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    const path = String(input);
    let body: ApiEnvelope<unknown>;
    if (path === "/api/v1/passes") {
      body = envelope([]);
    } else if (path === "/api/v1/snapshot") {
      body = envelope(filterSnapshotData);
    } else if (path.endsWith("/doc")) {
      body = envelope(docData);
    } else {
      body = envelope(itemDataFor(path.slice("/api/v1/items/".length)));
    }
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as typeof fetch;
}

function renderApp() {
  const client = new QueryClient();
  return render(
    <QueryClientProvider client={client}>
      <App />
    </QueryClientProvider>,
  );
}

/** The queue's titles, top to bottom: what the rail is actually drawing. */
function railTitles(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll(".queue-row__title")).map(
    (node) => node.textContent ?? "",
  );
}

/** One of the header's counts, which is the filter control for its state. */
function statButton(container: HTMLElement, label: string): HTMLElement {
  const button = Array.from(container.querySelectorAll<HTMLElement>("button.stat--filter")).find(
    (node) => node.querySelector(".stat__label")?.textContent === label,
  );
  if (!button) throw new Error(`the header carries no filter for ${label}`);
  return button;
}

beforeEach(() => {
  window.history.replaceState(null, "", "/item/item-queue");
  stubFetch();
  globalThis.WebSocket = SilentSocket as unknown as typeof WebSocket;
});

afterEach(() => {
  cleanup();
  globalThis.fetch = originalFetch;
  globalThis.WebSocket = originalWebSocket;
});

describe("the narrowing rule", () => {
  test("the state half selects one projected state or several, and nothing means all", () => {
    const blocked = items.find((item) => item.item_id === "item-blocked")!;
    const working = items.find((item) => item.item_id === "item-working")!;
    expect(matchesStateFilter(blocked, [])).toBe(true);
    expect(matchesStateFilter(blocked, ["blocked"])).toBe(true);
    expect(matchesStateFilter(blocked, ["working"])).toBe(false);
    expect(matchesStateFilter(blocked, ["blocked", "working"])).toBe(true);
    expect(matchesStateFilter(working, ["blocked", "working"])).toBe(true);
    // Liveness rides on the same field: the terminal half and the in-flight half are two
    // selections, not two controls.
    expect(matchesStateFilter(blocked, ["landed", "closed"])).toBe(false);
  });

  test("the two halves combine, so one question is one screen", () => {
    const parser = { ...items[2], title: "blocked parser" };
    expect(matchesBoardFilter(parser, { states: ["blocked"], text: "parser" })).toBe(true);
    expect(matchesBoardFilter(parser, { states: ["blocked"], text: "renderer" })).toBe(false);
    expect(matchesBoardFilter(parser, { states: ["working"], text: "parser" })).toBe(false);
    expect(matchesBoardFilter(parser, EMPTY_FILTER)).toBe(true);
    expect(matchesBoardFilter(parser, { states: [], text: "PARSER" })).toBe(true);
  });

  test("hidden is counted from the whole board, never from the narrowed list", () => {
    expect(hiddenCount(items, EMPTY_FILTER)).toBe(0);
    expect(hiddenCount(items, { states: ["blocked"], text: "" })).toBe(3);
    expect(hiddenCount(items, { states: [], text: "parser" })).toBe(1);
  });

  test("toggling a state adds and removes it without re-sorting the selection", () => {
    expect(toggleState([], "blocked")).toEqual(["blocked"]);
    expect(toggleState(["blocked"], "working")).toEqual(["blocked", "working"]);
    expect(toggleState(["blocked", "working"], "blocked")).toEqual(["working"]);
    expect(filterStatesLabel(["in_queue", "closed"])).toBe("In queue, Closed");
  });

  test("whitespace is not a narrowing, and a state is", () => {
    expect(filterActive(EMPTY_FILTER)).toBe(false);
    expect(filterActive({ states: [], text: "   " })).toBe(false);
    expect(filterActive({ states: ["blocked"], text: "" })).toBe(true);
    expect(filterActive({ states: [], text: "seed" })).toBe(true);
  });
});

describe("the narrowing in the URL", () => {
  test("a filter round-trips, and an unfiltered board carries no leftover parameters", () => {
    const filter = { states: ["blocked", "working"], text: "parser" };
    const search = searchFromFilter(filter);
    expect(search).toBe("?states=blocked%2Cworking&q=parser");
    expect(filterFromSearch(search)).toEqual(filter);
    expect(searchFromFilter(EMPTY_FILTER)).toBe("");
    expect(filterFromSearch("")).toEqual(EMPTY_FILTER);
    // A parameter this screen does not know is ignored rather than guessed at, and an
    // empty state slot is not a state.
    expect(filterFromSearch("?states=&q=&sort=title")).toEqual(EMPTY_FILTER);
    expect(filterFromSearch("?states=blocked,%20working")).toEqual({
      states: ["blocked", "working"],
      text: "",
    });
  });
});

describe("narrowing the board", () => {
  test("clicking a count narrows the rail, keeps the whole-board total, and says what it hid", async () => {
    const { container } = renderApp();
    // The queue is the live rows only: the landed item is in the archive now (design §4.5).
    await waitFor(() => expect(railTitles(container)).toEqual([
      "queued parser",
      "working parser",
      "blocked renderer",
    ]));

    fireEvent.click(statButton(container, "Blocked"));

    await waitFor(() => expect(railTitles(container)).toEqual(["blocked renderer"]));
    // The narrowing is a link: a reload of this URL lands on the same screen.
    expect(window.location.search).toBe("?states=blocked");
    expect(container.textContent).toContain("Showing");
    expect(container.textContent).toContain("1");
    expect(container.textContent).toContain("4");
    expect(container.textContent).toContain("+2 hidden");
    // The whole-board number stays on screen; only the narrowed set is reported beside it.
    const itemsStat = Array.from(container.querySelectorAll(".stat")).find((node) =>
      node.textContent?.startsWith("Items"),
    );
    expect(itemsStat?.querySelector(".stat__value")?.textContent).toBe("4");
    // The pressed count says which state is selected, for a reader and for a screen reader.
    expect(statButton(container, "Blocked").getAttribute("aria-pressed")).toBe("true");
    expect(statButton(container, "Working").getAttribute("aria-pressed")).toBe("false");
    expect(container.textContent).toContain("states: Blocked");
  });

  test("a second state joins the first, and one control clears every filter", async () => {
    const { container } = renderApp();
    await waitFor(() => expect(railTitles(container)).toHaveLength(3));

    fireEvent.click(statButton(container, "Blocked"));
    await waitFor(() => expect(railTitles(container)).toEqual(["blocked renderer"]));
    fireEvent.click(statButton(container, "Working"));
    await waitFor(() => expect(railTitles(container)).toEqual(["working parser", "blocked renderer"]));
    expect(window.location.search).toBe("?states=blocked%2Cworking");

    // The text half narrows the same rail, so both halves are one screen and one clear.
    fireEvent.change(container.querySelector(".queue-filter__input")!, {
      target: { value: "parser" },
    });
    await waitFor(() => expect(railTitles(container)).toEqual(["working parser"]));
    expect(window.location.search).toBe("?states=blocked%2Cworking&q=parser");

    fireEvent.click(
      Array.from(container.querySelectorAll("button")).find(
        (node) => node.textContent === "Clear filters",
      )!,
    );

    await waitFor(() => expect(railTitles(container)).toHaveLength(3));
    expect(window.location.search).toBe("");
    expect((window.location.pathname + window.location.search)).toBe("/item/item-queue");
    expect(container.querySelector(".filter-line")).toBeNull();
  });

  test("a pasted URL opens the sender's screen, reload and all", async () => {
    window.history.replaceState(null, "", "/?states=blocked&q=renderer#/item/item-blocked");
    const { container } = renderApp();

    await waitFor(() => expect(railTitles(container)).toEqual(["blocked renderer"]));
    expect(container.querySelector<HTMLInputElement>(".queue-filter__input")?.value).toBe(
      "renderer",
    );
    expect(container.textContent).toContain("+2 hidden");
  });

  test("a board hidden entirely by a filter says so instead of reading as an empty store", async () => {
    const { container } = renderApp();
    await waitFor(() => expect(railTitles(container)).toHaveLength(3));

    // A needle no row carries hides every live row without emptying the store.
    fireEvent.change(container.querySelector(".queue-filter__input")!, {
      target: { value: "nothing matches this" },
    });

    await waitFor(() => expect(railTitles(container)).toEqual([]));
    expect(container.textContent).toContain(
      "no item matches the filter — 3 queue items, none shown",
    );
    expect(container.textContent).not.toContain("the store carries no items");
    expect(container.textContent).toContain("+3 hidden");
    // The way back is on the same screen, next to the sentence that explains the emptiness.
    expect(
      Array.from(container.querySelectorAll("button")).some(
        (node) => node.textContent === "Clear filters",
      ),
    ).toBe(true);
  });
});
