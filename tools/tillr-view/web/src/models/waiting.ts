import type { WaitingRow } from "./types";

export function waitingIdentity(row: WaitingRow): string {
  return row.observation || row.document || row.item_id;
}

export function waitingActors(rows: WaitingRow[]) {
  const counts = { owner: 0, drain: 0, design: 0, operator: 0 };
  for (const row of rows) {
    if (["ready", "moving", "done"].includes(row.class)) continue;
    if (row.mover.startsWith("owner; answer ") && row.mover.includes("#OQ-")) counts.owner++;
    else if (row.mover.startsWith("design seat;") || row.mover.includes("design seat")) counts.design++;
    else if (row.mover.startsWith("operator;") || ["needs you", "needs a cause"].includes(row.class)) counts.operator++;
    else counts.drain++;
  }
  return counts;
}

export function waitingOrder(a: WaitingRow, b: WaitingRow): number {
  if (a.document || b.document) {
    if (!a.document) return -1;
    if (!b.document) return 1;
    return a.document.localeCompare(b.document);
  }
  return (a.place ?? Infinity) - (b.place ?? Infinity) || waitingIdentity(a).localeCompare(waitingIdentity(b));
}
