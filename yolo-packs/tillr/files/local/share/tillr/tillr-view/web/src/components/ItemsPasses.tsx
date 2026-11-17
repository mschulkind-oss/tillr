import { useEffect, useMemo, useRef, useState } from "react";
import type { PassRow, SnapshotData, WaitingRow } from "../models/types";
import { URL_CHANGE_EVENT } from "../models/url";
import { waitingIdentity, waitingOrder } from "../models/waiting";
import { ClassState, EntityLink } from "./EntityLink";
import { EntityText, newestPasses, passCounts, passStamp } from "./Home";
import { useScreenHash } from "./ObservatoryShell";
import { moveItemToTop } from "../models/api";

function useListParams() {
  const hash = useScreenHash();
  const params = new URLSearchParams(hash.split("?")[1]);
  const set = (key: string, value: string) => {
    const next = new URLSearchParams(window.location.hash.split("?")[1]);
    if (value) next.set(key, value); else next.delete(key);
    window.history.replaceState(null, "", `${window.location.pathname}${window.location.search}${hash.split("?")[0]}${next.size ? `?${next}` : ""}`);
    window.dispatchEvent(new Event(URL_CHANGE_EVENT));
  };
  return { params, set };
}

/** Keep membership and order still while reading. Existing rows may update their facts;
 * arrivals and removals wait until the reader explicitly accepts the new list. */
function useStableList<T>(incoming: T[], identity: (row: T) => string, scope: string) {
  const [held, setHeld] = useState(false);
  const [accepted, setAccepted] = useState<{ scope: string; ids: string[] }>({ scope, ids: [] });
  const [focused, setFocused] = useState<string | null>(null);
  const listRef = useRef<HTMLElement>(null);
  const ids = incoming.map(identity);
  const sameScope = accepted.scope === scope;
  const pinned = held && sameScope;
  const visibleIDs = pinned ? accepted.ids : ids;
  const retained = useRef(new Map<string, T>());
  for (const row of incoming) retained.current.set(identity(row), row);
  const visible = visibleIDs.map(id => retained.current.get(id)).filter((row): row is T => row !== undefined);
  const newer = pinned ? ids.filter(id => !accepted.ids.includes(id)).length : 0;
  const removed = pinned ? accepted.ids.filter(id => !ids.includes(id)).length : 0;
  const updates = newer + removed;
  useEffect(() => {
    if (!pinned) setAccepted({ scope, ids });
    if (!sameScope) { setHeld(false); setFocused(null); }
  }, [scope, ids.join("\n"), pinned, sameScope]);
  useEffect(() => {
    const onScroll = () => {
      const shell = listRef.current?.closest(".observatory-shell");
      if (window.scrollY > 0 || (shell?.scrollTop ?? 0) > 0 || (listRef.current?.scrollTop ?? 0) > 0) setHeld(true);
    };
    window.addEventListener("scroll", onScroll, true);
    return () => window.removeEventListener("scroll", onScroll, true);
  }, []);
  useEffect(() => {
    const row = [...(listRef.current?.querySelectorAll<HTMLElement>("[data-row-id]") ?? [])].find(row => row.dataset.rowId === focused);
    row?.focus({ preventScroll: true });
    row?.scrollIntoView?.({ block: "nearest" });
  }, [focused]);
  const focus = (id: string) => { setHeld(true); setFocused(id); };
  const accept = () => {
    setAccepted({ scope, ids });
    setHeld(false);
    setFocused(null);
    if (listRef.current) listRef.current.scrollTop = 0;
    const shell = listRef.current?.closest(".observatory-shell");
    if (shell) shell.scrollTop = 0;
    // jsdom does not implement scrollTo; a real browser does.
    if (window.scrollY > 0) window.scrollTo(0, 0);
  };
  return { visible, newer, updates, focused, focus, accept, listRef };
}

