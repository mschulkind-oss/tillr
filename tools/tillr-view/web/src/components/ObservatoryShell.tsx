import { useEffect, useState, type ReactNode } from "react";
import type { ApiEnvelope, PassRow, SnapshotData } from "../models/types";
import { LoopSummary } from "./Home";
import { BackendAlarms } from "./BackendAlarms";
import { NewIdea } from "./NewIdea";
import { Glossary } from "./Glossary";
import { refusalCode } from "./RefusalState";
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

const NAV = [["Home", "#/"], ["Items", "#/items"], ["Graph", "#/graph"], ["Passes", "#/passes"], ["Runs", "#/runs"], ["Performance", "#/performance"], ["Trunk", "#/trunk"], ["Questions", "#/questions"], ["Designs", "#/designs"]];
export function ObservatoryShell({ children, hash, snapshot, snapshotError, passes, passesError, live, paused, onPause, onRefresh }: {
  children: ReactNode; hash: string; snapshot?: ApiEnvelope<SnapshotData>; snapshotError: Error | null;
  passes?: PassRow[]; passesError?: Error | null; live: boolean; paused: boolean;
  onPause: () => void; onRefresh: () => void;
}) {
  const [clock, setClock] = useState(Date.now);
  useEffect(() => {
    setClock(Date.now());
    const timer = window.setInterval(() => setClock(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [paused]);
  const snapshotMismatch = snapshotError != null && refusalCode(snapshotError) === "unknown-route";
  const pageOwnsPassFailure = ["", "#/", "#/passes"].includes(hash.split("?")[0]);
  const summaryError = passesError && refusalCode(passesError) === "unknown-route" && (pageOwnsPassFailure || snapshotMismatch) ? null : passesError;
  const generated = snapshot ? Date.parse(snapshot.snapshot.generated_at) : NaN;
  const age = Number.isFinite(generated) ? Math.max(0, Math.floor((clock - generated) / 1000)) : null;
  return <Glossary className="observatory-shell">
    <div className="observatory-shell__sticky">
      <BackendAlarms alarms={snapshot?.data.alarms} />
      <header className="observatory-nav">
        <strong>tillr</strong><nav aria-label="Observatory">{NAV.map(([label, url]) => <a key={url} href={url}
          aria-current={(hash || "#/").split("?")[0] === url ? "page" : undefined}>{label}</a>)}</nav>
        <span className="observatory-nav__freshness">{paused ? "paused" : live ? "live" : "polling"} · seq {snapshot?.snapshot.event_head_seq ?? "not recorded"} · {age === null ? "age not recorded" : `${age}s old`}</span>
        <NewIdea key={snapshot?.snapshot.store_path ?? ""} enabled={snapshot?.snapshot.writes_enabled === true} items={snapshot?.data.items ?? []} store={snapshot?.snapshot.store_path ?? ""} onSaved={() => onRefresh()} />
        <button type="button" onClick={onPause}>{paused ? "Resume updates" : "Pause updates"}</button>
        <button type="button" onClick={onRefresh}>Refresh</button>
      </header>
      {snapshotError && !snapshotMismatch ? <p role="alert">Snapshot refused: {snapshotError.message}</p> : null}
      <LoopSummary drain={snapshot?.data.drain_summary} readError={snapshotMismatch ? null : snapshotError} snapshot={snapshot?.data} passes={passes} error={summaryError} now={clock} />
    </div>
    {children}
  </Glossary>;
}
