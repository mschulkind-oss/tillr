// The board's "now" reading: which runs are live, and the two clocks a live run carries.
// A live run's elapsed is the viewer's clock read from the run's own start; the lease is
// the store's own expiry. Neither is a recorded duration, and every surface that draws one
// says which it is (design §4.3, §4.4, §6; ledger `D-OBS10`/`D-OBS12`).

import type { RunSummary, SnapshotData } from "./types";

/** The store's spelling for a lane holding a lease (`run.LaneStateLeased`). A lane in any
 * other state is not running, whatever its last run row says. */
const LANE_STATE_LEASED = "leased";

/** One live run the now band draws: a leased lane with the run it names. The item title
 * is the snapshot's, not the lane's, because a lane carries an id and an operator reads a
 * title. */
export interface LiveRun {
  laneId: string;
  runId: string;
  /** The item the run belongs to, when a run record in the snapshot names it. Null is a
   * named absence: the lane is leased and the run's item is not in this snapshot. */
  itemId: string | null;
  /** The item's title from the snapshot's own rows, or null when the item is not there. */
  title: string | null;
  /** The run's recorded start. Null when the snapshot names the run but not its start —
   * an absence the screen says out loud rather than drawing as a zero. */
  startedAtMs: number | null;
  /** The lane's own expiry from `lanes[].leased_until_ms`; null when the store recorded
   * no expiry for this leased lane, which is not a missing lease or an expired one. */
  leasedUntilMs: number | null;
}

/** liveRuns is the now band's rows: every leased lane, paired with the run it names and
 * that run's item. The runs are the snapshot's own — an item's `latest_run` and
 * `recent_runs` are the same `runs` table — so the band invents no second reading of the
 * store. A lane that is not leased, or names no run, is not a live run and is skipped; the
 * lanes keep their own order, so the band invents no second order either. */
export function liveRuns(snapshot: SnapshotData | undefined): LiveRun[] {
  if (snapshot === undefined) return [];

  // The wire carries runs twice: the item list's `latest_run` is the one the operator's row
  // shows, and `recent_runs` is the tail. A live run the server projects from a lease is in
  // both; reading both and keeping the first by id means neither copy is required alone.
  // The run's item is its owner's row in the same list, which is also where the title and
  // the selection come from: `runs` carries no title, and a title is what the band shows.
  const runs = new Map<string, RunSummary>();
  const owners = new Map<string, { itemId: string; title: string }>();
  for (const item of snapshot.items) {
    if (item.latest_run === null) continue;
    runs.set(item.latest_run.run_id, item.latest_run);
    owners.set(item.latest_run.run_id, { itemId: item.item_id, title: item.title });
  }
  for (const run of snapshot.recent_runs) {
    if (!runs.has(run.run_id)) runs.set(run.run_id, run);
  }

  const out: LiveRun[] = [];
  for (const lane of snapshot.lanes) {
    if (lane.state !== LANE_STATE_LEASED || lane.run_id === "") continue;
    const run = runs.get(lane.run_id);
    const owner = owners.get(lane.run_id);
    out.push({
      laneId: lane.lane_id,
      runId: lane.run_id,
      itemId: owner?.itemId ?? null,
      title: owner?.title ?? null,
      // A run row with no start (the live projection carries the allocation's clock) is
      // an absent measurement, not 1970 and not zero elapsed.
      startedAtMs: run !== undefined && run.started_at_ms > 0 ? run.started_at_ms : null,
      leasedUntilMs: lane.leased_until_ms,
    });
  }
  return out;
}

/** durationText is a span of milliseconds in the design's §6 format: `12s`, `4m07s`,
 * `1h12m`. The same shape labels a live elapsed and a recorded duration; the words beside
 * it are what tell the operator which one they are reading. */
export function durationText(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  if (total < 60) return `${total}s`;
  const minutes = Math.floor(total / 60);
  if (minutes < 60) return `${minutes}m${String(total % 60).padStart(2, "0")}s`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h${String(minutes % 60).padStart(2, "0")}m`;
}

/** liveElapsedText is the viewer's clock reading for a run that has not finished: the gap
 * between the run's recorded start and `now`. Null when the run's start is not in the
 * snapshot, so the caller names the absence instead of drawing `0s` as if the run had
 * just begun. */
export function liveElapsedText(startedAtMs: number | null, now: number): string | null {
  if (startedAtMs === null) return null;
  return durationText(now - startedAtMs);
}

/** leaseText is the lease's own clock. A leased lane with a null expiry is held
 * indefinitely; without evidence of a lease, null instead names a missing record. */
export function leaseText(leasedUntilMs: number | null, now: number, leased = false): string {
  if (leasedUntilMs === null) return leased ? "lease has no expiry" : "lease not recorded";
  const left = leasedUntilMs - now;
  if (left < 0) return `lease expired ${durationText(-left)} ago`;
  return `lease ${durationText(left)} left`;
}