function useRowKeys(ids: string[], focused: string | null, focus: (id: string) => void, href: (id: string) => string) {
  useEffect(() => {
    const key = (event: KeyboardEvent) => {
      const target = event.target;
      if (event.metaKey || event.ctrlKey || event.altKey || (target instanceof HTMLElement &&
        (target.matches("input, textarea, select, button, summary") || target.isContentEditable))) return;
      if (event.key === "u") { event.preventDefault(); window.location.hash = "#/"; return; }
      if (!["j", "k", "Enter", "[", "]"].includes(event.key) || !ids.length) return;
      event.preventDefault();
      const at = ids.indexOf(focused ?? "");
      const delta = ["j", "]"].includes(event.key) ? 1 : -1;
      const next = event.key === "Enter" ? Math.max(0, at) : at < 0 ? 0 : Math.max(0, Math.min(ids.length - 1, at + delta));
      const id = ids[next];
      focus(id);
      if (["Enter", "[", "]"].includes(event.key)) window.location.hash = href(id);
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [ids.join("\n"), focused, focus, href]);
}

const CLASSES = ["needs you", "waits on work", "needs a cause", "ready", "moving", "waits on the loop"];
export function ItemsView({ waiting, snapshot, error, writesEnabled = false, onMoved }: { waiting?: WaitingRow[]; snapshot?: SnapshotData; error?: Error | null; writesEnabled?: boolean; onMoved?: () => Promise<unknown> }) {
  const [moving, setMoving] = useState<string | null>(null);
  const [moveError, setMoveError] = useState<string | null>(null);
  const { params, set } = useListParams();
  const preset = params.get("state") || "not-moving";
  const klass = params.get("class") || "";
  const reason = params.get("reason") || "";
  const rows = useMemo(() => {
    const source: WaitingRow[] = preset === "landed" ? (snapshot?.items ?? []).filter(item => item.projected_state.state === "landed").map(item => ({
      item_id: item.item_id, rank: item.rank ?? "", class: "landed", where: "trunk", why: item.latest_decision?.reason ?? "not recorded",
      mover: "—", since: "not recorded", class_source: "state",
    })) : waiting ?? [];
    return source.filter(row => (preset !== "not-moving" || !["ready", "moving"].includes(row.class)) && (!klass || row.class === klass) && (!reason || row.why === reason))
      .sort(waitingOrder);
  }, [waiting, snapshot, preset, klass, reason]);
  const list = useStableList(rows, waitingIdentity, `${preset}|${klass}|${reason}`);
  useRowKeys(list.visible.map(waitingIdentity), list.focused, list.focus, id => list.visible.find(row => waitingIdentity(row) === id)?.document ? window.location.hash : `#/item/${encodeURIComponent(id)}`);
  const move = async (id: string) => {
    setMoving(id); setMoveError(null);
    try {
      await moveItemToTop(id);
      // The reader's own confirmed move must not wait behind the focused-list pin.
      list.accept();
      await onMoved?.();
    } catch (error) { setMoveError(`${id}: ${error instanceof Error ? error.message : String(error)}`); }
    finally { setMoving(null); }
  };
  const options = preset === "landed" ? rows : waiting ?? [];
  return <main className="home ledger" data-testid="stable-list" ref={list.listRef}>
    <h1>Items</h1>
    {!writesEnabled ? <p>This server is read-only.</p> : null}
    {moveError ? <p role="alert">Move refused: {moveError}</p> : null}
    <div className="ledger__filters">
      {[["not-moving", "not moving"], ["unlanded", "all unlanded"], ["landed", "landed"]].map(([value, label]) => <button key={value} type="button" aria-pressed={preset === value} onClick={() => set("state", value)}>{label}</button>)}
      <label>Class <select aria-label="Class" value={klass} onChange={event => set("class", event.target.value)}><option value="">any</option>{[...new Set([...CLASSES, ...options.map(row => row.class), ...(klass ? [klass] : [])])].map(value => <option key={value}>{value}</option>)}</select></label>
      <label>Reason <select aria-label="Reason" value={reason} onChange={event => set("reason", event.target.value)}><option value="">any</option>{[...new Set([...options.map(row => row.why), ...(reason ? [reason] : [])])].sort().map(value => <option key={value}>{value}</option>)}</select></label>
    </div>
    <div className="ledger__newer">{list.updates ? <button type="button" onClick={list.accept}>{list.newer ? `${list.newer} newer ↑` : `${list.updates} updates ↑`}</button> : null}</div>
    {error ? <p role="alert">Items refused: {error.message}</p> : (preset === "landed" ? snapshot === undefined : waiting === undefined) ? <p role="status">Reading items…</p> : list.visible.length === 0 ? <p>No items match these filters.</p> :
      <table><thead><tr>{["Rank", "Item", "Class", "Where", "Why", "Mover", "Since", "Last run", ...(writesEnabled ? ["Order"] : [])].map(label => <th key={label}>{label}</th>)}</tr></thead><tbody>{list.visible.map(row => {
        const item = snapshot?.items.find(item => item.item_id === row.item_id);
        const identity = waitingIdentity(row);
        return <tr key={identity} data-row-id={identity} tabIndex={0} data-focused={list.focused === identity} onFocus={() => list.focus(identity)}>
          <td>{row.rank || "unranked"}</td><td>{row.document || <EntityLink kind="item" id={row.item_id} />}</td><td><ClassState value={row.class} /></td><td>{row.where}</td><td>{row.why}{row.graph_changes?.map((change, index) => <p key={index}>{change}</p>)}</td>
          <td><EntityText value={row.mover} snapshot={snapshot} rows={waiting} /></td><td>{row.since}</td><td>{item?.latest_run ? <EntityLink kind="run" id={item.latest_run.run_id} state={item.latest_run.state} /> : "—"}</td>
          {writesEnabled ? <td>{!row.document && preset !== "landed" && !["not-started", "landed", "closed", "resolved-elsewhere"].includes(item?.projected_state.raw_state ?? "") ? <button type="button" aria-label={`Move ${row.item_id} to top`} disabled={moving !== null} onClick={() => void move(row.item_id)}>Move to top</button> : null}</td> : null}
        </tr>;
      })}</tbody></table>}
  </main>;
}

export function PassSpend({ pass }: { pass: PassRow }) {
  return <>{([
    ["Spend started in this loop", pass.spend_started_in_loop],
    ["Spend carried over from earlier loops", pass.spend_carried_over],
  ] as const).map(([label, total]) => <p key={label}>{label}: {total ?
    `${total.tokens_unreported ? "≥ " : ""}${total.tokens} tokens · ${total.tokens_unreported} unrecorded runs` : "not recorded"}</p>)}</>;
}

function answer(pass: PassRow): string {
  const decisions = (pass.decisions ?? []).map(decision => [decision.item_id, decision.rank, decision.action, decision.reason, decision.lane_id ?? "", decision.run_id ?? ""]);
  decisions.sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
  return JSON.stringify([decisions, pass.session_id ?? null, pass.stop ?? null, pass.detail ?? null, pass.phases ?? null, pass.spend_started_in_loop ?? null, pass.spend_carried_over ?? null]);
}
export function foldPasses(passes: PassRow[]) {
  const groups: { pass: PassRow; count: number; oldest: number }[] = [];
  for (const pass of newestPasses(passes)) {
    const previous = groups[groups.length - 1];
    if (previous && answer(previous.pass) === answer(pass)) { previous.count++; previous.oldest = pass.dispatched_at_ms; }
    else groups.push({ pass, count: 1, oldest: pass.dispatched_at_ms });
  }
  return groups;
}
function changed(pass: PassRow, older?: PassRow): string {
  if (!older) return "first recorded answer";
  const old = new Map(older.decisions.map(row => [row.item_id, row]));
  const changes = pass.decisions.flatMap(row => {
    const before = old.get(row.item_id);
    old.delete(row.item_id);
    return before && JSON.stringify([before.action, before.reason, before.lane_id, before.run_id]) === JSON.stringify([row.action, row.reason, row.lane_id, row.run_id]) ? [] : [`${row.item_id}: ${before ? `${before.action} · ${before.reason}` : "not present"} → ${row.action} · ${row.reason}`];
  });
  return [...changes, ...[...old.keys()].map(id => `${id}: no longer present`)].join("; ") || "session, stop detail or phases changed";
}
export function PassesView({ passes, error, more = false, loadingOlder = false, onOlder }: { passes?: PassRow[]; error?: Error | null; more?: boolean; loadingOlder?: boolean; onOlder?: () => void }) {
  const { params, set } = useListParams();
  const item = params.get("item") || "";
  const action = params.get("action") || "";
  const session = params.get("session") || "";
  const range = params.get("range") || "24h";
  const showHolds = params.get("holds") !== "hide";
  const ordered = newestPasses(passes ?? []);
  const cutoff = range === "all" ? -Infinity : Date.now() - ({ "24h": 86400000, "7d": 604800000, "30d": 2592000000 }[range] ?? 86400000);
  const filtered = ordered.filter(pass => pass.dispatched_at_ms >= cutoff && (!session || pass.session_id === session) &&
    (!item || pass.decisions.some(row => row.item_id === item)) && (!action || pass.decisions.some(row => row.action === action && (!item || row.item_id === item))) &&
    (showHolds || pass.decisions.some(row => row.action !== "hold")));
  // Fold the complete ledger first: hiding an intervening pass must not manufacture a streak.
  const groups = foldPasses(ordered);
  const allowed = new Set(filtered.map(pass => pass.dispatched_at_ms));
  const matchingGroups = groups.filter(group => allowed.has(group.pass.dispatched_at_ms));
  const list = useStableList(matchingGroups, group => passStamp(group.pass.dispatched_at_ms), `${range}|${item}|${action}|${session}|${showHolds}`);
  useRowKeys(list.visible.map(group => passStamp(group.pass.dispatched_at_ms)), list.focused, list.focus, id => `#/pass/${id}`);
  const latest = ordered[0];
  return <main className="home ledger" data-testid="stable-list" ref={list.listRef}>
    <h1>Passes</h1>
    <div className="ledger__filters"><label>Range <select value={range} onChange={event => set("range", event.target.value)}>{["24h", "7d", "30d", "all"].map(value => <option key={value}>{value}</option>)}</select></label>
      <label>Item <input title="Filters to passes that touched this item" value={item} onChange={event => set("item", event.target.value)} /></label>
      <label>Action <select value={action} onChange={event => set("action", event.target.value)}><option value="">All actions</option>{[...new Set(ordered.flatMap(pass => pass.decisions.map(row => row.action)))].sort().map(value => <option key={value} value={value}>{value}</option>)}</select></label>
      <label><input type="checkbox" checked={showHolds} onChange={event => set("holds", event.target.checked ? "" : "hide")} />show passes where nothing moved</label>
    </div>
    {more ? <p>Older passes have not been loaded; filters search the loaded history. <button type="button" disabled={loadingOlder} onClick={onOlder}>Load older passes</button></p> : null}
    {latest ? <p>Session {latest.session_id || "not recorded"} · {latest.stop || "stop not recorded"} · {latest.detail || "detail not recorded"}</p> : null}
    <div className="ledger__newer">{list.updates ? <button type="button" onClick={list.accept}>{list.newer ? `${list.newer} newer ↑` : `${list.updates} updates ↑`}</button> : null}</div>
    {error ? <p role="alert">Passes refused: {error.message}</p> : passes === undefined ? <p role="status">Reading passes…</p> : list.visible.length === 0 ? <p>No pass in this range.{latest ? <> Newest recorded: <EntityLink kind="pass" id={passStamp(latest.dispatched_at_ms)} /></> : " No pass recorded."}</p> : list.visible.map((group, index) => {
      const { pass, count } = group;
      const id = passStamp(pass.dispatched_at_ms);
      const older = groups[groups.findIndex(row => row.pass.dispatched_at_ms === pass.dispatched_at_ms) + 1]?.pass;
      return <section key={id} data-row-id={id} tabIndex={0} data-testid="pass-group" data-focused={list.focused === id} onFocus={() => list.focus(id)}>
        <EntityLink kind="pass" id={id} /> · {passCounts(pass)} {count > 1 ? `· ×${count} passes since ${passStamp(group.oldest)}, same answer` : null}
        <p>changed: <EntityText value={changed(pass, older)} passes={passes} /></p>
        <p>phases {pass.phases ? Object.entries(pass.phases).map(([name, ms]) => `${name.replace("_ms", "")} ${ms === null ? "—" : `${ms}ms`}`).join(" · ") : "— · no timing row"}</p>
        <PassSpend pass={pass} />
        <details open={index < 2 ? true : undefined}><summary>Decisions</summary><ul>{pass.decisions.filter(row => (!item || row.item_id === item) && (!action || row.action === action)).map((row, i) => <li key={`${row.item_id}-${i}`}>
          {row.action} · <EntityLink kind="item" id={row.item_id} /> · {row.reason} {row.lane_id ? <EntityLink kind="lane" id={row.lane_id} /> : null} {row.run_id ? <EntityLink kind="run" id={row.run_id} /> : null}
        </li>)}</ul></details>
      </section>;
    })}
  </main>;
}
