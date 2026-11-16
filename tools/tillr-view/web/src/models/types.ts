// The typed mirror of `tillr-view serve`'s answers (tools/tillr-view/serve.go,
// serveobs.go). Every nullable field is nullable here because the server sends null when
// the source did not report a value: a missing count is never a zero.

/** The provenance marker every span, exchange and claim carries (design §2.3). */
export type EpistemicState =
  | "observed"
  | "derived_clock"
  | "unobserved_seam"
  | "unreadable"
  | "absent"
  | "lane_copy"
  /** A transcript entry whose source never recorded a clock at all. It is not one of the
   * five provenance states: those describe a record's relationship to a clock. */
  | "no_clock";

export interface SourceState {
  path: string;
  state: string;
  note?: string;
}

export interface SourceStates {
  store: SourceState;
  loop_dir: SourceState;
  trace_index: SourceState;
}

/** The cursor one response was read at, so two responses are never mixed. */
/** The program that answered, so a board knows which build it is (Go: build.go). A server
 * older than the field omits it, which is itself the answer: an unversioned board cannot
 * be vouched for. */
export interface BuildStamp {
  revision: string;
  time: string;
  modified: boolean;
  front_end: string;
  executable: string;
}

export interface ServerSnapshot {
  store_path: string;
  schema_version: number;
  event_head_seq: number;
  trace_watermark_ns: number | null;
  generated_at: string;
  /** Absent from a server built before this field existed. */
  build?: BuildStamp;
  /** The configured Vantage base URL the document link is built from, and absent when the
   * operator configured none — a board that draws the link and says it goes nowhere. */
  vantage_base_url?: string;
}

export interface LandingRow {
  kind: "landing" | "acquittal" | "gap";
  landing_id?: string; item_id?: string; run_id?: string; pass_id?: string;
  verdict_id?: string; result?: string; reason?: string;
  base_sha?: string; head_sha?: string; trunk_before?: string; trunk_after?: string;
  gated_tree?: string; landed_tree?: string; conflict_paths?: string[]; gate_skipped_reason?: string;
  event_seq: number; at_ms: number | null; epistemic_state: string;
  fix_run_id?: string; fixer_item?: string; red_sha?: string; failing_checks?: string[];
}

export interface LandingsData { rows: LandingRow[]; }

export interface ApiEnvelope<T> {
  snapshot: ServerSnapshot;
  data: T;
}

export interface ApiErrorBody {
  code: string;
  message: string;
}

export interface ApiFailure {
  snapshot: ServerSnapshot;
  error: ApiErrorBody;
}

export interface WaitingRow {
  rank: string;
  item_id: string;
  class: string;
  where: string;
  why: string;
  mover: string;
  since: string;
  class_source: string;
}

export interface PerformanceCheck {
  check_id: string; count: number; sample_count: number;
  median_ms: number | null; p95_ms: number | null; max_ms: number | null;
  waited_ms: number; failures: number;
}

export interface PerformanceItem {
  item_id: string; run_count: number; usage_tokens: number | null; cost_usd: number | null;
  lower_bound: boolean; unrecorded_runs: number;
  tokens_unrecorded_runs: number; cost_unrecorded_runs: number;
}

export interface CheckExecution extends GateSpan {
  run_id: string; item_id: string | null; verdict_id: string; tick_id: string;
  duration_ms: number | null;
}

export interface PassRow {
  dispatched_at_ms: number;
  decisions: { item_id: string; rank: string; action: string; reason: string; lane_id?: string; run_id?: string }[];
  phases?: { stage_ms: number | null; dispatch_ms: number | null; gate_ms: number | null; land_ms: number | null } | null;
  session_id?: string;
  stop?: string;
  detail?: string;
}

export interface DispatchView {
  decision_id: string;
  pass_id?: string;
  item_id: string;
  lane_id: string;
  run_id: string;
  action: string;
  reason: string;
  dispatched_at_ms: number;
}

export interface RunSummary {
  run_id: string;
  lane_id: string;
  backend: string;
  state: string;
  exit_code: number | null;
  started_at_ms: number;
  finished_at_ms: number;
  cost_usd: number | null;
  usage_tokens: number | null;
}

/** The seven human states the server's projector emits (serveread.go ProjectItemState). */
export interface ItemProjection {
  state: string;
  raw_state: string;
  underlying?: string;
  waiting_on?: string[];
  /** The loop's own hold reason, when its latest decision was a question hold: one of
   * `question-open`, `questions-unknown`, `design-doc-missing`. The state stays one of the
   * seven; this is why a blocked row is blocked, so the rail can say which. */
  hold?: string;
}

