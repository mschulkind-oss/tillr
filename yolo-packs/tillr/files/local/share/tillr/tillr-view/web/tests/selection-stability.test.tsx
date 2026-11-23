// What a store write may move, and what it may not (design §3.3, ledger `OQ-OBD1`). The
// queue is a summary and keeps updating; the item being read is a detail view and does not.
// Before the pin, the pane's run came from the snapshot's row for the item, so a run that
// started or a row that left the queue replaced the run under the reader while the selection
// itself stayed put — the assertion that fails on the board this suite was written against.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test } from "vitest";

import { App } from "../src/App";
import type { ApiEnvelope, ItemView, SnapshotData } from "../src/models/types";
import {
  dispatchedItemData,
  envelope,
  runData,
  twoItemSnapshotData,
} from "./fixtures";

const originalFetch = globalThis.fetch;
const originalWebSocket = globalThis.WebSocket;

/** A socket a case can open and push a frame through: the live channel's two deliveries,
 * without a server. `close` does nothing, so no reconnect timer outlives the test. */
class FakeSocket {
  static opened: FakeSocket[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: unknown }) => void) | null = null;
  onclose: (() => void) | null = null;
  constructor(readonly url: string) {
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

/** The selected item's row after a store write: a lane picked it up, the run failed, and the
 * loop blocked the item on it. The row keeps its place in the queue — a terminal state would
 * move it to the archive, which is a different case — and its light moves from working to
 * blocked. */
function pickedUpAndBlocked(row: ItemView): ItemView {
  return {
    ...row,
    updated_at_ms: 1762979800000,
    projected_state: {
      state: "blocked",
      raw_state: "blocked",
      underlying: "the last run failed",
    },
    latest_run: {
      run_id: "run-brand-new",
      lane_id: "lane-2",
      backend: "pi",
      state: "failed",
      exit_code: 1,
      started_at_ms: 1762979800000,
      finished_at_ms: 1762979900000,
      cost_usd: null,
      usage_tokens: null,
    },
  };
}

let snapshot: SnapshotData = twoItemSnapshotData;
let snapshotReads = 0;

function stubFetch() {
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    const path = String(input);
    let body: ApiEnvelope<unknown>;
    if (path === "/api/v1/passes") {
      body = envelope([]);
    } else if (path === "/api/v1/snapshot") {
      snapshotReads++;
      body = envelope(snapshot);
    } else if (path === "/api/v1/items/item-seed-02/doc") {
      body = envelope({
        item_id: "item-seed-02",
        path: "docs/design/x.md",
        markdown: "# x",
        bytes: 3,
        truncated: false,
        anchor: "",
      });
    } else if (path === "/api/v1/items/item-seed-02") {
      body = envelope(dispatchedItemData);
    } else {
      body = envelope(runData);
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

beforeEach(() => {
  window.history.replaceState(null, "", "/item/item-seed-02");
  snapshot = twoItemSnapshotData;
  snapshotReads = 0;
  FakeSocket.opened = [];
  globalThis.WebSocket = FakeSocket as unknown as typeof WebSocket;
  stubFetch();
});

afterEach(() => {
  cleanup();
  globalThis.fetch = originalFetch;
  globalThis.WebSocket = originalWebSocket;
});

describe("a store write under a reader", () => {
  test("a new latest run does not replace the item page's pinned run", async () => {
    const { container } = renderApp();
    await waitFor(() => expect(container.querySelector(".detail__badges")?.textContent).toContain("run-item-seed-02"));
    const socket = FakeSocket.opened[0]; act(() => socket.accept());
    const readsBefore = snapshotReads;
    snapshot = { ...twoItemSnapshotData, items: [pickedUpAndBlocked(twoItemSnapshotData.items[1])] };
    act(() => socket.deliver({ type: "snapshot.invalidated", event_head_seq: 49, changed: ["snapshot"] }));
    await waitFor(() => expect(snapshotReads).toBeGreaterThan(readsBefore));
    expect((window.location.pathname + window.location.search)).toBe("/item/item-seed-02");
    expect(container.querySelector(".detail__badges")?.textContent).toContain("run-item-seed-02");
    expect(container.querySelector(".detail__badges")?.textContent).not.toContain("run-brand-new");
  });
  test("an item disappearing from the list keeps its run and title on the page", async () => {
    const { container } = renderApp();
    await waitFor(() => expect(container.querySelector(".detail__badges")?.textContent).toContain("run-item-seed-02"));
    const socket = FakeSocket.opened[0]; act(() => socket.accept());
    const readsBefore = snapshotReads;
    snapshot = { ...twoItemSnapshotData, items: [] };
    act(() => socket.deliver({ type: "snapshot.invalidated", event_head_seq: 50, changed: ["snapshot"] }));
    await waitFor(() => expect(snapshotReads).toBeGreaterThan(readsBefore));
    expect(container.querySelector(".detail__title")?.textContent).toBe("second seed");
    expect((window.location.pathname + window.location.search)).toBe("/item/item-seed-02");
    expect(container.querySelector(".detail__badges")?.textContent).toContain("run-item-seed-02");
  });
});
