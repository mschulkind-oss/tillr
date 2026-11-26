import { localTime } from "../models/board";
import type { AlertFilterParams } from "../models/api";
import { useAlertQuery, useAlertsQuery } from "../models/queries";
import { navigate } from "../models/route";
import type {
  AlertEpisodeView,
  AlertItemLinkView,
  AlertsData,
  AlertSilenceView,
} from "../models/types";
import { EntityLink } from "./EntityLink";
import { RefusalState, refusalCode } from "./RefusalState";

export function alertGlyphState(state: string): string {
  switch (state) {
    case "open":
      return "alarm";
    case "acknowledged":
      return "needs you";
    case "silenced":
      return "waits on the loop";
    case "resolved":
      return "pass";
    default:
      return "not known";
  }
}

export function formatAlertDuration(ms?: number): string {
  if (ms === undefined || ms < 0) return "";
  const totalSec = Math.floor(ms / 1000);
  if (totalSec < 60) return `${totalSec}s`;
  const totalMin = Math.floor(totalSec / 60);
  if (totalMin < 60) return `${totalMin}m`;
  const hours = Math.floor(totalMin / 60);
  const mins = totalMin % 60;
  if (hours < 24) return mins > 0 ? `${hours}h ${mins}m` : `${hours}h`;
  const days = Math.floor(hours / 24);
  const remHours = hours % 24;
  return remHours > 0 ? `${days}d ${remHours}h` : `${days}d`;
}

export function alertsFilterUrl(filter: AlertFilterParams): string {
  const params = new URLSearchParams();
  if (filter.state) params.set("state", filter.state);
  if (filter.rule) params.set("rule", filter.rule);
  if (filter.severity) params.set("severity", filter.severity);
  if (filter.settlement) params.set("settlement", filter.settlement);
  const q = params.toString();
  return q ? `/alerts?${q}` : "/alerts";
}

export function isAlertsPromoted(data?: AlertsData): boolean {
  if (!data || data.absent) return false;
  return data.episodes.some(
    (ep) =>
      ep.state !== "resolved" &&
      (ep.severity === "high" ||
        ep.settlement_state !== "settled" ||
        ep.escalated_to === "owner"),
  );
}

function formatVars(vars: Record<string, string>): string {
  const entries = Object.entries(vars);
  if (!entries.length) return "global";
  return entries.map(([k, v]) => `${k}=${v}`).join(", ");
}

function LinkedItemsCell({ links }: { links: AlertItemLinkView[] }) {
  if (!links.length) return <span className="muted">none</span>;
  return (
    <ul className="alerts-linked-items">
      {links.map((link) => (
        <li key={`${link.alert_id}:${link.item_id}`}>
          <EntityLink
            kind="item"
            id={link.item_id}
            title={link.item_title}
            state={link.item_state || "not known"}
          />{" "}
          <span className="chip">{link.relation}</span>
          {link.active_silence ? <span className="chip">active silence</span> : null}
        </li>
      ))}
    </ul>
  );
}

function AlertRow({
  ep,
  currentFilter = {},
}: {
  ep: AlertEpisodeView;
  currentFilter?: AlertFilterParams;
}) {
  const unsettledAge = formatAlertDuration(ep.unsettled_for_ms);
  return (
    <tr data-alert-id={ep.alert_id} data-state={ep.state} data-settlement={ep.settlement_state}>
      <td>
        <EntityLink kind="alert" id={ep.alert_id} state={alertGlyphState(ep.state)} />
      </td>
      <td>
        <a href={alertsFilterUrl({ ...currentFilter, rule: ep.rule_id })}>{ep.rule_id}</a>
      </td>
      <td>
        <div>{ep.subject_key}</div>
        <div className="muted">{formatVars(ep.variables)}</div>
      </td>
      <td>{ep.severity}</td>
      <td>
        <div>{ep.state}</div>
        <div>
          {ep.settlement_state}
          {unsettledAge ? ` · unsettled ${unsettledAge}` : ""}
        </div>
      </td>
      <td>
        <div>
          <time dateTime={new Date(ep.opened_at).toISOString()}>{localTime(ep.opened_at)}</time>
        </div>
        <div className="muted">
          ep {ep.episode_num} · {ep.observation_count} obs
        </div>
      </td>
      <td>
        {ep.latest_transition ? (
          <>
            <div>{ep.latest_transition.state_change}</div>
            <div className="muted">
              <time dateTime={new Date(ep.latest_transition.timestamp).toISOString()}>
                {localTime(ep.latest_transition.timestamp)}
              </time>{" "}
              · {ep.latest_transition.actor || "not recorded"}
            </div>
          </>
        ) : (
          <span className="muted">no transitions recorded</span>
        )}
      </td>
      <td>
        <LinkedItemsCell links={ep.linked_items} />
      </td>
    </tr>
  );
}

