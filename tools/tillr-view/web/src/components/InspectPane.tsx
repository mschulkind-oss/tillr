// The inspector: the selected run's pipeline and claims, then its causal record — the
// agent's own transcript, the inter-tool exchanges, or the timeline waterfall — and the
// payload of whatever exchange or artifact is open. An item with no run gets an explanation
// of what a run is and which record said there is none, because an unexplained empty pane is
// the failure this observatory exists to prevent.

import { ClaimAuditBanner } from "./ClaimAuditBanner";
import { SpanViewer } from "./SpanViewer";
import { assembleSpans } from "../models/spans";
import { InterToolExchangeView } from "./InterToolExchangeView";
import { ItemWithoutRun } from "./ItemWithoutRun";
import { PayloadInspectorDrawer } from "./PayloadInspectorDrawer";
import { RefusalState, refusalCode } from "./RefusalState";
import type { RefusalSentence } from "./RefusalState";

import { TranscriptView } from "./TranscriptView";
import { RunUsageFigures } from "./Usage";
import type {
  ArtifactData,
  Exchange,
  ExchangesData,
  GateData,
  ItemData,
  RunData,
  RunSummary,
  TimelineData,
  TranscriptData,
} from "../models/types";
import type { Tab } from "../models/url";

/** The run route's own refusal. A code that is not here falls through to the shared
 * component's `the face refused: <code>` branch. */
const RUN_REFUSALS: Record<string, RefusalSentence> = {
  "run-not-found": {
    title: "The run is not in the store",
    body: "The selection names a run the store does not carry, which is a defect in the link rather than a run with no record.",
    tone: "notice--blocked",
    retry: true,
  },
};

export interface InspectPaneProps {
  itemId: string | null;
  item: ItemData | undefined;
  /** The store's row for the selected item, which carries the run a selection starts on. */
  latestRun: RunSummary | null;
  onSelectRun: (runId: string) => void;
  runId: string | null;
  run: RunData | undefined;
  /** The run read's refusal: a run selected and not answered is a refusal, never an empty
   * inspector. */
  runError?: Error | null;
  onRetryRun?: () => void;
  tab: Tab;
  onSelectTab: (tab: Tab) => void;
  exchanges: ExchangesData | undefined;
  timeline: TimelineData | undefined;
  gate?: GateData;
  gateError?: Error | null;
  transcript: TranscriptData | undefined;
  transcriptError: Error | null;
  exchangeCount: number;
  spanCount: number;
  selectedExchange: string | null;
  onSelectExchange: (exchangeId: string | null) => void;
  onOpenArtifact: (kind: string) => void;
  exchange: Exchange | null;
  artifact: ArtifactData | undefined;
  artifactKind: string | null;
  artifactError: Error | null;
  onCloseInspector: () => void;
}

export function InspectPane(props: InspectPaneProps) {
  const {
    itemId,
    item,
    latestRun,
    onSelectRun,
    runId,
    run,
    runError,
    onRetryRun,
    tab,
    onSelectTab,
    exchanges,
    timeline,
    gate,
    transcript,
    transcriptError,
    exchangeCount,
    spanCount,
    selectedExchange,
    onSelectExchange,
    exchange,
    artifact,
    artifactKind,
    artifactError,
    onCloseInspector,
  } = props;

  return (
    <aside className="pane pane--inspect inspect" aria-label="Inspector">
      <div className="pane__head">
        <h2 className="pane__title">Inspect</h2>
        <span className="pane__count">
          {runId === null ? "no run selected" : <code>{runId}</code>}
        </span>
      </div>
      <div className="pane__scroll">
        {itemId === null ? (
          <div className="takeaway">
            <div>
              <p className="takeaway__title">Nothing to inspect</p>
              The inspector follows the selection: an item&apos;s runs, and the exchanges and
              payloads inside them.
            </div>
          </div>
        ) : runId !== null && runError ? (
          <RefusalState
            code={refusalCode(runError)}
            message={runError.message}
            sentences={RUN_REFUSALS}
            onRetry={onRetryRun}
          />
        ) : runId === null ? (
          <div className="inspect__section">
            <ItemWithoutRun item={item} hasRun={latestRun !== null} />
            {latestRun ? (
              <button type="button" className="btn" onClick={() => onSelectRun(latestRun.run_id)}>
                inspect run <code>{latestRun.run_id}</code>
              </button>
            ) : null}
          </div>
        ) : (
          <>
            <div className="inspect__section">
              <div className="run-summary">
                <span className={`state-badge state-badge--${run?.run.state ?? "working"}`}>
                  {run?.run.state ?? "reading"}
                </span>
                {run ? (
                  <>
                    <span>
                      exit {run.run.exit_code === null ? "not recorded" : run.run.exit_code}
                    </span>
                    {run.run.seat ? <span>· seat {run.run.seat}</span> : null}
                    <span>· {run.run.backend}</span>
                    <span>· lane {run.run.lane_id || "unrecorded"}</span>
                    <RunUsageFigures usage={run.usage} />
                  </>
                ) : (
                  <span>reading the run…</span>
                )}
              </div>

            </div>
            <div className="inspect__section">
              <ClaimAuditBanner run={run} />
            </div>
            <div className="inspect__section">
              <nav className="tabs" aria-label="Causal record">
                <button
                  type="button"
                  className={tab === "transcript" ? "tab tab--active" : "tab"}
                  aria-pressed={tab === "transcript"}
                  onClick={() => onSelectTab("transcript")}
                >
                  Transcript
                </button>
                <button
                  type="button"
                  className={tab === "exchanges" ? "tab tab--active" : "tab"}
                  aria-pressed={tab === "exchanges"}
                  onClick={() => onSelectTab("exchanges")}
                >
                  Exchanges ({exchangeCount})
                </button>
                <button
                  type="button"
                  className={tab === "timeline" ? "tab tab--active" : "tab"}
                  aria-pressed={tab === "timeline"}
                  onClick={() => onSelectTab("timeline")}
                >
                  Timeline ({spanCount})
                </button>
                <button
                  type="button"
                  className={tab === "gate" ? "tab tab--active" : "tab"}
                  aria-pressed={tab === "gate"}
                  onClick={() => onSelectTab("gate")}
                >
                  Gate ({gate?.spans.length ?? 0})
                </button>
              </nav>
              {tab === "transcript" ? (
                <TranscriptView runId={runId} transcript={transcript} error={transcriptError} />
              ) : tab === "exchanges" ? (
                <InterToolExchangeView
                  runId={runId}
                  exchanges={exchanges}
                  selectedExchange={selectedExchange}
                  onSelectExchange={onSelectExchange}
                />
              ) : run ? (
                <SpanViewer spans={assembleSpans(run, timeline, gate)} />
              ) : <p>Reading the run…</p>}
            </div>
            {exchange || artifactKind || artifactError ? (
              <PayloadInspectorDrawer
                exchange={exchange}
                artifact={artifact}
                artifactKind={artifactKind}
                artifactError={artifactError}
                onClose={onCloseInspector}
              />
            ) : null}
          </>
        )}
      </div>
    </aside>
  );
}
