// The live channel of design §4.2: one socket per screen, and the same invalidation the
// [Refresh Snapshot] button calls. Every panel still reads its query hook, so no
// component learns the transport exists — a dropped socket falls back to the query hooks'
// slow poll rather than to a blank screen, and a frame that names a resource the client
// does not cache is ignored rather than swallowed into a full refetch.

import { useQueryClient } from "@tanstack/react-query";
import type { InvalidateQueryFilters, QueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";

import { queryKeys } from "./queries";

/** One frame from `GET /api/v1/ws` (design §4.2). `changed` names cache keys, not ids. */
export interface InvalidationFrame {
  type: "snapshot.invalidated";
  event_head_seq: number;
  changed: string[];
}

/** How long the first reconnect waits, and the ceiling the doubling reaches. A server
 * that is restarting answers within a second; a store that stays unreadable must not be
 * polled every half second forever. */
export const RECONNECT_FIRST_MS = 500;
export const RECONNECT_MAX_MS = 10_000;

/** How long a connection has to stay open before it counts as proof the link works. An
 * unreadable or locked store *does* complete the handshake and *then* closes with a
 * reason, so "the socket opened" proves nothing on its own: a connection this short is a
 * failure, and the delay after it grows. */
export const RECONNECT_STABLE_MS = 1_000;

/** The shortest gap between two lane-strip refreshes the live channel asks for. A running
 * lane's log grows several times a second, and the server names each growth in a frame
 * (measured 2026-11-17: 92 frames a minute with the drain busy, 52 of them `runlog:`), so a
 * refresh per frame re-read every lane's log tail one to two times a second per open tab. The
 * strip shows a lane's latest action in words; two seconds is below what a reader can tell
 * apart, and the last frame in a burst is never lost — it is answered when the gap ends. */
export const LANES_REFRESH_MIN_MS = 2_000;

/** The query roots whose hooks poll on their own interval whether or not the socket is up
 * (queries.ts: lanes, waiting, passes, designs, questions). The socket's connect-time
 * refresh skips them. */
const POLLED_ROOTS: ReadonlySet<string> = new Set(["lanes", "waiting", "passes", "designs", "questions"]);

/** The reconnect delay after `attempt` consecutive failures, counted from zero. */
export function reconnectDelay(attempt: number): number {
  return Math.min(RECONNECT_FIRST_MS * 2 ** attempt, RECONNECT_MAX_MS);
}

/** The cache key a resource name in a frame refers to, or null for a name this client
 * does not know: a server that grows a resource must not make an older client invalidate
 * everything, and invalidating nothing is what leaves the next frame to fix it.
 *
 * `runs:<id>` is a run's whole subtree — its payload, exchanges, timeline and artifacts.
 * `runlog:<id>` is only its payload, which is what a log that grew actually changes: a
 * run's log grows once per line, and refetching that subtree once per line is not a
 * refresh, it is a loop. */
export function invalidationKey(name: string): readonly unknown[] | null {
  if (name === "snapshot") return queryKeys.snapshot();
  if (name === "landings") return queryKeys.landings();
  if (name === "alerts") return queryKeys.alertsRoot();
  if (name.startsWith("alerts:")) return queryKeys.alert(name.slice("alerts:".length));
  if (name.startsWith("runlog:")) return queryKeys.run(name.slice("runlog:".length));
  if (name.startsWith("items:")) return queryKeys.item(name.slice("items:".length));
  if (name.startsWith("runs:")) return queryKeys.runSubtree(name.slice("runs:".length));
  return null;
}

/** The socket URL for the page's own origin, so a served front end and the Vite dev
 * proxy both reach the server the HTTP reads already use. */
export function socketUrl(location: Location): string {
  const scheme = location.protocol === "https:" ? "wss:" : "ws:";
  return `${scheme}//${location.host}/api/v1/ws`;
}

/** A frame this client understands, or null for anything else: a malformed or unknown
 * frame is a protocol the client is older than, not a reason to drop the socket. */
export function parseFrame(data: unknown): InvalidationFrame | null {
  if (typeof data !== "string") return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(data);
  } catch {
    return null;
  }
  if (typeof parsed !== "object" || parsed === null) return null;
  const frame = parsed as Partial<InvalidationFrame>;
  if (frame.type !== "snapshot.invalidated" || !Array.isArray(frame.changed)) return null;
  return {
    type: frame.type,
    event_head_seq: typeof frame.event_head_seq === "number" ? frame.event_head_seq : 0,
    changed: frame.changed.filter((name): name is string => typeof name === "string"),
  };
}

