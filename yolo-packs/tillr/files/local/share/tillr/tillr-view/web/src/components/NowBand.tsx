// The now band: a copy of the live run's row, pinned above the queue's list and outside its
// order (design §4.4, ledger `D-OBS12`). It is not a second queue — the item keeps its rank
// position — and its elapsed is the viewer's clock read from the run's start, labelled as
// such (ledger `D-OBS10`). Its other clock is the lane's own lease expiry. With no lease
// live there is no band at all: an empty box would be the absence-drawn-as-a-value the
// truthfulness table forbids.

import { useNow } from "../models/clock";
import { leaseText, liveElapsedText, liveRuns } from "../models/now";
import type { SnapshotData } from "../models/types";

export interface NowBandProps {
  /** The board's snapshot: the band reads `lanes` and the runs it names. Undefined while
   * the first read is in flight, and the band is absent with it. */
  snapshot: SnapshotData | undefined;
  /** The viewer's clock, in milliseconds. Tests pin it; the board ticks it once a second. */
  now?: number;
  /** Selects the run's item, the same move as clicking its row. */
  onSelectItem?: (itemId: string) => void;
}

export function NowBand({ snapshot, now: pinned, onSelectItem }: NowBandProps) {
  const ticking = useNow();
  const now = pinned ?? ticking;
  // No live run is not an empty band: it is no band, the same rule the truthfulness table
  // states for every other absence (design §5.2).
  const live = liveRuns(snapshot);
  if (live.length === 0) return null;

  return (
    <section className="now-band" aria-label="Live runs">
      <div className="now-band__head">
        <h2 className="now-band__title">Now</h2>
        <span className="now-band__count">
          {live.length} live run{live.length === 1 ? "" : "s"}
        </span>
      </div>
      <ul className="now-band__list">
        {live.map((run) => {
          const elapsed = liveElapsedText(run.startedAtMs, now);
          const itemId = run.itemId;
          return (
            <li className="now-band__run" key={run.laneId} data-lane={run.laneId}>
              <span className="now-band__item">
                {itemId === null ? (
                  // The lane is leased and the snapshot carries no run for it: the band
                  // says which fact is missing rather than borrowing a row from elsewhere.
                  <span className="now-band__absent">the run is not in the snapshot</span>
                ) : (
                  <button
                    type="button"
                    className="now-band__link"
                    onClick={() => onSelectItem?.(itemId)}
                    title={run.title ?? itemId}
                  >
                    {run.title ?? itemId}
                  </button>
                )}
              </span>
              <span className="now-band__meta">
                <span className="now-band__lane">lane {run.laneId}</span>
                {run.seat ? <span className="now-band__seat">seat {run.seat}</span> : null}
                {elapsed === null ? (
                  <span className="now-band__absent">no start recorded</span>
                ) : (
                  // `data-clock` is the machine-readable half of the label: the value is
                  // the viewer's clock, and the words say so on screen too.
                  <span
                    className="now-band__elapsed"
                    data-clock="viewer"
                    title="from the run's start to your clock; the store records no finish for a live run"
                  >
                    elapsed <strong>{elapsed}</strong>
                    <span className="now-band__clock">viewer&apos;s clock</span>
                  </span>
                )}
                <span className="now-band__lease">
                  {leaseText(run.leasedUntilMs, now, true)}
                </span>
              </span>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
