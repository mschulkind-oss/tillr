// The board's one place for the questions waiting on the owner (design §8, ledger
// `OQ-OBD2`): every item whose design document still asks something, with the item, its
// question ids, and a link into Vantage built as `<base>/#/<path>#<OQ-id>`.
//
// It is a view rather than a fifth pane: the board's four regions are unchanged, and this
// is the list the header's Questions control opens. The list is the server's answer to
// `GET /api/v1/questions`, which reads Vantage's index fresh per request — so an answer
// written in Vantage leaves the list on the next read rather than after a restart. The
// board never requests the configured base URL: it is a link target, not a service this
// screen waits on.

import { RefusalState, refusalCode } from "./RefusalState";
import { StateGlyph } from "./StateGlyph";
import { stateLabel } from "../models/board";
import { vantageLink } from "../models/document";
import type { ApiEnvelope, ItemView, QuestionItem, QuestionsData } from "../models/types";

export interface QuestionsViewProps {
  data: ApiEnvelope<QuestionsData> | undefined;
  error: Error | null;
  /** The first read has not answered yet: the list is still being read. */
  pending: boolean;
  /** A read is in flight over a list already on screen. */
  refreshing: boolean;
  onRetry: () => void;
  onClose: () => void;
  /** The configured Vantage base URL, or "" when the operator configured none. The links
   * are drawn either way; without a base they are inert and one line says so. */
  baseUrl: string;
  /** The snapshot's rows, so an item's state is the same projection the rail draws. */
  items: ItemView[];
  onSelectItem: (itemId: string) => void;
}

/** QuestionLink is one question's door into Vantage. The href is built from the same
 * resolver the document pane uses, and an unconfigured base draws the same words in the
 * same place as an inert span: the reader learns what a configured board would offer, and
 * the one line above says why this one does not go anywhere. */
function QuestionLink({
  baseUrl,
  doc,
  questionId,
}: {
  baseUrl: string;
  doc: string;
  questionId: string;
}) {
  const href = vantageLink(baseUrl, doc, questionId);
  if (baseUrl.trim() === "") {
    return (
      <span
        className="question__link question__link--inert"
        aria-disabled="true"
        data-href={href}
        title={`${href} — no Vantage base URL is configured, so this link goes nowhere`}
      >
        {questionId}
      </span>
    );
  }
  return (
    <a
      className="question__link"
      href={href}
      data-href={href}
      target="_blank"
      rel="noreferrer"
      title={`open ${questionId} in Vantage: ${href}`}
    >
      {questionId}
    </a>
  );
}

/** QuestionItemRow is one item and every question its document still asks. The state is
 * the snapshot's projection when the board has the row, and the route's raw state when it
 * does not — never a state this pane invents. */
function QuestionItemRow({
  item,
  projectedState,
  baseUrl,
  onSelectItem,
}: {
  item: QuestionItem;
  projectedState: string | null;
  baseUrl: string;
  onSelectItem: (itemId: string) => void;
}) {
  const state = projectedState ?? item.state;
  return (
    <li className="question-item" data-state={state}>
      <div className="question-item__head">
        <StateGlyph state={state} />
        <span className="question-item__state">{stateLabel(state)}</span>
        {item.hold ? <span className="question-item__hold">{item.hold}</span> : null}
        <button
          type="button"
          className="question-item__title"
          onClick={() => onSelectItem(item.item_id)}
          title="open this item on the board"
        >
          {item.title}
        </button>
        <code className="question-item__id">{item.item_id}</code>
        <code className="question-item__doc" title={item.design_doc}>
          {item.design_doc}
        </code>
      </div>
      <ul className="question-list">
        {item.questions.map((question) => (
          <li className="question" key={question.question_id}>
            <QuestionLink
              baseUrl={baseUrl}
              doc={item.design_doc}
              questionId={question.question_id}
            />
            <span className="question__text">{question.text}</span>
            {question.preference ? (
              <span className="question__tag" title="the design states a preference">
                preference
              </span>
            ) : null}
            {question.leaning ? (
              <span className="question__leaning">leaning: {question.leaning}</span>
            ) : null}
          </li>
        ))}
      </ul>
    </li>
  );
}

