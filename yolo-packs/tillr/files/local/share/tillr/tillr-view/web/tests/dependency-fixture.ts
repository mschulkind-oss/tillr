import type { ItemView, WaitingRow } from "../src/models/types";

export function graphItem(id: string, blockers: string[] = [], state = "in_queue"): ItemView {
  return { item_id: id, title: `Build ${id} and verify the complete behavior without hiding labels`, blocked_by: blockers, projected_state: {state, raw_state: state}, updated_at_ms: 0, cost_usd: null, latest_decision: null, latest_run: null };
}

/** Three columns: 20 roots, 20 single dependencies, 20 triple dependencies (80 edges). */
export const sixtyItems = Array.from({length: 60}, (_, i) => graphItem(`item-${i}`, i < 20 ? [] : i < 40 ? [`item-${i-20}`] : [`item-${i-20}`, `item-${i-40}`, `item-${(i-39)%20}`]));
export const fixtureClasses: WaitingRow[] = sixtyItems.map((item, i) => ({item_id:item.item_id, class:i < 20 ? "ready" : "waits on work", rank:String(i), where:"queue", why:"fixture", mover:"fixture", since:"fixture", class_source:"rule"}));
