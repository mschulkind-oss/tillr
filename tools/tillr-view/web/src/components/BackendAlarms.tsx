import type { BackendAlarm } from "../models/types";

export function BackendAlarms({ alarms }: { alarms?: BackendAlarm[] }) {
  if (!alarms?.length) return null;
  return <section className="backend-alarms" role="alert" aria-label="Backend alarms">
    <ul>{alarms.map(alarm => <li key={`${alarm.kind}:${alarm.backend}`}>
      <strong>ALARM {alarm.kind} ({alarm.backend})</strong>, since {alarm.since}: {alarm.cause};{" "}
      {alarm.items.length} item(s){alarm.items.length ? ` [${alarm.items.join(", ")}]` : ""},{" "}
      {alarm.lanes.length} lane(s){alarm.lanes.length ? ` [${alarm.lanes.join(", ")}]` : ""};{" "}
      clears: {alarm.clears}; operator: <code>{alarm.command}</code>
    </li>)}</ul>
  </section>;
}
