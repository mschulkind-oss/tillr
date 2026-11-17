import { LaneStrip } from "./LaneStrip";
import type { DrainSummary, LaneRow, PassRow, SnapshotData, WaitingRow } from "../models/types";
import { ClassState, EntityLink, type EntityKind } from "./EntityLink";

export function passStamp(ms: number): string {
  return new Date(ms).toISOString().replace(/[-:]/g, "").replace(/\.\d{3}Z$/, "Z");
}

export function passCounts(pass: PassRow): string {
  const counts = new Map<string, number>();
  for (const decision of pass.decisions ?? []) counts.set(decision.action, (counts.get(decision.action) ?? 0) + 1);
  const rest = [...counts].filter(([action]) => action !== "dispatch" && action !== "hold");
  return `${counts.get("dispatch") ?? 0} dispatched · ${counts.get("hold") ?? 0} held` + rest.map(([action, count]) => ` · ${count} ${action}`).join("");
}

export function newestPasses(passes: PassRow[]): PassRow[] {
  return [...passes].sort((a, b) => b.dispatched_at_ms - a.dispatched_at_ms);
}

const STOP_GUIDANCE: Record<string, string> = {
  "queue-drained": "restart: tillr-drain loop, after a hold moves",
  "lanes-leased": "release the retained lane with tillr run release before restarting",
  "operator-stop": "restart only when the operator wants processing resumed",
  "host-boundary-violation": "inspect the boundary violation; do not retry the offending run",
  "gate-harness-error": "repair the gate harness before restarting",
  "fault": "diagnose the recorded fault before restarting",
};

/** Shares the header's recorded-session rule: running runs alone never prove loop liveness. */
export function LoopSummary({ drain, passes, error, snapshot, now = Date.now() }: {
  drain?: DrainSummary; passes?: PassRow[]; error?: Error | null; snapshot?: SnapshotData; now?: number;
}) {
  const session = drain?.session;
  const ended = session != null && session.ended_at_ms !== null;
  const alive = session != null && !ended && now <= session.next_pass_at_ms + (drain?.session_grace_ms ?? 0);
  const state = ended ? "stopped" : alive ? "moving" : "not known";
  const latest = newestPasses(passes ?? [])[0];
  return <section className="loop-summary" aria-label="Loop summary">
    <div className="loop-summary__state"><ClassState value={state} />
      <span>{ended ? drain?.last_stop_reason || "stop reason not recorded" : alive ? "The recorded heartbeat is current." : session ? "Loop heartbeat overdue." : drain?.session_note || "No session recorded."}</span>
      <span>{ended ? STOP_GUIDANCE[drain?.last_stop_reason ?? ""] ?? "inspect the recorded stop before deciding whether to restart" : alive ? "watch the next pass" : "inspect the loop session before restarting"}</span>
    </div>
    {!alive && drain?.stop_note ? <p><EntityText value={drain.stop_note} snapshot={snapshot} passes={passes} /></p> : null}
    {error ? <p role="alert">Passes refused: {error.message}</p> : latest ? <>
      <p>{latest.stop || "stop not recorded"} · {latest.detail ? <q><EntityText value={latest.detail} snapshot={snapshot} passes={passes} /></q> : "stop detail not recorded"}</p>
      <p>last pass <EntityLink kind="pass" id={passStamp(latest.dispatched_at_ms)} /> → {passCounts(latest)}</p>
    </> : <p>{passes ? "No pass recorded." : "Reading passes…"}</p>}
    {session ? <p>started {new Date(session.started_at_ms).toISOString()} · last heartbeat {new Date(session.refreshed_at_ms).toISOString()}</p> : null}
    <p>session {session ? <a className="session-link" href={`#/passes?session=${encodeURIComponent(session.session_id)}`}><code>{session.session_id}</code></a> : "not recorded"}</p>
  </section>;
}

/** The waiting route mixes prose and references in mover. Match known identities exactly;
 * retain common dangling references as links rather than dropping unknown store entities. */
