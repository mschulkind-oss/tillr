// The now band (design §4.4, ledger `D-OBS12`/`D-OBS10`): a live run is copied above the
// queue's list, outside its order, and its elapsed is labelled as the viewer's clock. The
// three failures this suite exists to catch are a band that moves the row it copies, a live
// elapsed that reads as a recorded duration, and an empty band where there is no live run.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { QueueHarness as App } from "./queue-harness";
import { NowBand } from "../src/components/NowBand";
import { QueueRail } from "../src/components/QueueRail";
import { SpanViewer } from "../src/components/SpanViewer";
import { assembleSpans } from "../src/models/spans";
import { EMPTY_FILTER, queueOrder } from "../src/models/board";
import { durationText, leaseText, liveElapsedText, liveRuns } from "../src/models/now";
import { docData, envelope, liveSnapshotData, runData, snapshotData } from "./fixtures";
import type { ItemData, SnapshotData } from "../src/models/types";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

/** A fixed viewer's clock 65s after the live run's start. */
const NOW = 1762979465000;

function rail(read: SnapshotData | null = liveSnapshotData, now = NOW) {
  const snapshot = read ?? undefined;
  return render(
    <QueueRail
      items={queueOrder(snapshot?.items ?? [])}
      total={snapshot?.items.length ?? 0}
      selectedItem={null}
      onSelectItem={() => {}}
      filter={EMPTY_FILTER}
      onFilterChange={() => {}}
      onClearFilter={() => {}}
      hidden={0}
      filterRef={{ current: null }}
      storeAbsent={false}
      snapshot={snapshot}
      now={now}
    />,
  );
}

function railTitles(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll(".queue .queue-row__title")).map(
    (node) => node.textContent ?? "",
  );
}

describe("the now band draws the live run", () => {
  test("names the item, the lane, the elapsed and the lease left, and leaves the row in place", () => {
    const { container } = rail();
    const band = container.querySelector(".now-band");
    expect(band).not.toBeNull();
    expect(band?.textContent).toContain("live seed");
    expect(band?.textContent).toContain("lane lane-1");
    // 65s from the start, and 115s left on the lease.
    expect(band?.textContent).toContain("1m05s");
    expect(band?.textContent).toContain("1m55s left");

    // The band is a copy, not a move: the live item is still the middle row, in rank order.
    expect(railTitles(container)).toEqual(["queued seed", "live seed", "later seed"]);
    // And it is outside the list it copies, where a second queue would not be.
    expect(container.querySelector(".queue .now-band")).toBeNull();
    expect(container.querySelector(".queue .queue-row__title")).not.toBeNull();
  });

  test("is absent, not an empty box, when no run is live", () => {
    const { container } = rail(snapshotData);
    expect(container.querySelector(".now-band")).toBeNull();
    expect(container.querySelector('[aria-label="Live runs"]')).toBeNull();
  });

  test("is absent while the first read is still in flight", () => {
    const { container } = rail(null);
    expect(container.querySelector(".now-band")).toBeNull();
  });
});

describe("a live elapsed is the viewer's clock, and says so", () => {
  test("the band labels its elapsed as the viewer's clock", () => {
    const { container } = rail();
    const elapsed = container.querySelector("[data-clock='viewer']");
    expect(elapsed).not.toBeNull();
    expect(elapsed?.textContent).toContain("1m05s");
    expect(elapsed?.textContent).toContain("viewer's clock");
    // The label names the missing fact too: the store records no finish for a live run.
    expect(elapsed?.getAttribute("title")).toContain("the store records no finish");
  });

  test("a recorded duration is not labelled as the viewer's clock", () => {
    // The same format labels both; the words are what keep them apart. The stage strip's
    // clock is a recorded `started → finished` pair, so it carries no viewer label.
    const { container } = render(
      <SpanViewer spans={assembleSpans(runData)} />,
    );
    expect(container.querySelector('[data-span-id$="/run"] .waterfall__bar')?.getAttribute("title")).toContain("24800.00 ms");
    expect(container.querySelector("[data-clock='viewer']")).toBeNull();
    expect(container.textContent).not.toContain("viewer's clock");
  });

  test("the viewer's clock ticks without a new snapshot", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(1762979405000));
    const { container } = render(<NowBand snapshot={liveSnapshotData} />);
    expect(container.querySelector(".now-band__elapsed")?.textContent).toContain("5s");
    act(() => {
      vi.advanceTimersByTime(60_000);
    });
    expect(container.querySelector(".now-band__elapsed")?.textContent).toContain("1m05s");
  });
});

