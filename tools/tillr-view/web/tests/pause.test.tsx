// The pause control's suite (design §3.3, ledger `OQ-OBD1`). The board has two ways to move
// on its own — the live channel's invalidations and the polling timer under a channel that
// is down — and the control has to stop both, say so with the age of the read it is showing,
// and re-read exactly once when the operator resumes. A test that only stopped one path
// would pass on a board that still moved under a reader.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { QueueHarness as App } from "./queue-harness";
import { FALLBACK_POLL_MS } from "../src/models/queries";
import type { ApiEnvelope, SnapshotData } from "../src/models/types";
import type { ItemData } from "../src/models/types";
import {
  dispatchedItemData,
  envelope,
  liveSnapshotData,
  runData,
  twoItemSnapshotData,
} from "./fixtures";

const originalFetch = globalThis.fetch;
const originalWebSocket = globalThis.WebSocket;

/** A socket a case opens and pushes frames through. `close` does nothing: the hook's
 * reconnect timer must not outlive the test. */
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

/** A channel that never opens: the screen is on its polling fallback, which is the second
 * path the pause has to stop. */
class SilentSocket {
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: unknown }) => void) | null = null;
  onclose: (() => void) | null = null;
  close() {}
}

/** The selected item's row after the store write: the same item, picked up by another lane. */
function pickedUp(row: (typeof twoItemSnapshotData.items)[number]) {
  return {
    ...row,
    updated_at_ms: 1762979800000,
    projected_state: { state: "working", raw_state: "in-progress" },
    latest_run: {
      run_id: "run-brand-new",
      lane_id: "lane-2",
      backend: "pi",
      state: "running",
      exit_code: null,
      started_at_ms: 1762979800000,
      finished_at_ms: 0,
      cost_usd: null,
      usage_tokens: null,
    },
  };
}

/** A dossier for any item the snapshot carries and this suite has no fixture for: the
 * pause's own cases only need the pane to render, not to say anything specific. */
function queuedItem(itemId: string): ItemData {
  return {
    item: {
      item_id: itemId,
      title: itemId,
      body_ref: "",
      design_doc: "",
      allowed_paths: [],
      projected_state: { state: "in_queue", raw_state: "in_queue" },
    },
    criteria: [],
    blocked_by: [],
    decisions: [],
    verdicts: [],
    closed_blockers: [],
    attempts: [],
    runs: [],
    owner_decisions: [],
    state_moves: [],
  };
}

let snapshot: SnapshotData = twoItemSnapshotData;
let requested: string[] = [];

function snapshotReads(): number {
  return requested.filter((path) => path === "/api/v1/snapshot").length;
}

function stubFetch() {
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    const path = String(input);
    requested.push(path);
    let body: ApiEnvelope<unknown>;
    if (path === "/api/v1/passes") {
      body = envelope([]);
    } else if (path === "/api/v1/snapshot") {
      body = envelope(snapshot);
    } else if (path.endsWith("/doc")) {
      body = envelope({
        item_id: path.split("/")[4] ?? "",
        path: "docs/design/x.md",
        markdown: "# x",
        bytes: 3,
        truncated: false,
        anchor: "",
      });
    } else if (path === "/api/v1/items/item-seed-02") {
      body = envelope(dispatchedItemData);
    } else if (path.startsWith("/api/v1/items/")) {
      body = envelope(queuedItem(path.slice("/api/v1/items/".length)));
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

/** Let every resolved promise and every timer due at zero run, so a read that the click
 * asked for has reached the screen by the time the assertion runs. */
async function flush(): Promise<void> {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(0);
  });
}

beforeEach(() => {
  window.history.replaceState(null, "", "/#/item/item-seed-02");
  snapshot = twoItemSnapshotData;
  requested = [];
  FakeSocket.opened = [];
  stubFetch();
  vi.useFakeTimers();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  globalThis.fetch = originalFetch;
  globalThis.WebSocket = originalWebSocket;
});

