import type { LaneRow } from "./types";

/** Without the loop's configured lanes, an idle lane stays visible for this long. */
export const IDLE_VISIBLE_MS = 60 * 60 * 1000;

/** Which lanes a page draws and which it folds into one line. A busy lane (anything not
 * idle) is always drawn. An idle lane is drawn when it is one of the lanes the loop session
 * says it was configured to run; when the session carries none (an older loop), an idle
 * lane is drawn only while it has been idle less than an hour. Order is preserved. */
export function collapseIdleLanes(lanes: LaneRow[], configured: string[] | undefined, now: number): { shown: LaneRow[]; hidden: LaneRow[] } {
  const known = configured && configured.length > 0 ? new Set(configured) : null;
  const shown: LaneRow[] = [], hidden: LaneRow[] = [];
  for (const lane of lanes) {
    const keep = lane.state !== "idle" || (known ? known.has(lane.lane_id)
      : lane.state_since_ms === null || now - lane.state_since_ms < IDLE_VISIBLE_MS);
    (keep ? shown : hidden).push(lane);
  }
  return { shown, hidden };
}
