// The run's own transcript: what the agent thought, what it said, and every tool call with
// the arguments it passed and what came back, in the order the run recorded them. It is a
// page and not the log — the tail while the run is live, with a cursor back through the
// rest — because a run's log is megabytes and a pane that tried to hold it would be a pane
// that cannot open.
//
// The entries are the server's: `tool_call` carries the arguments and the matching
// `tool_result` carries what came back, so they are drawn as one block. An entry with no
// clock, or a transcript whose source is a seam, says so through the badge rather than
// looking like an empty run.
//
// The shim's subprocess record is read and reported but not listed while the agent's stream
// is there: a shell command is not a message, and the shim runs on the harness's own
// bootstrap as well as on the agent's tools. The pane says so instead of leaving the reader
// to wonder why the `proc` source is named and its rows are nowhere.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Markdown from "react-markdown";

import { EpistemicBadge } from "./EpistemicBadge";
import { RefusalState, refusalCode } from "./RefusalState";
import type { RefusalSentence } from "./RefusalState";
import { fetchRunTranscript, fetchRunTranscriptEntry } from "../models/api";
import type { TranscriptData, TranscriptEntry } from "../models/types";

/** The transcript route's own refusals. A code that is not here falls through to the shared
 * component's `the face refused: <code>` branch. */
const TRANSCRIPT_REFUSALS: Record<string, RefusalSentence> = {
  "run-not-found": {
    title: "The run is not in the store",
    body: "The store does not carry this run, so there is no transcript to read — a defect in the link rather than an empty log.",
    tone: "notice--blocked",
    retry: true,
  },
  "entry-not-found": {
    title: "The transcript entry is not in the store",
    body: "The page named an entry the store does not carry, so the pane says so rather than showing an empty body.",
    tone: "notice--blocked",
    retry: true,
  },
};

export interface TranscriptViewProps {
  /** The run these entries belong to, or null when the selection names no run. */
  runId: string | null;
  transcript: TranscriptData | undefined;
  /** Why the read failed, when it did: a refusal is a state, not an empty list. */
  error: Error | null;
}

/** How many bytes of one entry's body a collapsed block shows before it asks to be opened.
 * It is a rendering choice, not a bound: the server already bounded the page. */
const PREVIEW_BYTES = 1400;

function clock(ms: number | null): string {
  if (ms === null) return "no clock";
  return new Date(ms).toISOString();
}

function duration(ms: number | undefined): string | null {
  if (ms === undefined) return null;
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
}

/** body is the text of an entry, whichever field its kind keeps it in. */
function identity(entry: TranscriptEntry): string { return entry.id ?? `${entry.source}:${entry.seq}`; }
function mergeEntries(previous: TranscriptEntry[], next: TranscriptEntry[]): TranscriptEntry[] {
  const entries = new Map(previous.map(entry => [identity(entry), entry]));
  for (const entry of next) entries.set(identity(entry), entry);
  return [...entries.values()].sort((a,b) => (a.at_ms ?? 0) - (b.at_ms ?? 0) || a.seq - b.seq);
}

function body(entry: TranscriptEntry): string {
  if (entry.kind === "tool_call") return entry.tool?.arguments ?? "";
  return entry.text ?? "";
}

/** isCollapsible answers whether an entry's body is long enough to hide behind a control,
 * which is the entry's own size rather than its kind. */
function isCollapsible(entry: TranscriptEntry): boolean {
  return body(entry).length > PREVIEW_BYTES;
}

function EntryBody({ text, markdown = false }: { text: string; markdown?: boolean }) {
  return markdown ? <div className="transcript__markdown"><Markdown>{text}</Markdown></div> : <pre className="transcript__body">{text}</pre>;
}

interface CallProps {
  entry: TranscriptEntry;
  result: TranscriptEntry | undefined;
  /** A body the operator asked to see whole, keyed by its source identity. */
  expanded: Record<string, TranscriptEntry>;
  onExpand: (entry: TranscriptEntry) => void;
  /** True for a call the run recorded and never answered. */
  unanswered: boolean;
}

