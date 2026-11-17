import { useEffect, useState, type ReactNode } from "react";
import type { ApiEnvelope, PassRow, SnapshotData } from "../models/types";
import { LoopSummary } from "./Home";
import { URL_CHANGE_EVENT } from "../models/url";

export function useScreenHash(): string {
  const [hash, setHash] = useState(() => window.location.hash);
  useEffect(() => {
    const changed = () => setHash(window.location.hash);
    window.addEventListener("hashchange", changed);
    window.addEventListener("popstate", changed);
    window.addEventListener(URL_CHANGE_EVENT, changed);
    return () => {
      window.removeEventListener("hashchange", changed);
      window.removeEventListener("popstate", changed);
      window.removeEventListener(URL_CHANGE_EVENT, changed);
    };
  }, []);
  return hash;
}

const NAV = [["Home", "#/"], ["Items", "#/items"], ["Passes", "#/passes"], ["Runs", "#/runs"], ["Performance", "#/performance"], ["Trunk", "#/trunk"], ["Questions", "#/questions"], ["Designs", "#/designs"]];
export function ObservatoryShell({ children, hash, snapshot, snapshotError, passes, passesError, live, paused, onPause, onRefresh }: {
  children: ReactNode; hash: string; snapshot?: ApiEnvelope<SnapshotData>; snapshotError: Error | null;
  passes?: PassRow[]; passesError?: Error | null; live: boolean; paused: boolean;
  onPause: () => void; onRefresh: () => void;
}) {
  const [clock, setClock] = useState(Date.now);
  const [frozenAt, setFrozenAt] = useState<number | null>(null);
  useEffect(() => {
    if (paused) { setFrozenAt(Date.now()); return; }
    setFrozenAt(null);
    setClock(Date.now());
    const timer = window.setInterval(() => setClock(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [paused]);
  const generated = snapshot ? Date.parse(snapshot.snapshot.generated_at) : NaN;
  const age = Number.isFinite(generated) ? Math.max(0, Math.floor((clock - generated) / 1000)) : null;
  return <div className="observatory-shell">
    <div className="observatory-shell__sticky">
      <header className="observatory-nav">
        <strong>tillr</strong><nav aria-label="Observatory">{NAV.map(([label, url]) => <a key={url} href={url}
          aria-current={(hash || "#/").split("?")[0] === url ? "page" : undefined}>{label}</a>)}</nav>
        <span className="observatory-nav__freshness">{paused ? "paused" : live ? "live" : "polling"} · seq {snapshot?.snapshot.event_head_seq ?? "not recorded"} · {age === null ? "age not recorded" : `${age}s old`}</span>
        <button type="button" onClick={onPause}>{paused ? "Resume updates" : "Pause updates"}</button>
        <button type="button" onClick={onRefresh}>Refresh</button>
      </header>
      {snapshotError ? <p role="alert">Snapshot refused: {snapshotError.message}</p> : null}
      <LoopSummary drain={snapshot?.data.drain_summary} snapshot={snapshot?.data} passes={passes} error={passesError} now={paused ? frozenAt ?? clock : clock} />
    </div>
    {children}
  </div>;
}
