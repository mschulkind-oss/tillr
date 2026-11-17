// The one renderer of a refusal (design §5.2, ledger `D-OBS13`). Every pane that reads a
// route hands its failure here, so a broken read is the same fact on every surface: the
// server's `error.code` and `error.message`, in the pane's own words where the pane has
// them and as `the face refused: <code>` where it does not. An unknown code is a refusal —
// never an empty panel, and never a generic "something went wrong" that throws away the one
// fact the server gave.
//
// The codes are the server's kebab-case vocabulary (`apierror.go` in tools/tillr-view). A
// pane's sentence table is keyed by that spelling; a pane that guesses the spelling does not
// fail to compile, it loses the sentence to the unknown branch.

import type { ReactNode } from "react";

/** One known code's own sentence. `body` is the pane's explanation; when it is absent the
 * server's message is the whole body. `retry` says whether the pane offers a re-read, and
 * defaults to "whatever the call site passed". */
export interface RefusalSentence {
  title: ReactNode;
  body?: ReactNode;
  tone?: "notice--blocked" | "notice--working" | "notice--accent";
  retry?: boolean;
}

export interface RefusalStateProps {
  /** The server's `error.code`, or "" when the failure carried none. */
  code: string;
  /** The server's `error.message`: the one fact an unknown code keeps. */
  message: string;
  /** The pane's own sentences, keyed by code. A code absent here is not a loss. */
  sentences?: Record<string, RefusalSentence>;
  onRetry?: () => void;
  retryLabel?: string;
}

/** refusalCode reads the code off a failure, and "" when the failure carried none: a
 * transport that never reached the server names no code, which is itself a fact. */
export function refusalCode(error: Error): string {
  const code = (error as { code?: unknown }).code;
  return typeof code === "string" ? code : "";
}

export function RefusalState({
  code,
  message,
  sentences = {},
  onRetry,
  retryLabel = "Read it again",
}: RefusalStateProps) {
  if (code === "unknown-route") return <p role="alert" data-refusal={code}>{message}</p>;
  const known = code === "" ? undefined : sentences[code];
  const tone = known?.tone ?? "notice--blocked";
  const retry = known?.retry ?? onRetry !== undefined;
  return (
    <div className={`notice ${tone}`} data-refusal={code === "" ? "uncoded" : code}>
      <h3 className="notice__title">
        {known !== undefined ? (
          known.title
        ) : code === "" ? (
          "the face refused"
        ) : (
          <>
            the face refused: <code>{code}</code>
          </>
        )}
      </h3>
      {known?.body !== undefined ? <p className="notice__body">{known.body}</p> : null}
      <p className="notice__body">
        {code === "" ? (
          message
        ) : (
          <>
            <code>{code}</code> — {message}
          </>
        )}
      </p>
      {retry && onRetry !== undefined ? (
        <div className="notice__actions">
          <button type="button" className="btn" onClick={onRetry}>
            {retryLabel}
          </button>
        </div>
      ) : null}
    </div>
  );
}
