// The live channel of design §4.2: one socket per screen, and the same invalidation the
// [Refresh Snapshot] button calls. Every panel still reads its query hook, so no
// component learns the transport exists — a dropped socket falls back to the query hooks'
// slow poll rather than to a blank screen, and a frame that names a resource the client
// does not cache is ignored rather than swallowed into a full refetch.

import { useQueryClient } from "@tanstack/react-query";
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
        if (!paused.current) void client.invalidateQueries();
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
        if (frame.changed.some(name => name === "snapshot" || name.startsWith("runlog:") || name.startsWith("runs:"))) {
          void client.invalidateQueries({ queryKey: queryKeys.lanes() });
        }
        for (const name of frame.changed) {
          const key = invalidationKey(name);
          if (key !== null) void client.invalidateQueries({ queryKey: key });
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
      socket?.close();
      socket = null;
    };
  }, [client, url, delay, now]);
  return { live };
}