export function HomeAlertsPanel({
  data,
  error,
  promoted,
}: {
  data?: AlertsData;
  error?: Error | null;
  promoted: boolean;
}) {
  const activeOrUnsettled =
    data?.episodes.filter((ep) => ep.state !== "resolved" || ep.settlement_state !== "settled") ?? [];
  const preview = activeOrUnsettled.slice(0, 5);
  return (
    <section
      className={promoted ? "home-alerts home-alerts--promoted" : "home-alerts"}
      aria-label="Watch alerts"
      data-promoted={promoted ? "true" : "false"}
    >
      <h2>
        Watch alerts{" "}
        {data && !data.absent
          ? `· ${data.counts.active} active · ${data.counts.unsettled} unsettled`
          : ""}{" "}
        <a href="/alerts">all → Alerts</a>
      </h2>
      {error ? (
        <p role="alert">Watch alerts refused: {error.message}</p>
      ) : !data ? (
        <p role="status">Reading watch alerts…</p>
      ) : data.absent ? (
        <p>No watch alert table in this store yet.</p>
      ) : (
        <>
          <p className="alerts-counts">
            open {data.counts.open} · acked {data.counts.acknowledged} · silenced{" "}
            {data.counts.silenced} · resolved {data.counts.resolved} · active silences{" "}
            {data.counts.active_silences}
          </p>
          {preview.length === 0 ? (
            <p>No active or unsettled watch alerts.</p>
          ) : (
            <div className="alerts-table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Alert</th>
                    <th>Rule</th>
                    <th>Subject / vars</th>
                    <th>Severity</th>
                    <th>State / settlement</th>
                    <th>Fired</th>
                    <th>Latest transition</th>
                    <th>Linked items</th>
                  </tr>
                </thead>
                <tbody>
                  {preview.map((ep) => (
                    <AlertRow key={ep.alert_id} ep={ep} />
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </section>
  );
}

export function AlertsLedger({
  route,
  live,
  paused,
}: {
  route: string;
  live: boolean;
  paused: boolean;
}) {
  const params = new URLSearchParams(route.split("?")[1] ?? "");
  const filter: AlertFilterParams = {
    state: params.get("state") ?? "",
    rule: params.get("rule") ?? "",
    severity: params.get("severity") ?? "",
    settlement: params.get("settlement") ?? "",
  };
  const alerts = useAlertsQuery(filter, live, paused, true);
  const data = alerts.data?.data;

  const setFilter = (patch: Partial<AlertFilterParams>) => {
    navigate(alertsFilterUrl({ ...filter, ...patch }));
  };

  return (
    <main className="entity-page" aria-label="Alerts ledger">
      <h1>Watch alerts</h1>
      <form
        className="alerts-filters"
        aria-label="Alert filters"
        onSubmit={(e) => e.preventDefault()}
      >
        <label>
          State{" "}
          <select
            aria-label="State filter"
            value={filter.state ?? ""}
            onChange={(e) => setFilter({ state: e.target.value })}
          >
            <option value="">All states</option>
            <option value="open">open</option>
            <option value="acknowledged">acknowledged</option>
            <option value="silenced">silenced</option>
            <option value="resolved">resolved</option>
          </select>
        </label>
        <label>
          Rule{" "}
          <input
            type="text"
            aria-label="Rule filter"
            placeholder="rule id"
            value={filter.rule ?? ""}
            onChange={(e) => setFilter({ rule: e.target.value })}
          />
        </label>
        <label>
          Severity{" "}
          <select
            aria-label="Severity filter"
            value={filter.severity ?? ""}
            onChange={(e) => setFilter({ severity: e.target.value })}
          >
            <option value="">All severities</option>
            <option value="high">high</option>
            <option value="warning">warning</option>
            <option value="info">info</option>
          </select>
        </label>
        <label>
          Settlement{" "}
          <select
            aria-label="Settlement filter"
            value={filter.settlement ?? ""}
            onChange={(e) => setFilter({ settlement: e.target.value })}
          >
            <option value="">All settlements</option>
            <option value="uninvestigated">uninvestigated</option>
            <option value="investigating">investigating</option>
            <option value="investigated">investigated</option>
            <option value="settled">settled</option>
          </select>
        </label>
        {(filter.state || filter.rule || filter.severity || filter.settlement) ? (
          <a href="/alerts">Clear filters</a>
        ) : null}
      </form>

      {alerts.error ? (
        <RefusalState
          code={refusalCode(alerts.error)}
          message={alerts.error.message}
          onRetry={() => void alerts.refetch()}
        />
      ) : !data ? (
        <p role="status">Reading alerts…</p>
      ) : data.absent ? (
        <p>No watch alert table in this store yet.</p>
      ) : (
        <>
          <p className="alerts-counts" aria-label="Alert counts">
            {data.counts.total} total · {data.counts.active} active (open {data.counts.open},
            acknowledged {data.counts.acknowledged}, silenced {data.counts.silenced}) · resolved{" "}
            {data.counts.resolved} · {data.counts.unsettled} unsettled (uninvestigated{" "}
            {data.counts.uninvestigated}, investigating {data.counts.investigating}, investigated{" "}
            {data.counts.investigated}) · settled {data.counts.settled} · active silences{" "}
            {data.counts.active_silences}
          </p>

          <section aria-label="Alert episodes">
            <h2>Episodes</h2>
            {data.episodes.length === 0 ? (
              <p>No alert episodes match the current filter.</p>
            ) : (
              <div className="alerts-table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>Alert</th>
                      <th>Rule</th>
                      <th>Subject / vars</th>
                      <th>Severity</th>
                      <th>State / settlement</th>
                      <th>Fired</th>
                      <th>Latest transition</th>
                      <th>Linked items</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.episodes.map((ep) => (
                      <AlertRow key={ep.alert_id} ep={ep} currentFilter={filter} />
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>

          <section aria-label="Active silences">
            <h2>Active silences</h2>
            {data.active_silences.length === 0 ? (
              <p>No active silences.</p>
            ) : (
              <table>
                <thead>
                  <tr>
                    <th>Silence</th>
                    <th>Rule</th>
                    <th>Variables</th>
                    <th>Tracking item</th>
                    <th>By / at</th>
                    <th>Reason</th>
                    <th>Bound</th>
                    <th>Source alert</th>
                  </tr>
                </thead>
                <tbody>
                  {data.active_silences.map((s: AlertSilenceView) => (
                    <tr key={s.silence_id}>
                      <td>
                        <code>{s.silence_id}</code>
                      </td>
                      <td>
                        <a href={alertsFilterUrl({ ...filter, rule: s.rule_id })}>{s.rule_id}</a>
                      </td>
                      <td>{formatVars(s.vars)}</td>
                      <td>
                        <EntityLink
                          kind="item"
                          id={s.tracking_item_id}
                          title={s.tracking_item_title}
                          state={s.tracking_item_state || "not known"}
                        />
                      </td>
                      <td>
                        {s.created_by} ·{" "}
                        <time dateTime={new Date(s.created_at).toISOString()}>
                          {localTime(s.created_at)}
                        </time>
                      </td>
                      <td>{s.reason}</td>
                      <td>
                        {s.silence_until_ms !== undefined
                          ? `until ${localTime(s.silence_until_ms)}`
                          : s.silence_on_fingerprint
                            ? `on change (${s.silence_on_fingerprint})`
                            : "not recorded"}
                      </td>
                      <td>
                        {s.source_alert_id ? (
                          <EntityLink kind="alert" id={s.source_alert_id} />
                        ) : (
                          <span className="muted">none</span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </section>
        </>
      )}
    </main>
  );
}

function formatBundle(raw?: string): string {
  if (!raw) return "";
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}

export function AlertPage({
  id,
  live,
  paused,
}: {
  id: string;
  live: boolean;
  paused: boolean;
}) {
  const alert = useAlertQuery(id, live, paused);
  const data = alert.data?.data;

  return (
    <main className="entity-page" aria-label="Alert page">
      <p>
        <a href="/alerts">← All alerts</a>
      </p>
      <h1>
        Alert <EntityLink kind="alert" id={id} state={data ? alertGlyphState(data.episode.state) : "not known"} />
      </h1>
      {alert.error ? (
        <RefusalState
          code={refusalCode(alert.error)}
          message={alert.error.message}
          onRetry={() => void alert.refetch()}
        />
      ) : !data ? (
        <p role="status">Reading alert…</p>
      ) : data.absent ? (
        <p>No watch alert table in this store yet.</p>
      ) : (
        <>
          <section aria-label="Alert metadata">
            <h2>Episode</h2>
            <dl className="alerts-meta">
              <dt>Rule</dt>
              <dd>
                <a href={alertsFilterUrl({ rule: data.episode.rule_id })}>
                  {data.episode.rule_id}
                </a>
              </dd>
              <dt>Subject</dt>
              <dd>{data.episode.subject_key}</dd>
              <dt>Variables</dt>
              <dd>{formatVars(data.episode.variables)}</dd>
              <dt>Severity</dt>
              <dd>{data.episode.severity}</dd>
              <dt>Condition state</dt>
              <dd>{data.episode.state}</dd>
              <dt>Settlement state</dt>
              <dd>
                {data.episode.settlement_state}
                {data.episode.unsettled_for_ms !== undefined
                  ? ` · unsettled ${formatAlertDuration(data.episode.unsettled_for_ms)}`
                  : ""}
              </dd>
              <dt>Episode / observations</dt>
              <dd>
                episode {data.episode.episode_num} · {data.episode.observation_count} observations
              </dd>
              <dt>Opened</dt>
              <dd>
                <time dateTime={new Date(data.episode.opened_at).toISOString()}>
                  {new Date(data.episode.opened_at).toISOString()}
                </time>{" "}
                · seq {data.episode.opened_seq}
              </dd>
              <dt>Last observed</dt>
              <dd>
                <time dateTime={new Date(data.episode.last_observed_at).toISOString()}>
                  {new Date(data.episode.last_observed_at).toISOString()}
                </time>{" "}
                · seq {data.episode.last_observed_seq}
              </dd>
              {data.episode.resolved_at !== undefined ? (
                <>
                  <dt>Resolved</dt>
                  <dd>
                    <time dateTime={new Date(data.episode.resolved_at).toISOString()}>
                      {new Date(data.episode.resolved_at).toISOString()}
                    </time>
                    {data.episode.resolved_reason ? ` · ${data.episode.resolved_reason}` : ""}
                  </dd>
                </>
              ) : null}
              {data.episode.outcome ? (
                <>
                  <dt>Outcome</dt>
                  <dd>
                    {data.episode.outcome}
                    {data.episode.item_link_kind ? ` (${data.episode.item_link_kind})` : ""}
                  </dd>
                </>
              ) : null}
              {data.episode.escalated_to ? (
                <>
                  <dt>Escalated to</dt>
                  <dd>{data.episode.escalated_to}</dd>
                </>
              ) : null}
              {data.episode.silence_id ? (
                <>
                  <dt>Silence</dt>
                  <dd>
                    <code>{data.episode.silence_id}</code>
                  </dd>
                </>
              ) : null}
              {data.episode.investigated_by ? (
                <>
                  <dt>Investigation</dt>
                  <dd>
                    by {data.episode.investigated_by}
                    {data.episode.investigated_at !== undefined
                      ? ` at ${new Date(data.episode.investigated_at).toISOString()}`
                      : ""}
                    {data.episode.investigation_cause
                      ? ` · cause: ${data.episode.investigation_cause}`
                      : ""}
                    {data.episode.investigation_evidence
                      ? ` · evidence: ${data.episode.investigation_evidence}`
                      : ""}
                  </dd>
                </>
              ) : null}
              {data.episode.settled_by ? (
                <>
                  <dt>Settled</dt>
                  <dd>
                    by {data.episode.settled_by}
                    {data.episode.settled_at !== undefined
                      ? ` at ${new Date(data.episode.settled_at).toISOString()}`
                      : ""}
                    {data.episode.settled_seq !== undefined
                      ? ` · seq ${data.episode.settled_seq}`
                      : ""}
                  </dd>
                </>
              ) : null}
              {data.episode.acked_by ? (
                <>
                  <dt>Acknowledged</dt>
                  <dd>
                    by {data.episode.acked_by}
                    {data.episode.acked_at !== undefined
                      ? ` at ${new Date(data.episode.acked_at).toISOString()}`
                      : ""}
                    {data.episode.acked_note ? ` · ${data.episode.acked_note}` : ""}
                  </dd>
                </>
              ) : null}
            </dl>
          </section>

          <section aria-label="Transition history">
            <h2>Transition history</h2>
            {data.transitions.length === 0 ? (
              <p>
                No transition events are recorded for this episode (stored timestamps: opened{" "}
                <time dateTime={new Date(data.episode.opened_at).toISOString()}>
                  {new Date(data.episode.opened_at).toISOString()}
                </time>
                , last observed{" "}
                <time dateTime={new Date(data.episode.last_observed_at).toISOString()}>
                  {new Date(data.episode.last_observed_at).toISOString()}
                </time>
                {data.episode.resolved_at !== undefined ? (
                  <>
                    , resolved{" "}
                    <time dateTime={new Date(data.episode.resolved_at).toISOString()}>
                      {new Date(data.episode.resolved_at).toISOString()}
                    </time>
                  </>
                ) : null}
                ).
              </p>
            ) : (
              <table>
                <thead>
                  <tr>
                    <th>Seq</th>
                    <th>Time</th>
                    <th>State change</th>
                    <th>Actor</th>
                    <th>Detail</th>
                  </tr>
                </thead>
                <tbody>
                  {data.transitions.map((tr) => (
                    <tr key={`${tr.seq}:${tr.state_change}`}>
                      <td>{tr.seq}</td>
                      <td>
                        <time dateTime={new Date(tr.timestamp).toISOString()}>
                          {new Date(tr.timestamp).toISOString()}
                        </time>
                      </td>
                      <td>{tr.state_change}</td>
                      <td>{tr.actor || "not recorded"}</td>
                      <td>{tr.detail || "—"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </section>

          <section aria-label="Linked items">
            <h2>Linked items</h2>
            {data.linked_items.length === 0 ? (
              <p>No items linked to this alert.</p>
            ) : (
              <table>
                <thead>
                  <tr>
                    <th>Item</th>
                    <th>State</th>
                    <th>Relation</th>
                    <th>Linked by</th>
                    <th>Linked at</th>
                    <th>Note</th>
                  </tr>
                </thead>
                <tbody>
                  {data.linked_items.map((link) => (
                    <tr key={`${link.alert_id}:${link.item_id}`}>
                      <td>
                        <EntityLink
                          kind="item"
                          id={link.item_id}
                          title={link.item_title}
                          state={link.item_state || "not known"}
                        />
                      </td>
                      <td>{link.item_state || "not known"}</td>
                      <td>
                        {link.relation}
                        {link.active_silence ? " (active silence)" : ""}
                      </td>
                      <td>{link.linked_by || "not recorded"}</td>
                      <td>
                        <time dateTime={new Date(link.linked_at).toISOString()}>
                          {new Date(link.linked_at).toISOString()}
                        </time>
                      </td>
                      <td>{link.note || "—"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </section>

          <section aria-label="Captured bundle">
            <h2>Captured bundle</h2>
            {data.episode.bundle_json ? (
              <pre className="alerts-bundle">{formatBundle(data.episode.bundle_json)}</pre>
            ) : (
              <p>No bundle captured.</p>
            )}
          </section>

          <section aria-label="Related episodes">
            <h2>Other episodes of {data.episode.rule_id} · {data.episode.subject_key}</h2>
            {data.related_episodes.length === 0 ? (
              <p>No other episodes for this rule and subject.</p>
            ) : (
              <ul>
                {data.related_episodes.map((rel) => (
                  <li key={rel.alert_id}>
                    <EntityLink
                      kind="alert"
                      id={rel.alert_id}
                      state={alertGlyphState(rel.state)}
                    />{" "}
                    · ep {rel.episode_num} · {rel.state} · {rel.settlement_state} ·{" "}
                    <time dateTime={new Date(rel.opened_at).toISOString()}>
                      {new Date(rel.opened_at).toISOString()}
                    </time>
                  </li>
                ))}
              </ul>
            )}
          </section>
        </>
      )}
    </main>
  );
}
