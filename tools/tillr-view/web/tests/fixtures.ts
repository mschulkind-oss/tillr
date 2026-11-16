// Fixtures the observatory suite drives the components with. They are deliberately a
// partial drain: one observed span, one derived clock, one unobserved seam, one absent
// clock. A component that invents a value shows it here.

import type {
  ApiEnvelope,
  DocData,
  ExchangesData,
  ItemData,
  RunData,
  ServerSnapshot,
  SnapshotData,
  TimelineData,
  TranscriptData,
} from "../src/models/types";

export const cursor: ServerSnapshot = {
  store_path: ".tillr/state.db",
  schema_version: 3,
  event_head_seq: 48,
  trace_watermark_ns: null,
  generated_at: "2026-11-12T20:31:14Z",
};

export function envelope<T>(data: T): ApiEnvelope<T> {
  return { snapshot: cursor, data };
}

/** One item's design document as the route now answers it: metadata and a count, never
 * prose. The frontmatter block is the shape the repository's design documents write. */
export const docData: DocData = {
  item_id: "item-seed-02",
  path: "docs/design/a-design.md",
  frontmatter: {
    title: "A design",
    status: "in-review",
    stage: "DESIGN",
    updated: "2026-11-13",
  },
  anchor: "",
  open_questions: 0,
  questions_state: "questions-ok",
  bytes: 1024,
};

export const snapshotData: SnapshotData = {
  sources: {
    store: { path: ".tillr/state.db", state: "present" },
    loop_dir: { path: ".tillr/loop", state: "present" },
    trace_index: { path: "index.db", state: "absent", note: "no index at this path" },
  },
  drain_summary: {
    active: false,
    running_runs: 0,
    ticks: 2,
    dispatched: 2,
    landed: 2,
    rejected: 0,
    infra_failures: null,
    total_cost_usd: null,
    last_stop_reason: "queue-drained",
    stop_note: "",
    last_decision: null,
    build: {
      revision: "4a79a8e7f383d495c32f8d21c4454575dfe90be8",
      time: "2026-11-14T11:00:00Z",
      modified: false,
      digest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      executable: "/workspace/.yolo/durable/bin/tillr-drain",
    },
    build_note: "",
    gate_ms: 82400,
    gate_budget_ms: 330000,
    gate_trend: [
      { tick_id: "tick-1", at_ms: 1762979400000, gate_ms: 71600, budget_ms: 330000, over_budget: false },
      { tick_id: "tick-2", at_ms: 1762979460000, gate_ms: 82400, budget_ms: 330000, over_budget: false },
    ],
  },
  lanes: [{ lane_id: "lane-1", state: "idle", run_id: "", leased_until_ms: null }],
  items: [
    {
      item_id: "item-seed-02",
      title: "seed two",
      projected_state: { state: "working", raw_state: "in-progress" },
      updated_at_ms: 1762979400000,
      cost_usd: null,
      latest_decision: null,
      latest_run: {
        run_id: "run-item-seed-02",
        lane_id: "lane-1",
        backend: "pi",
        state: "ok",
        exit_code: 0,
        started_at_ms: 1762979400000,
        finished_at_ms: 1762979424800,
        cost_usd: null,
        usage_tokens: 12000,
      },
    },
  ],
  recent_runs: [],
};

/** A drain with one live run: `lane-1` is leased and names a run whose owner is the middle
 * item. The run's `finished_at_ms` is zero because the store's live projection has no
 * finish to record — the fact the now band must not draw as a duration. The item keeps
 * rank `2`, between two others, so a band that moved it would be visible here. */
export const liveSnapshotData: SnapshotData = {
  ...snapshotData,
  drain_summary: { ...snapshotData.drain_summary, active: true, running_runs: 1 },
  lanes: [
    { lane_id: "lane-1", state: "leased", run_id: "run-live-1", leased_until_ms: 1762979580000 },
    { lane_id: "lane-2", state: "idle", run_id: "", leased_until_ms: null },
  ],
  items: [
    {
      item_id: "item-queued",
      title: "queued seed",
      projected_state: { state: "in_queue", raw_state: "in_queue" },
      updated_at_ms: 1762979400000,
      cost_usd: null,
      latest_decision: null,
      latest_run: null,
      rank: "1",
    },
    {
      item_id: "item-live",
      title: "live seed",
      projected_state: { state: "working", raw_state: "in-progress" },
      updated_at_ms: 1762979400000,
      cost_usd: null,
      latest_decision: null,
      latest_run: {
        run_id: "run-live-1",
        lane_id: "lane-1",
        backend: "pi",
        state: "running",
        exit_code: null,
        started_at_ms: 1762979400000,
        finished_at_ms: 0,
        cost_usd: null,
        usage_tokens: null,
      },
      rank: "2",
    },
    {
      item_id: "item-after",
      title: "later seed",
      projected_state: { state: "in_queue", raw_state: "in_queue" },
      updated_at_ms: 1762979400000,
      cost_usd: null,
      latest_decision: null,
      latest_run: null,
      rank: "3",
    },
  ],
  recent_runs: [],
};