function ToolCallBlock({ entry, result, expanded, onExpand, unanswered }: CallProps) {
  const whole = body(expanded[identity(entry)] ?? entry);
  const text = whole;
  const failed = result?.is_error === true || (result?.exit_code ?? 0) !== 0;
  return (
    <details className={failed ? "transcript-entry transcript-entry--failed" : "transcript-entry"}>
      <summary className="transcript-entry__head">
        <span className="transcript-entry__kind">tool</span>
        <strong className="transcript-entry__name">{entry.tool?.name ?? "a tool"}</strong>
        <span className="transcript-entry__clock">{clock(entry.at_ms)}</span>
        {result?.duration_ms !== undefined ? (
          <span className="transcript-entry__meta">{duration(result.duration_ms)}</span>
        ) : null}
        {failed ? <span className="transcript-entry__meta">failed</span> : null}
        <span className="transcript-entry__command">{whole.split("\n")[0].slice(0, 120)}</span>
      </summary>
      <pre className="transcript__body transcript__body--args">{text}</pre>
      {entry.truncated && !expanded[identity(entry)] ? (
        <button type="button" className="btn btn--tiny" onClick={() => onExpand(entry)}>
          read all {entry.bytes} bytes
        </button>
      ) : null}
      {unanswered ? (
        <p className="transcript__note">
          no result recorded yet — the tool may still be running, or the run ended before it answered
        </p>
      ) : (
        <div className="transcript-entry__result">
          <span className="transcript-entry__kind">result</span>
          {result === undefined ? (
            <p className="transcript__note">this page does not hold the call&apos;s result</p>
          ) : result.truncated && !expanded[identity(result)] ? (
            <>
              <pre className="transcript__body">{`${(result.text ?? "").slice(0, PREVIEW_BYTES)}…`}</pre>
              <button type="button" className="btn btn--tiny" onClick={() => onExpand(result)}>
                read all {result.bytes} bytes
              </button>
            </>
          ) : (
            <pre className="transcript__body">{body(expanded[identity(result)] ?? result)}</pre>
          )}
        </div>
      )}
    </details>
  );
}

export function TranscriptView(props: TranscriptViewProps) {
  return <TranscriptSession key={props.runId} {...props} />;
}