export interface ObservatorySocketOptions {
  /** The socket URL. Defaults to this page's origin; a test names its own. */
  url?: string;
  /** The backoff, injectable so a test can watch the reconnect without waiting it. */
  reconnectDelayMs?: (attempt: number) => number;
  /** The clock the minimum-lifetime rule reads, for the same reason. Defaults to `Date.now`. */
  nowMs?: () => number;
  /** The operator's pause (design §3.3). While it is on, the socket stays up — it is the
   * channel that makes resuming live — but a frame is read and dropped rather than applied:
   * the screen is frozen where the operator left it, and the header says how old that is. */
  paused?: boolean;
}

/** What the screen has to know about the transport. `live` false means no frame can
 * arrive, so the query hooks re-read on a slow timer and the header says so: a stale
 * screen an operator cannot tell from a live one is the failure this observatory exists
 * to prevent. */
export interface ObservatorySocketState {
  live: boolean;
}

/** Subscribes the query cache to `/api/v1/ws`. Call it once, at the root: it owns one
 * socket for the screen's lifetime and invalidates the keys the existing hooks read. */
/** Every invalidation the live channel makes leaves a read already running alone. By default
 * an invalidated in-flight query is cancelled and restarted, so under a busy drain each
 * frame aborted the 2 s snapshot read before it finished (58 of 78 reads, owner's HAR,
 * 2026-11-22). A read in flight started after the change the frame names is not stale in
 * any way a restart would fix; it is the refresh. */
const NO_CANCEL = { cancelRefetch: false } as const;

/** Refresh the matching queries without cancelling a read in flight. A read already running
 * may have started before the change the frame names, so it cannot be the whole answer: each
 * one in flight gets one more read when it settles. That is at most one extra read per
 * running one, however many frames arrive meanwhile, so a busy drain's frames cost one
 * trailing read rather than a restart per frame that never lets any finish. */
const trailing = new WeakMap<QueryClient, Set<string>>();
function refresh(client: QueryClient, filters: InvalidateQueryFilters): void {
  const cache = client.getQueryCache();
  const pending = trailing.get(client) ?? new Set<string>();
  trailing.set(client, pending);
  for (const query of cache.findAll({ ...filters, fetchStatus: "fetching" })) {
    if (pending.has(query.queryHash)) continue;
    pending.add(query.queryHash);
    const unsubscribe = cache.subscribe(event => {
      if (event.query !== query || event.type !== "updated") return;
      if (event.action.type !== "success" && event.action.type !== "error") return;
      unsubscribe();
      pending.delete(query.queryHash);
      void client.invalidateQueries({ queryKey: query.queryKey, exact: true }, NO_CANCEL);
    });
  }
  void client.invalidateQueries(filters, NO_CANCEL);
}

