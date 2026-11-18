// The board's header: the store's identity, the counts an operator reads first, and the one
// line saying what is moving this screen. Every number here comes from the snapshot's
// envelope or its drain_summary; a value the server sent as null is printed as a named
// absence, and a screen with no live channel says so before its numbers look current.

import { EpistemicBadge } from "./EpistemicBadge";
import { loopAlive } from "../models/loop";
import { StateGlyph } from "./StateGlyph";
import {
  FILTER_STATES,
  TERMINAL_STATES,
  boardCounts,
  filterActive,
  filterStatesLabel,
  seconds,
  stateLabel,
  storeState,
} from "../models/board";
import { useNow } from "../models/clock";
import { durationText } from "../models/now";
import { FALLBACK_POLL_MS } from "../models/queries";
import type { BoardCounts, BoardFilter, BoardView } from "../models/board";
import type { ApiEnvelope, LaneView, SnapshotData } from "../models/types";

export interface ObservatoryHeaderBarProps {
  snapshot: ApiEnvelope<SnapshotData> | undefined;
  error: Error | null;
  onRefresh: () => void;
  refreshing: boolean;
  /** Whether the live channel is up. When it is not, the screen is polling, and saying so
   * is the difference between a stale screen and a screen that claims to be live. */
  live: boolean;
  /** The operator's pause (design §3.3). While it is on, nothing automatic updates the
   * screen — not the live channel's invalidations, not the polling timer — and the header
   * has to say so before its own numbers look current. */
  paused?: boolean;
  onPause?: () => void;
  /** Resuming re-reads once, because nothing else will have updated the screen. */
  onResume?: () => void;
  /** When the oldest read still on screen was answered, in the viewer's clock. The paused
   * header reports the age from it; a zero means no read is recorded yet. */
  readAtMs?: number;
  /** Opens the queue rail where it is a drawer (narrow windows). */
  onToggleRail?: () => void;
  /** The board's narrowing. Every state count is its control (design §4.4), so the header
   * is where the operator narrows the queue — and where the board says what that hid. */
  filter: BoardFilter;
  /** How many rows the narrowing left on screen. The whole-board total is `counts.total`,
   * which stays visible: the board reports the narrowed set, never a shrunken board. */
  shown: number;
  onToggleState: (state: string) => void;
  onClearFilter: () => void;
  /** The planner's own count of items it holds for a question, or null when the server does
   * not report it. This is the header's Questions control; the list itself is the view. */
  questions?: number | null;
  /** Whether the questions view is the screen on show. */
  questionsOpen?: boolean;
  onOpenQuestions?: () => void;
  /** Which half of the store the rail is drawing. The finished count is always visible;
   * when the archive is open, its control says so (design §4.5). */
  view?: BoardView;
  onSelectView?: (view: BoardView) => void;
  /** Rows the current view carries before the filter, so the one line about a narrowing
   * reports the view's inventory rather than the whole store's. */
  viewTotal?: number;
}

/** The live channel's own line: which transport is moving this screen, in the same place
 * whatever else the header could not read. A paused screen says `paused` and not `live`,
 * because the chip answers "is this screen moving", which is the operator's question.
 */
function LiveChip({ live, paused }: { live: boolean; paused: boolean }) {
  if (paused) {
    return (
      <span
        className="live-chip live-chip--paused"
        title="every automatic update is stopped; the screen is exactly as you left it"
      >
        <span className="live-chip__dot" />
        paused
      </span>
    );
  }
  return (
    <span
      className={live ? "live-chip live-chip--live" : "live-chip live-chip--down"}
      title={live ? "pushed by the live channel" : `live channel down — re-reading every ${FALLBACK_POLL_MS / 1000}s`}
    >
      <span className="live-chip__dot" />
      {live ? "live" : `every ${FALLBACK_POLL_MS / 1000}s`}
    </span>
  );
}

/** The loop is alive only while its recorded session meets the next-pass deadline.
 * Runs and leases can outlive the loop and must never stand in for its heartbeat. */
