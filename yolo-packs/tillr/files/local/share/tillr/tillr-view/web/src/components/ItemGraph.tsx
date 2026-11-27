import type { ChildrenRecord, ItemGraphAttempt, ItemGraphNode, ItemNodeExecution } from "../models/types";
import { EntityLink } from "./EntityLink";
import { formatBytes } from "./RunningWork";

function Children({ record }: { record?: ChildrenRecord }) {
  if (!record?.recorded) return <p>child sessions: not recorded{record?.note ? ` (${record.note})` : ""}</p>;
  if (!record.sessions.length) return <p>child sessions: {record.note ? `unreadable (${record.note})` : "none"}</p>;
  return <div className="item-graph-children"><p>child sessions:</p>{record.sessions.map(child => <article className="item-graph-child" aria-label={`Child session ${child.session_id}`} key={child.session_id}>
    <strong>observed</strong> · <EntityLink kind="run" id={child.session_id} /> · {child.role || "role not recorded"}
  </article>)}{record.note ? <p role="alert">{record.note}</p> : null}</div>;
}

function formatExecutionSpend(exec: ItemNodeExecution): string | null {
  const s = exec.spend;
  if (!s) return null;
  const parts: string[] = [];
  if (s.tokens !== null && s.tokens !== undefined) parts.push(`${s.tokens.toLocaleString()} tok`);
  if (s.cost_usd !== null && s.cost_usd !== undefined) parts.push(`$${s.cost_usd.toFixed(2)}`);
  if (parts.length > 0) return `${s.live ? "live " : ""}${parts.join(" · ")}`;
  return s.note || null;
}

function ExecutionRecord({ exec }: { exec: ItemNodeExecution }) {
  const spendText = formatExecutionSpend(exec);
  return (
    <div className="item-graph-execution" aria-label={`Execution ${exec.run_id}`}>
      <p>
        Run <EntityLink kind="run" id={exec.run_id} />
        {exec.backend ? ` · ${exec.backend}` : ""}
        {exec.seat ? ` · ${exec.seat}` : ""}
      </p>
      <p>
        segments: {exec.segment_count}
        {exec.ending ? ` · ending: ${exec.ending.state}${exec.ending.exit_code !== null && exec.ending.exit_code !== undefined ? ` (exit ${exec.ending.exit_code})` : ""}${exec.ending.end_cause ? ` · ${exec.ending.end_cause}` : ""}` : ""}
      </p>
      {exec.log ? (
        <p>
          log: {exec.log.present ? `${exec.log.path || "log"} (${formatBytes(exec.log.bytes ?? 0)})` : "not present"}
          {" · "}
          <a href={exec.log.transcript_url || `#/run/${encodeURIComponent(exec.run_id)}`}>transcript</a>
        </p>
      ) : null}
      {exec.lease?.active ? (
        <p>
          lease: active ({exec.lease.lane_id}
          {exec.lease.lease_no_expiry ? ", no expiry" : exec.lease.leased_until_ms ? `, until ${new Date(exec.lease.leased_until_ms).toISOString()}` : ""})
        </p>
      ) : null}
      {spendText ? <p>spend: {spendText}</p> : null}
    </div>
  );
}

function Node({ node }: { node: ItemGraphNode }) {
  const execByRun = new Map<string, ItemNodeExecution>();
  for (const ex of node.executions ?? []) execByRun.set(ex.run_id, ex);

  return <article className="item-graph-node" aria-label={`Node ${node.id}`}>
    <h4>{node.id}</h4><p>{node.kind} · {node.seat || node.command_ref || node.command?.join(" ") || "not recorded"} · {node.access || "access not recorded"} · {node.state}</p>
    {node.runs.length ? node.runs.map(run => {
      const exec = execByRun.get(run);
      return <div key={run}>
        {exec ? <ExecutionRecord exec={exec} /> : <p>Run <EntityLink kind="run" id={run} /></p>}
        {node.kind === "agent" ? <Children record={node.children[run]} /> : null}
      </div>;
    }) : node.kind === "agent" ? <Children /> : null}
    {node.outputs.map(output => <details key={`${output.run_id}-${output.name}`}><summary>{output.name} · {output.type}</summary>{output.body !== null ? <pre>{output.body}</pre> : <p>{output.note || "output unreadable"}</p>}</details>)}
  </article>;
}
function GraphAttempt({ attempt }: { attempt: ItemGraphAttempt }) {
  const levels = [...new Set(attempt.nodes.map(node => node.level))];
  return <div>{levels.map(level => {
    const nodes = attempt.nodes.filter(node => node.level === level);
    const parents = [...new Set(nodes.map(node => node.parent))];
    return <div key={level} className="item-graph-level">{parents.map(parent => <section key={parent || "root"}><h3>Stage {parent || "root"}</h3><div className="item-graph-row">{nodes.filter(node => node.parent === parent).map(node => <Node key={node.id} node={node} />)}</div></section>)}</div>;
  })}</div>;
}
export function ItemGraph({ attempts }: { attempts: ItemGraphAttempt[] }) {
  if (!attempts.length) return null;
  return <section aria-label="Item graph"><h2>Nodes · attempt {attempts[0].attempt}</h2><GraphAttempt attempt={attempts[0]} />
    {attempts.slice(1).map(attempt => <details key={attempt.attempt}><summary>Attempt {attempt.attempt}</summary><GraphAttempt attempt={attempt} /></details>)}
  </section>;
}