export function EntityText({ value, snapshot, rows = [], passes = [] }: { value: string; snapshot?: SnapshotData; rows?: WaitingRow[]; passes?: PassRow[] }) {
  const known = new Map<string, EntityKind>();
  for (const pass of passes) for (const decision of pass.decisions ?? []) {
    known.set(decision.item_id, "item");
    if (decision.lane_id) known.set(decision.lane_id, "lane");
    if (decision.run_id) known.set(decision.run_id, "run");
  }
  for (const row of rows) known.set(row.item_id, "item");
  for (const item of snapshot?.items ?? []) {
    known.set(item.item_id, "item");
    if (item.latest_run) known.set(item.latest_run.run_id, "run");
  }
  for (const lane of snapshot?.lanes ?? []) {
    known.set(lane.lane_id, "lane");
    if (lane.run_id) known.set(lane.run_id, "run");
  }
  const escaped = [...known.keys()].sort((a, b) => b.length - a.length).map(id => id.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"));
  const pattern = new RegExp(`(${[...escaped, "(?:item|run|lane|landing)[-_][\\w.-]+", "OQ-[\\w.-]+", "docs/[\\w./-]+\\.md", "(?:go-test|vitest|fork-replay):[\\w.-]+"].join("|")})`, "g");
  return <>{value.split(pattern).map((part, index) => {
    const kind = known.get(part) ?? (/^item[-_]/.test(part) ? "item" : /^run[-_]/.test(part) ? "run" : /^lane[-_]/.test(part) ? "lane" : /^landing[-_]/.test(part) ? "landing" : part.startsWith("OQ-") ? "question" : part.startsWith("docs/") ? "doc" : /^(go-test|vitest|fork-replay):/.test(part) ? "check" : null);
    return kind ? <EntityLink key={index} kind={kind} id={part} /> : part;
  })}</>;
}

const CLASSES = ["needs you", "needs a cause", "waits on work", "unclassified", "ready", "moving"];
export function Home({ snapshot, waiting, waitingError, passes, passesError, lanes, lanesError, now }: {
  lanes?: LaneRow[]; lanesError?: Error | null; now?: number;
  snapshot?: SnapshotData; waiting?: WaitingRow[]; waitingError?: Error | null;
  passes?: PassRow[]; passesError?: Error | null;
}) {
  const rows = waiting ?? [];
  const classes = [...CLASSES, ...new Set(rows.map(row => row.class).filter(value => !CLASSES.includes(value)))];
  const notMoving = rows.filter(row => !["ready", "moving"].includes(row.class)).length;
  return <main className="home">
    <h1>Home</h1>
    <div className="home__columns">
      <div>
        {/* The list is the planner's answer, so a planner that cannot be asked leaves the LIST
            unknown — never the drain "not moving". The loop's own state is the heartbeat in
            the header and the loop summary, and it does not depend on the planner. */}
        <h2>Not moving · {waiting ? `${notMoving} of ${rows.length} unlanded` : waitingError ? "unknown: the planner could not be asked" : "not yet read"} <a href="#/items">all → Items</a></h2>
        {waitingError ? <p role="alert">The planner could not be asked, so which items are waiting is unknown (the loop's own state is in the header, and does not depend on it): {waitingError.message}</p> : waiting === undefined ? <p role="status">Reading waiting rows…</p> : rows.length === 0 ? <p>No unlanded items in the waiting answer.</p> : classes.map(value => {
          const group = rows.filter(row => row.class === value).sort((a, b) => {
            if (!a.rank) return b.rank ? 1 : a.item_id.localeCompare(b.item_id);
            if (!b.rank) return -1;
            return a.rank < b.rank ? -1 : a.rank > b.rank ? 1 : a.item_id.localeCompare(b.item_id);
          });
          if (!group.length) return null;
          return <section key={value} data-class={value}>
            <h2><ClassState value={value} /> · {group.length}</h2>
            <table><thead><tr><th>Rank</th><th>Item</th><th>Class</th><th>Where</th><th>Why</th><th>Since</th></tr></thead>
              <tbody>{group.slice(0, 8).map(row => <tr key={row.item_id}>
                <td>{row.rank || "unranked"}</td>
                <td><EntityLink kind="item" id={row.item_id} state={row.class} />
                  <div className="home__mover">mover: <EntityText value={row.mover} snapshot={snapshot} rows={rows} passes={passes} /></div></td>
                <td><ClassState value={row.class} /></td><td>{row.where}</td><td>{row.why}</td><td>{row.since}{row.waited ? ` · waited ${row.waited}` : ""}</td>
              </tr>)}</tbody></table>
            {group.length > 8 ? <a href={`#/items?class=${encodeURIComponent(value)}`}>{group.length - 8} more {value}</a> : null}
          </section>;
        })}
      </div>
      <aside>
        <LaneStrip lanes={lanes} error={lanesError} now={now} />
        <h2>Last passes</h2>
        {passesError ? <p role="alert">Passes refused: {passesError.message}</p> : passes ? passes.length ? <ul>{newestPasses(passes).slice(0, 3).map(pass => <li key={pass.dispatched_at_ms}>
          <EntityLink kind="pass" id={passStamp(pass.dispatched_at_ms)} /> · {passCounts(pass)}
        </li>)}</ul> : <p>No pass recorded.</p> : <p>Reading passes…</p>}
      </aside>
    </div>
  </main>;
}
