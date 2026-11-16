// An item with no run selected is a fact about the item or about the selection, not a read
// still in flight: every blocked item has no run at all, and an item the store does have a
// run for is one the operator has not opened yet. The inspector says which of the two it
// is, what holds the item, and what a run will bring with it — because an unexplained empty
// pane is the failure this observatory exists to prevent (design §6.1).

import { StateGlyph } from "./StateGlyph";
import type { ItemData } from "../models/types";

export interface ItemWithoutRunProps {
  item: ItemData | undefined;
  /** Whether the store lists a run for this item. A blocked item has none, so the absence
   * is a fact about the item; an item with a run and no selection is a different fact, and
   * the heading must not claim the first when it means the second. */
  hasRun: boolean;
}

export function ItemWithoutRun({ item, hasRun }: ItemWithoutRunProps) {
  if (!item) return null;
  const detail = item.item;
  const waitingOn = detail.projected_state.waiting_on ?? [];
  return (
    <section className="notice notice--accent">
      <h2 className="notice__title">
        {hasRun ? "No run selected for " : "No run for "}
        <code>{detail.item_id}</code>
      </h2>
      <p className="notice__body">
        {detail.projected_state.underlying ?? "the store records no run for this item"}
        {waitingOn.length > 0 ? ` — waiting on ${waitingOn.join(", ")}` : ""}
      </p>
      <p className="notice__body">
        {hasRun
          ? "The store does have a run for it. Press Enter, or use the run button in the item pane, to open its pipeline here."
          : "No lane has picked it up yet. A run appears when one does, and the pipeline, its exchanges and its timeline appear with it."}
      </p>
      <div className="notice__actions">
        <span className={`state-badge state-badge--${detail.projected_state.state}`}>
          <StateGlyph state={detail.projected_state.state} />
          {detail.projected_state.state}
        </span>
        <span className="pane__count">raw: {detail.projected_state.raw_state}</span>
      </div>
    </section>
  );
}
