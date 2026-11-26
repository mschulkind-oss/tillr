import { DocumentIndexNotice } from "./components/DocumentIndexNotice";
import { lazy, Suspense, useEffect, useState } from "react";
import { AlertPage, AlertsLedger } from "./components/AlertsPage";
import { DesignsPage } from "./components/Designers";
import { LanePage } from "./components/LanePage";
import { Home } from "./components/Home";
import { RunsPage } from "./components/RunsPage";
import { AdminPage } from "./components/AdminPage";
import { ItemsView, PassesView } from "./components/ItemsPasses";
import { ItemPage, RunPage } from "./components/ItemRunPages";
import { PerformanceView, CheckView } from "./components/PerformanceView";
import { ObservatoryShell } from "./components/ObservatoryShell";
import { TrunkView, useTrunkRoute } from "./components/TrunkView";
import { QuestionsView } from "./components/QuestionsView";
import { EntityLink, ENTITY_KINDS, type EntityKind } from "./components/EntityLink";
import { RefusalState, refusalCode } from "./components/RefusalState";
import { useAlertsQuery, useLanesQuery, usePassesQuery, usePassHistoryQuery, useQuestionsQuery, useDesignsQuery, useRefreshAll, useSnapshotQuery, useWaitingQuery } from "./models/queries";
import { useObservatorySocket } from "./models/socket";
import { RenderTimer, usePagePerf } from "./models/perfHooks";
import { navigate, useLinkInterception, useScreenRoute } from "./models/route";

const DependencyGraph = lazy(() => import("./components/DependencyGraph").then(module => ({ default: module.DependencyGraph })));

/** Replace old inspection links, preserving their artifact/tab selection. The older
 * query-only item spelling is also accepted; it never opens a second board layout. */
