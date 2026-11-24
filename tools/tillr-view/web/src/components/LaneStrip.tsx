import { useState } from "react";
import { collapseIdleLanes, DRAINING_LANE_NOTE, formatLaneRowSummary, groupLanes } from "../models/lanes";
import type { LaneRow } from "../models/types";
import { UsageSummary } from "./Usage";
import { EntityLink } from "./EntityLink";

/** A running worker is quiet when its stream has not grown for five minutes. */
export const LANE_QUIET_MS = 5 * 60 * 1000;
function age(ms: number): string {
  const seconds = Math.floor(Math.max(0, ms) / 1000);
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  return `${Math.floor(seconds / 3600)}h ${Math.floor(seconds % 3600 / 60)}m`;
}

/** The one line that stands for idle lanes outside the configured count. */
export function MoreIdleLanes({ count, open, onToggle }: { count: number; open: boolean; onToggle: () => void }) {
  if (count === 0) return null;
  return <p className="lane-strip__more">{count} more idle lane{count === 1 ? "" : "s"} <button type="button" onClick={onToggle}>{open ? "(hide)" : "(show)"}</button></p>;
}

function LaneItemRow({ row, now, draining = false }: { row: LaneRow; now: number; draining?: boolean }) {
  const quiet = row.state === "running" && row.log_grew_at_ms !== null && now - row.log_grew_at_ms >= LANE_QUIET_MS;
  const action = row.latest_action;
  const note = draining || row.group === "draining" ? (row.group_note || DRAINING_LANE_NOTE) : null;
  return <li data-state={row.state}>
    <div><EntityLink kind="lane" id={row.lane_id} state={row.state === "running" ? "moving" : "not known"} /> · {row.lane_id.startsWith("express-") ? "express · " : ""}{row.state}{row.running_nodes ? ` · ${row.running_nodes} nodes running` : ""}
      {row.state_since_ms !== null ? <span title="Elapsed since the recorded state began"> · ~{age(now - row.state_since_ms)}</span> : " · since —"}
      {quiet ? <strong className="lane-strip__quiet"> · quiet {age(now - row.log_grew_at_ms!)}</strong> : null}
      {note ? <span className="lane-strip__draining"> · {note}</span> : null}</div>
    {row.state === "idle" && row.held_item_id ? <div className="lane-strip__derived">holding <EntityLink kind="item" id={row.held_item_id} />{row.held_reason ? ` · ${row.held_reason}` : ""}</div> : null}
    {row.run_id ? <div>{row.state === "idle" ? "last: " : ""}
      {row.item_id ? <EntityLink kind="item" id={row.item_id} /> : "item unrecorded"} · <EntityLink kind="run" id={row.run_id} />
      {row.state === "idle" ? ` · ${row.outcome || "end unrecorded"}` : null}
      <div>{row.seat || (row.seat_source === "none" ? "no seat asked (policy default)" : "seat unrecorded")} · {row.backend || "backend unrecorded"} · attempt {row.attempt || "—"}</div>
    </div> : <div>last run —</div>}
    {row.state === "settling" ? <div className="lane-strip__derived">{row.last_step_at_ms !== null
      ? `after ${row.last_step} ~${age(now - row.last_step_at_ms)}` : "step —"}</div> : null}
    {row.state === "running" ? <div className="lane-strip__action">
      {action.kind === "liveness" ? action.at_ms !== null ? `log grew ${age(now - action.at_ms)} ago · liveness signal` : "log growth unrecorded · liveness signal"
        : action.kind === "action" ? <>{action.text}{action.at_ms !== null ? ` · ${action.clock === "derived_clock" ? "~" : ""}${age(now - action.at_ms)} ago` : " · time unrecorded"}</> : "action unrecorded"}
    </div> : null}
    <UsageSummary usage={row.usage} />
    {row.note ? <small>{row.note}</small> : null}
  </li>;
}

export function LaneStrip({ lanes, error, now = Date.now(), configured }: { lanes?: LaneRow[]; error?: Error | null; now?: number; configured?: string[] }) {
  const [expanded, setExpanded] = useState(false);
  const groups = lanes ? groupLanes(lanes, configured) : null;
  const headerSummary = lanes ? formatLaneRowSummary(lanes, configured) : null;
  const { shown, hidden } = collapseIdleLanes(lanes ?? [], configured, now);
  const drawn = expanded ? lanes ?? [] : shown;
  const ordinary = lanes?.filter(row => !row.lane_id.startsWith("express-") && !row.lane_id.startsWith("desk-")) ?? [];
  const express = lanes?.filter(row => row.lane_id.startsWith("express-")) ?? [];
  const count = (state: LaneRow["state"]) => ordinary.filter(row => row.state === state).length;
  return <section className="lane-strip" aria-label="Lane work">
    <h2>Lanes</h2>
    {error ? <p role="alert">Lanes refused: {error.message}</p> : lanes === undefined ? <p role="status">Reading lanes…</p> : <>
      <p>{headerSummary ?? `${count("running")} of ${ordinary.length} running${express.length ? `, ${express.filter(row => row.state === "running").length} of ${express.length} express` : ""} · ${count("settling")} settling · ${count("idle")} idle`}</p>
      {lanes.length === 0 ? <p>No lanes recorded.</p> : groups ? <>
        <div className="lane-strip__group" data-lane-group="configured">
          <h3>Configured lanes ({groups.configured.length})</h3>
          {groups.configured.length ? <ul>{groups.configured.map(row => <LaneItemRow key={row.lane_id} row={row} now={now} />)}</ul> : <p className="lane-strip__empty">None configured.</p>}
        </div>
        <div className="lane-strip__group" data-lane-group="draining">
          <h3>Draining lanes ({groups.draining.length}) · {DRAINING_LANE_NOTE}</h3>
          {groups.draining.length ? <ul>{groups.draining.map(row => <LaneItemRow key={row.lane_id} row={row} now={now} draining />)}</ul> : <p className="lane-strip__empty">None finishing old work.</p>}
        </div>
        {groups.desks.length ? <div className="lane-strip__group" data-lane-group="desk">
          <h3>Document desks ({groups.desks.length})</h3>
          <ul>{groups.desks.map(row => <LaneItemRow key={row.lane_id} row={row} now={now} />)}</ul>
        </div> : null}
        <div className="lane-strip__group" data-lane-group="idle">
          <h3>Idle lanes holding nothing ({groups.idle.length})</h3>
          {groups.idle.length ? <>
            <MoreIdleLanes count={groups.idle.length} open={expanded} onToggle={() => setExpanded(!expanded)} />
            {expanded ? <ul>{groups.idle.map(row => <LaneItemRow key={row.lane_id} row={row} now={now} />)}</ul> : null}
          </> : <p className="lane-strip__empty">None.</p>}
        </div>
      </> : <><ul>{drawn.map(row => <LaneItemRow key={row.lane_id} row={row} now={now} />)}</ul><MoreIdleLanes count={hidden.length} open={expanded} onToggle={() => setExpanded(!expanded)} /></>}
    </>}
  </section>;
}
