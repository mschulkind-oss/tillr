import { useNow } from "../models/clock";
import type { ItemView, LaneRow, RunSummary } from "../models/types";
import { EntityLink } from "./EntityLink";

const FINISHED_LIMIT = 20;
function age(ms: number): string {
  const seconds = Math.floor(Math.max(0, ms) / 1000);
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  return `${Math.floor(seconds / 3600)}h ${Math.floor(seconds % 3600 / 60)}m`;
}
/** Busy lanes first; the order inside each group is the server's. */
export function sortLanes(lanes: LaneRow[]): LaneRow[] {
  return [...lanes.filter(lane => lane.state !== "idle"), ...lanes.filter(lane => lane.state === "idle")];
}
/** The last finished runs the snapshot already carries (each item's latest run), newest first. */
export function finishedRuns(items: ItemView[] | undefined, limit = FINISHED_LIMIT): { item: ItemView; run: RunSummary }[] {
  return (items ?? []).flatMap(item => item.latest_run && item.latest_run.finished_at_ms > 0 ? [{ item, run: item.latest_run }] : [])
    .sort((a, b) => b.run.finished_at_ms - a.run.finished_at_ms).slice(0, limit);
}
const runQuery = { tab: "transcript" };
function Row({ lane, title, now }: { lane: LaneRow; title?: string; now: number }) {
  const node = lane.nodes?.find(n => n.attempt === lane.attempt && n.state === "running");
  const seat = node?.seat || lane.seat;
  const where = [node?.node_id, seat].filter(Boolean).join(" · ") || (lane.seat_source === "none" ? "no seat asked" : "seat unrecorded");
  const idle = lane.state === "idle";
  return <tr data-state={lane.state}>
    <td><EntityLink kind="lane" id={lane.lane_id} state={lane.state === "running" ? "moving" : "not known"} />{lane.lane_id.startsWith("express-") ? " express" : ""}</td>
    <td>{idle ? "idle" : lane.item_id ? <><a href={`#/item/${encodeURIComponent(lane.item_id)}`}>{title || lane.item_id}</a></> : "item unrecorded"}</td>
    <td>{idle ? "—" : where}</td>
    <td>{lane.state}</td>
    <td>{lane.state_since_ms === null ? "since —" : `~${age(now - lane.state_since_ms)}`}</td>
    <td>{lane.run_id ? <>{idle ? "last: " : ""}<EntityLink kind="run" id={lane.run_id} query={runQuery} /></> : "—"}</td>
  </tr>;
}

export function RunsPage({ lanes, lanesError, items, now: fixed }: { lanes?: LaneRow[]; lanesError?: Error | null; items?: ItemView[]; now?: number }) {
  const ticking = useNow();
  const now = fixed ?? ticking;
  const titles = new Map((items ?? []).map(item => [item.item_id, item.title]));
  const finished = finishedRuns(items);
  return <main className="entity-page" aria-label="Runs page">
    <h1>Runs</h1>
    <section className="lane-strip" aria-label="Current lanes">
      <h2>Lanes</h2>
      {lanesError ? <p role="alert">could not read the store: {lanesError.message}</p> : lanes === undefined ? <p role="status">Reading lanes…</p> : lanes.length === 0 ? <p>No lanes recorded.</p> :
        <table><thead><tr><th>Lane</th><th>Item</th><th>Seat / node</th><th>State</th><th>In state</th><th>Run</th></tr></thead>
          <tbody>{sortLanes(lanes).map(lane => <Row key={lane.lane_id} lane={lane} title={titles.get(lane.item_id)} now={now} />)}</tbody></table>}
    </section>
    <section aria-label="Recently finished runs">
      <h2>Recently finished</h2>
      {items === undefined ? <p role="status">Reading runs…</p> : finished.length === 0 ? <p>No finished runs recorded.</p> :
        <table><thead><tr><th>Run</th><th>Lane</th><th>Item</th><th>Seat</th><th>Finished</th><th>Exit</th></tr></thead>
          <tbody>{finished.map(({ item, run }) => <tr key={run.run_id}>
            <td><EntityLink kind="run" id={run.run_id} query={runQuery} /></td>
            <td><EntityLink kind="lane" id={run.lane_id} /></td>
            <td><a href={`#/item/${encodeURIComponent(item.item_id)}`}>{item.title || item.item_id}</a></td>
            <td>{run.seat || "—"}</td>
            <td>{new Date(run.finished_at_ms).toISOString()}</td>
            <td>{run.exit_code === null ? run.state : run.exit_code}</td>
          </tr>)}</tbody></table>}
    </section>
  </main>;
}
