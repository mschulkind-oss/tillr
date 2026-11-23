// The browser's half of the performance log: what a page view, a fetch and a slow render
// become in the batch posted to `/api/v1/client-timing`. Every time is the injected clock's,
// so nothing here waits or reads a real one.

import { afterEach, describe, expect, test, vi } from "vitest";

import { fetchSnapshot } from "../src/models/api";
import { MAX_FETCHES, PerfRecorder, SLOW_RENDER_MS, pageOf, perf, type ClientBatch } from "../src/models/perf";

function recorder(times: number[]) {
  const sent: { batch: ClientBatch; beacon: boolean }[] = [];
  let i = 0;
  const r = new PerfRecorder({ now: () => times[Math.min(i++, times.length - 1)], send: (batch, beacon) => sent.push({ batch, beacon }), wallClock: () => "2026-11-17T00:00:00.000Z" });
  return { r, sent };
}

afterEach(() => vi.unstubAllGlobals());

describe("the client timing recorder", () => {
  test("a page view is timed from navigation to its first data and to settled", () => {
    // load starts at 0 (navigation); firstData at 420; settled at 900; a hash change at 1000,
    // its data at 1180 and settled at 1300.
    const { r, sent } = recorder([420, 900, 1000, 1180, 1300]);
    r.beginPage("/", "load");
    r.firstData();
    r.settled();
    r.beginPage("/run/run-1?tab=transcript", "route");
    r.firstData();
    r.settled();
    r.flush("interval");
    expect(sent).toHaveLength(1);
    expect(sent[0].beacon).toBe(false);
    expect(sent[0].batch.page_views).toEqual([
      { page: "/", nav: "load", started_ms: 0, first_data_ms: 420, settled_ms: 900, requests: 0 },
      { page: "/run/{id}", nav: "route", started_ms: 1000, first_data_ms: 180, settled_ms: 300, requests: 0 },
    ]);
  });

  test("fetches carry the page they served and are capped; slow renders only above the threshold", () => {
    const { r, sent } = recorder([0]);
    r.beginPage("/items", "load");
    for (let n = 0; n < MAX_FETCHES + 3; n++) r.fetched({ path: "/api/v1/why", request_id: `x-${n}`, ms: 12, ttfb_ms: 10, bytes: 100, transfer_bytes: 40, status: 200 });
    r.rendered("screen", SLOW_RENDER_MS - 1);
    r.rendered("screen", SLOW_RENDER_MS + 30);
    r.flush("pagehide", true);
    const batch = sent[0].batch;
    expect(sent[0].beacon).toBe(true);
    expect(batch.fetches).toHaveLength(MAX_FETCHES);
    expect(batch.dropped_fetches).toBe(3);
    expect(batch.fetches[0].page).toBe("/items");
    expect(batch.renders).toEqual([{ id: "screen", ms: SLOW_RENDER_MS + 30, page: "/items" }]);
    // The beacon closes the open page view, so a tab closed before its data arrived still
    // reports that it never got any.
    expect(batch.page_views).toEqual([{ page: "/items", nav: "load", started_ms: 0, first_data_ms: null, settled_ms: null, requests: MAX_FETCHES + 3 }]);
  });

  test("an empty recorder sends nothing", () => {
    const { r, sent } = recorder([0]);
    r.flush("interval");
    expect(sent).toHaveLength(0);
  });

  test("ids are folded out of the page", () => {
    expect(pageOf("")).toBe("/");
    expect(pageOf("/item/item-a")).toBe("/item/{id}");
    expect(pageOf("/performance?range=7d")).toBe("/performance");
  });

  test("every API read reports its server request id and body size", async () => {
    const body = JSON.stringify({ snapshot: { event_head_seq: 1 }, data: { items: [] } });
    vi.stubGlobal("fetch", vi.fn(async () => new Response(body, { status: 200, headers: { "X-Request-Id": "abc-7" } })));
    const spy = vi.spyOn(perf, "fetched");
    await fetchSnapshot();
    expect(spy).toHaveBeenCalledWith(expect.objectContaining({ path: "/api/v1/snapshot", request_id: "abc-7", bytes: body.length, status: 200 }));
  });
});
