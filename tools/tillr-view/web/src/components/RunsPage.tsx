import { useState } from "react";
import { relativeTime } from "../models/board";
import { useNow } from "../models/clock";
import { collapseIdleLanes, DRAINING_LANE_NOTE, formatLaneRowSummary, groupLanes } from "../models/lanes";
import type { ItemView, LaneRow, RunSummary } from "../models/types";
import { EntityLink } from "./EntityLink";
import { MoreIdleLanes } from "./LaneStrip";

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
function Row({ lane, title, now, draining = false }: { lane: LaneRow; title?: string; now: number; draining?: boolean }) {
  const node = lane.nodes?.find(n => n.attempt === lane.attempt && n.state === "running");
  const seat = node?.seat || lane.seat;
  const where = [node?.node_id, seat].filter(Boolean).join(" · ") || (lane.seat_source === "none" ? "no seat asked" : "seat unrecorded");
  const idle = lane.state === "idle";
  const heldItem = lane.held_item_id;
  const note = draining || lane.group === "draining" ? (lane.group_note || DRAINING_LANE_NOTE) : null;
  return <tr data-state={lane.state}>
    <td><EntityLink kind="lane" id={lane.lane_id} state={lane.state === "running" ? "moving" : "not known"} />{lane.lane_id.startsWith("express-") ? " express" : ""}</td>
    <td>{idle && !heldItem ? "idle" : (heldItem || lane.item_id) ? <><a href={`/item/${encodeURIComponent(heldItem || lane.item_id)}`}>{title || heldItem || lane.item_id}</a></> : "item unrecorded"}</td>
    <td>{idle && !heldItem ? "—" : where}</td>
    <td>{lane.state}{note ? ` · ${note}` : ""}</td>
    <td>{lane.state_since_ms === null ? "since —" : `~${age(now - lane.state_since_ms)}`}</td>
    <td>{lane.run_id ? <>{idle ? "last: " : ""}<EntityLink kind="run" id={lane.run_id} query={runQuery} /></> : "—"}</td>
  </tr>;
}

function LanesTable({ rows, titles, now, draining = false }: { rows: LaneRow[]; titles: Map<string, string>; now: number; draining?: boolean }) {
  return <table><thead><tr><th>Lane</th><th>Item</th><th>Seat / node</th><th>State</th><th>In state</th><th>Run</th></tr></thead>
    <tbody>{rows.map(lane => <Row key={lane.lane_id} lane={lane} title={titles.get(lane.held_item_id || lane.item_id)} now={now} draining={draining} />)}</tbody></table>;
}

export function RunsPage({ lanes, lanesError, items, now: fixed, configured }: { lanes?: LaneRow[]; lanesError?: Error | null; items?: ItemView[]; now?: number; configured?: string[] }) {
  const [expanded, setExpanded] = useState(false);
  const ticking = useNow();
  const now = fixed ?? ticking;
  const titles = new Map((items ?? []).map(item => [item.item_id, item.title]));
  const finished = finishedRuns(items);
  const groups = lanes ? groupLanes(lanes, configured) : null;
  const headerSummary = lanes ? formatLaneRowSummary(lanes, configured) : null;
  const { shown, hidden } = collapseIdleLanes(lanes ?? [], configured, now);
  return <main className="entity-page" aria-label="Runs page">
    <h1>Runs</h1>
    <section className="lane-strip" aria-label="Current lanes">
      <h2>Lanes</h2>
      {lanesError ? <p role="alert">could not read the store: {lanesError.message}</p> : lanes === undefined ? <p role="status">Reading lanes…</p> : lanes.length === 0 ? <p>No lanes recorded.</p> : groups ? <>
        {headerSummary ? <p>{headerSummary}</p> : null}
        <div className="lane-strip__group" data-lane-group="configured">
          <h3>Configured lanes ({groups.configured.length})</h3>
          {groups.configured.length ? <LanesTable rows={sortLanes(groups.configured)} titles={titles} now={now} /> : <p className="lane-strip__empty">None configured.</p>}
        </div>
        <div className="lane-strip__group" data-lane-group="draining">
          <h3>Draining lanes ({groups.draining.length}) · {DRAINING_LANE_NOTE}</h3>
          {groups.draining.length ? <LanesTable rows={sortLanes(groups.draining)} titles={titles} now={now} draining /> : <p className="lane-strip__empty">None finishing old work.</p>}
        </div>
        {groups.desks.length ? <div className="lane-strip__group" data-lane-group="desk">
          <h3>Document desks ({groups.desks.length})</h3>
          <LanesTable rows={sortLanes(groups.desks)} titles={titles} now={now} />
        </div> : null}
        <div className="lane-strip__group" data-lane-group="idle">
          <h3>Idle lanes holding nothing ({groups.idle.length})</h3>
          {groups.idle.length ? <>
            <MoreIdleLanes count={groups.idle.length} open={expanded} onToggle={() => setExpanded(!expanded)} />
            {expanded ? <LanesTable rows={groups.idle} titles={titles} now={now} /> : null}
          </> : <p className="lane-strip__empty">None.</p>}
        </div>
      </> : <>
        <LanesTable rows={sortLanes(expanded ? lanes : shown)} titles={titles} now={now} />
        <MoreIdleLanes count={hidden.length} open={expanded} onToggle={() => setExpanded(!expanded)} />
      </>}
    </section>
    <section aria-label="Recently finished runs">
      <h2>Recently finished</h2>
      {items === undefined ? <p role="status">Reading runs…</p> : finished.length === 0 ? <p>No finished runs recorded.</p> :
        <table><thead><tr><th>Run</th><th>Lane</th><th>Item</th><th>Seat</th><th>Finished</th><th>Exit</th></tr></thead>
          <tbody>{finished.map(({ item, run }) => <tr key={run.run_id}>
            <td><EntityLink kind="run" id={run.run_id} query={runQuery} /></td>
            <td><EntityLink kind="lane" id={run.lane_id} /></td>
            <td><a href={`/item/${encodeURIComponent(item.item_id)}`}>{item.title || item.item_id}</a></td>
            <td>{run.seat || "—"}</td>
            <td title={new Date(run.finished_at_ms).toLocaleString()}>{relativeTime(run.finished_at_ms, now)}</td>
            <td>{run.exit_code === null ? run.state : run.exit_code}</td>
          </tr>)}</tbody></table>}
    </section>
  </main>;
}
