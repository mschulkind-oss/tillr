import type { DrainSummary } from "./types";

// Runs and leases are not evidence that the loop process is still alive.
// The server judged the heartbeat against its own clock when it read the session, and
// says so in `active`. The browser's clock and a snapshot that is a few seconds old would
// otherwise turn a live loop into "no loop is running" (2026-11-23: heartbeats landed
// every few seconds under store load, wider than the 2-interval window). The
// browser re-judges by its clock only for a server that sends no verdict.
export function loopAlive(drain: DrainSummary | undefined, now: number): boolean {
  const session = drain?.session;
  if (session == null || session.ended_at_ms !== null) return false;
  if (typeof drain?.active === "boolean") return drain.active;
  return now - session.refreshed_at_ms <= 2 * (drain?.heartbeat_interval_ms ?? 1000) &&
    now <= session.next_pass_at_ms + (drain?.session_grace_ms ?? 5000);
}
