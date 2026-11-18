import { useItemQuery, useRunTranscriptQuery } from "../models/queries";
import { useNow } from "../models/clock";
import type { ChildrenRecord, ItemData, LaneRow, PipelineStep } from "../models/types";
import { EntityLink } from "./EntityLink";
import { TranscriptView } from "./TranscriptView";
import { DependencyLinks } from "./DependencyLinks";
import { UsageSummary } from "./Usage";

export interface GraphNode { id: string; label: string; runId?: string; state: string }
export interface GraphEdge { from: string; to: string }
/** One renderer for recorded nodes and any directed dependencies supplied by the caller. */
export function ItemGraph({ nodes, edges }: { nodes: GraphNode[]; edges: GraphEdge[] }) {
  return <div className="lane-graph"><ol>{nodes.map(node => <li key={node.id} data-state={node.state}>{node.runId ? <a href={`#/run/${encodeURIComponent(node.runId)}?tab=transcript`}>{node.label}</a> : node.label} · {node.state}</li>)}</ol>
    {edges.length ? <ul aria-label="Graph edges">{edges.map(edge => <li key={`${edge.from}/${edge.to}`}>{edge.from} → {edge.to}</li>)}</ul> : null}</div>;
}
function elapsed(start: number, finish: number) { return `${(Math.max(0, finish - start) / 1000).toFixed(1)}s`; }
export function PipelineRow({ steps, now }: { steps: PipelineStep[]; now: number }) {
  return <ol className="lane-pipeline">{["stage", "graph", "gate", "land"].map(name => {
    const step = steps.find(step => step.step === name), running = !!step && step.finished_at_ms === null;
    return <li key={name} aria-current={running ? "step" : undefined}>{name} {step ? <>{running ? "▶" : "✓"} {elapsed(step.started_at_ms, step.finished_at_ms ?? now)}</> : "·"}</li>;
  })}</ol>;
}
export function ChildSessions({ runID, record }: { runID: string; record?: ChildrenRecord }) {
  return <div><EntityLink kind="run" id={runID} />{record?.note ? <p role="alert">worker records unreadable: {record.note}</p> : null}{!record?.recorded ? <p>child sessions: not recorded</p> : !record.sessions.length ? <p>child sessions: {record.note ? "unreadable" : "none"}</p> : <ul>{record.sessions.map(child => <li key={child.session_id}><a href={`#/run/${encodeURIComponent(child.session_id)}?tab=transcript`}>{child.role || "role not recorded"}</a> · started {child.started_at_ms === null ? "not recorded" : new Date(child.started_at_ms).toISOString()} · end {child.end_clock === "derived" ? "~" : ""}{child.ended_at_ms === null ? "not recorded" : new Date(child.ended_at_ms).toISOString()}{child.end_clock === "derived" ? " (derived from last transcript message)" : ""} · {child.turn_count === null ? "turn count not recorded" : `${child.turn_count} turns`} · transcript {child.transcript}</li>)}</ul>}</div>;
}
export function activeLaneRun(lane: LaneRow): string | null {
  return lane.nodes?.find(node => node.attempt === lane.attempt && node.state === "running")?.run_id || lane.run_id || null;
}
export function LaneDetail({ lane, item, now }: { lane: LaneRow; item?: ItemData; now: number }) {
  const recorded = lane.nodes ?? [];
  const current = recorded.filter(node => node.attempt === lane.attempt);
  const nodes: GraphNode[] = recorded.length ? current.map(node => ({id:node.node_id,label:node.node_id,runId:node.run_id,state:node.state})) : lane.run_id ? [{ id: lane.run_id, label: `${lane.seat || "worker"} · agent · ${lane.backend}`, runId: lane.run_id, state: lane.state === "running" ? "running" : lane.outcome === "landed" ? "passed" : "failed" }] : [];
  const attempts = recorded.length ? [...new Set(recorded.map(node => node.attempt))] : (item?.runs ?? []).map((_,index) => index+1);
  const runLink = (id: string) => {const run = item?.runs.find(run => run.run_id === id); return <span key={id}><EntityLink kind="run" id={id} /> {run?.lane_id ? <>· <EntityLink kind="lane" id={run.lane_id} /></> : null} · {run?.state ?? "run state not read"}</span>;};
  return <>
    <h1>Lane <EntityLink kind="lane" id={lane.lane_id} /></h1>
    <p>{lane.state} · {lane.state_since_ms === null ? "start not recorded" : elapsed(lane.state_since_ms, now)} · {lane.outcome}</p>
    <p>{lane.item_id ? <EntityLink kind="item" id={lane.item_id} /> : "No item"} · {item?.item.title} · attempt {lane.attempt || "not recorded"}{item || recorded.length ? ` of ${attempts.length}` : ""} · {lane.seat || "no seat asked (policy default)"} · {lane.backend || "backend not recorded"} {lane.run_id ? <EntityLink kind="run" id={lane.run_id} /> : null}</p>
    <UsageSummary usage={lane.usage} />
    <section><h2>Pipeline</h2><PipelineRow steps={lane.pipeline ?? []} now={now} />{lane.pipeline_note ? <p>{lane.pipeline_note}</p> : !lane.pipeline ? <p>Pipeline timing was not recorded.</p> : null}</section>
    <section><h2>Graph</h2><p>{nodes.length} node{nodes.length === 1 ? "" : "s"} · {item?.item.selected_workflow || lane.workflow_ref || "no workflow recorded"} · run order as recorded</p><ItemGraph nodes={nodes} edges={[]} /></section>
    <section><h2>Criteria</h2>{!lane.progress?.length ? <p>worker progress is not recorded</p> : <p>Worker claims are derived from the worker’s report, not judge confirmation.</p>}{item ? <ul>{item.criteria.map(c => <li key={c.criterion_id}>{c.criterion_id} · {c.state}{lane.progress?.some(claim => claim.criterion_id === c.criterion_id) ? " · ~claimed · derived (worker report, not a verdict)" : ""} · {c.text}</li>)}</ul> : <p>Criteria not read yet.</p>}</section>
    {item ? <DependencyLinks id={item.item.item_id} blockedBy={item.blocked_by} unblocks={item.unblocks} /> : <section><h2>Waits on</h2>Dependencies not read yet.<h2>Unblocks</h2>Unblocks not read yet.</section>}
    <section><h2>Now</h2><p>{lane.latest_action.kind === "unrecorded" ? "latest action not recorded" : lane.latest_action.text} · {lane.latest_action.kind === "liveness" ? "liveness signal, not an action" : lane.latest_action.kind}</p>{nodes.length ? nodes.map(node => <ChildSessions key={node.runId} runID={node.runId!} record={lane.child_sessions?.[node.runId!]} />) : <p>child sessions: not recorded</p>}</section>
    <section><h2>Attempts on this item</h2>{item || recorded.length ? <ol>{attempts.map(number => <li key={number}>attempt {number} · {recorded.length ? recorded.filter(node => node.attempt === number).map(node => runLink(node.run_id)) : item?.runs[number-1] ? runLink(item.runs[number-1].run_id) : null}</li>)}</ol> : <p>Attempts not read yet.</p>}</section>
    <section><h2>Lane history</h2>{lane.history ? <ol>{lane.history.map(run => <li key={run.run_id}><EntityLink kind="item" id={run.item_id} /> · <EntityLink kind="run" id={run.run_id} /> · <EntityLink kind="lane" id={run.lane_id} /> · {run.state} · {run.started_at_ms ? new Date(run.started_at_ms).toISOString() : "start not recorded"}</li>)}</ol> : <p>Lane history not recorded by this server.</p>}</section>
  </>;
}
export function LanePage({ id, lanes, error, live, paused }: { id: string; lanes?: LaneRow[]; error: Error | null; live: boolean; paused: boolean }) {
  const lane = lanes?.find(lane => lane.lane_id === id);
  const item = useItemQuery(lane?.item_id || null, live, paused);
  const runID = lane ? activeLaneRun(lane) : null;
  const transcript = useRunTranscriptQuery(runID, live, !!runID, paused);
  const now = useNow();
  return <main className="entity-page" aria-label="Lane page">
    {!lane ? <h1>Lane <EntityLink kind="lane" id={id} /></h1> : null}
    {error ? <p role="alert">Lanes refused: {error.message}</p> : !lanes ? <p>Reading lane…</p> : !lane ? <p role="alert">Lane <EntityLink kind="lane" id={id} /> is not recorded.</p> : <>
      <LaneDetail lane={lane} item={item.data?.data} now={now} />
      {item.error ? <p role="alert">Item refused: {item.error.message}</p> : null}
      <h2>Transcript (live)</h2>{runID ? <><a href={`#/run/${encodeURIComponent(runID)}?tab=transcript`}>Open full transcript</a><TranscriptView key={runID} runId={runID} transcript={transcript.data?.data} error={transcript.error} /></> : <p>No run recorded.</p>}
    </>}
  </main>;
}