/** QuestionsView is the list, the header's count and the two answers that are not the
 * list: an unread index and a document the tree does not hold. Neither is drawn as "no
 * questions", because a finished design and an unreadable one lead to different work. */
export function QuestionsView({
  data,
  error,
  pending,
  refreshing,
  onRetry,
  onClose,
  baseUrl,
  items,
  onSelectItem,
}: QuestionsViewProps) {
  const stateOf = (itemId: string): string | null =>
    items.find((row) => row.item_id === itemId)?.projected_state.state ?? null;

  const header = (
    <div className="questions__head">
      <div>
        <h2 className="questions__title">Questions waiting on you</h2>
        <p className="questions__sub">
          {data
            ? `${data.data.counts.items} ${data.data.counts.items === 1 ? "item" : "items"} · ${data.data.counts.questions} ${
                data.data.counts.questions === 1 ? "question" : "questions"
              }${
                data.data.counts.holds !== data.data.counts.items
                  ? ` · the planner holds ${data.data.counts.holds}`
                  : ""
              }`
            : "reading Vantage's index…"}
        </p>
      </div>
      <div className="questions__actions">
        <button type="button" className="btn" onClick={onRetry} disabled={refreshing}>
          {refreshing ? "Refreshing…" : "Refresh"}
        </button>
        <button type="button" className="btn btn--quiet" onClick={onClose}>
          Back to the board
        </button>
      </div>
    </div>
  );

  if (error) {
    return (
      <section className="questions" aria-label="Questions waiting on you">
        {header}
        <RefusalState
          code={refusalCode(error)}
          message={error.message}
          sentences={{}}
          onRetry={onRetry}
        />
      </section>
    );
  }

  if (pending && !data) {
    return (
      <section className="questions" aria-label="Questions waiting on you">
        {header}
        <div className="skeleton" aria-label="reading Vantage's question index">
          <span className="skeleton__line skeleton__line--title" />
          <span className="skeleton__line" />
          <span className="skeleton__line" style={{ width: "86%" }} />
        </div>
      </section>
    );
  }

  if (!data) return null;
  const board = data.data;
  const configured = baseUrl.trim() !== "";

  return (
    <section className="questions" aria-label="Questions waiting on you">
      {header}

      {configured ? null : (
        <p className="questions__note questions__note--inert">
          no Vantage base URL is configured, so these links point at the questions they
          name and go nowhere
        </p>
      )}

      {board.state === "questions-unknown" ? (
        <div className={`notice ${board.items.length === 0 ? "notice--blocked" : "notice--working"}`} role="status">
          <strong>The question index could not be read.</strong>
          <span>
            {board.note ??
              "the tool that answers it is absent, failed, or timed out, so an empty list is not a finished design"}
          </span>
        </div>
      ) : null}

      {board.items.length === 0 && board.state === "questions-ok" ? (
        <p className="questions__empty">
          No design document carries an unanswered question.
        </p>
      ) : (
        <ul className="questions__list">
          {board.items.map((item) => (
            <QuestionItemRow
              key={item.item_id}
              item={item}
              projectedState={stateOf(item.item_id)}
              baseUrl={baseUrl}
              onSelectItem={onSelectItem}
            />
          ))}
        </ul>
      )}

      {board.unknown.length > 0 ? (
        <div className="questions__group">
          <h3 className="questions__group-title">
            Questions unknown — the index could not answer for these
          </h3>
          <ul className="questions__plain">
            {board.unknown.map((item) => (
              <li key={item.item_id}>
                <code>{item.item_id}</code> — {item.title}
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      {board.missing.length > 0 ? (
        <div className="questions__group">
          <h3 className="questions__group-title">
            Design documents the store names but the tree does not hold
          </h3>
          <ul className="questions__plain">
            {board.missing.map((item) => (
              <li key={item.item_id}>
                <code>{item.item_id}</code> — <code>{item.design_doc}</code>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </section>
  );
}
