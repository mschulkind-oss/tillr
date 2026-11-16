import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchLanding, fetchLandings } from "../models/api";
import type { LandingRow } from "../models/types";
import { FALLBACK_POLL_MS, queryKeys } from "../models/queries";
import { RefusalState, refusalCode } from "./RefusalState";

export function useTrunkRoute() {
  const read = () => window.location.hash;
  const [hash, setHash] = useState(read);
  useEffect(() => {
    const update = () => setHash(read());
    window.addEventListener("hashchange", update);
    window.addEventListener("popstate", update);
    return () => {
      window.removeEventListener("hashchange", update);
      window.removeEventListener("popstate", update);
    };
  }, []);
  return hash === "#/trunk" || hash.startsWith("#/trunk?") || hash.startsWith("#/landing/") ? hash : null;
}

const itemLink = (item: string) => `#/item/${encodeURIComponent(item)}`;
const runLink = (item: string, run: string) => `${itemLink(item)}?run=${encodeURIComponent(run)}`;

function LandingFacts({ row }: { row: LandingRow }) {
  return <>
    <p>{row.result}{row.reason ? ` — ${row.reason}` : ""}</p>
    <p>{row.at_ms === null ? "Clock unrecorded" : new Date(row.at_ms).toISOString()} · {row.epistemic_state}</p>
    {row.trunk_before || row.trunk_after ? <p><code>{row.trunk_before}</code> → <code>{row.trunk_after}</code></p> : null}
    <div className="trunk__links">
      {row.item_id ? <a href={itemLink(row.item_id)}>Item {row.item_id}</a> : null}
      {row.run_id && row.item_id ? <a href={runLink(row.item_id, row.run_id)}>Run {row.run_id}</a> : null}
      {row.pass_id ? <a href={`#/pass/${encodeURIComponent(row.pass_id)}`}>Pass {row.pass_id}</a> : <span>Pass unrecorded</span>}
      {row.fix_run_id && row.item_id ? <a href={runLink(row.item_id, row.fix_run_id)}>Fix run {row.fix_run_id}</a> : null}
      {row.fixer_item ? <a href={itemLink(row.fixer_item)}>Fixer item {row.fixer_item}</a> : null}
      {row.red_sha ? <a href={`#/trunk?sha=${encodeURIComponent(row.red_sha)}`}>Red commit {row.red_sha}</a> : null}
    </div>
    {row.conflict_paths?.length ? <p>Conflicts: {row.conflict_paths.join(", ")}</p> : null}
    {row.failing_checks?.length ? <p>Failing checks: {row.failing_checks.join(", ")}</p> : null}
    {row.gated_tree || row.landed_tree ? <p>Gated tree: <code>{row.gated_tree || "unrecorded"}</code><br />Landed tree: <code>{row.landed_tree || "unrecorded"}</code>{row.gated_tree && row.landed_tree ? ` · ${row.gated_tree === row.landed_tree ? "same tree" : "different trees"}` : ""}</p> : null}
    {row.gate_skipped_reason ? <p>Gate skipped: {row.gate_skipped_reason}</p> : null}
  </>;
}

export function TrunkView({ route, paused, live }: { route: string; paused: boolean; live: boolean }) {
  const id = route.startsWith("#/landing/") ? decodeURIComponent(route.slice("#/landing/".length)) : null;
  const commit = id === null ? new URLSearchParams(route.split("?")[1] ?? "").get("sha") : null;
  // Refusal events can add a repair link after the landing was recorded. The
  // socket invalidates the ledger; without it, the open screen polls.
  const history = useQuery({ queryKey: queryKeys.landings(), queryFn: ({ signal }) => fetchLandings(signal), enabled: id === null, refetchInterval: paused || live ? false : FALLBACK_POLL_MS, retry: false, refetchOnWindowFocus: false });
  const detail = useQuery({ queryKey: queryKeys.landing(id ?? ""), queryFn: ({ signal }) => fetchLanding(id as string, signal), enabled: id !== null, refetchInterval: paused || live ? false : FALLBACK_POLL_MS, retry: false, refetchOnWindowFocus: false });
  const query = id === null ? history : detail;
  const rows = history.data?.data.rows ?? [];
  const shown = commit ? rows.filter(row => row.red_sha === commit || row.trunk_before === commit || row.trunk_after === commit) : rows;
  return <main className="trunk" aria-label={id ? "Landing detail" : "Trunk"}>
    <div className="trunk__head">
      <h2>{id ? `Landing ${id}` : commit ? `Trunk commit ${commit}` : "Trunk"}</h2>
      <a href="#/">Back to the board</a>
      {id || commit ? <a href="#/trunk">All landings</a> : null}
      <button className="btn" type="button" onClick={() => void query.refetch()}>Refresh</button>
    </div>
    {query.error ? <RefusalState code={refusalCode(query.error)} message={query.error.message} sentences={{}} onRetry={() => void query.refetch()} /> : query.isPending ? <p role="status">Reading landings…</p> : id ? <LandingFacts row={detail.data!.data} /> : <>
      <p>Newest first. Clocks come from recorded events; gaps have no inferred time.</p>
      {commit ? <p>Recorded relationships for this commit. To inspect its Git contents: <code>git show {commit}</code></p> : null}
      {shown.length === 0 ? <p>{commit ? "No recorded relationships for this commit." : "No landings recorded."}</p> : null}
      <ol className="trunk__rows">{[...shown].reverse().map((row, index) => <li className={`trunk__row trunk__row--${row.kind}`} key={row.landing_id ?? `${row.kind}-${row.event_seq}-${index}`}>
        {row.kind === "gap" ? <p>Unrecorded trunk move: <code>{row.trunk_before}</code> → <code>{row.trunk_after}</code> · clock unrecorded</p> : <>
          {row.landing_id ? <h3><a href={`#/landing/${encodeURIComponent(row.landing_id)}`}>{row.landing_id}</a></h3> : <h3>Trunk-red acquittal</h3>}
          <LandingFacts row={row} />
        </>}
      </li>)}</ol>
    </>}
  </main>;
}