export function useObservatorySocket(
  options: ObservatorySocketOptions = {},
): ObservatorySocketState {
  const client = useQueryClient();
  const url =
    options.url ?? (typeof window === "undefined" ? null : socketUrl(window.location));
  const delay = options.reconnectDelayMs ?? reconnectDelay;
  const now = options.nowMs ?? Date.now;
  const [live, setLive] = useState(false);
  // A ref, not a dependency: the pause must not tear down and rebuild the socket, and a
  // frame is answered without waiting for a render to reach this closure.
  const paused = useRef(options.paused ?? false);
  useEffect(() => {
    paused.current = options.paused ?? false;
  }, [options.paused]);
  useEffect(() => {
    if (url === null || typeof WebSocket === "undefined") return;
    let stopped = false;
    let attempt = 0;
    let timer: ReturnType<typeof setTimeout> | null = null;
    let socket: WebSocket | null = null;
    // The lane strip's refresh, rate-limited (LANES_REFRESH_MIN_MS): when it last ran, and the
    // one trailing refresh a burst inside the gap leaves scheduled.
    let lanesAt = Number.NEGATIVE_INFINITY;
    let lanesTimer: ReturnType<typeof setTimeout> | null = null;
    const refreshLanes = () => {
      const wait = lanesAt + LANES_REFRESH_MIN_MS - now();
      if (wait <= 0) {
        lanesAt = now();
        void refresh(client, { queryKey: queryKeys.lanes() });
        return;
      }
      if (lanesTimer !== null) return;
      lanesTimer = setTimeout(() => {
        lanesTimer = null;
        if (stopped || paused.current) return;
        lanesAt = now();
        void refresh(client, { queryKey: queryKeys.lanes() });
      }, wait);
    };

    const open = () => {
      const opened = new WebSocket(url);
      // Null until the handshake completes, so a socket that never opened is a failure by
      // definition rather than one the clock happens to have aged.
      let openedAt: number | null = null;
      socket = opened;
      opened.onopen = () => {
        openedAt = now();
        setLive(true);
        // The screen was read over HTTP before this socket existed, so anything that
        // changed in between is in neither the cache nor a frame: one read on connect
        // closes that window, and it is the only unconditional one — unless the operator
        // has paused, in which case the screen they paused is the screen they keep.
        // The reads that poll on their own clock (lanes, waiting, passes, designs,
        // questions) are not re-read here: they were fetched with this page and refetch
        // themselves, and re-reading the slow ones (/why) on connect only restarts them.
        if (!paused.current) void refresh(client, { predicate: query => !POLLED_ROOTS.has(String(query.queryKey[0])) });
      };
      opened.onmessage = (event: MessageEvent) => {
        const frame = parseFrame(event.data);
        if (frame === null) return;
        // A frame is the proof the link works — the server only sends one for a store it
        // could read — so whatever failed before it is history.
        attempt = 0;
        // The pause outranks the frame: the store may have moved, and the operator asked
        // to keep reading what they were reading.
        if (paused.current) return;
        // Only a frame that names a run's log or subtree is lane activity: a `snapshot`
        // frame does not refetch /lanes, which polls on its own.
        if (frame.changed.some(name => name.startsWith("runlog:") || name.startsWith("runs:"))) {
          refreshLanes();
        }
        // The Items order and the waiting rows come from /why, so a `snapshot` frame still
        // refreshes it, but without cancelRefetch: invalidating an in-flight query cancels
        // and restarts it by default, so a busy drain's frames would keep the slow read from
        // ever finishing. With it off, a read already running is the refresh.
        if (frame.changed.includes("snapshot")) void refresh(client, { queryKey: queryKeys.waiting() });
        for (const name of frame.changed) {
          const key = invalidationKey(name);
          if (key !== null) void refresh(client, { queryKey: key });
        }
      };
      opened.onclose = () => {
        if (socket === opened) socket = null;
        setLive(false);
        if (stopped) return;
        // A connection that stayed up long enough to be a working link starts the backoff
        // over; one that closed straight after the handshake is the failure the backoff is
        // for, and the delay has to keep growing.
        if (openedAt !== null && now() - openedAt >= RECONNECT_STABLE_MS) attempt = 0;
        timer = setTimeout(open, delay(attempt));
        attempt += 1;
      };
    };
    open();
    return () => {
      stopped = true;
      if (timer !== null) clearTimeout(timer);
      if (lanesTimer !== null) clearTimeout(lanesTimer);
      socket?.close();
      socket = null;
    };
  }, [client, url, delay, now]);
  return { live };
}
