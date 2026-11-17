import type { CommentItemView, DesignerView, DesignsData } from "../models/types";
import { RefusalState, refusalCode } from "./RefusalState";

export function DesignerCard({ designer }: { designer: DesignerView | null | undefined }) {
  if (!designer) return <p className="empty">no last designer recorded</p>;
  return <section aria-label="Last designer">
    <h3>Last designer</h3>
    <dl className="doc__card">
      <div><dt>Session</dt><dd>{designer.session_id}</dd></div>
      <div><dt>Cache</dt><dd>{designer.state === "warm" ? `warm until ${designer.warm_until}` : designer.state}</dd></div>
      <div><dt>Context</dt><dd>{designer.context_tokens === null ? "context size not recorded" : `${designer.context_tokens.toLocaleString()} tokens`}</dd></div>
    </dl>
    <h4>Batches handled</h4>
    {designer.batches.length === 0 ? <p>no batches recorded</p> : <ul>{designer.batches.map(batch => <li key={batch.run_id}>
      <a href={`#/item/${encodeURIComponent(batch.item_id)}`}>{batch.item_id}</a> · <a href={`#/run/${encodeURIComponent(batch.run_id)}`}>{batch.run_id}</a> · {batch.reason}
    </li>)}</ul>}
  </section>;
}

function reactionText(reaction: unknown): string {
  if (!reaction || typeof reaction !== "object") return JSON.stringify(reaction);
  const row = reaction as Record<string, unknown>;
  return `${row.actor ?? "turn"}: ${row.summary ?? row.comment ?? row.text ?? row.kind ?? JSON.stringify(row)}`;
}

export function CommentThreads({ data }: { data: CommentItemView }) {
  return <section aria-label="Review comments">
    <h3>Review comments</h3>
    {data.confirmation_note && <p role="status">{data.confirmation_note}</p>}
    <h4>Session choice</h4>
    {data.runs.length === 0 ? <p>not staged yet</p> : <ul>{data.runs.map(run => <li key={run.run_id}><a href={`#/run/${encodeURIComponent(run.run_id)}`}>{run.run_id}</a> · <span>{run.reason}</span></li>)}</ul>}
    {data.comments.map(comment => <article key={comment.id}>
      <h4>{comment.id}</h4><p>{comment.comment}</p>
      {(comment.reactions ?? []).map((reaction, index) => <p key={index}>{reactionText(reaction)}</p>)}
      {comment.replies.length === 0 ? <p>no reply recorded</p> : comment.replies.map(reply => <div key={`${reply.run_id}:${reply.id}`}><p>{reply.summary}</p><p><span>{reply.state}</span> · turn {reply.round} · <a href={`#/run/${encodeURIComponent(reply.run_id)}`}>{reply.run_id}</a></p></div>)}
    </article>)}
  </section>;
}

export function DesignsPage({ data, error, pending, onRetry, path }: { data: DesignsData | undefined; error: Error | null; pending: boolean; onRetry: () => void; path?: string }) {
  if (error) return <main className="home"><RefusalState code={refusalCode(error)} message={error.message} onRetry={onRetry} /></main>;
  if (pending || !data) return <main className="home"><p>reading designers</p></main>;
  const designs = path ? data.designs.filter(design => design.path === path) : data.designs;
  return <main className="home">
    <h1>{path ?? "Designs"}</h1>
    <p>Comments within the cache lifetime: {data.arrivals.within_lifetime} · after it: {data.arrivals.after_lifetime} · no session to resume: {data.arrivals.no_session}</p>
    <p>Use these arrival counts to choose comments.refresh_hours. Unmeasured arrivals: {data.arrivals.unmeasured}.</p>
    {designs.length === 0 && <p>no design records found</p>}
    {designs.map(design => <article key={design.path}>
      <h2><a href={`#/design/${encodeURIComponent(design.path)}`}>{design.path}</a></h2>
      <DesignerCard designer={design.last_designer} />
    </article>)}
  </main>;
}