export function LoopBand({ drain, now: suppliedNow }: { drain: SnapshotData["drain_summary"]; now?: number }) {
  const clock = useNow();
  const now = suppliedNow ?? clock;
  const session = drain.session;
  const alive = loopAlive(drain, now);
  const stop = alive ? null : drain.stop;
  const label = alive ? "Loop alive" : "no loop is running";
  return <span className="loop-band" title={drain.session_note}>
    {label}{alive && session ? ` · next pass ${new Date(session.next_pass_at_ms).toLocaleTimeString()}` : null}
    {stop ? <> · stopped since {new Date(stop.at_ms).toISOString()} · {stop.item_id || "no single item (loop-wide)"} · {stop.step} · {stop.sentence} Next: {stop.next}</> : !alive ? <> · {session?.ended_at_ms != null ? `Loop stopped since ${new Date(session.ended_at_ms).toISOString()}` : "Loop unobserved"} · stop explanation not recorded</> : null}
  </span>;
}

/** LanePill is the header's always-visible lane state: how many of the store's lanes have
 * running work, out of how many the store knows. Settling leases are not running work. It is drawn even when the answer is zero,
 * which is the whole point: a stopped board says `0/1 lanes running · idle` instead of
 * leaving the operator to infer an idle drain from a `working` glyph over no live run. It
 * sits beside the R37 projection fix rather than replacing the NowBand, which stays absent
 * when there is no live run (design §4.4, ledger `D-OBS12`). */
function LanePill({ lanes }: { lanes?: LaneView[] }) {
  if (lanes === undefined) {
    return (
      <span
        className="lane-pill lane-pill--unknown"
        title="the first read of the store is still in flight, so the lane count is unknown"
      >
        <span className="lane-pill__dot" />—/— lanes running
      </span>
    );
  }
  const ordinary = lanes.filter(lane => !lane.lane_id.startsWith("express-") && !lane.lane_id.startsWith("desk-"));
  const express = lanes.filter(lane => lane.lane_id.startsWith("express-"));
  const total = ordinary.length;
  if ([...ordinary, ...express].some(lane => lane.work_state === undefined && lane.state === "leased")) {
    return <span className="lane-pill lane-pill--unknown" title="this server records leases but not whether their runs are still running or settling">
      <span className="lane-pill__dot" />—/— lanes running · work state unrecorded
    </span>;
  }
  const running = ordinary.filter(lane => lane.work_state === "running").length;
  const expressRunning = express.filter(lane => lane.work_state === "running").length;
  const idle = running + expressRunning === 0;
  return (
    <span
      className={idle ? "lane-pill lane-pill--idle" : "lane-pill lane-pill--running"}
      title={
        idle
          ? `ordinary and express pools are idle (${total} ordinary, ${express.length} express); settling work and document desks are counted separately`
          : `${running} of ${total} ordinary lanes, ${expressRunning} of ${express.length} express lanes have running work`
      }
    >
      <span className="lane-pill__dot" />
      {express.length ? `${running} of ${total} running, ${expressRunning} of ${express.length} express` : `${running}/${total} lanes running`}{idle ? " · idle" : ""}
    </span>
  );
}

/** How old the oldest read on screen is, ticking while the pause is on. It is a duration,
 * not a timestamp: "42s" is what the operator needs to judge the screen, and it has to keep
 * counting or the pause would look like a fresh read that stopped changing. */
function PausedFor({ readAtMs }: { readAtMs: number | undefined }) {
  const now = useNow();
  if (readAtMs === undefined || readAtMs <= 0) {
    return <span className="head-paused__age">no read recorded</span>;
  }
  return (
    <span className="head-paused__age" data-age={Math.max(0, now - readAtMs)}>
      {durationText(Math.max(0, now - readAtMs))}
    </span>
  );
}

function LiveSentence({
  live,
  paused,
  readAtMs,
}: {
  live: boolean;
  paused: boolean;
  readAtMs?: number;
}) {
  if (paused) {
    return (
      <span className="head-artifact head-paused">
        Updates: paused — the oldest read on screen is <PausedFor readAtMs={readAtMs} /> old;
        resume to go live
      </span>
    );
  }
  return (
    <span className="head-artifact">
      {live
        ? "Updates: pushed by the live channel"
        : `Updates: live channel down — re-reading every ${FALLBACK_POLL_MS / 1000}s`}
    </span>
  );
}

/** The pause control and the explicit refresh, in the row beside the live chip: the chip
 * says what is moving the screen, and this is the way to stop it or read it now by hand. */
function PauseControl({
  paused,
  onPause,
  onResume,
  onRefresh,
  refreshing,
}: {
  paused: boolean;
  onPause?: () => void;
  onResume?: () => void;
  onRefresh: () => void;
  refreshing: boolean;
}) {
  return (
    <>
      {paused ? (
        <button type="button" className="btn btn--paused" onClick={onResume}>
          Resume
        </button>
      ) : (
        <button
          type="button"
          className="btn"
          onClick={onPause}
          title="stop every automatic update, so the screen holds still while you read"
        >
          Pause
        </button>
      )}
      <button type="button" className="btn" onClick={onRefresh} disabled={refreshing}>
        {refreshing ? "Refreshing…" : "Refresh Snapshot"}
      </button>
    </>
  );
}