export interface ItemView {
  item_id: string;
  title: string;
  projected_state: ItemProjection;
  updated_at_ms: number;
  cost_usd: number | null;
  latest_decision: DispatchView | null;
  latest_run: RunSummary | null;
  /** The queue's order token, ascending, sparse and lexical (`'3'`, `'k'`). The board
   * serves it; a server that predates it sends nothing, so the queue renders unranked
   * rather than inventing an order. */
  rank?: string | null;
  /** How many of the item's criteria are still open, when the server counts them. */
  open_criteria?: number | null;
}

export interface LaneView {
  lane_id: string;
  state: string;
  run_id: string;
  leased_until_ms: number | null;
}

/** GateTick is one tick's gate wall time in the header's trend. It is the gate program's
 * own measurement, recorded by the loop beside the staging, dispatch and landing times, so
 * a slow lane is never read as a slow gate. */
export interface GateTick {
  tick_id: string;
  at_ms: number;
  gate_ms: number;
  budget_ms: number;
  over_budget: boolean;
}

/** The drain build that wrote the newest loop result (Go: tillr-drain's build stamp). A
 * record written before the stamp existed carries none, which is a build that cannot be
 * named rather than a build with nothing to say. */
export interface DrainBuild {
  revision: string;
  time: string;
  modified: boolean;
  digest: string;
  executable: string;
}

export interface DrainSummary {
  active: boolean;
  session?: {
    session_id: string;
    started_at_ms: number;
    refreshed_at_ms: number;
    next_pass_at_ms: number;
    ended_at_ms: number | null;
  } | null;
  session_note?: string;
  session_grace_ms?: number;
  running_runs: number;
  ticks: number;
  dispatched: number;
  landed: number;
  rejected: number;
  infra_failures: number | null;
  total_cost_usd: number | null;
  last_stop_reason: string | null;
  stop_note: string;
  last_decision: DispatchView | null;
  /** The loop's own build stamp, and why there is none. A server older than the field sends
   * neither, and the header says the drain build cannot be named. */
  build?: DrainBuild | null;
  build_note?: string;
  /** The most recent tick's gate wall time and the budget it was measured against. A server
   * that predates the field sends neither, and the header says the gate was not measured. */
  gate_ms?: number | null;
  gate_budget_ms?: number | null;
  gate_trend?: GateTick[];
}

/** ServerCounts is the board's own arithmetic, computed once in Go: the state the store
 * projects each item into, how many of the in-queue items the planner would dispatch, and
 * how many runs the store still calls running. A server that predates the field sends
 * none, and the client counts the rows itself rather than showing zero. */
export interface ServerCounts {
  by_state: Record<string, number>;
  dispatchable: number;
  blocked: number;
  runs_live: number;
  /** The items whose latest decision is a question hold: the planner's own count, from the
   * store's dispatch rows. A server older than the field omits it, and the header says the
   * question count is unknown rather than drawing zero. */
  question_holds?: number;
}

/** One question Vantage's index reports, as the board's questions route answers it. */
export interface Question {
  question_id: string;
  text: string;
  state: string;
  preference?: boolean;
  leaning?: string;
  blocked_on?: string[];
}

/** One item the questions route lists because its design document still asks something. */
export interface QuestionItem {
  item_id: string;
  title: string;
  state: string;
  design_doc: string;
  questions: Question[];
  questions_state: string;
  /** The loop's latest question-hold reason, or absent when its latest decision was not a
   * question hold: the list is the index's, and the planner has not answered this one yet. */
  hold?: string;
}

export interface QuestionsCounts {
  items: number;
  questions: number;
  holds: number;
}

/** `GET /api/v1/questions`: the board's one place for the questions waiting on the owner.
 * Unknown and missing are the two answers that are not the list, kept apart because neither
 * is "no questions". */
export interface QuestionsData {
  items: QuestionItem[];
  unknown: QuestionItem[];
  missing: QuestionItem[];
  counts: QuestionsCounts;
  state: "questions-ok" | "questions-unknown";
  note?: string;
}

export interface SnapshotData {
  sources: SourceStates;
  /** The server's own counts, when the build that answered has them. */
  counts?: ServerCounts;
  drain_summary: DrainSummary;
  lanes: LaneView[];
  items: ItemView[];
  recent_runs: RunSummary[];
}

export interface CriterionView {
  criterion_id: string;
  text: string;
  state: string;
}

