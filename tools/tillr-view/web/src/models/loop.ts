import type { DrainSummary } from "./types";

// Runs and leases are not evidence that the loop process is still alive.
export function loopAlive(drain: DrainSummary | undefined, now: number): boolean {
  const session = drain?.session;
  return session != null && session.ended_at_ms === null &&
    now - session.refreshed_at_ms <= 2 * (drain?.heartbeat_interval_ms ?? 1000) &&
    now <= session.next_pass_at_ms + (drain?.session_grace_ms ?? 5000);
}
