// The live channel's suite. It drives the hook against a fake WebSocket and a real
// QueryClient, so every case asserts what the cache was asked to do — the one thing the
// hook exists for — rather than that a listener was attached.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import {
  LANES_REFRESH_MIN_MS,
  RECONNECT_FIRST_MS,
  RECONNECT_MAX_MS,
  RECONNECT_STABLE_MS,
  invalidationKey,
  parseFrame,
  reconnectDelay,
  socketUrl,
  useObservatorySocket,
} from "../src/models/socket";
import { queryKeys } from "../src/models/queries";

/** One fake socket: what a browser's WebSocket exposes to this hook, and the two
 * deliveries a test makes. */
class FakeSocket {
  static opened: FakeSocket[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: unknown }) => void) | null = null;
  onclose: (() => void) | null = null;
  readonly url: string;
  closed = false;

  constructor(url: string) {
    this.url = url;
    FakeSocket.opened.push(this);
  }

  close() {
    this.closed = true;
    this.onclose?.();
  }

  /** The server's handshake completing. */
  accept() {
    this.onopen?.();
  }

  /** One frame, as the server writes it. */
  deliver(frame: unknown) {
    this.onmessage?.({ data: typeof frame === "string" ? frame : JSON.stringify(frame) });
  }
}

/** Every attempt the hook asked the backoff for, in order, and a delay of zero so a
 * reconnect is the next tick rather than half a second of test. Module-level because the
 * hook holds these in an effect: an inline arrow is a new function on every render, which
 * would restart the socket. */
const attempts: number[] = [];
function recordDelay(attempt: number): number {
  attempts.push(attempt);
  return 0;
}

/** The hook, rendered inside the provider it needs. */
function Probe({ url, nowMs }: { url: string; nowMs?: () => number }) {
  useObservatorySocket({ url, reconnectDelayMs: recordDelay, nowMs });
  return null;
}

/** The hook's own answer, rendered so a case can read the live flag off the screen. */
function LiveProbe({ url }: { url: string }) {
  const { live } = useObservatorySocket({ url, reconnectDelayMs: recordDelay });
  return <span data-live={live ? "true" : "false"} />;
}

const PROBE_URL = "ws://127.0.0.1:4318/api/v1/ws";

function renderProbe(url = PROBE_URL, nowMs?: () => number) {
  const client = new QueryClient();
  const invalidate = vi.spyOn(client, "invalidateQueries");
  const view = render(
    <QueryClientProvider client={client}>
      <Probe url={url} nowMs={nowMs} />
    </QueryClientProvider>,
  );
  return { client, invalidate, view };
}

const originalWebSocket = globalThis.WebSocket;

beforeEach(() => {
  attempts.length = 0;
  FakeSocket.opened = [];
  globalThis.WebSocket = FakeSocket as unknown as typeof WebSocket;
});

afterEach(() => {
  // Every case unmounts, so a reconnect one of them scheduled cannot open a socket in the
  // next one's list.
  cleanup();
  globalThis.WebSocket = originalWebSocket;
});

