import type { ChildrenRecord, ItemGraphAttempt, ItemGraphNode } from "../models/types";
import { EntityLink } from "./EntityLink";

function Children({ record }: { record?: ChildrenRecord }) {
  if (!record?.recorded) return <p>child sessions: not recorded{record?.note ? ` (${record.note})` : ""}</p>;
  if (!record.sessions.length) return <p>child sessions: {record.note ? `unreadable (${record.note})` : "none"}</p>;
  return <div className="item-graph-children"><p>child sessions:</p>{record.sessions.map(child => <article className="item-graph-child" aria-label={`Child session ${child.session_id}`} key={child.session_id}>
    <strong>observed</strong> · <EntityLink kind="run" id={child.session_id} /> · {child.role || "role not recorded"}
  </article>)}{record.note ? <p role="alert">{record.note}</p> : null}</div>;
}
function Node({ node }: { node: ItemGraphNode }) {
  return <article className="item-graph-node" aria-label={`Node ${node.id}`}>
    <h4>{node.id}</h4><p>{node.kind} · {node.seat || node.command_ref || node.command?.join(" ") || "not recorded"} · {node.access || "access not recorded"} · {node.state}</p>
    {node.runs.length ? node.runs.map(run => <div key={run}><p>Run <EntityLink kind="run" id={run} /></p>{node.kind === "agent" ? <Children record={node.children[run]} /> : null}</div>) : node.kind === "agent" ? <Children /> : null}
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
