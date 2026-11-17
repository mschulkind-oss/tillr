import type { WaitingRow } from "./types";

export function waitingIdentity(row: WaitingRow): string {
  return row.document || row.item_id;
}

export function waitingOrder(a: WaitingRow, b: WaitingRow): number {
  if (a.document || b.document) {
    if (!a.document) return -1;
    if (!b.document) return 1;
    return a.document.localeCompare(b.document);
  }
  if (!a.rank) return b.rank ? 1 : a.item_id.localeCompare(b.item_id);
  if (!b.rank) return -1;
  return a.rank < b.rank ? -1 : a.rank > b.rank ? 1 : a.item_id.localeCompare(b.item_id);
}