describe("the pause control", () => {
  test("stops the live channel's invalidations, says how old the read is, and resumes once", async () => {
    globalThis.WebSocket = FakeSocket as unknown as typeof WebSocket;
    const { container } = renderApp();
    await flush();
    const socket = FakeSocket.opened[0];
    act(() => socket.accept());
    await flush();
    expect(container.textContent).toContain("seed two");
    const readsBefore = snapshotReads();

    fireEvent.click(screen.getByRole("button", { name: "Pause" }));
    await flush();
    // It says so while it is on, in the header, and the chip stops claiming to be live.
    expect(container.textContent).toContain("paused");
    expect(container.textContent).toContain("Updates: paused");

    // A store write arrives on the live channel: the screen must not move.
    snapshot = {
      ...twoItemSnapshotData,
      items: [
        {
          item_id: "item-new-head",
          title: "brand new head",
          projected_state: { state: "in_queue", raw_state: "in_queue" },
          updated_at_ms: 1762979900000,
          cost_usd: null,
          latest_decision: null,
          latest_run: null,
          rank: "0",
        },
        twoItemSnapshotData.items[0],
        pickedUp(twoItemSnapshotData.items[1]),
      ],
    };
    act(() =>
      socket.deliver({ type: "snapshot.invalidated", event_head_seq: 49, changed: ["snapshot"] }),
    );
    await flush();
    expect(container.textContent).not.toContain("brand new head");
    expect(snapshotReads()).toBe(readsBefore);

    // The age is of the read still on screen, and it keeps counting while paused.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(12_000);
    });
    expect(container.querySelector(".head-paused__age")?.textContent).toBe("12s");

    // Resuming re-reads once, and the screen catches up with the store.
    fireEvent.click(screen.getByRole("button", { name: "Resume" }));
    await flush();
    expect(container.textContent).not.toContain("Updates: paused");
    expect(container.textContent).toContain("brand new head");
    expect(snapshotReads()).toBe(readsBefore + 1);
  });

  test("freezes the now band's clock, which is the viewer's time, not a store read", async () => {
    globalThis.WebSocket = FakeSocket as unknown as typeof WebSocket;
    // Put the viewer's clock at the fixture's own start, so the elapsed is "0s" and a
    // frozen band is a number that stays put rather than two hours that look alike.
    vi.setSystemTime(1762979400000);
    snapshot = liveSnapshotData;
    const { container } = renderApp();
    await flush();
    act(() => FakeSocket.opened[0].accept());
    await flush();
    await flush();

    const elapsed = () => container.querySelector(".now-band__elapsed")?.textContent ?? "";
    expect(elapsed()).toContain("elapsed 0s");

    fireEvent.click(screen.getByRole("button", { name: "Pause" }));
    await flush();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(elapsed()).toContain("elapsed 0s");

    // ... and it is the pause that froze it: the clock is still running, so once the screen
    // is live again the band catches up to the viewer's own time (twenty seconds from the
    // run's start) rather than resuming from where it was pinned.
    fireEvent.click(screen.getByRole("button", { name: "Resume" }));
    await flush();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(elapsed()).toContain("elapsed 20s");
  });

  test("stops the polling timer when the live channel is down, and resumes once", async () => {
    globalThis.WebSocket = SilentSocket as unknown as typeof WebSocket;
    const { container } = renderApp();
    await flush();
    // The first read lands the queue; the auto-selection that opens its head is the next
    // render, so a second flush is what puts the item pane on screen.
    await flush();
    expect(container.textContent).toContain("seed two");
    expect(container.textContent).toContain("live channel down");
    const readsBefore = snapshotReads();

    fireEvent.click(screen.getByRole("button", { name: "Pause" }));
    await flush();

    snapshot = {
      ...twoItemSnapshotData,
      items: [
        {
          item_id: "item-new-head",
          title: "brand new head",
          projected_state: { state: "in_queue", raw_state: "in_queue" },
          updated_at_ms: 1762979900000,
          cost_usd: null,
          latest_decision: null,
          latest_run: null,
          rank: "0",
        },
        ...twoItemSnapshotData.items,
      ],
    };

    // Two poll intervals go by and nothing is read: the timer is stopped, not just slow.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(FALLBACK_POLL_MS * 2);
    });
    expect(snapshotReads()).toBe(readsBefore);
    expect(container.textContent).not.toContain("brand new head");

    fireEvent.click(screen.getByRole("button", { name: "Resume" }));
    await flush();
    expect(container.textContent).toContain("brand new head");
    expect(snapshotReads()).toBe(readsBefore + 1);
  });
});
