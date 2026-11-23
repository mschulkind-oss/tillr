import { lazy, Suspense, useEffect, useState } from "react";
import { DesignsPage } from "./components/Designers";
import { LanePage } from "./components/LanePage";
import { Home } from "./components/Home";
import { ItemsView, PassesView } from "./components/ItemsPasses";
import { ItemPage, RunPage } from "./components/ItemRunPages";
import { PerformanceView, CheckView } from "./components/PerformanceView";
import { ObservatoryShell, useScreenHash } from "./components/ObservatoryShell";
import { TrunkView, useTrunkRoute } from "./components/TrunkView";
import { QuestionsView } from "./components/QuestionsView";
import { EntityLink, ENTITY_KINDS, type EntityKind } from "./components/EntityLink";
import { RefusalState, refusalCode } from "./components/RefusalState";
import { useLanesQuery, usePassesQuery, usePassHistoryQuery, useQuestionsQuery, useDesignsQuery, useRefreshAll, useSnapshotQuery, useWaitingQuery } from "./models/queries";
import { useObservatorySocket } from "./models/socket";
import { RenderTimer, usePagePerf } from "./models/perfHooks";
import { URL_CHANGE_EVENT } from "./models/url";

const DependencyGraph = lazy(() => import("./components/DependencyGraph").then(module => ({ default: module.DependencyGraph })));

/** Replace old inspection links, preserving their artifact/tab selection. The older
 * query-only item spelling is also accepted; it never opens a second board layout. */
export function canonicalRoute(hash: string, search: string): string | null {
  const params = new URLSearchParams(hash.split("?")[1]);
  if (hash.startsWith("#/item/") && params.get("run")) {
    const id = params.get("run")!; params.delete("run");
    return `#/run/${encodeURIComponent(id)}${params.size ? `?${params}` : ""}`;
  }
  const legacy = new URLSearchParams(search);
  if ((!hash || hash === "#/") && legacy.get("item")) {
    const item = legacy.get("item")!; legacy.delete("item");
    return canonicalRoute(`#/item/${encodeURIComponent(item)}?${legacy}`, "") ?? `#/item/${encodeURIComponent(item)}`;
  }
  return null;
}
export function App() {
  const hash = useScreenHash(), path = hash.split("?")[0];
  usePagePerf(hash);
  const params = new URLSearchParams(hash.split("?")[1]);
  const home = !hash || path === "#/", items = path === "#/items", passesOpen = path === "#/passes", questionsOpen = path === "#/questions";
  const designsOpen = path === "#/designs" || path.startsWith("#/design/");
  const itemOpen = path.startsWith("#/item/"), runOpen = path.startsWith("#/run/");
  const kind = path.split("/")[1]; let id = path.split("/").slice(2).join("/");
  try { id = decodeURIComponent(id); } catch { /* The refusal retains the malformed id. */ }
  useEffect(() => {
    const target = canonicalRoute(window.location.hash, window.location.search);
    if (!target) return;
    const search = new URLSearchParams(window.location.search); for (const key of ["item", "run", "tab", "exchange", "artifact"]) search.delete(key);
    window.history.replaceState(null, "", `${window.location.pathname}${search.size ? `?${search}` : ""}${target}`);
    window.dispatchEvent(new Event(URL_CHANGE_EVENT));
  }, [hash]);
  const [paused, setPaused] = useState(false);
  const { live } = useObservatorySocket({ paused });
  const snapshot = useSnapshotQuery(live, paused), passes = usePassesQuery(true, paused);
  const [historyPages, setHistoryPages] = useState(1);
  const range = params.get("range") || "24h";
  useEffect(() => setHistoryPages(1), [range]);
  const passHistory = usePassHistoryQuery(range, historyPages, passesOpen, paused);
  const waiting = useWaitingQuery(home || items || path === "#/graph", paused), questions = useQuestionsQuery(questionsOpen, paused);
  const designs = useDesignsQuery(designsOpen, paused);
  const lanes = useLanesQuery(home || kind === "lane", paused, kind === "lane", kind === "lane" ? id : undefined);
  const trunk = useTrunkRoute();
  const { refresh } = useRefreshAll();
  const onPause = () => { setPaused(!paused); if (paused) refresh(); };
  let screen;
  if (snapshot.error) screen = <main className="home"><RefusalState code={refusalCode(snapshot.error)} message={snapshot.error.message} onRetry={refresh} /></main>;
  else if (home) screen = <Home lanes={lanes.data?.data} lanesError={lanes.error} snapshot={snapshot.data?.data} waiting={waiting.data?.data} waitingError={waiting.error} passes={passes.data?.data} passesError={passes.error} />;
  else if (kind === "lane" && id) screen = <LanePage key={id} id={id} lanes={lanes.data?.data} error={lanes.error} live={live} paused={paused} />;
  else if (designsOpen) screen = <DesignsPage data={designs.data?.data} error={designs.error} pending={designs.isPending} onRetry={() => void designs.refetch()} path={kind === "design" ? id : undefined} />;
  else if (items) screen = <ItemsView waiting={waiting.data?.data} snapshot={snapshot.data?.data} error={waiting.error ?? snapshot.error} writesEnabled={snapshot.data?.snapshot.writes_enabled === true} onMoved={() => Promise.all([waiting.refetch(), snapshot.refetch()])} />;
  else if (path === "#/graph") screen = <Suspense fallback={<main className="entity-page"><h1>Dependency graph</h1><p>Loading graph…</p></main>}><DependencyGraph items={snapshot.data?.data.items} waiting={waiting.data?.data} selected={params.get("item") ?? undefined} error={waiting.error} /></Suspense>;
  else if (passesOpen) screen = <PassesView passes={passHistory.data?.data.rows} error={passHistory.error} more={passHistory.data?.data.more} loadingOlder={passHistory.isFetching} onOlder={() => setHistoryPages(value => value + 1)} />;
  else if (itemOpen && id) screen = <ItemPage key={id} id={id} live={live} paused={paused} row={snapshot.data?.data.items.find(item => item.item_id === id)} baseURL={snapshot.data?.snapshot.vantage_base_url ?? ""} />;
  else if (runOpen && id) screen = <RunPage key={id} id={id} route={hash} live={live} paused={paused} />;
  else if (path === "#/performance") screen = <PerformanceView route={hash} snapshot={snapshot.data} snapshotError={snapshot.error} paused={paused} />;
  else if (kind === "check" && id) screen = <CheckView route={hash} paused={paused} />;
  else if (trunk) screen = <TrunkView route={trunk} paused={paused} live={live} />;
  else if (questionsOpen) screen = <QuestionsView data={questions.data} error={questions.error} pending={questions.isPending} refreshing={questions.isFetching && !questions.isPending} onRetry={() => void questions.refetch()} onClose={() => { window.location.hash = ""; }} baseUrl={snapshot.data?.snapshot.vantage_base_url ?? ""} items={snapshot.data?.data.items ?? []} onSelectItem={item => { window.location.hash = `#/item/${encodeURIComponent(item)}`; }} />;
  else screen = <main className="home"><h1>Page unavailable</h1><p role="alert">This page is not built yet: {ENTITY_KINDS.includes(kind as EntityKind) && id ? <EntityLink kind={kind as EntityKind} id={id} /> : hash}.</p><a href="#/">Return to Home</a></main>;
  return <ObservatoryShell hash={hash} snapshot={snapshot.data} snapshotError={snapshot.error} passes={passes.data?.data} passesError={passes.error} live={live} paused={paused} onPause={onPause} onRefresh={refresh}><RenderTimer id="screen">{screen}</RenderTimer></ObservatoryShell>;
}
