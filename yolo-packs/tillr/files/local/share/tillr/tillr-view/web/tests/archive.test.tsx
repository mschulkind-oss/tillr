// The archive view (design §4.5): terminal items leave the spine for a view that is the
// same list, in the same order, with the same row shape. The failures this suite exists to
// catch are an empty queue that reads as an empty store, a landed item that cannot be
// found again, and a summary band whose order is not the store's own clock.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test } from "vitest";

import { QueueHarness as App } from "./queue-harness";
import {
  archiveItems,
  finishedCount,
  isTerminal,
  queueItems,
  recentlyFinished,
} from "../src/models/board";
import type { ApiEnvelope, ItemData, SnapshotData } from "../src/models/types";
import { archiveSnapshotData, docData, envelope } from "./fixtures";

const originalFetch = globalThis.fetch;
const originalWebSocket = globalThis.WebSocket;

/** A live channel the case can push a frame through. It opens on the next task, as a real
 * socket's handshake does, so the screen is on its fallback timer until the case accepts it. */
class FakeSocket {
  static opened: FakeSocket[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: unknown }) => void) | null = null;
  onclose: (() => void) | null = null;
  readonly url: string;

  constructor(url: string) {
    this.url = url;
    FakeSocket.opened.push(this);
  }

  close() {}

  accept() {
    this.onopen?.();
  }

  deliver(frame: unknown) {
    this.onmessage?.({ data: JSON.stringify(frame) });
  }
}

