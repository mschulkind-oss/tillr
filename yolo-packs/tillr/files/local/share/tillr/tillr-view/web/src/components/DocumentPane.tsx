// The selected item's design document, as metadata and a link — never as prose (design
// §8, ledger `OQ-OBD2`). The document belongs to the tool that owns the directive dialect,
// the question cards and the answer flow: read it there, where the answers are written.
// A second renderer here would be a second thing to keep in step with the first, which is
// the failure the shared pipeline exists to prevent.
//
// So the pane shows four facts and nothing else: the frontmatter card, the count of open
// questions Vantage's index reported, the document's path, and a link into Vantage. The
// count is a count — the questions themselves are not rendered. An index that could not
// answer is said in words, never drawn as zero: "unknown" and "none open" dispatch
// differently, and a zero would erase the difference (design §8.3).

import { RefusalState, refusalCode } from "./RefusalState";
import type { RefusalSentence } from "./RefusalState";
import { vantageLink } from "../models/document";
import type { DocData, DocumentFrontmatter } from "../models/types";

export interface DocumentPaneProps {
  doc: DocData | undefined;
  error: Error | null;
  pending: boolean;
  onRetry: () => void;
  /** The Vantage base URL the response cursor carried, or "" when the operator configured
   * none. The link is drawn either way; without a base it is inert and says so. */
  baseUrl: string;
}

/** The four sentences the document route's refusals earn, keyed by the server's kebab-case
 * codes (`apierror.go` in tools/tillr-view) — not the snake_case spelling the first draft of
 * this client guessed. A key on the wrong spelling does not fail to compile; it loses the
 * sentence to the shared component's unknown-code branch, which still names the code.
 *
 * A code that is not here is not a fifth sentence: `RefusalState` prints `the face refused:
 * <code>` with the server's message, and the code is visible instead of being folded into
 * "unavailable" (design §5.2, ledger `D-OBS13`). */
const DOC_REFUSALS: Record<string, RefusalSentence> = {
  "no-design-doc": {
    title: "This item names no design document",
    body: "The store records no design document for it, so there is no prose to read — the criteria above are the whole commitment.",
    tone: "notice--accent",
    retry: false,
  },
  "design-doc-missing": {
    title: "The design document this item names is not there",
    body: "The file is gone, which is a defect in the store rather than an empty document.",
    tone: "notice--blocked",
    retry: true,
  },
  "design-doc-outside-root": {
    title: "The design document is outside the root this face serves",
    body: "The reference climbs out of the served root, so this face will not read it.",
    tone: "notice--blocked",
    retry: false,
  },
  "design-doc-unreadable": {
    title: "The design document could not be read",
    body: "The file exists and could not be read, which is a defect rather than an empty document.",
    tone: "notice--working",
    retry: true,
  },
};

/** The card's rows and their labels, in the order a reader scans them: what the document is
 * called, where it stands, and what it asks for next. */
const FRONTMATTER_ROWS: Array<[keyof DocumentFrontmatter, string]> = [
  ["title", "Title"],
  ["status", "Status"],
  ["stage", "Stage"],
  ["next", "Next"],
  ["updated", "Updated"],
];

/** FrontmatterCard is the one glance that separates a draft from a ruling. A key the
 * document does not declare says so: an absent `stage` is a document with no stage, not a
 * card that forgot to draw one (design §5.2's rule, one field at a time). */
function FrontmatterCard({ frontmatter }: { frontmatter: DocumentFrontmatter }) {
  return (
    <dl className="doc__card">
      {FRONTMATTER_ROWS.map(([key, label]) => {
        const value = (frontmatter[key] ?? "").trim();
        return (
          <div className="doc__fact" key={key}>
            <dt className="doc__fact-label">{label}</dt>
            <dd className={value === "" ? "doc__fact-value doc__fact-value--absent" : "doc__fact-value"}>
              {value === "" ? "not recorded" : value}
            </dd>
          </div>
        );
      })}
    </dl>
  );
}

/** questionLine is the server's answer as a sentence. `null` is the two states that are not
 * a count — an index that could not be read, and a document that is not there — and each
 * says which one it is. Drawing either as "0 open questions" is the mistake the server
 * sends a null to prevent. */
function questionLine(count: number | null, state: DocData["questions_state"]): string {
  if (state === "design-doc-missing") return "the design document is not there";
  if (count === null) return "question count unknown: the index could not be read";
  return count === 1 ? "1 open question" : `${count} open questions`;
}

export function DocumentPane({ doc, error, pending, onRetry, baseUrl }: DocumentPaneProps) {
  if (pending) {
    return (
      <div className="skeleton" aria-label="reading the design document">
        <span className="skeleton__line skeleton__line--title" />
        <span className="skeleton__line" />
        <span className="skeleton__line" style={{ width: "92%" }} />
        <span className="skeleton__line" style={{ width: "78%" }} />
      </div>
    );
  }
  if (error) {
    return (
      <RefusalState
        code={refusalCode(error)}
        message={error.message}
        sentences={DOC_REFUSALS}
        onRetry={onRetry}
      />
    );
  }
  if (!doc) return null;
  const configured = baseUrl.trim() !== "";
  const link = vantageLink(baseUrl, doc.path, doc.anchor);
  return (
    <div className="doc">
      <div className="doc__bar">
        <span className="doc__path" title={doc.path}>
          {doc.path}
        </span>
        <span>· {doc.bytes.toLocaleString()} bytes</span>
      </div>

      {!doc.frontmatter ? (
        <p className="empty">
          this document opens with no frontmatter, so there is no metadata card to show
        </p>
      ) : (
        <FrontmatterCard frontmatter={doc.frontmatter} />
      )}

      <p className="doc__count">{questionLine(doc.open_questions, doc.questions_state)}</p>

      <p className="doc__link-line">
        {configured ? (
          <a className="doc__link" href={link} target="_blank" rel="noreferrer">
            Open in Vantage
          </a>
        ) : (
          // Drawn and inert: the same words in the same place, so the reader learns what a
          // configured board would offer, and the one line beside it says why this one
          // does not go anywhere. A hidden link would be the disappearing control this
          // board refuses to ship.
          <span className="doc__link doc__link--inert" aria-disabled="true">
            Open in Vantage
          </span>
        )}
        {configured ? null : (
          <span className="doc__inert-note">
            no Vantage base URL is configured, so this link goes nowhere
          </span>
        )}
      </p>
    </div>
  );
}