/** The tone a state's number is drawn in. Only the four the design colours have one; the
 * rest keep the neutral value so a new state is legible before it is styled. */
const STATE_TONES: Record<string, "queue" | "blocked" | "landed" | "working"> = {
  in_queue: "queue",
  working: "working",
  blocked: "blocked",
  landed: "landed",
};

/** One count. It is a control when `onClick` is given — the design's rule is that each
 * count *is* a filter — and the same box on screen either way, because a header whose
 * numbers change shape when they become clickable is a header that has to be re-read. */
function Stat({
  label,
  value,
  tone,
  state,
  title,
  active,
  onClick,
}: {
  label: string;
  value: number | string;
  tone?: "queue" | "blocked" | "landed" | "working";
  /** The projected state this count counts, when it counts one. A count that is not a state
   * (the running total, the item total) has none, and draws no glyph rather than borrowing
   * a state's shape. */
  state?: string;
  title?: string;
  /** Whether this count's state is one of the active filters. */
  active?: boolean;
  onClick?: () => void;
}) {
  const className = [
    "stat",
    tone ? `stat--${tone}` : "",
    onClick ? "stat--filter" : "",
    active ? "stat--active" : "",
  ]
    .filter(Boolean)
    .join(" ");
  const body = (
    <>
      <span className="stat__label">
        {state === undefined ? null : <StateGlyph state={state} />}
        {label}
      </span>
      <span className="stat__value">{value}</span>
    </>
  );
  if (!onClick) {
    return (
      <div className={className} title={title}>
        {body}
      </div>
    );
  }
  return (
    <button
      type="button"
      className={className}
      title={title}
      aria-pressed={active === true}
      onClick={onClick}
    >
      {body}
    </button>
  );
}

/** Stats is the header's counts, and every projected state is a filter over the rail: the
 * five the design names are always drawn (a zero is a fact about the board), and a state
 * the store grew that this build has no box for is drawn from `counts.states`, so the
 * header's numbers still add up to the items it is describing. */
function Stats({
  counts,
  drain,
  filter,
  onToggleState,
  questions,
  questionsOpen,
  onOpenQuestions,
  view,
  onSelectView,
}: {
  counts: BoardCounts;
  drain: SnapshotData["drain_summary"] | undefined;
  filter: BoardFilter;
  onToggleState: (state: string) => void;
  questions?: number | null;
  questionsOpen?: boolean;
  onOpenQuestions?: () => void;
  view: BoardView;
  onSelectView?: (view: BoardView) => void;
}) {
  const others = Object.entries(counts.states)
    .filter(([state, n]) => n > 0 && !(FILTER_STATES as readonly string[]).includes(state))
    .sort(([a], [b]) => a.localeCompare(b));
  const filterable = [...FILTER_STATES, ...others.map(([state]) => state)];
  const finished = TERMINAL_STATES.reduce((sum, state) => sum + (counts.states[state] ?? 0), 0);
  return (
    <div className="stats" role="group" aria-label={`Board counts, ${counts.total} items`}>
      {filterable.map((state) => {
        const n = counts.states[state] ?? 0;
        const active = filter.states.includes(state);
        return (
          <Stat
            key={state}
            label={stateLabel(state)}
            value={n}
            tone={STATE_TONES[state]}
            state={state}
            active={active}
            onClick={() => onToggleState(state)}
            title={`${n} item${n === 1 ? "" : "s"} the store projects as ${stateLabel(state)} — ${active ? "click to drop this filter" : "click to filter the queue to them"}`}
          />
        );
      })}
      {/* Finished is every terminal state in one number, and it is what the archive is:
       * the count stays on the header while the queue is on screen, and clicking it opens
       * the view (design §4.5). */}
      <Stat
        label="Finished"
        value={finished}
        tone="landed"
        active={view === "archive"}
        onClick={
          onSelectView === undefined
            ? undefined
            : () => onSelectView(view === "archive" ? "queue" : "archive")
        }
        title={`${finished} item${finished === 1 ? "" : "s"} reached a terminal state — ${
          view === "archive" ? "click to go back to the queue" : "click to open the archive"
        }`}
      />
      <Stat
        label="Running"
        value={counts.running}
        tone="working"
        title={
          drain
            ? `${drain.active ? "the loop is active" : "the loop is idle"} — live runs, which is not an item state and so is not a filter`
            : "no drain summary"
        }
      />
      <Stat
        label="Items"
        value={counts.total}
        title="every item the snapshot carries, which is the sum of the states the store reports"
      />
      <Stat
        label="Questions"
        value={questions === null || questions === undefined ? "—" : questions}
        tone="blocked"
        active={questionsOpen}
        onClick={onOpenQuestions}
        title={
          questions === null || questions === undefined
            ? "this server does not report the planner's question holds, so the count is unknown rather than zero"
            : `${questions} item${questions === 1 ? "" : "s"} the planner holds for a question waiting on you — click to open the list`
        }
      />
    </div>
  );
}