export interface LandingView {
  attempt_id: string;
  verdict_id: string;
  result: string;
  reason: string;
  trunk_before: string;
  trunk_after: string;
}

/** One row of `candidates`. The Go struct has no JSON tags, so the wire names are the
 * field names: that is the API's shape today, not a spelling this client chose. */
export interface StoreCandidate {
  CandidateID: string;
  ItemID: string;
  BaseRef: string;
  BaseSHA: string;
  HeadSHA: string;
  TreeSHA: string;
  State: string;
  EnqueuedMS: number;
}

export interface RecordedArtifact {
  path: string;
  sha256: string;
  size_bytes: number;
  mtime_ms: number;
  epistemic_state: EpistemicState;
  clobber_warning?: string;
}

export interface ArtifactPayload {
  kind: string;
  present: boolean;
  path?: string;
  sha256?: string;
  size_bytes?: number;
  mtime_ms?: number | null;
  epistemic_state: EpistemicState;
  clobber_warning?: string;
  content_type?: string;
  body?: string | null;
  files?: RecordedArtifact[];
  note?: string;
  searched_paths?: string[];
}

export interface AttemptView {
  candidate: StoreCandidate;
  landings: LandingView[] | null;
  gate_record: ArtifactPayload | null;
  item_verdicts: ArtifactPayload | null;
}

export interface ItemDetail {
  item_id: string;
  title: string;
  body_ref: string;
  design_doc: string;
  /** The item's fence, decoded: an item that names no path answers an empty array. */
  allowed_paths: string[];
  projected_state: ItemProjection;
}

/** One item's dossier. It carries no question list: the board does not track question
 * state, and the one question fact the pane shows — the count — comes from the document
 * route, which owns the parse (design §8.1, ledger `D-OBS6`). */
export interface ItemData {
  item: ItemDetail;
  criteria: CriterionView[];
  blocked_by: string[];
  decisions: DispatchView[];
  attempts: AttemptView[];
}

export interface StageView {
  stage: string;
  status: string;
  started_at_ms: number | null;
  finished_at_ms: number | null;
  epistemic_state: EpistemicState;
  summary: string;
  record_ref: string | null;
}

export interface ClaimView {
  claim: string;
  status: string;
  reason?: string;
  epistemic_state: EpistemicState;
  observed_at: string;
}

/** A run's spend. `null` is "the source did not report it", never zero. */
export interface CostView {
  turns: number | null;
  tokens: number | null;
  cost_total_usd: number | null;
  note: string;
}

export interface CompletenessView {
  log_state: string;
  proc_state: string;
  git_state: string;
  brief_state: string;
  note: string;
}

export interface RunDetail {
  run_id: string;
  item_id: string;
  lane_id: string;
  backend: string;
  state: string;
  exit_code: number | null;
  started_at_ms: number;
  finished_at_ms: number;
  usage_tokens: number | null;
  cost_usd: number | null;
}

export interface RunData {
  run: RunDetail;
  stages: StageView[];
  claim_checks: ClaimView[];
  cost: CostView;
  completeness: CompletenessView;
}

export interface Exchange {
  exchange_id: string;
  seq: number;
  layer: string;
  caller: string;
  callee: string;
  channel: string;
  started_at_ms: number | null;
  finished_at_ms: number | null;
  ts_source: string;
  epistemic_state: EpistemicState;
  exit_code: number | null;
  summary: string;
  request_ref: string;
  response_ref: string;
  inline_preview: string | null;
}

export interface ExchangesData {
  run: string;
  exchanges: Exchange[];
  sources: SourceStates;
  note?: string;
}

export interface TimelineSpan {
  span_id: string;
  parent_span_id: string | null;
  lane_id: string;
  category: string;
  label: string;
  started_at_ms: number | null;
  finished_at_ms: number | null;
  ts_source: string;
  epistemic_state: EpistemicState;
  cost_usd: number | null;
  status: string;
  exchange_id: string | null;
}

export interface TimelineData {
  run: string;
  spans: TimelineSpan[];
  sources: SourceStates;
  note?: string;
}

/** One gate span on the unified axis (Go: serve.go `gateSpanView`). The nanosecond
 * bounds arrive as decimal STRINGS because a Unix nanosecond value near 1.79e18 is above
 * `Number.MAX_SAFE_INTEGER`; `started_at_ms` and `finished_at_ms` are the same instants as
 * computed floats, which is all the bar geometry needs. */