export const runData: RunData = {
  run: {
    run_id: "run-item-seed-02",
    item_id: "item-seed-02",
    lane_id: "lane-1",
    backend: "pi",
    state: "ok",
    exit_code: null,
    started_at_ms: 1762979400000,
    finished_at_ms: 1762979424800,
    usage_tokens: 12000,
    cost_usd: null,
  },
  stages: [
    {
      stage: "stage",
      status: "absent",
      started_at_ms: null,
      finished_at_ms: null,
      epistemic_state: "absent",
      summary: "the run's staging inputs are not preserved",
      record_ref: null,
    },
    {
      stage: "run",
      status: "ok",
      started_at_ms: 1762979400000,
      finished_at_ms: 1762979424800,
      epistemic_state: "observed",
      summary: "the store's run row",
      record_ref: null,
    },
    {
      stage: "boundary",
      status: "unobserved",
      started_at_ms: null,
      finished_at_ms: null,
      epistemic_state: "unobserved_seam",
      summary: "the loop's boundary check is a tick-level bracket",
      record_ref: null,
    },
  ],
  claim_checks: [
    { claim: "commit_exists", status: "verified", epistemic_state: "observed", observed_at: "2026-11-12T20:29:20Z" },
    {
      claim: "tests_passed",
      status: "unverified",
      reason: "proc-exec.jsonl missing",
      epistemic_state: "unobserved_seam",
      observed_at: "",
    },
  ],
  cost: { turns: 7, tokens: 12000, cost_total_usd: null, note: "the store holds the run's token total" },
  completeness: {
    log_state: "indexed",
    proc_state: "missing",
    git_state: "unindexed",
    brief_state: "absent",
    note: "stream states are tillr-trace's index of this run's artifacts",
  },
};

export const timelineData: TimelineData = {
  run: "run-item-seed-02",
  spans: [
    {
      span_id: "run-item-seed-02/run",
      parent_span_id: null,
      lane_id: "lane-1",
      category: "stage",
      label: "run ok",
      started_at_ms: 1762979400000,
      finished_at_ms: 1762979424800,
      ts_source: "store",
      epistemic_state: "observed",
      cost_usd: null,
      status: "ok",
      exchange_id: null,
    },
    {
      span_id: "run-item-seed-02/gate",
      parent_span_id: null,
      lane_id: "lane-1",
      category: "gate_check",
      label: "gate (from the tick's records)",
      started_at_ms: 1762979410000,
      finished_at_ms: 1762979412000,
      ts_source: "file_mtime",
      epistemic_state: "derived_clock",
      cost_usd: null,
      status: "recorded",
      exchange_id: "run-item-seed-02/gate",
    },
    {
      span_id: "run-item-seed-02/seam/proc/gap",
      parent_span_id: null,
      lane_id: "lane-1",
      category: "gap",
      label: "no proc-exec.jsonl for this run",
      started_at_ms: null,
      finished_at_ms: null,
      ts_source: "none",
      epistemic_state: "unobserved_seam",
      cost_usd: null,
      status: "unobserved",
      exchange_id: "run-item-seed-02/seam/proc",
    },
  ],
  sources: snapshotData.sources,
  note: "only the supervisor's layer is observable",
};