/** GateLine is the gate's own wall time for the last tick, and the trend across ticks.
 *
 * It is deliberately the gate's number and not the tick's: staging, the lane's run and the
 * landing are measured beside it, so a board that showed one confusing number could not tell
 * a slow model from a slow gate. A bar over the budget is drawn as an overrun; the trend is
 * the recent ticks, oldest first, and its label names the worst.
 *
 * A store holding no drain_ticks row (written before the gate was measured) is a named
 * absence, never a zero-length bar: zero would read as a fast gate. */
function GateLine({ drain }: { drain: SnapshotData["drain_summary"] | undefined }) {
  const measured =
    drain && typeof drain.gate_ms === "number" ? drain.gate_ms : null;
  if (!drain || measured === null) {
    return <span className="head-gate head-gate--none">Gate: no tick has measured the gate yet</span>;
  }
  const trend = drain.gate_trend ?? [];
  const budget = typeof drain.gate_budget_ms === "number" ? drain.gate_budget_ms : null;
  const worst = trend.reduce((max, tick) => Math.max(max, tick.gate_ms), measured);
  const over = trend.filter((tick) => tick.over_budget).length;
  const top = Math.max(worst, 1);
  const overrun = budget !== null && measured > budget;
  return (
    <span className="head-gate">
      <span className={overrun ? "head-gate__now head-gate__now--over" : "head-gate__now"}>
        Gate: {seconds(measured)}
        {budget !== null ? ` of a ${seconds(budget)} budget` : " (no budget recorded)"}
        {overrun && budget !== null ? ` — over by ${seconds(measured - budget)}` : ""}
      </span>
      <span
        className="head-gate__spark"
        role="img"
        aria-label={`Gate time over the last ${trend.length} tick${trend.length === 1 ? "" : "s"}`}
      >
        {trend.map((tick) => (
          <span
            key={tick.tick_id}
            className={tick.over_budget ? "head-gate__bar head-gate__bar--over" : "head-gate__bar"}
            title={`${tick.tick_id}: ${seconds(tick.gate_ms)} of ${seconds(tick.budget_ms)}${tick.over_budget ? " — over budget" : ""}`}
            style={{ height: `${Math.max(2, Math.round((tick.gate_ms / top) * 14))}px` }}
          />
        ))}
      </span>
      <span className="head-gate__trend">
        {trend.length} tick{trend.length === 1 ? "" : "s"} · worst {seconds(worst)}
        {over > 0 ? ` · ${over} over budget` : ""}
      </span>
    </span>
  );
}

/** FilterLine is the one line the board owes while any narrowing is on: what is hidden,
 * out of how much, and the single control that clears every filter. Without it, a board
 * narrowed to nothing reads as an empty store (design §4.4, §5.1). */
function FilterLine({
  filter,
  shown,
  total,
  view,
  onClearFilter,
}: {
  filter: BoardFilter;
  shown: number;
  total: number;
  view: BoardView;
  onClearFilter: () => void;
}) {
  if (!filterActive(filter)) return null;
  const hidden = total - shown;
  const states = filterStatesLabel(filter.states);
  const text = filter.text.trim();
  const noun = view === "archive" ? "finished items" : "queue items";
  return (
    <div className="filter-line" role="status">
      <span className="filter-line__text">
        Showing <strong>{shown}</strong> of <strong>{total}</strong> {noun}
        {hidden === 0 ? " · nothing hidden" : ` · +${hidden} hidden`}
        {filter.states.length > 0 ? <span> · states: {states}</span> : null}
        {text !== "" ? <span> · text: “{text}”</span> : null}
      </span>
      <button type="button" className="btn btn--quiet" onClick={onClearFilter}>
        Clear filters
      </button>
    </div>
  );
}

