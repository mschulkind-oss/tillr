import { useMemo, useState, type ReactNode } from "react";
import { navigate } from "../models/route";
import { useItemDocQuery, useItemQuery, useRunArtifactQuery, useRunExchangesQuery, useRunGateQuery, useRunQuery, useRunTimelineQuery, useRunTranscriptQuery } from "../models/queries";
import type { DispatchView, EarlierFindingFateView, ItemData, ItemView, LinkedAlertView, SplitCallView, TimelineData, VerdictComparisonView, VerdictExaminedView, VerdictFindingView, VerifyRefusalView } from "../models/types";
import { alertGlyphState, alertsFilterUrl } from "./AlertsPage";
import { assembleSpans } from "../models/spans";
import { ItemDetailPane } from "./ItemDetailPane";
import { EntityLink } from "./EntityLink";
import { SpanViewer } from "./SpanViewer";
import { TranscriptView } from "./TranscriptView";
import { InterToolExchangeView } from "./InterToolExchangeView";
import { ClaimAuditBanner } from "./ClaimAuditBanner";
import { PayloadInspectorDrawer } from "./PayloadInspectorDrawer";
import { RefusalState, refusalCode } from "./RefusalState";
import { RunUsageFigures, UsageSummary } from "./Usage";
import { DependencyLinks } from "./DependencyLinks";
import { PreservedWork } from "./PreservedWork";
import { ItemGraph } from "./ItemGraph";
import { DrainScheduleLedger, TimeOrderStream, WallTimeCard } from "./RunTiming";

export function foldedDecisions(decisions: DispatchView[]) {
  const folds: { decision: DispatchView; count: number; since: number }[] = [];
  for (const decision of [...decisions].sort((a,b) => b.dispatched_at_ms - a.dispatched_at_ms)) {
    const previous = folds.at(-1);
    if (previous && previous.decision.action === decision.action && previous.decision.reason === decision.reason) { previous.count++; previous.since = decision.dispatched_at_ms; }
    else folds.push({ decision, count: 1, since: decision.dispatched_at_ms });
  }
  return folds;
}
function NextStep({ refusal }: { refusal: VerifyRefusalView }) {
  if (!refusal.next_step) return <>No next step started yet</>;
  return <>Next step: {refusal.next_step}{refusal.next_run_id ? <>{" · run "}<EntityLink kind="run" id={refusal.next_run_id} /></> : null}</>;
}
function signedDelta(n: number): string {
  return n > 0 ? `+${n}` : `${n}`;
}

function ComparisonSummary({ comparison }: { comparison?: VerdictComparisonView }) {
  if (!comparison) return null;
  const hasDelta = comparison.files_delta !== undefined || comparison.added_lines_delta !== undefined;
  const labels: string[] = [];
  if (comparison.new_finding_after_fix) labels.push("new finding after fix (S2 advisory)");
  if (comparison.repair_growing) labels.push("repair growing (S3 advisory)");
  if (comparison.has_unexamined_paths) labels.push("unexamined paths (S4 advisory)");
  if (!comparison.previous_run_id && !hasDelta && !labels.length) return null;
  return <div aria-label="Verdict comparison">
    Comparison{comparison.previous_run_id ? <> vs <EntityLink kind="run" id={comparison.previous_run_id} /></> : null}
    {hasDelta ? <>: {comparison.files_delta !== undefined ? `${signedDelta(comparison.files_delta)} files` : null}{comparison.files_delta !== undefined && comparison.added_lines_delta !== undefined ? ", " : null}{comparison.added_lines_delta !== undefined ? `${signedDelta(comparison.added_lines_delta)} lines` : null}</> : null}
    {labels.length ? <> · {labels.join(" · ")}</> : null}
  </div>;
}

function ExaminedSummary({ examined }: { examined?: VerdictExaminedView }) {
  if (!examined) return null;
  if (examined.whole) return <div>Examined: whole candidate</div>;
  return <div>Unexamined paths: {(examined.unexamined_paths ?? []).map((p, i) => <span key={i}>{i > 0 ? ", " : null}<code>{p}</code></span>)}</div>;
}