describe("each absence is named, never a zero", () => {
  test("a lease the store did not record says so", () => {
    const snapshot: SnapshotData = {
      ...liveSnapshotData,
      lanes: [{ ...liveSnapshotData.lanes[0], leased_until_ms: null }],
    };
    const { container } = rail(snapshot);
    expect(container.querySelector(".now-band__lease")?.textContent).toBe("lease not recorded");
  });

  test("a run with no start says so instead of drawing 0s", () => {
    const snapshot: SnapshotData = {
      ...liveSnapshotData,
      items: liveSnapshotData.items.map((item) =>
        item.item_id === "item-live" && item.latest_run !== null
          ? { ...item, latest_run: { ...item.latest_run, started_at_ms: 0 } }
          : item,
      ),
    };
    const { container } = rail(snapshot);
    expect(container.querySelector(".now-band__elapsed")).toBeNull();
    expect(container.querySelector(".now-band")?.textContent).toContain("no start recorded");
  });

  test("a leased lane whose run is not in the snapshot still appears, and names what is missing", () => {
    const snapshot: SnapshotData = {
      ...liveSnapshotData,
      lanes: [
        {
          lane_id: "lane-9",
          state: "leased",
          run_id: "run-missing",
          leased_until_ms: 1762979580000,
        },
      ],
    };
    const { container } = rail(snapshot);
    const band = container.querySelector(".now-band");
    expect(band?.textContent).toContain("lane lane-9");
    expect(band?.textContent).toContain("the run is not in the snapshot");
    expect(band?.textContent).toContain("no start recorded");
  });
});

describe("the derivation and its vocabulary", () => {
  test("liveRuns reads the lanes and the runs they name", () => {
    expect(liveRuns(liveSnapshotData)).toEqual([
      {
        laneId: "lane-1",
        runId: "run-live-1",
        itemId: "item-live",
        title: "live seed",
        startedAtMs: 1762979400000,
        leasedUntilMs: 1762979580000,
      },
    ]);
    // An idle lane, a lane with no run, and no snapshot at all are all no live run.
    expect(liveRuns(snapshotData)).toEqual([]);
    expect(liveRuns(undefined)).toEqual([]);
  });

  test("the duration format is the design's three tiers", () => {
    expect(durationText(12_000)).toBe("12s");
    expect(durationText(4 * 60_000 + 7_000)).toBe("4m07s");
    expect(durationText(72 * 60_000)).toBe("1h12m");
    // A clock that runs backwards is not a negative duration on screen.
    expect(durationText(-5_000)).toBe("0s");
  });

  test("a lease is left, expired, or not recorded", () => {
    expect(leaseText(1762979580000, NOW)).toBe("lease 1m55s left");
    expect(leaseText(1762979400000, NOW)).toBe("lease expired 1m05s ago");
    expect(leaseText(null, NOW)).toBe("lease not recorded");
  });

  test("a live elapsed is the gap from the start, or no reading at all", () => {
    expect(liveElapsedText(1762979400000, NOW)).toBe("1m05s");
    expect(liveElapsedText(null, NOW)).toBeNull();
  });
});

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

function emptyItemData(itemId: string): ItemData {
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
  };
}

function stubLiveFetch() {
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    const path = String(input);
    let body: unknown;
    if (path === "/api/v1/passes") {
      body = envelope([]);
    } else if (path === "/api/v1/snapshot") {
      body = envelope(liveSnapshotData);
    } else if (path.endsWith("/doc")) {
      body = envelope(docData);
    } else {
      body = envelope(emptyItemData(path.slice("/api/v1/items/".length)));
    }
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as typeof fetch;
}

beforeEach(() => {
  window.history.replaceState(null, "", "/#/item/item-queued");
  stubLiveFetch();
  globalThis.WebSocket = SilentSocket as unknown as typeof WebSocket;
});

afterEach(() => {
  globalThis.fetch = originalFetch;
  globalThis.WebSocket = originalWebSocket;
});

describe("the board wires the band above the rail", () => {
  test("a live snapshot puts the band on screen without moving the item's row", async () => {
    const client = new QueryClient();
    const { container } = render(
      <QueryClientProvider client={client}>
        <App />
      </QueryClientProvider>,
    );

    await waitFor(() => expect(container.querySelector(".now-band")).not.toBeNull());
    expect(container.querySelector(".now-band")?.textContent).toContain("live seed");
    // The live item is still one of the three rows, in its rank position.
    expect(railTitles(container)).toEqual(["queued seed", "live seed", "later seed"]);
  });
});