/** BuildLine names the program answering, because a board that is not the current tree has
 * no other way to say so: measured 2026-11-14, a `tillr-view` started before the item
 * `rank` field drew the whole queue in item-id order and nothing on screen said why.
 *
 * The header is a dense strip, so this is the SHORT form — revision and where the front end
 * came from — and the executable, the commit time and the build's date are the tooltip. The
 * first version drew the whole stamp and the header clipped it to the word "build", which
 * made the one line that answers "is this current?" invisible. A server too old to carry
 * the field says so, with room to wrap, because that sentence has to be readable.
 */
function BuildLine({ build }: { build?: import("../models/types").BuildStamp }) {
  if (!build) {
    return (
      <span
        className="head-build head-build--unknown"
        title="this server answered without a build stamp, so it predates the field"
      >
        build unknown — this server is older than the build stamp, so the board cannot vouch
        for the code it is drawing
      </span>
    );
  }
  const revision = build.revision ? build.revision.slice(0, 12) : "no-revision";
  const frontEnd = build.front_end === "embedded" ? "embedded" : build.front_end === "none" ? "no-ui" : build.front_end;
  return (
    <span
      className="head-build"
      title={`${build.executable}\nbuilt ${build.time || "at an unknown time"}${build.modified ? ", from a dirty tree" : ""}\nfront end: ${build.front_end}`}
    >
      build <strong>{revision}</strong>
      {build.modified ? "+dirty" : ""} · {frontEnd}
      {build.checkout?.state === "behind" ? (
        <strong className="head-build__behind" title={build.checkout.note}>
          {" "}· behind the checkout — restart with `just observatory`
        </strong>
      ) : null}
    </span>
  );
}

/** DrainBuildLine names the drain loop that wrote the record the board is reading, not the
 * server answering the request. They are different processes and can be different vintages:
 * a `tillr-view` restarted this morning can be reading last night's drain result, and a
 * `tillr-view` that predates this field cannot show it at all.
 *
 * A loop result written before the stamp existed names no build. That is drawn as a build
 * that cannot be named rather than as an empty stamp, the same distinction the server's own
 * BuildLine makes for a `--ui-at` server and an older binary.
 */
function DrainBuildLine({ drain }: { drain: SnapshotData["drain_summary"] | undefined }) {
  const build = drain?.build;
  if (!build || (!build.revision && !build.digest)) {
    return (
      <span
        className="head-build head-build--unknown"
        title={drain?.build_note || "the newest loop result names no build"}
      >
        drain build unknown — {drain?.build_note || "the newest loop result names no build, so the board cannot vouch for the drain that wrote it"}
      </span>
    );
  }
  const revision = build.revision ? build.revision.slice(0, 12) : "no-revision";
  const digest = build.digest ? build.digest.slice(0, 12) : "no-digest";
  return (
    <span
      className="head-build"
      title={`${build.executable}\nbuilt ${build.time || "at an unknown time"}${build.modified ? ", from a dirty tree" : ""}\ndigest ${build.digest}`}
    >
      drain build <strong>{revision}</strong>
      {build.modified ? "+dirty" : ""} · digest {digest}
    </span>
  );
}

/** RestartLine is the answer to the latest restart request of each role. A role nobody has
 * asked to restart is named as such, because an absent line would read as "no restart is
 * possible" rather than "none has been asked". */
export function RestartLine({ drain }: { drain: SnapshotData["drain_summary"] | undefined }) {
  const restarts = drain?.restarts ?? {};
  return (
    <span className="head-build head-restart">
      {(["drain", "view"] as const).map((role) => {
        const answer = restarts[role];
        return (
          <span key={role} className="head-restart__role" title={answer ? `${answer.request_id}\n${answer.from_build}\n-> ${answer.to_build}${answer.detail ? `\n${answer.detail}` : ""}` : "no restart has been requested"}>
            {role} restart: <strong>{answer ? answer.answer : "none requested"}</strong>
            {answer?.detail ? ` — ${answer.detail}` : ""}{" "}
          </span>
        );
      })}
    </span>
  );
}