function TranscriptSession({ runId, transcript, error }: TranscriptViewProps) {
  // The pages loaded before the tail, oldest first, and the cursor the next one is asked
  // for. `undefined` means "not asked yet": the tail's own before cursor is the first.
  const [earlier, setEarlier] = useState<TranscriptEntry[]>([]);
  const [following, setFollowing] = useState(true);
  const [visible, setVisible] = useState(transcript);
  const scroller = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (following && transcript) setVisible(previous => ({...transcript, entries: mergeEntries(previous?.entries ?? [], transcript.entries)}));
  }, [following, transcript]);
  useEffect(() => {
    if (following && scroller.current) scroller.current.scrollTop = scroller.current.scrollHeight;
  }, [following, visible]);
  const visibleIDs = new Set((visible?.entries ?? []).map(identity));
  const newEntries = (transcript?.entries ?? []).filter(entry => !visibleIDs.has(identity(entry))).length;
  const [cursor, setCursor] = useState<string | null | undefined>(undefined);
  const [loading, setLoading] = useState(false);
  // A page or an expansion the server refused keeps the failure, not only its message:
  // the code is the fact the pane prints (`RefusalState`), and a string would lose it.
  const [pageError, setPageError] = useState<Error | null>(null);
  const [expanded, setExpanded] = useState<Record<string, TranscriptEntry>>({});

  const before = cursor === undefined ? (visible?.before ?? null) : cursor;
  const loadEarlier = useCallback(async () => {
    if (runId === null || before === null) return;
    setFollowing(false);
    setLoading(true);
    setPageError(null);
    try {
      const page = await fetchRunTranscript(runId, before);
      setEarlier((previous) => [...page.data.entries, ...previous]);
      setCursor(page.data.before);
    } catch (failure) {
      setPageError(failure instanceof Error ? failure : new Error(String(failure)));
    } finally {
      setLoading(false);
    }
  }, [before, runId]);

  const expand = useCallback(
    async (entry: TranscriptEntry) => {
      if (runId === null) return;
      try {
        const answer = await fetchRunTranscriptEntry(runId, entry.seq, undefined, entry.id);
        const whole = answer.data.entries[0];
        if (whole && identity(whole) === identity(entry)) setExpanded((previous) => ({ ...previous, [identity(entry)]: whole }));
        else throw new Error("The transcript expansion did not return the requested identity.");
      } catch (failure) {
        setPageError(failure instanceof Error ? failure : new Error(String(failure)));
      }
    },
    [runId],
  );

  const entries = useMemo(
    () => {
      return mergeEntries(earlier, visible?.entries ?? []);
    },
    [earlier, visible],
  );

  // The results are drawn under their calls, so a result is looked up rather than repeated:
  // the stream records a call and its answer as two messages and the pane draws them as one.
  const results = useMemo(() => {
    const byCall = new Map<string, TranscriptEntry>();
    for (const entry of entries) {
      if (entry.kind === "tool_result" && entry.tool_call_id) {
        byCall.set(entry.tool_call_id, entry);
      }
    }
    return byCall;
  }, [entries]);
  const answered = useMemo(() => {
    const calls = new Set<string>();
    for (const entry of entries) {
      if (entry.kind === "tool_call" && entry.tool_call_id) calls.add(entry.tool_call_id);
    }
    return calls;
  }, [entries]);

  if (error) {
    return (
      <section className="transcript">
        <RefusalState
          code={refusalCode(error)}
          message={error.message}
          sentences={TRANSCRIPT_REFUSALS}
        />
        <EpistemicBadge state="unreadable" />
      </section>
    );
  }
  if (runId === null) {
    return (
      <section className="transcript">
        <p className="empty">No run is selected, so there is no transcript to read.</p>
      </section>
    );
  }
  if (!transcript) {
    return <section className="transcript">Reading the run&apos;s own record…</section>;
  }

  const seam = transcript.epistemic_state !== "observed";
  return (
    <section className="transcript">
      <div className="transcript__head">
        <span>
          the newest {transcript.entries.length} of the run&apos;s entries, oldest first
        </span>
        <EpistemicBadge state={transcript.epistemic_state} />
      </div>
      {transcript.note ? <p className="transcript__note">{transcript.note}</p> : null}
      {seam ? (
        <p className="transcript__note">
          the record is a seam, not an empty run: what is missing is named above, and what
          follows is the part of the run&apos;s own record that survives.
        </p>
      ) : null}
      {before !== null ? (
        <button type="button" className="btn btn--tiny" onClick={() => void loadEarlier()} disabled={loading}>
          {loading ? "reading…" : "load earlier entries"}
        </button>
      ) : null}
      {pageError ? (
        <RefusalState
          code={refusalCode(pageError)}
          message={pageError.message}
          sentences={TRANSCRIPT_REFUSALS}
        />
      ) : null}
      <button type="button" className="btn btn--tiny" onClick={() => setFollowing(value => !value)} aria-pressed={following}>{following ? "Following" : "Follow paused"}</button>
      {!following ? <button type="button" className="btn btn--tiny" onClick={() => setFollowing(true)}>{newEntries} new entries · resume follow</button> : null}
      <div className="transcript__entries" ref={scroller} onScroll={event => {
        const pane = event.currentTarget;
        if (pane.scrollHeight - pane.scrollTop - pane.clientHeight > 32) setFollowing(false);
      }}>
      {entries.length === 0 ? (
        <p className="empty">the run recorded no entry this pane can read</p>
      ) : null}
      {entries.map((entry) => {
        if (entry.kind === "tool_result") {
          // drawn under its call; a result whose call is on a page not loaded stands alone
          if (entry.tool_call_id && answered.has(entry.tool_call_id)) return null;
          return (
            <article key={identity(entry)} className="transcript-entry">
              <header className="transcript-entry__head">
                <span className="transcript-entry__kind">result</span>
                <strong className="transcript-entry__name">{entry.role ?? "a tool"}</strong>
                <span className="transcript-entry__clock">{clock(entry.at_ms)}</span>
              </header>
              <EntryBody text={body(entry)} />
            </article>
          );
        }
        if (entry.kind === "tool_call") {
          return (
            <ToolCallBlock
              key={identity(entry)}
              entry={entry}
              result={entry.tool_call_id ? results.get(entry.tool_call_id) : undefined}
              expanded={expanded}
              onExpand={(target) => void expand(target)}
              unanswered={entry.tool_call_id !== undefined && !results.has(entry.tool_call_id)}
            />
          );
        }
        if (entry.kind === "proc") {
          return (
            <article key={identity(entry)} className="transcript-proc">
              <span className="transcript-proc__clock">{clock(entry.at_ms)}</span>
              <code className="transcript-proc__cmd">{entry.text}</code>
              {entry.exit_code !== undefined ? (
                <span className={entry.exit_code === 0 ? "transcript-proc__ok" : "transcript-proc__fail"}>
                  exit {entry.exit_code}
                </span>
              ) : null}
            </article>
          );
        }
        if (entry.kind === "notice" || entry.kind === "step") {
          return (
            <article key={identity(entry)} className="transcript-notice">
              <span className="transcript-entry__kind">{entry.kind}</span>
              <span>{entry.text}</span>
              <span className="transcript-entry__clock">{clock(entry.at_ms)}</span>
              <EpistemicBadge state={entry.epistemic_state} />
            </article>
          );
        }
        const collapsible = isCollapsible(entry);
        return (
          <article
            key={identity(entry)}
            className={
              entry.kind === "thinking"
                ? "transcript-entry transcript-entry--thinking"
                : "transcript-entry"
            }
          >
            <header className="transcript-entry__head">
              <span className="transcript-entry__kind">{entry.kind}</span>
              {entry.role ? <strong className="transcript-entry__name">{entry.role}</strong> : null}
              <span className="transcript-entry__clock">{clock(entry.at_ms)}</span>
              {entry.epistemic_state !== "observed" ? (
                <EpistemicBadge state={entry.epistemic_state} />
              ) : null}
            </header>
            {collapsible && !expanded[identity(entry)] && !entry.truncated ? (
              <>
                <EntryBody text={`${body(entry).slice(0, PREVIEW_BYTES)}…`} markdown={entry.role === "assistant" && entry.kind === "message"} />
                <button
                  type="button"
                  className="btn btn--tiny"
                  onClick={() => void expand(entry)}
                >
                  read all {entry.bytes} bytes
                </button>
              </>
            ) : (
              <EntryBody text={body(expanded[identity(entry)] ?? entry)} markdown={entry.role === "assistant" && entry.kind === "message"} />
            )}
            {entry.truncated && !expanded[identity(entry)] ? (
              <button type="button" className="btn btn--tiny" onClick={() => void expand(entry)}>
                read all {entry.bytes} bytes
              </button>
            ) : null}
          </article>
        );
      })}
      </div>
      <p className="transcript__note">
        The shim&apos;s record of every shell command is read and not listed: a shell command is
        not a message. It becomes the transcript when the agent&apos;s own stream is missing.
      </p>
      <p className="transcript__sources">
        {(["log", "steps", "proc"] as const).map((name) => (
          <span key={name} className={`transcript__source transcript__source--${transcript.sources[name].state}`}>
            {name}: {transcript.sources[name].state}
            {transcript.sources[name].note ? ` (${transcript.sources[name].note})` : ""}
          </span>
        ))}
      </p>
    </section>
  );
}
