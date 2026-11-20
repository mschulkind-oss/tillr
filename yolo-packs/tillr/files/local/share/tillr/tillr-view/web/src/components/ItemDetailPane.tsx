// The selected item, in full: what it is, what holds it, the fence it may change, the
// criteria its verdict is judged against, its runs — and its design document as metadata
// and a link into Vantage, which renders the document. Every absence on this pane is
// named: an item with no fence, no run, no design document or no recorded blocker says
// which of those it is instead of rendering an empty box.

import type { ReactNode } from "react";

import { CommentThreads } from "./Designers";
import { DocumentPane } from "./DocumentPane";
import { EpistemicBadge } from "./EpistemicBadge";
import { RefusalState, refusalCode } from "./RefusalState";
import type { RefusalSentence } from "./RefusalState";
import { StateGlyph } from "./StateGlyph";
import { relativeTime, stateLabel } from "../models/board";
import type { DocumentPaneProps } from "./DocumentPane";
import type { CriterionView, ItemData, ItemProjection, RunSummary } from "../models/types";

export interface ItemDetailPaneProps {
  itemId: string | null;
  item: ItemData | undefined;
  itemError: Error | null;
  loading: boolean;
  onRetry: () => void;
  latestRun: RunSummary | null;
  /** When the store last touched the item, from the snapshot's row for it. */
  updatedAtMs: number | null;
  selectedRunId: string | null;
  onSelectRun: (runId: string) => void;
  doc: DocumentPaneProps;
  onBack?: () => void;
}

/** The item route's own refusals. A code that is not here falls through to the shared
 * component's `the face refused: <code>` branch, which is what makes an unrecognised code
 * a refusal rather than an empty pane. */
const ITEM_REFUSALS: Record<string, RefusalSentence> = {
  "item-not-found": {
    title: "The item is not in the store",
    body: "The store does not carry this item, which is a defect in the link rather than an item with nothing recorded.",
    tone: "notice--blocked",
    retry: true,
  },
};

function Facts({
  detail,
  latestRun,
  updatedAtMs,
}: {
  detail: ItemData["item"];
  latestRun: RunSummary | null;
  updatedAtMs: number | null;
}) {
  const fence = detail.allowed_paths;
  return (
    <>
      <dl className="detail__facts">
        <div className="fact">
          <dt className="fact__label">Fence</dt>
          <dd className="fact__value">
            {fence.length === 0
              ? "no path is allowed"
              : `${fence.length} allowed ${fence.length === 1 ? "path" : "paths"}`}
          </dd>
        </div>
        <div className="fact">
          <dt className="fact__label">Latest run</dt>
          <dd className="fact__value">
            {latestRun ? latestRun.run_id : "the store records no run"}
          </dd>
        </div>
        <div className="fact">
          <dt className="fact__label">Updated</dt>
          <dd className="fact__value">{relativeTime(updatedAtMs)}</dd>
        </div>
        <div className="fact">
          <dt className="fact__label">Raw state</dt>
          <dd className="fact__value">
            <code>{detail.projected_state.raw_state}</code>
          </dd>
        </div>
      </dl>
    </>
  );
}

/** WhyBlocked names what holds a blocked item. A blocked item with no blocker record is
 * itself a fact worth saying out loud, so it renders as a named absence, not as silence. */