export function ObservatoryHeaderBar({
  snapshot,
  error,
  onRefresh,
  refreshing,
  live,
  paused = false,
  onPause,
  onResume,
  readAtMs = 0,
  onToggleRail,
  filter,
  shown,
  onToggleState,
  onClearFilter,
  questions,
  questionsOpen,
  onOpenQuestions,
  view = "queue",
  onSelectView,
  viewTotal,
}: ObservatoryHeaderBarProps) {
  const railToggle = onToggleRail ? (
    <button type="button" className="btn rail-toggle" onClick={onToggleRail}>
      ☰ Queue
    </button>
  ) : null;

  if (error) {
    return (
      <header className="app-head app-head--refused">
        <div className="brand">
          {railToggle}
          <span className="brand__mark" aria-hidden="true">
            ◆
          </span>
          <span className="brand__name">Tillr Observatory</span>
        </div>
        <div className="head-meta">
          <strong>The store could not be read.</strong>
          <span className="head-artifact">{error.message}</span>
          <LiveSentence live={live} paused={paused} readAtMs={readAtMs} />
        </div>
        <div className="head-meta__row">
          <LiveChip live={live} paused={paused} />
          <LanePill />
          <PauseControl
            paused={paused}
            onPause={onPause}
            onResume={onResume}
            onRefresh={onRefresh}
            refreshing={refreshing}
          />
        </div>
      </header>
    );
  }

  if (!snapshot) {
    return (
      <header className="app-head">
        <div className="brand">
          {railToggle}
          <span className="brand__mark" aria-hidden="true">
            ◆
          </span>
          <span className="brand__name">Tillr Observatory</span>
          <span className="brand__sub">reading the store…</span>
        </div>
        <div className="stats" aria-hidden="true">
          <div className="stat skeleton">
            <span className="skeleton__line" style={{ width: "60%" }} />
            <span className="skeleton__line" style={{ width: "40%" }} />
          </div>
        </div>
        <div className="head-meta">
          <span className="head-artifact">Reading the store…</span>
          <LiveSentence live={live} paused={paused} readAtMs={readAtMs} />
          <div className="head-meta__row">
            <LanePill />
          </div>
        </div>
      </header>
    );
  }

  const cursor = snapshot.snapshot;
  const drain = snapshot.data.drain_summary;
  const counts = boardCounts(snapshot.data.items, drain, snapshot.data.counts);
  const absent = storeState(snapshot.data) !== "present";

  return (
    <header className="app-head">
      <div className="brand">
        {railToggle}
        <span className="brand__mark" aria-hidden="true">
          ◆
        </span>
        <span className="brand__name">Tillr Observatory</span>
        <span className="brand__sub">
          {absent
            ? "store absent"
            : view === "archive"
              ? `archive — the items that finished`
              : "the drain, read honestly"}
        </span>
      </div>
      <Stats
        counts={counts}
        drain={drain}
        filter={filter}
        onToggleState={onToggleState}
        questions={questions}
        questionsOpen={questionsOpen}
        onOpenQuestions={onOpenQuestions}
        view={view}
        onSelectView={onSelectView}
      />
      <div className="head-meta">
        <div className="head-meta__row">
          <LiveChip live={live} paused={paused} />
          <LoopBand drain={drain} />
          <LanePill lanes={snapshot.data.lanes} />
          <PauseControl
            paused={paused}
            onPause={onPause}
            onResume={onResume}
            onRefresh={onRefresh}
            refreshing={refreshing}
          />
        </div>
        <div className="head-meta__row">
          <code title={cursor.store_path}>{cursor.store_path}</code>
          <span>v{cursor.schema_version}</span>
          <span>seq #{cursor.event_head_seq}</span>
          <EpistemicBadge state="observed" />
        </div>
        <div className="head-meta__row">
          <span>
            {drain.dispatched} runs dispatched; usage is recorded on each Run page.
          </span>
          <span>·</span>
          <span>
            {drain.ticks} ticks · {drain.dispatched} dispatched · {drain.landed} landed
          </span>
        </div>
        <div className="head-meta__row head-gate-row">
          <GateLine drain={drain} />
        </div>
        <div className="head-meta__row head-artifact">
          <LiveSentence live={live} paused={paused} readAtMs={readAtMs} />
        </div>
        <div className="head-meta__row head-build-row">
          <BuildLine build={cursor.build} />
          <DrainBuildLine drain={drain} />
          <RestartLine drain={drain} />
        </div>
      </div>
      {/* Last child, so the grid puts it on its own full-width row under the band's three
          regions instead of pushing the meta column onto a third row. */}
      <FilterLine
        filter={filter}
        shown={shown}
        total={viewTotal ?? counts.total}
        view={view}
        onClearFilter={onClearFilter}
      />
    </header>
  );
}
