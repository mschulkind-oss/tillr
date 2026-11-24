import type { LaneRow, LaneView } from "./types";

/** Without the loop's configured lanes, an idle lane stays visible for this long. */
export const IDLE_VISIBLE_MS = 60 * 60 * 1000;

export const DRAINING_LANE_NOTE = "finishing old work, gets no new work";

export type LaneGroupKind = "configured" | "draining" | "idle" | "desk";

export interface GroupedLanes {
  configured: LaneRow[];
  draining: LaneRow[];
  desks: LaneRow[];
  idle: LaneRow[];
}

export function isDocumentDeskLane(laneId: string): boolean {
  return laneId.startsWith("desk-") && laneId !== "desk-trunk-fixer";
}

function splitNumericLane(id: string): [string, number] | null {
  const idx = id.lastIndexOf("-");
  if (idx < 0 || idx === id.length - 1) return null;
  const suffix = id.slice(idx + 1);
  if (!/^\d+$/.test(suffix)) return null;
  return [id.slice(0, idx + 1), Number(suffix)];
}

export function naturalLaneCompare(a: string, b: string): number {
  const pa = splitNumericLane(a);
  const pb = splitNumericLane(b);
  if (pa && pb && pa[0] === pb[0]) return pa[1] - pb[1];
  return a < b ? -1 : a > b ? 1 : 0;
}

export function laneRowBusy(lane: Pick<LaneRow, "state" | "held_item_id" | "group">): boolean {
  return lane.state !== "idle" || Boolean(lane.held_item_id) || lane.group === "draining";
}

export function classifyLane(
  lane: { lane_id: string; group?: LaneGroupKind; busy: boolean },
  configuredSet: ReadonlySet<string> | null,
): LaneGroupKind | null {
  if (configuredSet && configuredSet.size > 0) {
    if (configuredSet.has(lane.lane_id)) return "configured";
    if (isDocumentDeskLane(lane.lane_id)) return lane.busy || lane.group === "desk" ? "desk" : "idle";
    return lane.busy || lane.group === "draining" ? "draining" : "idle";
  }
  return lane.group ?? null;
}

/** Groups lanes into configured, draining, active document desks, and idle historical
 * lanes when the session records configured lanes (or the server stamped row groups).
 * Returns null when no session configuration is recorded. */
export function groupLanes(lanes: LaneRow[], configured: string[] | undefined): GroupedLanes | null {
  const configuredList = configured && configured.length > 0 ? configured : null;
  const hasServerGroups = lanes.some(lane => Boolean(lane.group));
  if (!configuredList && !hasServerGroups) return null;

  const configuredSet = configuredList ? new Set(configuredList) : null;
  const configuredOrder = new Map<string, number>();
  if (configuredList) {
    configuredList.forEach((id, idx) => {
      if (!configuredOrder.has(id)) configuredOrder.set(id, idx);
    });
  }

  const out: GroupedLanes = { configured: [], draining: [], desks: [], idle: [] };
  for (const lane of lanes) {
    const kind = classifyLane({ lane_id: lane.lane_id, group: lane.group, busy: laneRowBusy(lane) }, configuredSet) ?? "idle";
    if (kind === "configured") out.configured.push(lane);
    else if (kind === "draining") out.draining.push(lane);
    else if (kind === "desk") out.desks.push(lane);
    else out.idle.push(lane);
  }

  if (configuredOrder.size > 0) {
    out.configured.sort((a, b) => (configuredOrder.get(a.lane_id) ?? 0) - (configuredOrder.get(b.lane_id) ?? 0));
  } else {
    out.configured.sort((a, b) => naturalLaneCompare(a.lane_id, b.lane_id));
  }
  out.draining.sort((a, b) => naturalLaneCompare(a.lane_id, b.lane_id));
  out.desks.sort((a, b) => naturalLaneCompare(a.lane_id, b.lane_id));
  out.idle.sort((a, b) => naturalLaneCompare(a.lane_id, b.lane_id));
  return out;
}

