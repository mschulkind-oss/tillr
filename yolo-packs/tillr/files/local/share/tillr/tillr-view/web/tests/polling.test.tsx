// The fallback's suite. It drives the query hooks against a real QueryClient and a fake
// `fetch`, because the property is a count of requests over time rather than a config
// value: a screen with no live channel has to re-read on its own, and a screen with one
// must not. A stale screen an operator cannot tell from a live one is the failure this
// observatory exists to prevent.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { FALLBACK_POLL_MS, useLanesQuery, useRunQuery, useSnapshotQuery } from "../src/models/queries";
import { envelope, runData, snapshotData } from "./fixtures";

/** The requests the screen made, in order. */
let requested: string[] = [];

function stubFetch() {
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    requested.push(String(input));
    const body = String(input).endsWith("/lanes") ? envelope([]) : String(input).includes("/runs/") ? envelope(runData) : envelope(snapshotData);
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as typeof fetch;
}

const originalFetch = globalThis.fetch;

function Probe({ live }: { live: boolean }) {
  useSnapshotQuery(live);
  useRunQuery("run-item-seed-02", live);
  return null;
}

function renderScreen(live: boolean) {
  const client = new QueryClient();
  return render(
    <QueryClientProvider client={client}>
      <Probe live={live} />
    </QueryClientProvider>,
  );
}

/** Let every resolved promise and every scheduled timer up to `ms` run, so a refetch that
 * the query client scheduled has actually happened by the time the assertion runs. */
async function elapse(ms: number): Promise<void> {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

beforeEach(() => {
  requested = [];
  stubFetch();
  vi.useFakeTimers();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  globalThis.fetch = originalFetch;
});

function LaneProbe({ paused }: { paused: boolean }) {
  useLanesQuery(true, paused);
  return null;
}

test("lane rows poll step files and quiet ages, but Pause stops their timer", async () => {
  const client = new QueryClient();
  const view = render(<QueryClientProvider client={client}><LaneProbe paused={false} /></QueryClientProvider>);
  await elapse(0);
  expect(requested).toEqual(["/api/v1/lanes"]);
  await elapse(FALLBACK_POLL_MS);
  expect(requested).toHaveLength(2);
  view.rerender(<QueryClientProvider client={client}><LaneProbe paused /></QueryClientProvider>);
  await elapse(FALLBACK_POLL_MS * 3);
  expect(requested).toHaveLength(2);
});

describe("a screen with no live channel re-reads on a slow timer", () => {
  test("the hooks re-read once the poll interval elapses", async () => {
    renderScreen(false);
    await elapse(0);
    // One read each: the snapshot and the selected run.
    expect(requested).toHaveLength(2);

    await elapse(FALLBACK_POLL_MS);
    expect(requested).toHaveLength(4);
    expect(requested.filter((path) => path.endsWith("/api/v1/snapshot"))).toHaveLength(2);

    // ... and it keeps going, because nothing is going to push a frame.
    await elapse(FALLBACK_POLL_MS);
    expect(requested).toHaveLength(6);
  });

  test("a live screen does not poll: the socket is what moves it", async () => {
    renderScreen(true);
    await elapse(0);
    expect(requested).toHaveLength(2);

    await elapse(FALLBACK_POLL_MS * 3);
    expect(requested).toHaveLength(2);
  });
});