export interface GateSpan {
  span_id: string;
  trace_id: string;
  parent_span_id: string;
  check_id: string;
  kind: string;
  label: string;
  status: string;
  start_unix_nano: string;
  end_unix_nano: string;
  /** Computed from the nanosecond string. Null is the gate's "no clock", and the bar is
   * not drawn, exactly as the unified span viewer refuses a null. */
  started_at_ms: number | null;
  finished_at_ms: number | null;
  waited_ms_json: string;
  attributes_json: string;
  epistemic_state: EpistemicState;
}

/** One step the gate did not put a verdict on. It lives in the run's `gate.spans.json`
 * rather than the store's table: a landed gate skips ~22 steps. */
export interface GateSkip {
  check_id: string;
  kind: string;
  label: string;
  reason: string;
  planned: boolean;
  owed: boolean;
}

export interface GateData {
  run: string;
  verdict_id: string;
  plan_span: GateSpan | null;
  spans: GateSpan[];
  skipped_checks: GateSkip[];
  summary?: Record<string, unknown>;
  sources: SourceStates;
  note?: string;
}

/** One tool call as the transcript carries it: the name the model called and the arguments
 * it passed, as the JSON text the stream recorded. */
export interface ToolCallView {
  id: string;
  name: string;
  arguments: string;
}

/** One entry of a run's transcript (Go: transcript.go `transcriptEntry`). The kinds are
 * `step`, `message`, `thinking`, `tool_call`, `tool_result`, `proc` and `notice`; a
 * `tool_result` answers the `tool_call` its `tool_call_id` names. */
export interface TranscriptEntry {
  seq: number;
  source: string;
  kind: string;
  role?: string;
  at_ms: number | null;
  tool_call_id?: string;
  tool?: ToolCallView;
  text?: string;
  /** The record's own length. Larger than `text` exactly when `truncated`. */
  bytes: number;
  truncated: boolean;
  is_error?: boolean;
  exit_code?: number;
  duration_ms?: number;
  epistemic_state: EpistemicState;
  note?: string;
}

/** How each of the run's three records was found: the harness's stream, the tick's step
 * record, and the shim's subprocess record. */
export interface TranscriptSources {
  log: SourceState;
  steps: SourceState;
  proc: SourceState;
}

/** One page of a run's transcript. `before` is null at the run's first entry; `cursor`
 * resumes after the page. */
export interface TranscriptData {
  run: string;
  entries: TranscriptEntry[];
  sources: TranscriptSources;
  cursor: string;
  before: string | null;
  has_more: boolean;
  limit: number;
  epistemic_state: EpistemicState;
  note?: string;
}

/** One entry, answered whole rather than to a page's body budget. */
export interface TranscriptEntryData {
  run: string;
  entries: TranscriptEntry[];
  sources: TranscriptSources;
  epistemic_state: EpistemicState;
  note?: string;
}

export interface ArtifactData {
  run: string;
  artifact: ArtifactPayload;
}

/** The metadata card a design document's frontmatter declares (serve.go
 * `documentFrontmatter`). Every key is optional: the repository's documents do not all
 * carry all five, and a document with no `stage:` is a draft rather than a document whose
 * stage says nothing. */
export interface DocumentFrontmatter {
  title?: string;
  status?: string;
  stage?: string;
  next?: string;
  updated?: string;
}

/** One item's design document as metadata, never as text (serve.go documentData): the
 * frontmatter card, the section the item's `body_ref` names, the server's count of open
 * questions, and the path a Vantage link is built from. `frontmatter` is null when the
 * document opens with no frontmatter block at all — a state the pane names rather than
 * drawing as five blanks. */
export interface DocData {
  item_id: string;
  /** The document's path relative to the served root, slash-separated: the spelling a
   * Vantage link takes, and the one the pane shows. */
  path: string;
  frontmatter: DocumentFrontmatter | null;
  /** The fragment of the item's `body_ref`, and empty when that reference names a
   * different document than the one answered here. */
  anchor: string;
  /** The count of open questions Vantage's index reports for the document, or null when
   * the index could not answer. Null is "unknown", never a zero a reader could mistake
   * for a finished design (serve.go documentData). */
  open_questions: number | null;
  /** Which answer `open_questions` is: `questions-ok` (the tool's list, possibly empty),
   * `questions-unknown` (the tool was absent, failed or timed out), or
   * `design-doc-missing` (the tree does not hold the file). */
  questions_state: "questions-ok" | "questions-unknown" | "design-doc-missing";
  bytes: number;
}
