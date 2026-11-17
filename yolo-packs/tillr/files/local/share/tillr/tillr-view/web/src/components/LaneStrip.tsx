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

export function LaneStrip({ lanes, error, now = Date.now() }: { lanes?: LaneRow[]; error?: Error | null; now?: number }) {
  const ordinary = lanes?.filter(row => !row.lane_id.startsWith("express-") && !row.lane_id.startsWith("desk-")) ?? [];
  const express = lanes?.filter(row => row.lane_id.startsWith("express-")) ?? [];
  const count = (state: LaneRow["state"]) => ordinary.filter(row => row.state === state).length;
  return <section className="lane-strip" aria-label="Lane work">
    <h2>Lanes</h2>
    {error ? <p role="alert">Lanes refused: {error.message}</p> : lanes === undefined ? <p role="status">Reading lanes…</p> : <>
      <p>{count("running")} of {ordinary.length} running{express.length ? `, ${express.filter(row => row.state === "running").length} of ${express.length} express` : ""} · {count("settling")} settling · {count("idle")} idle</p>
      {lanes.length === 0 ? <p>No lanes recorded.</p> : <ul>{lanes.map(row => {
        const quiet = row.state === "running" && row.log_grew_at_ms !== null && now - row.log_grew_at_ms >= LANE_QUIET_MS;
        const action = row.latest_action;
        return <li key={row.lane_id} data-state={row.state}>
          <div><EntityLink kind="lane" id={row.lane_id} state={row.state === "running" ? "moving" : "not known"} /> · {row.lane_id.startsWith("express-") ? "express · " : ""}{row.state}{row.running_nodes ? ` · ${row.running_nodes} nodes running` : ""}
            {row.state_since_ms !== null ? <span title="Elapsed since the recorded state began"> · ~{age(now - row.state_since_ms)}</span> : " · since —"}
            {quiet ? <strong className="lane-strip__quiet"> · quiet {age(now - row.log_grew_at_ms!)}</strong> : null}</div>
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
      })}</ul>}
    </>}
  </section>;
}