function EarlierFindingFateList({ fates }: { fates?: EarlierFindingFateView[] }) {
  if (!fates?.length) return null;
  return <ul aria-label="Earlier finding fates">
    {fates.map((fate, index) => <li key={index}>
      {fate.criterion_id ? `${fate.criterion_id} · ` : null}<strong>{fate.fate === "fixed" ? "Fixed" : "Not fixed"}</strong>{": "}<span>{fate.finding}</span>
      {fate.paths?.length ? <>{" · "}{fate.paths.map((p, i) => <span key={i}>{i > 0 ? ", " : null}<code>{p}</code></span>)}</> : null}
    </li>)}
  </ul>;
}

function SplitCallSummary({ call }: { call?: SplitCallView }) {
  if (!call) return null;
  return <div className="split-call-summary">
    <div>
      Split call by {call.caller} · status: {call.status}
      {call.status === "pending" ? " · waiting on the design seat" : null}
      {call.design_run_id ? <>{" · design run "}<EntityLink kind="run" id={call.design_run_id} /></> : null}
    </div>
    {call.donor_ref || call.donor_sha ? <div>Donor: {call.donor_ref || "ref not recorded"}{call.donor_sha ? ` (${call.donor_sha})` : ""}</div> : null}
    {call.signals.length > 0 ? <ul>
      {call.signals.map((sig, i) => <li key={`${sig.signal}-${i}`}>
        {sig.signal}: {sig.files ?? 0} files (limit {sig.file_limit ?? 0}) · {sig.added_lines ?? 0} added lines (limit {sig.added_line_limit ?? 0})
      </li>)}
    </ul> : null}
    {call.status === "no-split" || call.no_split_reason ? <div>No split: {call.no_split_reason || "reason not recorded"}</div> : null}
    {call.last_rejection_reason ? <div>Last split rejection: {call.last_rejection_reason}</div> : null}
  </div>;
}
export function VerifyRefusals({ refusals }: { refusals: VerifyRefusalView[] }) {
  if (!refusals.length) return null;
  return <section aria-label="Verify refusals"><h2>Verify refusals</h2><ol>
    {refusals.map(r => <li key={r.event_seq}>
      Verify run <EntityLink kind="run" id={r.run_id} />{r.attempt !== null ? ` · attempt ${r.attempt}` : ""} · <time>{new Date(r.at_ms).toISOString()}</time> · <NextStep refusal={r} />
      {r.replan ? <> · replan: the findings said the plan was wrong</> : null}
      {r.candidate_size ? <div>Candidate size: {r.candidate_size.files} files (limit {r.candidate_size.file_limit}) · {r.candidate_size.added_lines} added lines (limit {r.candidate_size.added_line_limit})</div> : null}
      <ComparisonSummary comparison={r.verdict_comparison} />
      <ExaminedSummary examined={r.examined} />
      <SplitCallSummary call={r.split_call} />
      <pre className="refusal-detail">{r.detail}</pre>
      <FindingList findings={r.findings} />
      <EarlierFindingFateList fates={r.earlier_finding_fates} />
    </li>)}
  </ol></section>;
}
const findingKindLabel: Record<VerdictFindingView["kind"], string> = {
  gap: "Gap",
  regression: "Regression",
  "unasked-change": "Unasked change",
};

function FindingList({ findings }: { findings?: (VerdictFindingView & { criterion?: string })[] }) {
  if (!findings?.length) return null;
  return <ul aria-label="Findings">
    {findings.map((finding, index) => <li key={index}>
      {finding.criterion ? `${finding.criterion} · ` : null}<strong>{findingKindLabel[finding.kind] ?? finding.kind}</strong>{": "}<span>{finding.case}</span>{" · "}
      {finding.paths.map((path, i) => <span key={i}>{i > 0 ? ", " : null}<code>{path}</code></span>)}
    </li>)}
  </ul>;
}