export function App() {
  const route = useScreenRoute(), path = route.split("?")[0];
  useLinkInterception();
  usePagePerf(route);
  const params = new URLSearchParams(route.split("?")[1]);
  const home = path === "/", items = path === "/items", passesOpen = path === "/passes", questionsOpen = path === "/questions";
  const alertsOpen = path === "/alerts", alertOpen = path.startsWith("/alert/");
  const designsOpen = path === "/designs" || path.startsWith("/design/");
  const runsOpen = path === "/runs";
  const adminOpen = path === "/admin" || path.startsWith("/admin/");
  const itemOpen = path.startsWith("/item/"), runOpen = path.startsWith("/run/");
  const kind = path.split("/")[1]; let id = path.split("/").slice(2).join("/");
  try { id = decodeURIComponent(id); } catch { /* The refusal retains the malformed id. */ }
  const [paused, setPaused] = useState(false);
  const { live } = useObservatorySocket({ paused });
  const snapshot = useSnapshotQuery(live, paused), passes = usePassesQuery(true, paused);
  const [historyPages, setHistoryPages] = useState(1);
  const range = params.get("range") || "24h";
  useEffect(() => setHistoryPages(1), [range]);
  const passHistory = usePassHistoryQuery(range, historyPages, passesOpen, paused);
  const waiting = useWaitingQuery(home || items || path === "/graph", paused), questions = useQuestionsQuery(questionsOpen, paused);
  const designs = useDesignsQuery(designsOpen, paused);
  const lanes = useLanesQuery(home || runsOpen || kind === "lane", paused, kind === "lane", kind === "lane" ? id : undefined);
  const alerts = useAlertsQuery({}, live, paused, home);
  const trunk = useTrunkRoute();
  const { refresh } = useRefreshAll();
  const onPause = () => { setPaused(!paused); if (paused) refresh(); };
  let screen;
  if (snapshot.error) screen = <main className="home"><RefusalState code={refusalCode(snapshot.error)} message={snapshot.error.message} onRetry={refresh} /></main>;
  else if (home) screen = <Home lanes={lanes.data?.data} lanesError={lanes.error} snapshot={snapshot.data?.data} waiting={waiting.data?.data} whySource={waiting.data?.whySource} designsWaiting={waiting.data?.designsWaiting} waitingError={waiting.error} passes={passes.data?.data} passesError={passes.error} alerts={alerts.data?.data} alertsError={alerts.error} />;
  else if (alertsOpen) screen = <AlertsLedger route={route} live={live} paused={paused} />;
  else if (alertOpen && id) screen = <AlertPage key={id} id={id} live={live} paused={paused} />;
  else if (kind === "lane" && id) screen = <LanePage key={id} id={id} lanes={lanes.data?.data} error={lanes.error} live={live} paused={paused} configured={snapshot.data?.data.drain_summary.session?.lanes} />;
  else if (adminOpen) screen = <AdminPage route={route} />;
  else if (runsOpen) screen = <RunsPage lanes={lanes.data?.data} lanesError={lanes.error} configured={snapshot.data?.data.drain_summary.session?.lanes} items={snapshot.data?.data.items} />;
  else if (designsOpen) screen = <DesignsPage data={designs.data?.data} error={designs.error} pending={designs.isPending} onRetry={() => void designs.refetch()} path={kind === "design" ? id : undefined} />;
  else if (items) screen = <ItemsView waiting={waiting.data?.data} whySource={waiting.data?.whySource} designsWaiting={waiting.data?.designsWaiting} snapshot={snapshot.data?.data} error={waiting.error ?? snapshot.error} writesEnabled={snapshot.data?.snapshot.writes_enabled === true} onMoved={() => Promise.all([waiting.refetch(), snapshot.refetch()])} />;
  else if (path === "/graph") screen = <Suspense fallback={<main className="entity-page"><h1>Dependency graph</h1><p>Loading graph…</p></main>}><DependencyGraph items={snapshot.data?.data.items} waiting={waiting.data?.data} selected={params.get("item") ?? undefined} error={waiting.error} /></Suspense>;
  else if (passesOpen) screen = <PassesView passes={passHistory.data?.data.rows} error={passHistory.error} more={passHistory.data?.data.more} loadingOlder={passHistory.isFetching} onOlder={() => setHistoryPages(value => value + 1)} />;
  else if (itemOpen && id) screen = <ItemPage key={id} id={id} live={live} paused={paused} row={snapshot.data?.data.items.find(item => item.item_id === id)} baseURL={snapshot.data?.snapshot.vantage_base_url ?? ""} />;
  else if (runOpen && id) screen = <RunPage key={id} id={id} route={route} live={live} paused={paused} />;
  else if (path === "/performance") screen = <PerformanceView route={route} snapshot={snapshot.data} snapshotError={snapshot.error} paused={paused} />;
  else if (kind === "check" && id) screen = <CheckView route={route} paused={paused} />;
  else if (trunk) screen = <TrunkView route={trunk} paused={paused} live={live} />;
  else if (questionsOpen) screen = <QuestionsView data={questions.data} error={questions.error} pending={questions.isPending} refreshing={questions.isFetching && !questions.isPending} onRetry={() => void questions.refetch()} onClose={() => navigate("/")} baseUrl={snapshot.data?.snapshot.vantage_base_url ?? ""} items={snapshot.data?.data.items ?? []} onSelectItem={item => navigate(`/item/${encodeURIComponent(item)}`)} />;
  else screen = <main className="home"><h1>Page unavailable</h1><p role="alert">This page is not built yet: {ENTITY_KINDS.includes(kind as EntityKind) && id ? <EntityLink kind={kind as EntityKind} id={id} /> : route}.</p><a href="/">Return to Home</a></main>;
  return <ObservatoryShell route={route} snapshot={snapshot.data} snapshotError={snapshot.error} passes={passes.data?.data} passesError={passes.error} live={live} paused={paused} onPause={onPause} onRefresh={refresh}><RenderTimer id="screen">{(home || items || path === "/graph") && <DocumentIndexNotice status={waiting.data?.documentIndex} />}{screen}</RenderTimer></ObservatoryShell>;
}