function itemDataFor(itemId: string): ItemData {
  const row =
    archiveSnapshotData.items.find((candidate) => candidate.item_id === itemId) ??
    archiveSnapshotData.items[0];
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

/** A server whose snapshot the case can move, so "the tick that landed it" is one frame
 * and one re-read rather than a claim about code structure. */
function stubFetch(read: () => SnapshotData) {
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    const path = String(input);
    let body: ApiEnvelope<unknown>;
    if (path === "/api/v1/passes") {
      body = envelope([]);
    } else if (path === "/api/v1/snapshot") {
      body = envelope(read());
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

/** The rail's titles, top to bottom: what the list is actually drawing. */
function railTitles(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll(".queue-row__title")).map(
    (node) => node.textContent ?? "",
  );
}

function bandTitles(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll(".finished-band__title")).map(
    (node) => node.textContent ?? "",
  );
}

/** One of the header's counts, which is the control for its state. */
function statButton(container: HTMLElement, label: string): HTMLElement {
  const button = Array.from(container.querySelectorAll<HTMLElement>("button.stat--filter")).find(
    (node) => node.querySelector(".stat__label")?.textContent === label,
  );
  if (!button) throw new Error(`the header carries no filter for ${label}`);
  return button;
}

function buttonWithText(container: HTMLElement, text: string): HTMLElement | undefined {
  return Array.from(container.querySelectorAll<HTMLElement>("button")).find((node) =>
    node.textContent?.includes(text),
  );
}

beforeEach(() => {
  window.history.replaceState(null, "", "/#/item/item-queue");
  FakeSocket.opened = [];
  stubFetch(() => archiveSnapshotData);
  globalThis.WebSocket = FakeSocket as unknown as typeof WebSocket;
});

afterEach(() => {
  cleanup();
  globalThis.fetch = originalFetch;
  globalThis.WebSocket = originalWebSocket;
});

describe("the terminal split", () => {
  test("a terminal item is the three states the design names, and nothing else", () => {
    const byState = new Map(
      archiveSnapshotData.items.map((item) => [item.projected_state.state, item]),
    );
    expect(isTerminal(byState.get("landed")!)).toBe(true);
    expect(isTerminal(byState.get("resolved_elsewhere")!)).toBe(true);
    expect(isTerminal(byState.get("closed")!)).toBe(true);
    expect(isTerminal(byState.get("in_queue")!)).toBe(false);
    expect(isTerminal(byState.get("working")!)).toBe(false);
    expect(isTerminal(byState.get("blocked")!)).toBe(false);
  });

  test("the two halves are the same list split on state, each in the order it was given", () => {
    expect(queueItems(archiveSnapshotData.items).map((item) => item.item_id)).toEqual([
      "item-queue",
      "item-working",
      "item-blocked",
    ]);
    expect(archiveItems(archiveSnapshotData.items).map((item) => item.item_id)).toEqual([
      "item-landed",
      "item-closed",
      "item-resolved",
    ]);
    expect(finishedCount(archiveSnapshotData.items)).toBe(3);
  });

  test("the summary band is the newest terminal items first, by the store's own clock", () => {
    expect(recentlyFinished(archiveSnapshotData.items, 2).map((item) => item.item_id)).toEqual([
      "item-closed",
      "item-resolved",
    ]);
    expect(recentlyFinished(archiveSnapshotData.items, 5)).toHaveLength(3);
  });
});

describe("the archive view", () => {
  test("the queue draws only live rows, and the archive is the same rows in the same order", async () => {
    const { container } = renderApp();
    await waitFor(() =>
      expect(railTitles(container)).toEqual([
        "queued parser",
        "working parser",
        "blocked renderer",
      ]),
    );
    // The finished count is on the header while the queue is on screen.
    expect(statButton(container, "Finished").querySelector(".stat__value")?.textContent).toBe("3");

    fireEvent.click(statButton(container, "Finished"));

    await waitFor(() =>
      expect(railTitles(container)).toEqual([
        "closed renderer",
        "landed parser",

        "resolved elsewhere",
      ]),
    );
    // The view rides in the URL, so a reload lands on the archive.
    expect(window.location.search).toBe("?view=archive");
    // Same row shape: the archive draws `queue-row`s, not a second row component.
    expect(container.querySelectorAll(".queue-row")).toHaveLength(3);
    expect(container.querySelector(".pane__title")?.textContent).toBe("Archive");
    expect(
      Array.from(container.querySelectorAll(".queue-row__place")).map((node) => node.textContent),
    ).toEqual(["—", "—", "—"]);
  });

  test("the header's landed count opens the archive filtered to landed", async () => {
    const { container } = renderApp();
    await waitFor(() => expect(railTitles(container)).toHaveLength(3));

    fireEvent.click(statButton(container, "Landed"));

    await waitFor(() => expect(railTitles(container)).toEqual(["landed parser"]));
    expect(window.location.search).toBe("?states=landed&view=archive");
  });

  test("the filter reaches the archive as well as the queue", async () => {
    const { container } = renderApp();
    await waitFor(() => expect(railTitles(container)).toHaveLength(3));

    fireEvent.click(statButton(container, "Finished"));
    await waitFor(() => expect(railTitles(container)).toHaveLength(3));

    fireEvent.change(container.querySelector(".queue-filter__input")!, {
      target: { value: "renderer" },
    });

    await waitFor(() => expect(railTitles(container)).toEqual(["closed renderer"]));
    expect(window.location.search).toBe("?q=renderer&view=archive");
    expect(container.textContent).toContain("finished items");

    // The view is not a filter: clearing the narrowing leaves the reader in the archive.
    fireEvent.click(buttonWithText(container, "Clear filters")!);
    await waitFor(() => expect(railTitles(container)).toHaveLength(3));
    expect(window.location.search).toBe("?view=archive");
  });

  test("the summary band shows the newest finished first and opens the archive", async () => {
    const { container } = renderApp();
    await waitFor(() => expect(railTitles(container)).toHaveLength(3));

    expect(bandTitles(container)).toEqual([
      "closed renderer",
      "resolved elsewhere",
      "landed parser",
    ]);

    fireEvent.click(buttonWithText(container, "Open the archive")!);
    await waitFor(() => expect(railTitles(container)).toEqual([
      "closed renderer",
      "landed parser",
      "resolved elsewhere",
    ]));
    expect(window.location.search).toBe("?view=archive");
  });

  test("an empty queue says where the work went instead of reading as an empty store", async () => {
    const finishedOnly: SnapshotData = {
      ...archiveSnapshotData,
      items: archiveSnapshotData.items.filter(isTerminal),
    };
    stubFetch(() => finishedOnly);
    const { container } = renderApp();

    await waitFor(() =>
      expect(container.textContent).toContain(
        "the queue is empty — 3 finished items are in the archive",
      ),
    );
    expect(container.textContent).not.toContain("the store carries no items");

    fireEvent.click(buttonWithText(container, "Open the archive")!);
    await waitFor(() => expect(railTitles(container)).toHaveLength(3));
  });

  test("a finished item is reachable from the filter, which says to look in the archive", async () => {
    const { container } = renderApp();
    await waitFor(() => expect(railTitles(container)).toHaveLength(3));

    // The queue has no "landed parser"; the archive does.
    fireEvent.change(container.querySelector(".queue-filter__input")!, {
      target: { value: "landed" },
    });

    await waitFor(() => expect(railTitles(container)).toEqual([]));
    expect(container.textContent).toContain("no item matches the filter");
    const hint = buttonWithText(container, "open the archive");
    expect(hint?.textContent).toContain("1 finished item matches");

    fireEvent.click(hint!);
    await waitFor(() => expect(railTitles(container)).toEqual(["landed parser"]));
    expect(window.location.search).toBe("?q=landed&view=archive");
  });

  test("a landed item leaves the queue on the tick that lands it", async () => {
    let snapshot: SnapshotData = {
      ...archiveSnapshotData,
      items: archiveSnapshotData.items.map((item) =>
        item.item_id === "item-queue"
          ? { ...item, projected_state: { state: "in_queue", raw_state: "in-queue" } }
          : item,
      ),
    };
    stubFetch(() => snapshot);
    const { container } = renderApp();
    await waitFor(() => expect(railTitles(container)).toContain("queued parser"));

    const socket = FakeSocket.opened[0];
    socket.accept();
    // The tick that lands it: the next snapshot says landed, and one frame invalidates the
    // read the same way a landing's event does.
    snapshot = {
      ...snapshot,
      items: snapshot.items.map((item) =>
        item.item_id === "item-queue"
          ? { ...item, projected_state: { state: "landed", raw_state: "landed" } }
          : item,
      ),
    };
    socket.deliver({ type: "snapshot.invalidated", event_head_seq: 2, changed: ["snapshot"] });

    await waitFor(() => expect(railTitles(container)).not.toContain("queued parser"));
    expect(statButton(container, "Finished").querySelector(".stat__value")?.textContent).toBe("4");

    fireEvent.click(statButton(container, "Finished"));
    await waitFor(() => expect(railTitles(container)).toContain("queued parser"));
  });
});
