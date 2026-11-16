// The queue's summary of what the night closed: the last few items that reached a terminal
// state, newest first, copied above the list and outside its order (design §4.5). It is a
// summary, not the archive — the whole list is one control away — and it exists because
// "what did the tick close" is a question a queue should answer without leaving it.

import { StateGlyph } from "./StateGlyph";
import { relativeTime, stateLabel } from "../models/board";
import type { ItemView } from "../models/types";

export interface FinishedBandProps {
  /** The newest terminal items, already ordered and limited by `recentlyFinished`. */
  items: ItemView[];
  /** The viewer's clock, pinned by a test; the band reads `Date.now()` otherwise. */
  now?: number;
  onSelectItem: (itemId: string) => void;
  onOpenArchive: () => void;
}

export function FinishedBand({ items, now, onSelectItem, onOpenArchive }: FinishedBandProps) {
  // No finished items is not an empty band: a box saying "nothing finished" on every young
  // store would be the absence-drawn-as-a-value the truthfulness table forbids (design
  // §5.2). The header count already says zero.
  if (items.length === 0) return null;
  return (
    <section className="finished-band" aria-label="Recently finished">
      <div className="finished-band__head">
        <span className="finished-band__heading">Finished · last {items.length}</span>
        <button type="button" className="btn btn--quiet" onClick={onOpenArchive}>
          Open the archive →
        </button>
      </div>
      <ul className="finished-band__list">
        {items.map((item) => {
          const state = item.projected_state.state;
          return (
            <li key={item.item_id}>
              <button
                type="button"
                className="finished-band__row"
                data-state={state}
                title={`${item.item_id} — ${stateLabel(state)}, last changed ${relativeTime(item.updated_at_ms, now)}`}
                onClick={() => onSelectItem(item.item_id)}
              >
                <span className="finished-band__glyph">
                  <StateGlyph state={state} />
                </span>
                <span className="finished-band__title" title={item.title}>
                  {item.title}
                </span>
                <span className="finished-band__state">{stateLabel(state)}</span>
                <span className="finished-band__when">
                  {relativeTime(item.updated_at_ms, now)}
                </span>
              </button>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