function WhyBlocked({
  projection,
  blockedBy,
}: {
  projection: ItemProjection;
  blockedBy: string[];
}) {
  const waitingOn = projection.waiting_on ?? [];
  const why = projection.underlying ?? "";
  if (why === "" && blockedBy.length === 0 && waitingOn.length === 0) {
    return (
      <div className="blocked-reason">
        <strong>Blocked</strong>
        <p className="blocked-reason__why">
          the store records no reason it is blocked <EpistemicBadge state="absent" />
        </p>
      </div>
    );
  }
  return (
    <div className="blocked-reason">
      <strong>Blocked</strong>
      {why ? <p className="blocked-reason__why">{why}</p> : null}
      {waitingOn.length > 0 ? (
        <p className="blocked-reason__why">
          waiting on {waitingOn.join(", ")}
        </p>
      ) : null}
      {blockedBy.length > 0 ? (
        <ul className="chips" style={{ marginTop: "var(--s1)" }}>
          {blockedBy.map((id) => (
            <li key={id}>
              <span className="chip">{id}</span>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

function Criterion({ criterion, claimed }: { criterion: CriterionView; claimed: boolean }) {
  const done = criterion.state === "done";
  return (
    <li className={done ? "criterion criterion--done" : "criterion"}>
      <span className="criterion__mark" aria-hidden="true">
        {done ? "✓" : ""}
      </span>
      <span>
        <span className="criterion__id">{criterion.criterion_id} —</span> {criterion.text}{" "}
        <span className="criterion__state">
          ({done ? "done" : criterion.state === "" ? "open" : criterion.state}){claimed ? " · ~claimed · derived (worker report, not a verdict)" : ""}
        </span>
      </span>
    </li>
  );
}

function Section({
  title,
  note,
  children,
}: {
  title: string;
  note?: string;
  children: ReactNode;
}) {
  return (
    <section className="section">
      <div className="section__head">
        <h2 className="section__title">{title}</h2>
        {note ? <span className="section__note">{note}</span> : null}
      </div>
      {children}
    </section>
  );
}

export function ItemDetailPane({
  itemId,
  item,
  itemError,
  loading,
  onRetry,
  latestRun,
  updatedAtMs,
  selectedRunId,
  onSelectRun,
  doc,
  onBack,
}: ItemDetailPaneProps) {
  if (itemId === null) {
    // With no selection, a read still in flight is a skeleton rather than a prompt: the
    // prompt says the board is ready for input, which is not true yet.
    return (
      <main className="pane pane--detail" aria-label="Item">
        {loading ? (
          <div className="skeleton" aria-label="reading the board">
            <span className="skeleton__line skeleton__line--title" />
            <span className="skeleton__line" style={{ width: "88%" }} />
            <span className="skeleton__line" style={{ width: "72%" }} />
            <span className="skeleton__line" style={{ width: "80%" }} />
          </div>
        ) : (
        <div className="takeaway">
          <div>
            <p className="takeaway__title">Nothing selected</p>
            Pick an item from the queue. <kbd>j</kbd> and <kbd>k</kbd> walk the queue,{" "}
            <kbd>/</kbd> filters it, and <kbd>Enter</kbd> opens the selected item&apos;s run in
            the inspector.
          </div>
        </div>
        )}
      </main>
    );
  }

  if (itemError) {
    return (
      <main className="pane pane--detail" aria-label="Item">
        <div className="pane__scroll pane__scroll--pad">
          <RefusalState
            code={refusalCode(itemError)}
            message={itemError.message}
            sentences={ITEM_REFUSALS}
            onRetry={onRetry}
          />
        </div>
      </main>
    );
  }

  if (!item) {
    return (
      <main className="pane pane--detail" aria-label="Item">
        <div className="pane__scroll pane__scroll--pad">
          <div className="skeleton" aria-label={loading ? "reading the item" : "no item"}>
            <span className="skeleton__line skeleton__line--title" />
            <span className="skeleton__line" style={{ width: "88%" }} />
            <span className="skeleton__line" style={{ width: "72%" }} />
          </div>
        </div>
      </main>
    );
  }

  const detail = item.item;
  const state = detail.projected_state.state;
  const open = item.criteria.filter((criterion) => criterion.state !== "done").length;
  const fence = detail.allowed_paths;

  return (
    <main className="pane pane--detail" aria-label="Item">
      <div className="pane__head">
        <h2 className="pane__title">Item</h2>
        <span className="pane__count">
          {onBack ? (
            <button type="button" className="btn btn--quiet" onClick={onBack}>
              ← Queue
            </button>
          ) : (
            <span>
              <kbd>j</kbd> <kbd>k</kbd> move
            </span>
          )}
        </span>
      </div>
      <div className="pane__scroll">
        <article className="detail">
          <header className="detail__head">
            <div className="detail__eyebrow">
              <span className="detail__id">{detail.item_id}</span>
              <span>·</span>
              <span>{detail.body_ref || "no body reference"}</span>
            </div>
            <h1 className="detail__title">{detail.title}</h1>
            <div className="detail__badges">
              <span className={`state-chip state-chip--${state}`}>
                <StateGlyph state={state} />
                {stateLabel(state)}
              </span>
              {latestRun ? (
                <button
                  type="button"
                  className="btn btn--quiet"
                  onClick={() => onSelectRun(latestRun.run_id)}
                  aria-pressed={latestRun.run_id === selectedRunId}
                >
                  run <code>{latestRun.run_id}</code> · {latestRun.state}
                  {latestRun.seat ? ` · seat ${latestRun.seat}` : ""} ·{" "}
                  {latestRun.usage_tokens ?? "unrecorded"} tokens
                </button>
              ) : (
                <span className="state-badge">no run</span>
              )}
            </div>
          </header>

          <Facts detail={detail} latestRun={latestRun} updatedAtMs={updatedAtMs} />

          {state === "blocked" ? (
            <>
              <WhyBlocked projection={detail.projected_state} blockedBy={item.blocked_by} />
              {item.closed_blockers.length > 0 ? (
                <p role="alert" className="notice notice--blocked">
                  Blocked by closed item{item.closed_blockers.length === 1 ? "" : "s"} without landing: {item.closed_blockers.join(", ")}. Moved by owner/operator: remove the dependency or reopen the closed item{item.closed_blockers.length === 1 ? "" : "s"}.
                </p>
              ) : null}
            </>
          ) : null}

          <Section title="Fence" note={fence.length === 0 ? "no path is allowed" : undefined}>
            {fence.length === 0 ? (
              <p className="empty">
                the store records no allowed path for this item <EpistemicBadge state="absent" />
              </p>
            ) : (
              <ul className="chips">
                {fence.map((path) => (
                  <li key={path}>
                    <span className="chip">{path}</span>
                  </li>
                ))}
              </ul>
            )}
          </Section>

          <Section
            title="Criteria"
            note={`${open} open · ${item.criteria.length} total`}
          >
            {item.criteria.length === 0 ? (
              <p className="empty">
                the store carries no criterion for this item{" "}
                <EpistemicBadge state="absent" />
              </p>
            ) : (
              <ul className="render-list">
                {item.criteria.map((criterion) => (
                  <Criterion key={criterion.criterion_id} criterion={criterion} claimed={!!item.progress?.some(claim => claim.criterion_id === criterion.criterion_id)} />
                ))}
              </ul>
            )}
          </Section>

          <Section title="Verdicts">
            {item.verdicts.length === 0 ? (
              <p className="empty">the store records no verdict for this item</p>
            ) : (
              <div style={{ overflowX: "auto" }}>
                <table aria-label="Verdicts" className="verdicts__table">
                  <thead>
                    <tr>{["Criterion", "Verdict", "Actor", "Actor kind", "Claim class", "Time"].map(label => (
                      <th scope="col" key={label}>{label}</th>
                    ))}</tr>
                  </thead>
                  <tbody>
                    {item.verdicts.map((verdict, index) => (
                      <tr key={index}>
                        <td>{verdict.criterion_id}</td>
                        <td>{verdict.verdict}</td>
                        <td>{verdict.actor.trim() || "not recorded"}</td>
                        <td>{verdict.actor_kind}</td>
                        <td>{verdict.claim_class}</td>
                        <td><time dateTime={verdict.recorded_at}>{verdict.recorded_at}</time></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Section>

          <Section title="Runs">
            {latestRun ? (
              <ul className="timeline-list">
                <li>
                  <button
                    type="button"
                    className="btn btn--quiet"
                    onClick={() => onSelectRun(latestRun.run_id)}
                  >
                    <code>{latestRun.run_id}</code>
                  </button>
                  <span className="pane__count">
                    {latestRun.state} · lane {latestRun.lane_id || "unrecorded"}
                    {latestRun.seat ? ` · seat ${latestRun.seat}` : ""} ·{" "}
                    {relativeTime(latestRun.started_at_ms)}
                  </span>
                </li>
              </ul>
            ) : (
              <p className="empty">
                the store records no run for this item — a run appears when a lane picks it up
              </p>
            )}
            {item.decisions.length > 0 ? (
              <ul className="timeline-list">
                {item.decisions.slice(-6).map((decision) => (
                  <li key={decision.decision_id}>
                    <EpistemicBadge state="observed" />
                    <span>
                      {decision.action} — {decision.reason || "no reason recorded"}
                    </span>
                    <span className="pane__count">{relativeTime(decision.dispatched_at_ms)}</span>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="empty">
                no tick decision is recorded for this item{" "}
                <EpistemicBadge state="absent" />
              </p>
            )}
          </Section>

          <Section title="Workflow">
            {detail.selected_workflow ? (
              <><code>{detail.selected_workflow}</code><p>Source: {detail.workflow_source || "not recorded"}</p></>
            ) : <p className="empty">Workflow selection not recorded</p>}
          </Section>
          {item?.comments && <CommentThreads data={item.comments} />}
          <Section title="Design document" note={detail.design_doc || "none named"}>
            <DocumentPane {...doc} />
          </Section>
        </article>
      </div>
    </main>
  );
}
