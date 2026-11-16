import { useMemo, useState } from "react";
import { useItemDocQuery, useItemQuery, useRunArtifactQuery, useRunExchangesQuery, useRunGateQuery, useRunQuery, useRunTimelineQuery, useRunTranscriptQuery } from "../models/queries";
import type { DispatchView, ItemData, ItemView, TimelineData } from "../models/types";
import { assembleSpans } from "../models/spans";
import { ItemDetailPane } from "./ItemDetailPane";
import { EntityLink } from "./EntityLink";
import { SpanViewer } from "./SpanViewer";
import { TranscriptView } from "./TranscriptView";
import { InterToolExchangeView } from "./InterToolExchangeView";
import { ClaimAuditBanner } from "./ClaimAuditBanner";
import { PayloadInspectorDrawer } from "./PayloadInspectorDrawer";
import { RefusalState, refusalCode } from "./RefusalState";

export function foldedDecisions(decisions: DispatchView[]) {
  const folds: { decision: DispatchView; count: number; since: number }[] = [];
  for (const decision of [...decisions].sort((a,b) => b.dispatched_at_ms - a.dispatched_at_ms)) {
    const previous = folds.at(-1);
    if (previous && previous.decision.action === decision.action && previous.decision.reason === decision.reason) { previous.count++; previous.since = decision.dispatched_at_ms; }
    else folds.push({ decision, count: 1, since: decision.dispatched_at_ms });
  }
  return folds;
}
export function ItemTimeline({ item }: { item: ItemData }) {
  return <><section aria-label="Item timeline"><h2>Pass timeline (newest first)</h2>
    {foldedDecisions(item.decisions).map(({ decision: d, count, since }) => <p key={d.decision_id}><a href={`#/passes?item=${encodeURIComponent(item.item.item_id)}`}>{new Date(d.dispatched_at_ms).toISOString()} · {d.action} · {d.reason} · ×{count} pass{count === 1 ? "" : "es"} since {new Date(since).toISOString()}, same answer</a>{d.run_id ? <EntityLink kind="run" id={d.run_id} /> : null}</p>)}
    {!item.decisions.length ? <p>No pass has recorded a decision for this item.</p> : null}
  </section><section aria-label="Item landings"><h2>Landing attempts</h2>
    {item.attempts.flatMap(a => a.landings ?? []).map(l => <p key={l.attempt_id}><EntityLink kind="landing" id={l.attempt_id} /> {l.result} · {l.reason}</p>)}
  </section></>;
}
export function ItemPage({ id, live, paused, row, baseURL }: { id: string; live: boolean; paused: boolean; row?: ItemView; baseURL: string }) {
  const item = useItemQuery(id, live, paused), doc = useItemDocQuery(id, live, paused);
  const [pinned, setPinned] = useState(row);
  if (!pinned && row) setPinned(row);
  return <div className="entity-page" aria-label="Item page"><h1>Item <EntityLink kind="item" id={id} /></h1>
    <ItemDetailPane itemId={id} item={item.data?.data} itemError={item.error} loading={item.isPending} onRetry={() => void item.refetch()} latestRun={pinned?.latest_run ?? null} updatedAtMs={pinned?.updated_at_ms ?? null} selectedRunId={null}
      onSelectRun={run => { window.location.hash = `#/run/${encodeURIComponent(run)}`; }} doc={{ doc: doc.data?.data, error: doc.error, pending: doc.isPending, onRetry: () => void doc.refetch(), baseUrl: baseURL }} />
    {item.data ? <ItemTimeline item={item.data.data} /> : null}
  </div>;
}
export function RunPage({ id, route, live, paused }: { id: string; route: string; live: boolean; paused: boolean }) {
  const params = new URLSearchParams(route.split("?")[1]);
  const tab = params.get("tab") ?? "timeline", artifactKind = params.get("artifact"), exchangeID = params.get("exchange");
  const run = useRunQuery(id, live, paused), timeline = useRunTimelineQuery(id, live, true, paused), gate = useRunGateQuery(id, live, true, paused);
  const transcript = useRunTranscriptQuery(id, live, tab === "transcript", paused);
  const exchanges = useRunExchangesQuery(id, live, tab === "exchanges" || exchangeID !== null, paused);
  const artifact = useRunArtifactQuery(id, artifactKind, live, paused);
  const data = run.data?.data;
  const spans = useMemo(() => data ? assembleSpans(data, timeline.data?.data as TimelineData | undefined, gate.data?.data) : [], [data, timeline.data, gate.data]);
  const navigate = (key: string, value: string | null) => { const next = new URLSearchParams(params); if (value) next.set(key, value); else next.delete(key); window.location.hash = `#/run/${encodeURIComponent(id)}?${next}`; };
  if (run.error) return <main className="entity-page"><h1>Run <EntityLink kind="run" id={id} /></h1><RefusalState code={refusalCode(run.error)} message={run.error.message} onRetry={() => void run.refetch()} /></main>;
  return <main className="entity-page" aria-label="Run page"><h1>Run <EntityLink kind="run" id={id} /></h1>
    {!data ? <p>Reading the run…</p> : <><p><EntityLink kind="item" id={data.run.item_id} /> · <EntityLink kind="lane" id={data.run.lane_id} /> · {data.run.backend} · {data.run.state} · exit {data.run.exit_code ?? "not recorded"}</p>
      <p>Cost {data.cost.cost_total_usd === null ? "not recorded" : `$${data.cost.cost_total_usd}`} · {data.cost.tokens ?? "unrecorded"} tokens · {data.cost.turns ?? "unrecorded"} turns</p>
      <nav aria-label="Run artifacts">{["brief", "fence", "base_sha", "request", "gate.verdict", "land.attempt"].map(kind => <button key={kind} onClick={() => navigate("artifact", kind)}>Open {kind}</button>)}</nav>
      {timeline.error ? <p role="alert">Timeline refused: {timeline.error.message}</p> : !timeline.data ? <p>Reading timeline spans…</p> : null}
      {gate.error ? <p role="alert">Gate refused: {gate.error.message}</p> : !gate.data ? <p>Reading gate spans…</p> : null}
      {timeline.data?.data.note ? <p>{timeline.data.data.note}</p> : null}{gate.data?.data.note ? <p>{gate.data.data.note}</p> : null}
      <SpanViewer key={id} spans={spans} initialComplete={!timeline.isPending && !gate.isPending} paused={paused} />
      <nav aria-label="Causal record">{["timeline", "transcript", "exchanges", "claims"].map(name => <button key={name} aria-pressed={tab === name} onClick={() => navigate("tab", name)}>{name[0].toUpperCase() + name.slice(1)}</button>)}</nav>
      {tab === "transcript" ? <TranscriptView runId={id} transcript={transcript.data?.data} error={transcript.error} /> : tab === "exchanges" ? <InterToolExchangeView runId={id} exchanges={exchanges.data?.data} selectedExchange={exchangeID} onSelectExchange={value => navigate("exchange", value)} /> : tab === "claims" ? <ClaimAuditBanner run={data} /> : null}
      {exchangeID && exchanges.error ? <p role="alert">Exchange read refused: {exchanges.error.message}</p> : exchangeID && exchanges.data && !exchanges.data.data.exchanges.some(e => e.exchange_id === exchangeID) ? <p role="alert">Exchange {exchangeID} is not recorded in this run.</p> : exchangeID && !exchanges.data ? <p>Reading selected exchange…</p> : null}
      {artifactKind || (exchangeID && exchanges.data?.data.exchanges.some(e => e.exchange_id === exchangeID)) ? <PayloadInspectorDrawer exchange={exchanges.data?.data.exchanges.find(e => e.exchange_id === exchangeID) ?? null} artifact={artifact.data?.data} artifactKind={artifactKind} artifactError={artifact.error} onClose={() => { const next = new URLSearchParams(params); next.delete("artifact"); next.delete("exchange"); window.location.hash = `#/run/${encodeURIComponent(id)}?${next}`; }} /> : null}
    </>}
  </main>;
}
