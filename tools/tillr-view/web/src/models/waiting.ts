import type { WaitingRow } from "./types";

export function waitingIdentity(row: WaitingRow): string {
  return row.observation || row.document || row.item_id;
}

export function waitingOrder(a: WaitingRow, b: WaitingRow): number {
  if (a.document || b.document) {
    if (!a.document) return -1;
    if (!b.document) return 1;
    return a.document.localeCompare(b.document);
  }
  return (a.place ?? Infinity) - (b.place ?? Infinity) || waitingIdentity(a).localeCompare(waitingIdentity(b));
}