function summarizeGroups(
  entries: Array<{ lane_id: string; kind: LaneGroupKind; busy: boolean }>,
): string {
  let confTotal = 0, confOrdinary = 0, confExpress = 0, confBusy = 0, draining = 0, idle = 0;
  for (const entry of entries) {
    if (entry.kind === "configured") {
      confTotal++;
      if (entry.lane_id.startsWith("express-")) confExpress++;
      else if (!entry.lane_id.startsWith("desk-")) confOrdinary++;
      if (entry.busy) confBusy++;
    } else if (entry.kind === "draining") {
      draining++;
    } else if (entry.kind === "idle") {
      idle++;
    }
  }
  const confLabel = confOrdinary > 0 && confExpress > 0
    ? `${confTotal} configured (${confOrdinary} ordinary, ${confExpress} express)`
    : `${confTotal} configured`;
  const busyTotal = confBusy + draining;
  const busyLabel = draining > 0
    ? `${busyTotal} busy (${confBusy} of ${confTotal} configured + ${draining} draining old work)`
    : `${busyTotal} busy (${confBusy} of ${confTotal} configured, 0 draining)`;
  return `${confLabel}, ${busyLabel} · ${idle} idle holding nothing`;
}

export function formatLaneRowSummary(lanes: LaneRow[], configured: string[] | undefined): string | null {
  const groups = groupLanes(lanes, configured);
  if (!groups) return null;
  return summarizeGroups([
    ...groups.configured.map(l => ({ lane_id: l.lane_id, kind: "configured" as const, busy: laneRowBusy(l) })),
    ...groups.draining.map(l => ({ lane_id: l.lane_id, kind: "draining" as const, busy: true })),
    ...groups.desks.map(l => ({ lane_id: l.lane_id, kind: "desk" as const, busy: true })),
    ...groups.idle.map(l => ({ lane_id: l.lane_id, kind: "idle" as const, busy: false })),
  ]);
}

export function formatSnapshotLaneSummary(lanes: LaneView[], configured: string[] | undefined): string | null {
  const configuredList = configured && configured.length > 0 ? configured : null;
  const hasServerGroups = lanes.some(lane => Boolean(lane.group));
  if (!configuredList && !hasServerGroups) return null;
  const configuredSet = configuredList ? new Set(configuredList) : null;
  const seen = new Set<string>();
  const entries: Array<{ lane_id: string; kind: LaneGroupKind; busy: boolean }> = [];
  for (const lane of lanes) {
    seen.add(lane.lane_id);
    const ws = lane.work_state ?? (lane.state === "leased" ? "running" : "idle");
    const busy = ws !== "idle" || Boolean(lane.held_item_id) || lane.group === "draining";
    const kind = classifyLane({ lane_id: lane.lane_id, group: lane.group, busy }, configuredSet) ?? "idle";
    entries.push({ lane_id: lane.lane_id, kind, busy });
  }
  if (configuredList) {
    for (const id of configuredList) {
      if (id && !seen.has(id)) {
        seen.add(id);
        entries.push({ lane_id: id, kind: "configured", busy: false });
      }
    }
  }
  return summarizeGroups(entries);
}

/** Which lanes a page draws and which it folds into one line. A busy or draining lane is
 * always drawn. An idle lane is drawn when it is one of the lanes the loop session says it
 * was configured to run; when the session carries none (an older loop), an idle lane is
 * drawn only while it has been idle less than an hour. Order is preserved. */
export function collapseIdleLanes(lanes: LaneRow[], configured: string[] | undefined, now: number): { shown: LaneRow[]; hidden: LaneRow[] } {
  const known = configured && configured.length > 0 ? new Set(configured) : null;
  const hasServerGroups = lanes.some(lane => Boolean(lane.group));
  const shown: LaneRow[] = [], hidden: LaneRow[] = [];
  for (const lane of lanes) {
    const busy = laneRowBusy(lane);
    const keep = busy || (known ? known.has(lane.lane_id)
      : hasServerGroups ? lane.group === "configured" || lane.group === "desk"
      : lane.state_since_ms === null || now - lane.state_since_ms < IDLE_VISIBLE_MS);
    (keep ? shown : hidden).push(lane);
  }
  return { shown, hidden };
}