describe("the live channel invalidates the cache the panels already read", () => {
  test("a frame names the cache keys the existing hooks use", () => {
    const { invalidate } = renderProbe();
    const socket = FakeSocket.opened[0];
    socket.accept();
    // The one unconditional read: the screen was fetched over HTTP before this socket
    // existed, so the connect closes that window.
    expect(invalidate).toHaveBeenCalledTimes(1);
    expect(invalidate).toHaveBeenLastCalledWith();

    socket.deliver({
      type: "snapshot.invalidated",
      event_head_seq: 49,
      changed: ["items:item-seed-02", "runs:run-item-seed-02", "snapshot"],
    });
    expect(invalidate).toHaveBeenCalledTimes(5);
    expect(invalidate).toHaveBeenNthCalledWith(2, { queryKey: ["lanes"] });
    expect(invalidate).toHaveBeenNthCalledWith(3, { queryKey: ["items", "item-seed-02"] });
    expect(invalidate).toHaveBeenNthCalledWith(4, { queryKey: ["runs", "run-item-seed-02"] });
    expect(invalidate).toHaveBeenNthCalledWith(5, { queryKey: ["snapshot"] });
  });

  test("log growth also refreshes the Home lane strip", () => {
    const { invalidate } = renderProbe();
    const socket = FakeSocket.opened[0];
    socket.accept();
    invalidate.mockClear();
    socket.deliver({ type: "snapshot.invalidated", event_head_seq: 50, changed: ["runlog:run-1"] });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: queryKeys.lanes() });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: queryKeys.run("run-1") });
  });

  test("a burst of log growth refreshes the lane strip once per gap, and never drops the last", () => {
    vi.useFakeTimers();
    try {
      let clock = 1_000;
      const { invalidate } = renderProbe(PROBE_URL, () => clock);
      const socket = FakeSocket.opened[0];
      socket.accept();
      invalidate.mockClear();
      const lanes = () => invalidate.mock.calls.filter(call => JSON.stringify(call[0]) === JSON.stringify({ queryKey: queryKeys.lanes() })).length;
      socket.deliver({ type: "snapshot.invalidated", event_head_seq: 50, changed: ["runlog:run-1"] });
      expect(lanes()).toBe(1);
      // Three more frames inside the gap: no refresh now, one scheduled for when it ends.
      for (const at of [1_250, 1_500, 1_750]) {
        clock = at;
        socket.deliver({ type: "snapshot.invalidated", event_head_seq: 50, changed: ["runlog:run-1"] });
      }
      expect(lanes()).toBe(1);
      // The run's own payload is still refreshed per frame: only the strip is rate-limited.
      expect(invalidate.mock.calls.filter(call => JSON.stringify(call[0]) === JSON.stringify({ queryKey: queryKeys.run("run-1") })).length).toBe(4);
      clock = 3_000;
      vi.advanceTimersByTime(LANES_REFRESH_MIN_MS);
      expect(lanes()).toBe(2);
      // A frame after the gap is answered at once again.
      clock = 5_100;
      socket.deliver({ type: "snapshot.invalidated", event_head_seq: 51, changed: ["snapshot"] });
      expect(lanes()).toBe(3);
    } finally {
      vi.useRealTimers();
    }
  });

  test("a resource this client does not cache invalidates nothing", () => {
    const { invalidate } = renderProbe();
    const socket = FakeSocket.opened[0];
    socket.accept();
    invalidate.mockClear();
    socket.deliver({ type: "snapshot.invalidated", event_head_seq: 50, changed: ["lanes:lane-a"] });
    expect(invalidate).not.toHaveBeenCalled();
  });

  test("a frame the client is older than is ignored, and the socket stays open", () => {
    const { invalidate } = renderProbe();
    const socket = FakeSocket.opened[0];
    socket.accept();
    invalidate.mockClear();
    socket.deliver("{not json");
    socket.deliver({ type: "stream.hello", changed: ["snapshot"] });
    expect(invalidate).not.toHaveBeenCalled();
    expect(socket.closed).toBe(false);
    // ... and the next well-formed frame still lands, which is what "ignored" has to
    // mean: a client that dropped the socket on one bad frame would go dark on a version
    // skew.
    socket.deliver({ type: "snapshot.invalidated", event_head_seq: 51, changed: ["snapshot"] });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["snapshot"] });
  });

  test("a dropped socket reconnects instead of leaving the screen frozen", async () => {
    renderProbe();
    expect(FakeSocket.opened).toHaveLength(1);
    FakeSocket.opened[0].close();
    await vi.waitFor(() => expect(FakeSocket.opened).toHaveLength(2));
    expect(FakeSocket.opened[1].url).toBe("ws://127.0.0.1:4318/api/v1/ws");
  });

  // The failure this file was missing: an unreadable or locked store *completes* the
  // handshake and then closes with a reason, so `onopen` fires, the attempt counter was
  // reset by it, and the client reconnected every 500ms forever — each time invalidating
  // the whole cache.
  test("the backoff grows while the store cannot be read", async () => {
    renderProbe();
    for (let i = 0; i < 3; i += 1) {
      const socket = FakeSocket.opened[i];
      socket.accept();
      socket.close();
      await vi.waitFor(() => expect(FakeSocket.opened).toHaveLength(i + 2));
    }
    expect(attempts).toEqual([0, 1, 2]);
    // ... and the delay those attempts buy really does grow, which is the property.
    expect(reconnectDelay(attempts[1])).toBeGreaterThan(reconnectDelay(attempts[0]));
    expect(reconnectDelay(attempts[2])).toBeGreaterThan(reconnectDelay(attempts[1]));
  });

  test("a received frame proves the link worked, so the next drop starts over", async () => {
    renderProbe();
    for (let i = 0; i < 2; i += 1) {
      FakeSocket.opened[i].accept();
      FakeSocket.opened[i].close();
      await vi.waitFor(() => expect(FakeSocket.opened).toHaveLength(i + 2));
    }
    expect(attempts).toEqual([0, 1]);

    const working = FakeSocket.opened[2];
    working.accept();
    working.deliver({ type: "snapshot.invalidated", event_head_seq: 3, changed: ["snapshot"] });
    working.close();
    await vi.waitFor(() => expect(FakeSocket.opened).toHaveLength(4));
    expect(attempts).toEqual([0, 1, 0]);
  });

  test("a connection that stayed up long enough is not counted as a failure", async () => {
    let clock = 1_000;
    renderProbe(PROBE_URL, () => clock);
    // The first connection dies at once, so the backoff is owed for it.
    FakeSocket.opened[0].accept();
    FakeSocket.opened[0].close();
    await vi.waitFor(() => expect(FakeSocket.opened).toHaveLength(2));
    // The second one carries a frame-less second of health — an idle store sends nothing —
    // and only its lifetime can say the link worked.
    FakeSocket.opened[1].accept();
    clock += RECONNECT_STABLE_MS;
    FakeSocket.opened[1].close();
    await vi.waitFor(() => expect(FakeSocket.opened).toHaveLength(3));
    expect(attempts).toEqual([0, 0]);
  });

  test("the live flag is what a polling screen and a live one are told apart by", () => {
    const { container } = render(
      <QueryClientProvider client={new QueryClient()}>
        <LiveProbe url={PROBE_URL} />
      </QueryClientProvider>,
    );
    const live = () => container.querySelector("[data-live]")?.getAttribute("data-live");
    expect(live()).toBe("false");
    act(() => FakeSocket.opened[0].accept());
    expect(live()).toBe("true");
    act(() => FakeSocket.opened[0].close());
    expect(live()).toBe("false");
  });

  test("unmounting closes the socket and stops reconnecting", async () => {
    const { view } = renderProbe();
    view.unmount();
    expect(FakeSocket.opened[0].closed).toBe(true);
    // A close the hook caused itself must not schedule a reconnect.
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(FakeSocket.opened).toHaveLength(1);
  });
});