export const exchangesData: ExchangesData = {
  run: "run-item-seed-02",
  exchanges: [
    {
      exchange_id: "run-item-seed-02/gate.verdict",
      seq: 14,
      layer: "supervisor",
      caller: "tillr-loop",
      callee: "tillr-gate",
      channel: "stdout_record",
      started_at_ms: 1762979410000,
      finished_at_ms: null,
      ts_source: "file_mtime",
      epistemic_state: "observed",
      exit_code: 0,
      summary: "gate.verdict.json is there",
      request_ref: ".tillr/loop/run-item-seed-02/gate.plan.json",
      response_ref: ".tillr/loop/run-item-seed-02/gate.verdict.json",
      inline_preview: '{"outcome":"pass"}',
    },
    {
      exchange_id: "run-item-seed-02/seam/proc",
      seq: 15,
      layer: "harness",
      caller: "bash",
      callee: "subprocess",
      channel: "subprocess",
      started_at_ms: null,
      finished_at_ms: null,
      ts_source: "none",
      epistemic_state: "unobserved_seam",
      exit_code: null,
      summary: "no proc-exec.jsonl for this run",
      request_ref: "",
      response_ref: "",
      inline_preview: null,
    },
  ],
  sources: snapshotData.sources,
};

/** The board's two item shapes for the no-run case: a blocked item the loop never
 * dispatched, and a dispatched one whose run a selection keeps. A blocked item has no run
 * at all, which is why its exchanges and timeline reads are disabled. */
export const blockedItemData: ItemData = {
  item: {
    item_id: "item-seed-06",
    title: "sixth seed",
    body_ref: "items/06.md",
    design_doc: "",
    allowed_paths: [],
    projected_state: {
      state: "blocked",
      raw_state: "blocked",
      underlying: "item:item-seed-02: finish the prerequisite",
      waiting_on: ["item:item-seed-02"],
    },
  },
  criteria: [{ criterion_id: "ac-1", text: "it is checked", state: "pending" }],
  blocked_by: ["item-seed-02"],
  decisions: [],
  attempts: [],
};

export const dispatchedItemData: ItemData = {
  item: {
    item_id: "item-seed-02",
    title: "second seed",
    body_ref: "items/02.md",
    design_doc: "",
    allowed_paths: ["tools/a/", "tools/b/", "tools/c/"],
    projected_state: { state: "working", raw_state: "in-progress" },
  },
  criteria: [{ criterion_id: "ac-1", text: "it works", state: "pending" }],
  blocked_by: [],
  decisions: [],
  attempts: [],
};

/** Four rows, one per state the design's counts name, so a state filter has something to
 * include and something to hide without inventing a fifth shape. No `counts` object on
 * purpose: the header then counts the rows itself, which is the fallback a store read by an
 * older server exercises. */
function itemIn(item_id: string, title: string, state: string, rank: string) {
  return {
    item_id,
    title,
    projected_state: { state, raw_state: state },
    updated_at_ms: 1762979400000,
    cost_usd: null,
    latest_decision: null,
    latest_run: null,
    rank,
  };
}

function itemFinished(
  item_id: string,
  title: string,
  state: string,
  rank: string,
  updated_at_ms: number,
) {
  return { ...itemIn(item_id, title, state, rank), updated_at_ms };
}

export const filterSnapshotData: SnapshotData = {
  ...snapshotData,
  items: [
    itemIn("item-queue", "queued parser", "in_queue", "1"),
    itemIn("item-working", "working parser", "working", "2"),
    itemIn("item-blocked", "blocked renderer", "blocked", "3"),
    itemIn("item-landed", "landed parser", "landed", "4"),
  ],
};

/** A mixed board: three live items and three finished ones, in rank order. The finished
 * items carry different `updated_at_ms` values, so the summary band's newest-first order
 * is a fact a case can assert rather than one it infers from the rank. */
export const archiveSnapshotData: SnapshotData = {
  ...snapshotData,
  items: [
    itemIn("item-queue", "queued parser", "in_queue", "1"),
    itemIn("item-working", "working parser", "working", "2"),
    itemIn("item-blocked", "blocked renderer", "blocked", "3"),
    itemFinished("item-landed", "landed parser", "landed", "4", 1762979400000),
    itemFinished("item-closed", "closed renderer", "closed", "5", 1762979460000),
    itemFinished(
      "item-resolved",
      "resolved elsewhere",
      "resolved_elsewhere",
      "6",
      1762979430000,
    ),
  ],
};

export const twoItemSnapshotData: SnapshotData = {
  ...snapshotData,
  items: [
    {
      item_id: "item-seed-06",
      title: "sixth seed",
      projected_state: {
        state: "blocked",
        raw_state: "blocked",
        underlying: "item:item-seed-02: finish the prerequisite",
        waiting_on: ["item:item-seed-02"],
      },
      updated_at_ms: 1762979460000,
      cost_usd: null,
      latest_decision: null,
      latest_run: null,
    },
    snapshotData.items[0],
  ],
};

