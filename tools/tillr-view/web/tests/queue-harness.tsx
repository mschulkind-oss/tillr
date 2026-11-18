// The retained queue components' tests use this isolated composition, not a second
// production route. The application now renders full entity pages.
import { useRef, useState } from "react";
import { QueueRail } from "../src/components/QueueRail";
import { ObservatoryHeaderBar } from "../src/components/ObservatoryHeaderBar";
import { archiveItems, finishedCount, hiddenCount, isTerminalState, matchesBoardFilter, queueItems, queueOrder, recentlyFinished, toggleState } from "../src/models/board";
import { useSnapshotQuery, useRefreshAll } from "../src/models/queries";
import { useObservatorySocket } from "../src/models/socket";
import { useUrlFilter, useUrlSelection } from "../src/models/url";

export function QueueHarness() {
  const [paused, setPaused] = useState(false), [pausedAt, setPausedAt] = useState<number | null>(null);
  const { live } = useObservatorySocket({ paused });
  const snapshot = useSnapshotQuery(live, paused), { refresh, refreshing } = useRefreshAll();
  const [filter, setFilter] = useUrlFilter(), [selection, setSelection] = useUrlSelection();
  const ref = useRef<HTMLInputElement>(null);
  const all = queueOrder(snapshot.data?.data.items ?? []);
  const view = filter.view === "archive" ? "archive" : "queue";
  const rows = view === "archive" ? archiveItems(all) : queueItems(all);
  const filtered = rows.filter(row => matchesBoardFilter(row, filter));
  const clear = () => setFilter({ states: [], text: "", view: filter.view });
  const setView = (next: "archive" | "queue") => setFilter({ ...filter, view: next });
  return <>
    <ObservatoryHeaderBar snapshot={snapshot.data} error={snapshot.error} live={live} onRefresh={refresh} refreshing={refreshing} paused={paused} onPause={() => { setPaused(true); setPausedAt(Date.now()); }} onResume={() => { setPaused(false); setPausedAt(null); refresh(); }} readAtMs={snapshot.dataUpdatedAt} filter={filter} shown={filtered.length} onToggleState={state => { const states = toggleState(filter.states, state); setFilter({ ...filter, states, view: states.length > filter.states.length ? isTerminalState(state) ? "archive" : "queue" : filter.view }); }} onClearFilter={clear} view={view} onSelectView={setView} viewTotal={rows.length} />
    <QueueRail items={snapshot.data ? filtered : undefined} total={rows.length} selectedItem={selection.item} onSelectItem={item => setSelection({ item })} filter={filter} onFilterChange={text => setFilter({ ...filter, text })} onClearFilter={clear} hidden={hiddenCount(rows, filter)} filterRef={ref} storeAbsent={false} snapshot={snapshot.data?.data} view={view} onSelectView={setView} finished={finishedCount(all)} storeTotal={all.length} otherMatches={(view === "archive" ? queueItems(all) : archiveItems(all)).filter(row => matchesBoardFilter(row, filter)).length} recent={recentlyFinished(all,5)} now={paused ? pausedAt ?? undefined : undefined} />
  </>;
}