export function ItemTimeline({ item }: { item: ItemData }) {
  const history: { at: number; key: string; body: ReactNode }[] = [
    ...(item.graph_revisions ?? []).map(revision => ({ at: revision.recorded_at_ms, key: `graph-${revision.revision_id}`, body: <>
      {revision.rule ? `Graph proposal refused by ${revision.rule}: ${revision.detail}` : `Graph changed by ${revision.proposing_node}: ${revision.before_nodes} → ${revision.after_nodes} nodes`}
      {" · target "}{revision.target}{" · workflow "}{revision.workflow}{" · proposing run "}<EntityLink kind="run" id={revision.proposing_run} />
    </> })),
    ...item.runs.map(run => ({ at: run.started_at_ms, key: `run-${run.run_id}`, body: <>
      Run <EntityLink kind="run" id={run.run_id} />{run.seat ? ` · seat ${run.seat}` : ""} · {run.backend} · {run.end_cause ?? "end cause not recorded"} · {run.state} · exit {run.exit_code ?? "not recorded"}
      {" · finish "}{run.finished_at_ms ? new Date(run.finished_at_ms).toISOString() : "not recorded"}
      {" · "}{run.usage_tokens === null ? "tokens not recorded" : `${run.usage_tokens} tokens`}
    </> })),
    ...(item.verify_refusals ?? []).map(r => ({ at: r.at_ms, key: `verify-refusal-${r.event_seq}`, body: <>
      Verify refused · run <EntityLink kind="run" id={r.run_id} /> · {r.detail} · <NextStep refusal={r} />
      {r.split_call ? <> · split call ({r.split_call.status}){r.split_call.no_split_reason ? `: no split — ${r.split_call.no_split_reason}` : ""}</> : null}
    </> })),
    ...item.verdicts.map((v, i) => ({ at: Date.parse(v.recorded_at), key: `verdict-${i}`, body: <>
      Verdict {v.criterion_id} · {v.verdict} · {v.reason || "reason not recorded"} · {v.actor || "actor not recorded"} ({v.actor_kind || "kind not recorded"}) · evidence: {v.evidence?.length ? v.evidence.join(", ") : "not recorded"}
      <FindingList findings={v.findings} />
    </> })),
    ...item.owner_decisions.map(d => ({ at: d.decided_at_ms, key: `decision-${d.decision_id}`, body: <>
      Decision {d.decision_id} · {d.text} · {d.decided_by} · {d.status}{d.supersedes ? ` · supersedes ${d.supersedes}` : ""}
    </> })),
    ...[...item.state_moves].sort((a, b) => a.event_seq - b.event_seq).map(m => ({ at: m.at_ms, key: `move-${m.event_seq}`, body: <>
      State {m.from || "not recorded"} → {m.to}{m.action ? ` · ${m.action}` : ""} · {m.reason || "reason not recorded"} · {m.actor || "actor not recorded"}
    </> })),
  ];
  // Stable time sorting retains the source order for tied clocks, including the
  // event log's numeric sequence for state moves.
  history.sort((a, b) => a.at - b.at);
  return <><section aria-label="Item history"><h2>Item history (oldest first)</h2>
    {history.length ? <ol>{history.map(entry => <li key={entry.key}><time>{Number.isFinite(entry.at) ? new Date(entry.at).toISOString() : "time not recorded"}</time> · {entry.body}</li>)}</ol> : <p>No runs, verdicts, owner decisions or state moves recorded.</p>}
  </section><section aria-label="Item timeline"><h2>Pass timeline (newest first)</h2>
    {foldedDecisions(item.decisions).map(({ decision: d, count, since }) => <p key={d.decision_id}><a href={`/passes?item=${encodeURIComponent(item.item.item_id)}`}>{new Date(d.dispatched_at_ms).toISOString()} · {d.action} · {d.reason} · ×{count} pass{count === 1 ? "" : "es"} since {new Date(since).toISOString()}, same answer</a>{d.run_id ? <EntityLink kind="run" id={d.run_id} /> : null}</p>)}
    {!item.decisions.length ? <p>No pass has recorded a decision for this item.</p> : null}
  </section><section aria-label="Item landings"><h2>Landing attempts</h2>
    {item.attempts.flatMap(a => a.landings ?? []).map(l => <p key={l.attempt_id}><EntityLink kind="landing" id={l.attempt_id} /> {l.result} · {l.reason}</p>)}
  </section></>;
}
export function ItemLinkedAlerts({ alerts }: { alerts?: LinkedAlertView[] }) {
  return <section aria-label="Linked alerts"><h2>Linked alerts</h2>
    {!alerts || alerts.length === 0 ? <p>No alerts linked to this item.</p> : <table>
      <thead><tr><th>Alert</th><th>Rule</th><th>Subject</th><th>Severity</th><th>State / settlement</th><th>Relation</th><th>Linked by / at</th></tr></thead>
      <tbody>{alerts.map(la => <tr key={`${la.alert_id}:${la.relation}:${la.event_seq}`}>
        <td><EntityLink kind="alert" id={la.alert_id} state={alertGlyphState(la.state)} /></td>
        <td><a href={alertsFilterUrl({ rule: la.rule_id })}>{la.rule_id}</a></td>
        <td>{la.subject_key}</td>
        <td>{la.severity}</td>
        <td>{la.state} · {la.settlement_state}</td>
        <td>{la.relation}{la.note ? ` · ${la.note}` : ""}</td>
        <td>{la.linked_by || "not recorded"} · <time dateTime={new Date(la.linked_at).toISOString()}>{new Date(la.linked_at).toISOString()}</time></td>
      </tr>)}</tbody>
    </table>}
  </section>;
}
export function ItemPage({ id, live, paused, row, baseURL }: { id: string; live: boolean; paused: boolean; row?: ItemView; baseURL: string }) {
  const item = useItemQuery(id, live, paused), doc = useItemDocQuery(id, live, paused);
  const [pinned, setPinned] = useState(row);
  if (!pinned && row) setPinned(row);
  return <div className="entity-page" aria-label="Item page"><h1>Item <EntityLink kind="item" id={id} /></h1>
    {item.data?.data.idea ? <section aria-label="Idea words"><h2><span className="chip">idea</span> The words</h2><p>{item.data.data.idea.in_tree ? "in the tree" : "not yet in the tree"} · {item.data.data.idea.words_path}</p><pre className="idea-words">{item.data.data.idea.words}</pre></section> : null}
    {item.data?.data.idea ? <section aria-label="Idea outcomes"><h2>Items from this idea</h2>
      {item.data.data.idea.outcomes?.length ? <ul>{item.data.data.idea.outcomes.map(outcome => <li key={outcome.item_id}><EntityLink kind="item" id={outcome.item_id} /> · {outcome.title} · {outcome.state}</li>)}</ul> : <p>No items from this idea have been imported yet.</p>}
    </section> : null}
    {item.data?.data.split?.parent ? <section aria-label="Split parent">
      <h2>Split parent</h2>
      <p>{item.data.data.split.parent.progress_summary}</p>
      {item.data.data.split.parent.donor ? <p>Donor: {item.data.data.split.parent.donor.ref ? <><code>{item.data.data.split.parent.donor.ref}</code> · </> : null}<code>{item.data.data.split.parent.donor.sha}</code>{item.data.data.split.parent.donor.base_sha ? <> (base <code>{item.data.data.split.parent.donor.base_sha}</code>)</> : null}</p> : null}
      {item.data.data.split.parent.owner_words ? <pre className="split-owner-words">{item.data.data.split.parent.owner_words}</pre> : null}
      <table>
        <thead><tr><th>Part</th><th>Item</th><th>Title</th><th>Standing</th><th>Takes</th><th>State</th><th>Landed commit</th></tr></thead>
        <tbody>{item.data.data.split.parent.parts.map(part => <tr key={part.item_id}>
          <td>part {part.label}</td>
          <td><EntityLink kind="item" id={part.item_id} state={part.state} /></td>
          <td>{part.title}</td>
          <td>{part.required ? "required" : "optional"}</td>
          <td>{part.takes === "whole" ? "whole donor" : part.takes === "named" ? `named (${(part.paths ?? []).join(", ")})` : "nothing"}</td>
          <td>{part.state}</td>
          <td>{part.landed_commit ? <a href={`/trunk?sha=${encodeURIComponent(part.landed_commit)}`}><code>{part.landed_commit}</code></a> : "—"}</td>
        </tr>)}</tbody>
      </table>
      {item.data.data.split.parent.coverage.length ? <>
        <h3>Coverage</h3>
        <table>
          <thead><tr><th>Criterion</th><th>Clause / text</th><th>Owner</th></tr></thead>
          <tbody>{item.data.data.split.parent.coverage.flatMap(cov => cov.entries.map((entry, idx) => <tr key={`${cov.criterion_id}-${idx}`}>
            <td>{cov.criterion_id}</td>
            <td>{entry.clause || cov.criterion_text}</td>
            <td>{entry.closing_check ? "closing check" : entry.part_item_id ? <><EntityLink kind="item" id={entry.part_item_id} /> (part {entry.part_label})</> : `part ${entry.part_label ?? ""}`}</td>
          </tr>))}</tbody>
        </table>
      </> : null}
    </section> : null}
    {item.data?.data.split?.part ? <section aria-label="Split part">
      <h2>Split part</h2>
      <p>Parent <EntityLink kind="item" id={item.data.data.split.part.parent_id} /> · {item.data.data.split.part.parent_title} · part {item.data.data.split.part.label} ({item.data.data.split.part.required ? "required" : "optional"})</p>
      <p>Share: {item.data.data.split.part.share}</p>
      {item.data.data.split.part.donor ? <p>Donor: {item.data.data.split.part.donor.ref ? <><code>{item.data.data.split.part.donor.ref}</code> · </> : null}<code>{item.data.data.split.part.donor.sha}</code>{item.data.data.split.part.donor.base_sha ? <> (base <code>{item.data.data.split.part.donor.base_sha}</code>)</> : null}</p> : null}
      {item.data.data.split.part.donor || item.data.data.split.part.takes ? <p>Takes from donor: {item.data.data.split.part.takes === "whole" ? "whole donor" : item.data.data.split.part.takes === "named" ? <>named paths ({(item.data.data.split.part.paths ?? []).join(", ")}){item.data.data.split.part.shared_files?.length ? <>; shared files ({item.data.data.split.part.shared_files.join(", ")})</> : null}</> : "nothing"}</p> : null}
      {item.data.data.split.part.inherited_findings?.length ? <>
        <h3>Inherited findings</h3>
        <ul>{item.data.data.split.part.inherited_findings.map((finding, idx) => <li key={idx}>{finding}</li>)}</ul>
      </> : null}
      {item.data.data.split.part.leaves?.length ? <>
        <h3>Leaves to siblings</h3>
        <ul>{item.data.data.split.part.leaves.map(leaf => <li key={leaf.sibling_item_id}>
          part {leaf.sibling_label} (<EntityLink kind="item" id={leaf.sibling_item_id} state={leaf.sibling_state} /> · {leaf.sibling_title} · {leaf.sibling_state}): {leaf.work}
        </li>)}</ul>
      </> : null}
      {item.data.data.split.part.follows?.length ? <>
        <h3>Follows</h3>
        <ul>{item.data.data.split.part.follows.map(sib => <li key={sib.item_id}>
          part {sib.label} (<EntityLink kind="item" id={sib.item_id} state={sib.state} /> · {sib.title} · {sib.state})
        </li>)}</ul>
      </> : null}
    </section> : null}
    <ItemDetailPane itemId={id} item={item.data?.data} itemError={item.error} loading={item.isPending} onRetry={() => void item.refetch()} latestRun={pinned?.latest_run ?? null} updatedAtMs={pinned?.updated_at_ms ?? null} selectedRunId={null}
      onSelectRun={run => navigate(`/run/${encodeURIComponent(run)}`)} doc={{ doc: doc.data?.data, error: doc.error, pending: doc.isPending, onRetry: () => void doc.refetch(), baseUrl: baseURL }} />
    {item.data ? <><ItemLinkedAlerts alerts={item.data.data.linked_alerts} /><ItemGraph attempts={item.data.data.graph_attempts ?? []} /><DependencyLinks id={id} blockedBy={item.data.data.blocked_by} unblocks={item.data.data.unblocks} /><UsageSummary usage={item.data.data.usage} />{item.data.data.work_summary ? <p>{item.data.data.work_summary}</p> : null}<VerifyRefusals refusals={item.data.data.verify_refusals ?? []} /><ItemTimeline item={item.data.data} /></> : null}
  </div>;
}
export function RunPage({ id, route, live, paused }: { id: string; route: string; live: boolean; paused: boolean }) {
  const params = new URLSearchParams(route.split("?")[1]);
  const tab = params.get("tab") ?? "timeline", artifactKind = params.get("artifact"), exchangeID = params.get("exchange");
  const run = useRunQuery(id, live, paused), timeline = useRunTimelineQuery(id, live, true, paused), gate = useRunGateQuery(id, live, true, paused);
  const transcript = useRunTranscriptQuery(id, live, tab === "transcript" || tab === "time-order", paused);
  const exchanges = useRunExchangesQuery(id, live, tab === "exchanges" || exchangeID !== null, paused);
  const artifact = useRunArtifactQuery(id, artifactKind, live, paused);
  const data = run.data?.data;
  const spans = useMemo(() => data ? assembleSpans(data, timeline.data?.data as TimelineData | undefined, gate.data?.data) : [], [data, timeline.data, gate.data]);
  const setParam = (key: string, value: string | null) => { const next = new URLSearchParams(params); if (value) next.set(key, value); else next.delete(key); navigate(`/run/${encodeURIComponent(id)}?${next}`); };
  if (run.error) return <main className="entity-page"><h1>Run <EntityLink kind="run" id={id} /></h1><RefusalState code={refusalCode(run.error)} message={run.error.message} onRetry={() => void run.refetch()} /></main>;
  return <main className="entity-page" aria-label="Run page"><h1>Run <EntityLink kind="run" id={id} /></h1>
    {!data ? <p>Reading the run…</p> : <><p><EntityLink kind="item" id={data.run.item_id} /> · <EntityLink kind="lane" id={data.run.lane_id} />{data.run.seat ? <> · seat {data.run.seat}</> : null}{data.run.workflow ? <> · workflow {data.run.workflow}</> : null}{data.run.node ? <> · node {data.run.node}</> : null} · {data.run.backend} · {data.run.state} · exit {data.run.exit_code ?? "not recorded"}</p>
      <RunUsageFigures usage={data.usage} />
      <WallTimeCard timing={data.timing} />
      <PreservedWork records={data.preserved_work ?? []} />
      <nav aria-label="Run artifacts">{["brief", "fence", "base_sha", "request", "gate.verdict", "land.attempt"].map(kind => <button key={kind} onClick={() => setParam("artifact", kind)}>Open {kind}</button>)}</nav>
      {timeline.error ? <p role="alert">Timeline refused: {timeline.error.message}</p> : !timeline.data ? <p>Reading timeline spans…</p> : null}
      {gate.error ? <p role="alert">Gate refused: {gate.error.message}</p> : !gate.data ? <p>Reading gate spans…</p> : null}
      {timeline.data?.data.note ? <p>{timeline.data.data.note}</p> : null}{gate.data?.data.note ? <p>{gate.data.data.note}</p> : null}
      <SpanViewer key={id} spans={spans} initialComplete={!timeline.isPending && !gate.isPending} paused={paused} />
      <nav aria-label="Causal record">{["timeline", "time-order", "transcript", "exchanges", "claims"].map(name => <button key={name} aria-pressed={tab === name} onClick={() => setParam("tab", name)}>{(name[0].toUpperCase() + name.slice(1)).replace("-", " ")}</button>)}</nav>
      {tab === "timeline" ? <DrainScheduleLedger schedule={data.schedule} onOpenTimeOrder={() => setParam("tab", "time-order")} /> : tab === "time-order" ? <TimeOrderStream runId={id} schedule={data.schedule} transcript={transcript.data?.data} entriesPending={transcript.isPending} transcriptError={transcript.error} /> : tab === "transcript" ? <TranscriptView runId={id} transcript={transcript.data?.data} error={transcript.error} /> : tab === "exchanges" ? <InterToolExchangeView runId={id} exchanges={exchanges.data?.data} selectedExchange={exchangeID} onSelectExchange={value => setParam("exchange", value)} /> : tab === "claims" ? <ClaimAuditBanner run={data} /> : null}
      {exchangeID && exchanges.error ? <p role="alert">Exchange read refused: {exchanges.error.message}</p> : exchangeID && exchanges.data && !exchanges.data.data.exchanges.some(e => e.exchange_id === exchangeID) ? <p role="alert">Exchange {exchangeID} is not recorded in this run.</p> : exchangeID && !exchanges.data ? <p>Reading selected exchange…</p> : null}
      {artifactKind || (exchangeID && exchanges.data?.data.exchanges.some(e => e.exchange_id === exchangeID)) ? <PayloadInspectorDrawer exchange={exchanges.data?.data.exchanges.find(e => e.exchange_id === exchangeID) ?? null} artifact={artifact.data?.data} artifactKind={artifactKind} artifactError={artifact.error} onClose={() => { const next = new URLSearchParams(params); next.delete("artifact"); next.delete("exchange"); navigate(`/run/${encodeURIComponent(id)}?${next}`); }} /> : null}
    </>}
  </main>;
}