/** One run's transcript as the server serves it: the step, the task, the model's reasoning
 * and prose, a tool call with the arguments it passed, the shell command that call ran, what
 * came back, and a notice at the end. Every entry carries the record's own clock except the
 * last, which is derived: the stream recorded no time for it.
 *
 * `before: null` is the head of the transcript, so a case that mounts this renders no
 * "load earlier" control; `transcriptTailData` is the same run's tail page with one. */
export const transcriptData: TranscriptData = {
  run: "run-item-seed-02",
  entries: [
    {
      seq: 0,
      source: "steps",
      kind: "step",
      role: "run",
      at_ms: 1762979400000,
      text: "tillr-run exec --store .tillr/state.db",
      bytes: 39,
      truncated: false,
      exit_code: 0,
      duration_ms: 2400,
      epistemic_state: "observed",
    },
    {
      seq: 1,
      source: "log",
      kind: "message",
      role: "user",
      at_ms: 1762979401000,
      text: "read the design and land the change",
      bytes: 36,
      truncated: false,
      epistemic_state: "observed",
    },
    {
      seq: 2,
      source: "log",
      kind: "thinking",
      role: "assistant",
      at_ms: 1762979402000,
      text: "The fence names two paths, so the change is one commit.",
      bytes: 56,
      truncated: false,
      epistemic_state: "observed",
    },
    {
      seq: 3,
      source: "log",
      kind: "tool_call",
      role: "assistant",
      at_ms: 1762979402000,
      tool_call_id: "call-1",
      tool: { id: "call-1", name: "bash", arguments: '{"command":"ls tools/tillr-view"}' },
      bytes: 35,
      truncated: false,
      is_error: false,
      duration_ms: 12,
      epistemic_state: "observed",
    },
    {
      seq: 5,
      source: "log",
      kind: "tool_result",
      role: "bash",
      at_ms: 1762979402120,
      tool_call_id: "call-1",
      text: "serve.go transcript.go",
      bytes: 22,
      truncated: false,
      epistemic_state: "observed",
    },
    {
      seq: 6,
      source: "log",
      kind: "notice",
      at_ms: 1762979403000,
      text: "the agent finished its turn",
      bytes: 27,
      truncated: false,
      epistemic_state: "derived_clock",
    },
  ],
  sources: {
    log: { path: ".tillr/lanes/lane-1/run-item-seed-02.log", state: "present" },
    steps: { path: ".tillr/loop/run-item-seed-02/steps.jsonl", state: "present" },
    proc: { path: ".tillr/loop/run-item-seed-02/proc-exec.jsonl", state: "present" },
  },
  cursor: "MA==",
  before: null,
  has_more: false,
  limit: 100,
  epistemic_state: "observed",
};

/** The same run's tail page: one page in from the head, so a case can assert that the pane
 * offers to load earlier entries and that the offer is the server's cursor. */
export const transcriptTailData: TranscriptData = {
  ...transcriptData,
  entries: transcriptData.entries.slice(-2),
  before: "Njo3OjQ6OA==",
  has_more: false,
};

/** A run whose harness stream is not there: the seam the pane must draw instead of an empty
 * list. The step record survives, so the pane is not empty either. */
export const transcriptSeamData: TranscriptData = {
  ...transcriptData,
  entries: transcriptData.entries.slice(0, 1),
  sources: {
    log: { path: ".tillr/lanes/lane-1/run-item-seed-02.log", state: "absent", note: "no writer produced this file at this path" },
    steps: { path: ".tillr/loop/run-item-seed-02/steps.jsonl", state: "present" },
    proc: { path: ".tillr/loop/run-item-seed-02/proc-exec.jsonl", state: "present" },
  },
  epistemic_state: "unobserved_seam",
  note: "the agent's own stream is absent: its messages, its thinking and its tool calls are unobserved, and what follows is the step and subprocess record that survives",
};

/** One entry whose body the page truncated, which is what a click on "read all" answers. */
export const transcriptTruncatedData: TranscriptData = {
  ...transcriptData,
  entries: [
    {
      seq: 0,
      source: "log",
      kind: "message",
      role: "assistant",
      at_ms: null,
      text: "the account the run recorded",
      bytes: 400000,
      truncated: true,
      epistemic_state: "no_clock",
    },
  ],
};