describe("the wire's spelling is the server's", () => {
  test("the URL is this page's origin, ws or wss", () => {
    expect(socketUrl({ protocol: "http:", host: "127.0.0.1:4318" } as Location)).toBe(
      "ws://127.0.0.1:4318/api/v1/ws",
    );
    expect(socketUrl({ protocol: "https:", host: "board.example" } as Location)).toBe(
      "wss://board.example/api/v1/ws",
    );
  });

  test("every resource name the server sends has a cache key", () => {
    expect(invalidationKey("snapshot")).toEqual(["snapshot"]);
    expect(invalidationKey("items:item-seed-02")).toEqual(["items", "item-seed-02"]);
    expect(invalidationKey("runs:run-1")).toEqual(["runs", "run-1"]);
    expect(invalidationKey("lanes:lane-a")).toBeNull();
  });

  // A grown log names the run's own payload and not its subtree: the run's exchanges,
  // timeline and open artifacts are not what a log line changed, and refetching them once
  // per line is the loop the leaf key exists to stop. `runs:<id>` still reaches all four,
  // because a run's key is a prefix of them.
  test("the log's leaf key reaches the run payload and not its subtree", () => {
    expect(invalidationKey("runlog:run-1")).toEqual(queryKeys.run("run-1"));
    expect(queryKeys.run("run-1")).toHaveLength(3);
    for (const child of [
      queryKeys.runExchanges("run-1"),
      queryKeys.runTimeline("run-1"),
      queryKeys.runArtifact("run-1", "brief"),
    ]) {
      expect(child.slice(0, 2)).toEqual(queryKeys.run("run-1").slice(0, 2));
      expect(child).not.toEqual(queryKeys.run("run-1"));
    }
  });

  // The transcript's cache key is a CHILD of the run's own payload key, which is what makes
  // a grown log refresh the transcript: `runlog:<id>` invalidates `["runs", id, "run"]`, and
  // TanStack Query's prefix match reaches every key that starts with it.
  test("the log's leaf key reaches the transcript, which hangs off the run's payload", () => {
    const transcript = queryKeys.runTranscript("run-1");
    expect(transcript.slice(0, queryKeys.run("run-1").length)).toEqual(queryKeys.run("run-1"));
    expect(transcript).not.toEqual(queryKeys.run("run-1"));
    for (const sibling of [
      queryKeys.runExchanges("run-1"),
      queryKeys.runTimeline("run-1"),
      queryKeys.runArtifact("run-1", "brief"),
    ]) {
      expect(transcript.slice(0, 3)).not.toEqual(sibling);
    }
  });

  test("a frame with no cursor is still a frame, and a wrong type is not", () => {
    expect(parseFrame(JSON.stringify({ type: "snapshot.invalidated", changed: [] }))).toEqual({
      type: "snapshot.invalidated",
      event_head_seq: 0,
      changed: [],
    });
    expect(parseFrame(JSON.stringify({ type: "snapshot", changed: [] }))).toBeNull();
    expect(parseFrame(JSON.stringify({ type: "snapshot.invalidated" }))).toBeNull();
    expect(parseFrame(null)).toBeNull();
  });

  test("the backoff doubles to a ceiling, so an unreadable store is not hammered", () => {
    expect(reconnectDelay(0)).toBe(RECONNECT_FIRST_MS);
    expect(reconnectDelay(1)).toBe(RECONNECT_FIRST_MS * 2);
    expect(reconnectDelay(4)).toBe(8000);
    expect(reconnectDelay(20)).toBe(RECONNECT_MAX_MS);
  });
});
