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
  /** The tillr-trace program that reads a run's recorded artifacts on demand. */
  trace: SourceState;
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
  /** SHA-256 of the running executable, absent from older servers. */
  digest?: string;
  /** This build compared with the served checkout's HEAD (Go: build.go checkoutFreshness).
   * "behind" means the tillr sources moved since the server was built. */
  checkout?: { state: "current" | "behind" | "unknown"; head?: string; note: string };
}

export interface ServerSnapshot {
  writes_enabled?: boolean;
  store_path: string;
  schema_version: number;
  event_head_seq: number;
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
  parent_id?: string; parent_title?: string; part_label?: string;
}

export interface LandingsData { rows: LandingRow[]; }

export interface ApiEnvelope<T> {
  snapshot: ServerSnapshot;
  data: T;
  /** Set by the waiting read only: what /why says about its document index. */
  documentIndex?: DocumentIndexStatus;
  /** Set by the waiting read only: which pass (or fresh plan) /why answered from. */
  whySource?: WhySource;
  /** Set by the waiting read only: design documents with no item (/why `designs_waiting`). */
  designsWaiting?: WaitingRow[];
}

/** Where a /why answer came from: the loop's last recorded pass, or a fresh plan when no
 * loop is running or no pass is recorded (`fallback` says which). */
export interface WhySource {
  kind: "pass" | "planner";
  pass_id?: string;
  pass_at_ms?: number;
  age_s: number;
  fallback?: string;
  text: string;
}

export interface ApiErrorBody {
  code: string;
  message: string;
}

export interface ApiFailure {
  snapshot: ServerSnapshot;
  error: ApiErrorBody;
}

export interface BackendAlarm {
  kind: string;
  backend: string;
  since: string;
  cause: string;
  clears: string;
  command: string;
  items: string[];
  lanes: string[];
}

export interface WhySplitHeldPart {
  item_id: string;
  label: string;
  reason: string;
  mover: string;
}

export interface WhySplitParent {
  total_parts: number;
  required_parts: number;
  required_landed: number;
  landed: number;
  working: number;
  held: number;
  queued: number;
  closed: number;
  progress_summary: string;
  held_parts?: WhySplitHeldPart[];
}

export interface WhySplitPart {
  parent_id: string;
  parent_title: string;
  part_label: string;
  required: boolean;
}

export interface WaitingRow {
  alarm?: BackendAlarm;
  graph_changes?: string[];
  place?: number;
  item_id: string;
  document?: string;
  /** Publication observation identity; not an item and has no item controls. */
  observation?: string;
  class: string;
  position?: string;
  where: string;
  why: string;
  mover: string;
  since: string;
  waited?: string;
  class_source: string;
  split_parent?: WhySplitParent;
  split_part?: WhySplitPart;
}

export interface ModelUsage {
  backend: string; model: string | null; tokens?: number | null;
  input?: number | null; output?: number | null; cache_read?: number | null; cache_write?: number | null;
}
export interface UsageSum {
  tokens: number | null; models: ModelUsage[]; unrecorded_runs: number; lower_bound: boolean;
}
export interface RunUsage {
  backend: string; tokens: number | null; models: ModelUsage[];
  turns: (ModelUsage & { id: string })[];
  first_output_ms?: number; output_tokens_per_second?: number;
}

export interface PerformanceCheck {
  check_id: string; count: number; sample_count: number;
  median_ms: number | null; p95_ms: number | null; max_ms: number | null;
  waited_ms: number; failures: number;
}

export interface PerformanceItem {
  usage?: UsageSum;
  item_id: string; run_count: number; usage_tokens: number | null; cost_usd: number | null;
  lower_bound: boolean; unrecorded_runs: number;
  tokens_unrecorded_runs: number; cost_unrecorded_runs: number;
}

export interface CheckExecution extends GateSpan {
  run_id: string; item_id: string | null; verdict_id: string; tick_id: string;
  duration_ms: number | null;
}

export interface SpendTotals {
  cost_usd: number; tokens: number; runs: number; unreported: number;
  tokens_unreported: number; cost_unreported: number;
}

export interface PassRow {
  spend_started_in_loop?: SpendTotals | null;
  spend_carried_over?: SpendTotals | null;
  dispatched_at_ms: number;
  decisions: { item_id: string; place?: number; action: string; reason: string; lane_id?: string; run_id?: string; seat?: string }[];
  phases?: { claim_ms: number | null; stage_ms: number | null; dispatch_ms: number | null; gate_ms: number | null; land_ms: number | null } | null;
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
  seat?: string;
  workflow?: string;
  node?: string;
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
  kind?: "idea";
  /** All recorded dependency edges, including completed blockers; absent on older servers. */
  blocked_by?: string[];
  item_id: string;
  title: string;
  projected_state: ItemProjection;
  updated_at_ms: number;
  cost_usd: number | null;
  latest_decision: DispatchView | null;
  latest_run: RunSummary | null;
  /** Position in the full order, counted from 1; absent for parked or finished items. */
  place?: number;
  /** How many of the item's criteria are still open, when the server counts them. */
  open_criteria?: number | null;
  split_progress?: string;
  split_part?: WhySplitPart;
}

export interface PipelineStep {
  step: string;
  started_at_ms: number;
  finished_at_ms: number | null;
}
export interface LaneHistoryRun {
  run_id: string; item_id: string; lane_id: string; seat?: string; state: string;
  started_at_ms: number; finished_at_ms: number;
}
export interface LaneNode { node_id: string; run_id: string; attempt: number; seat?: string; state: "running" | "passed" | "failed" | "skipped" }
export interface ProgressClaim { run_id: string; criterion_id: string }
export interface ChildSession { run_id: string; session_id: string; role: string; started_at_ms: number | null; ended_at_ms: number | null; turn_count: number | null; end_clock?: string; transcript: string }
export interface ChildrenRecord { recorded: boolean; sessions: ChildSession[]; note?: string }
export interface LaneRow {
  progress?: ProgressClaim[];
  child_sessions?: Record<string, ChildrenRecord>;
  running_nodes?: number;
  nodes?: LaneNode[] | null;
  workflow_ref?: string;
  pipeline?: PipelineStep[] | null;
  pipeline_note?: string;
  history?: LaneHistoryRun[] | null;
  usage?: UsageSum;
  lane_id: string;
  group?: "configured" | "draining" | "idle" | "desk";
  group_note?: string;
  held_item_id?: string;
  held_run_id?: string;
  held_reason?: string;
  state: "idle" | "running" | "settling" | "paused";
  state_since_ms: number | null;
  run_id: string; item_id: string; seat: string; backend: string; attempt: number; outcome: string;
  /** Where `seat` was read: the run itself, the run's dispatch, the item, or "none" — the item
   * asked for no seat and the policy's default backend ran it. Absent from a server older than
   * the field. */
  seat_source?: "run" | "dispatch" | "item" | "none";
  last_step: string; last_step_at_ms: number | null; log_grew_at_ms: number | null;
  latest_action: { kind: "action" | "liveness" | "unrecorded"; text: string; at_ms: number | null; clock: string };
  note?: string;
}

export interface LaneView {
  /** Active versus settling work, separate from the raw lease state. Older servers omit it. */
  work_state?: "running" | "settling" | "paused" | "idle";
  group?: "configured" | "draining" | "idle" | "desk";
  group_note?: string;
  held_item_id?: string;
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

export interface StopExplanation {
  reason: string;
  at_ms: number;
  item_id: string;
  step: string;
  sentence: string;
  next: string;
}

export interface RepeatedStop {
  count: number;
  reason: string;
  detail: string;
  item_id: string;
  step: string;
  first_at_ms: number;
  last_at_ms: number;
  sentence: string;
  next: string;
}

/** The answer to one restart request, as the loop or the server recorded it (Go:
 * tillr-drain/restart). `from_build` and `to_build` are the builds' one-line stamps. */
export interface RestartAnswer {
  request_id: string;
  role: string;
  answer: "restarted" | "already-current" | "refused" | "superseded";
  from_build: string;
  to_build: string;
  detail?: string;
  at: string;
}

export interface DrainSummary {
  /** The latest answered restart request of each role, keyed `drain` and `view`. A role
   * never asked has no key, and a server older than the field sends none. */
  restarts?: Record<string, RestartAnswer>;
  active: boolean;
  stop?: StopExplanation | null;
  repeated_stop?: RepeatedStop | null;
  heartbeat_interval_ms?: number;
  session?: {
    session_id: string;
    started_at_ms: number;
    refreshed_at_ms: number;
    next_pass_at_ms: number;
    ended_at_ms: number | null;
    /** Lane ids the loop was configured to run; absent from an older loop. */
    lanes?: string[];
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
  unreported_runs?: number;
  cost_unreported_runs?: number;
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

/** Green is the proven count and main is the total in every mode; null means unreadable. */
export interface PushSummary {
  what: string;
  remote?: string;
  ref?: string;
  published_sha?: string;
  unpublished_green_count?: number | null;
  unpublished_main_count?: number | null;
  state_kind?: string;
  detail?: string;
  mover?: string;
  since?: string;
}

export interface RunningWorkWorktrees {
  on_disk: number;
  in_use: number;
  idle: number;
  count_limit: number;
  worktree_bytes: number;
  scratch_bytes: number;
  total_bytes: number;
  disk_budget_bytes: number;
  run_log_bytes: number;
}

export interface RunningWorkSummary {
  ordinary_running: number;
  express_running: number;
  command_running: number;
  agent_limit: number | null;
  worktrees: RunningWorkWorktrees;
}

export interface LiveSpend {
  turns?: number | null;
  tokens?: number | null;
  cost_usd?: number | null;
  note?: string;
}

export interface RunningWorkRow {
  run_id: string;
  item_id: string;
  item_title?: string;
  place?: number;
  node_id?: string;
  node_kind: string;
  seat?: string;
  seat_source?: string;
  backend?: string;
  express: boolean;
  lane_id?: string;
  started_at_ms: number;
  state_since_ms: number;
  leased_until_ms?: number | null;
  lease_no_expiry?: boolean;
  log_grew_at_ms?: number | null;
  latest_action: { kind: string; text: string };
  spend: LiveSpend;
}

export interface RunningWorkPayload {
  summary: RunningWorkSummary;
  rows: RunningWorkRow[];
}

export interface SnapshotData {
  push?: PushSummary;
  alarms?: BackendAlarm[];
  sources: SourceStates;
  /** The server's own counts, when the build that answered has them. */
  counts?: ServerCounts;
  drain_summary: DrainSummary;
  lanes: LaneView[];
  running_work?: RunningWorkSummary;
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
  selected_workflow?: string;
  workflow_source?: string;
  item_id: string;
  title: string;
  body_ref: string;
  design_doc: string;
  /** The item's fence, decoded: an item that names no path answers an empty array. */
  allowed_paths: string[];
  projected_state: ItemProjection;
}

export interface VerdictFindingView {
  kind: "gap" | "regression" | "unasked-change";
  case: string;
  paths: string[];
}

export interface EarlierFindingFateView {
  criterion_id?: string;
  finding: string;
  paths?: string[];
  fate: "fixed" | "not-fixed";
}

export interface VerdictExaminedView {
  whole: boolean;
  unexamined_paths?: string[];
}

export interface VerdictComparisonView {
  previous_run_id?: string;
  all_earlier_fixed: boolean;
  new_finding_at_new_place: boolean;
  new_finding_after_fix: boolean;
  files_delta?: number;
  added_lines_delta?: number;
  repair_growing: boolean;
  has_unexamined_paths: boolean;
}

/** One item's dossier. It carries no question list: the board does not track question
 * state, and the one question fact the pane shows — the count — comes from the document
 * route, which owns the parse (design §8.1, ledger `D-OBS6`). */
export interface VerdictView {
  criterion_id: string;
  verdict: string;
  actor: string;
  actor_kind: string;
  claim_class: string;
  recorded_at: string;
  reason?: string;
  evidence?: string[];
  findings?: VerdictFindingView[];
  earlier_finding_fates?: EarlierFindingFateView[];
  examined?: VerdictExaminedView;
}

export interface ItemRunView {
  run_id: string;
  lane_id: string;
  backend: string;
  seat?: string;
  workflow?: string;
  node?: string;
  state: string;
  exit_code: number | null;
  started_at_ms: number;
  finished_at_ms: number;
  end_cause: string | null;
  usage_tokens: number | null;
  cost_usd: number | null;
}

export interface OwnerDecisionView {
  decision_id: string;
  text: string;
  scope_kind: string;
  scope_ref: string;
  decided_by: string;
  decided_at_ms: number;
  status: string;
  supersedes: string;
  session_id: string;
}

export interface StateMoveView {
  event_seq: number;
  kind: string;
  at_ms: number;
  from: string;
  to: string;
  action: string;
  reason: string;
  actor: string;
}

export interface SplitSignalView {
  signal: string;
  files?: number;
  added_lines?: number;
  file_limit?: number;
  added_line_limit?: number;
}

export interface SplitCallView {
  call_id: number;
  caller: string;
  run_id: string;
  donor_ref?: string;
  donor_sha?: string;
  base_sha?: string;
  signals: SplitSignalView[];
  status: string;
  design_run_id?: string;
  no_split_reason?: string;
  last_rejection_reason?: string;
  created_at?: string;
  answered_at?: string;
}

export interface VerifyRefusalView {
  event_seq: number;
  at_ms: number;
  run_id: string;
  attempt: number | null;
  detail: string;
  replan: boolean;
  findings?: (VerdictFindingView & { criterion: string })[];
  earlier_finding_fates?: EarlierFindingFateView[];
  examined?: VerdictExaminedView;
  verdict_comparison?: VerdictComparisonView;
  candidate_size?: {
    files: number;
    added_lines: number;
    file_limit: number;
    added_line_limit: number;
  };
  split_call?: SplitCallView;
  next_run_id: string | null;
  next_attempt: number | null;
  next_step: string | null;
  next_seat: string | null;
  next_event_seq: number | null;
  next_at_ms: number | null;
}

export interface GraphRevisionView {
  revision_id: number;
  proposing_run: string;
  proposing_node: string;
  target: string;
  workflow: string;
  rule: string;
  detail: string;
  before_nodes: number;
  after_nodes: number;
  recorded_at_ms: number;
}

export interface ItemNodeSegment {
  ordinal: number;
  started_at_ms?: number;
}

export interface ItemNodeLog {
  path?: string;
  present: boolean;
  bytes?: number;
  transcript_url?: string;
}

export interface ItemNodeLease {
  lane_id: string;
  active: boolean;
  leased_until_ms?: number | null;
  lease_no_expiry?: boolean;
}

export interface ItemNodeEnding {
  state: string;
  exit_code?: number | null;
  end_cause?: string;
  started_at_ms?: number;
  finished_at_ms?: number;
}

export interface ItemNodeSpend {
  turns?: number | null;
  tokens?: number | null;
  cost_usd?: number | null;
  live?: boolean;
  note?: string;
}

export interface ItemNodeExecution {
  run_id: string;
  backend?: string;
  seat?: string;
  segment_count: number;
  segments: ItemNodeSegment[];
  log?: ItemNodeLog;
  lease?: ItemNodeLease;
  ending?: ItemNodeEnding;
  spend?: ItemNodeSpend;
}

export interface ItemGraphNode {
  id: string;
  parent: string;
  kind: string;
  seat: string;
  command_ref: string;
  command: string[] | null;
  access: string;
  state: string;
  level: number;
  runs: string[];
  executions?: ItemNodeExecution[];
  children: Record<string, ChildrenRecord>;
  outputs: { name: string; type: string; run_id: string; body: string | null; note?: string }[];
}
export interface ItemGraphAttempt { attempt: number; nodes: ItemGraphNode[] }

export interface SplitDonorView {
  run_id?: string;
  ref?: string;
  sha: string;
  base_sha?: string;
}

export interface SplitPartStatusView {
  label: string;
  item_id: string;
  title: string;
  share: string;
  leaves?: Record<string, string>;
  follows?: string[];
  required: boolean;
  takes?: string;
  paths?: string[];
  shared_files?: string[];
  inherited_findings?: string[];
  state: string;
  landed_commit?: string;
  landing_id?: string;
}

export interface SplitCoverageEntryView {
  clause?: string;
  part_label?: string;
  part_item_id?: string;
  closing_check?: boolean;
}

export interface SplitCoverageRowView {
  criterion_id: string;
  criterion_text: string;
  entries: SplitCoverageEntryView[];
}

export interface SplitParentDetailView {
  owner_words: string;
  donor?: SplitDonorView;
  total_parts: number;
  required_parts: number;
  required_landed: number;
  progress_summary: string;
  parts: SplitPartStatusView[];
  coverage: SplitCoverageRowView[];
}

export interface SplitSiblingRef {
  label: string;
  item_id: string;
  title: string;
  state: string;
}

export interface SplitLeafView {
  sibling_label: string;
  sibling_item_id: string;
  sibling_title: string;
  sibling_state: string;
  work: string;
}

export interface SplitPartDetailView {
  parent_id: string;
  parent_title: string;
  donor?: SplitDonorView;
  label: string;
  title: string;
  share: string;
  required: boolean;
  takes?: string;
  paths?: string[];
  shared_files?: string[];
  inherited_findings?: string[];
  follows?: SplitSiblingRef[];
  leaves?: SplitLeafView[];
}

export interface ItemSplitView {
  parent?: SplitParentDetailView;
  part?: SplitPartDetailView;
}

export interface ItemData {
  graph_attempts?: ItemGraphAttempt[];
  progress?: ProgressClaim[];
  idea?: { words: string; words_path: string; in_tree: boolean; outcomes?: { item_id: string; title: string; state: string }[] };
  graph_revisions?: GraphRevisionView[];
  usage?: UsageSum;
  comments?: CommentItemView;
  item: ItemDetail;
  verdicts: VerdictView[];
  closed_blockers: string[];
  criteria: CriterionView[];
  blocked_by: string[];
  unblocks?: string[];
  decisions: DispatchView[];
  attempts: AttemptView[];
  runs: ItemRunView[];
  owner_decisions: OwnerDecisionView[];
  state_moves: StateMoveView[];
  verify_refusals?: VerifyRefusalView[];
  work_summary?: string;
  linked_alerts?: LinkedAlertView[];
  split?: ItemSplitView;
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
  seat?: string;
  workflow?: string;
  node?: string;
  state: string;
  exit_code: number | null;
  started_at_ms: number;
  finished_at_ms: number;
  usage_tokens: number | null;
  cost_usd: number | null;
}

export interface SavedWork {
  index_ref?: string;
  ref: string;
  summary: string;
  reason: string;
  pruned?: boolean;
}

export interface RunData {
  usage?: RunUsage;
  preserved_work?: SavedWork[];
  run: RunDetail;
  stages: StageView[];
  claim_checks: ClaimView[];
  cost: CostView;
  completeness: CompletenessView;
  /** Where the run's wall time went; absent on a server that predates the field. */
  timing?: RunTiming;
  /** The drain's own scheduling decisions about the run, oldest first. */
  schedule?: ScheduleEntry[];
}

/** One figure of the wall-time split. `ms` is null when the run's records do not carry it:
 * render "not recorded", never zero. */
export interface TimingMetric {
  ms: number | null;
  epistemic_state: EpistemicState | "derived" | "lower_bound";
  source?: string;
  note?: string;
}

export interface RunTiming {
  wall_ms: TimingMetric;
  model_ms: TimingMetric;
  tool_ms: TimingMetric;
  tool_active_ms: TimingMetric;
  slot_wait_ms: TimingMetric;
  uncovered_ms?: TimingMetric;
}

export interface ScheduleEntry {
  at_ms: number | null;
  seq?: number;
  category: "allocation" | "gate" | "refusal" | "landing" | "retry" | "other";
  kind: string;
  summary: string;
  source: string;
  links?: Record<string, string>;
  epistemic_state: EpistemicState;
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
  /** Source, byte offset and entry position; independent of chronological sequence. */
  id?: string;
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
export interface DesignerBatch {
  item_id: string;
  run_id: string;
  reason: string;
  cost_usd: number | null;
}
export interface DesignerView {
  session_id: string;
  last_request: string;
  cache_lifetime_seconds: number | null;
  context_tokens: number | null;
  compacted: boolean;
  state: "warm" | "expired" | "compacted";
  warm_until: string | null;
  batches: DesignerBatch[];
  cost_usd: number | null;
  missing_costs: number;
}
export interface DesignsData {
  designs: {path: string; last_designer: DesignerView | null}[];
  arrivals: {within_lifetime: number; after_lifetime: number; no_session: number; unmeasured: number};
}
export interface CommentItemView {
  document: string;
  runs: DesignerBatch[];
  confirmation_note: string;
  comments: {id: string; comment: string; reactions: unknown[]; replies: {id: string; run_id: string; round: number; summary: string; state: "waiting on the served checkout" | "written" | "delivered" | "dropped" | "not confirmed"}[]}[];
}

export interface DocData {
  last_designer?: DesignerView | null;
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

/** What `GET /api/v1/why` says about the Vantage index its questions came from. "refreshing"
 * means an index older than the documents was used while a rebuild runs, or after one failed. */
export interface DocumentIndexStatus {
  state: "current" | "refreshing" | "failed" | "unavailable";
  age_ms?: number;
  refresh_error?: string;
  message?: string;
}

export interface AlertItemLinkView {
  alert_id: string;
  item_id: string;
  item_title?: string;
  item_state?: string;
  relation: string;
  linked_at: number;
  linked_by?: string;
  note?: string;
  event_seq: number;
  active_silence: boolean;
}

export interface AlertTransitionView {
  seq: number;
  timestamp: number;
  state_change: string;
  kind: string;
  actor: string;
  detail?: string;
}

export interface AlertTriageView {
  state: string;
  outcome?: string;
  outcome_kind?: string;
  run_id?: string;
  active_run_id?: string;
  item_id?: string;
  finding?: string;
  triaged_at?: number;
}

export interface AlertEpisodeView {
  alert_id: string;
  rule_id: string;
  subject_key: string;
  episode_num: number;
  state: string;
  settlement_state: string;
  severity: string;
  condition_fingerprint: string;
  opened_seq: number;
  opened_at: number;
  last_observed_seq: number;
  last_observed_at: number;
  observation_count: number;
  unsettled_for_ms?: number;
  resolved_seq?: number;
  resolved_at?: number;
  resolved_reason?: string;
  variables: Record<string, string>;
  variables_json: string;
  outcome?: string;
  item_link_kind?: string;
  tracking_item_id?: string;
  silence_id?: string;
  investigated_by?: string;
  investigated_at?: number;
  investigation_cause?: string;
  investigation_evidence?: string;
  settled_by?: string;
  settled_at?: number;
  settled_seq?: number;
  acked_by?: string;
  acked_at?: number;
  acked_note?: string;
  escalated_to?: string;
  triage?: AlertTriageView;
  bundle_json?: string;
  linked_items: AlertItemLinkView[];
  latest_transition?: AlertTransitionView;
}

export interface AlertSilenceView {
  silence_id: string;
  rule_id: string;
  vars: Record<string, string>;
  vars_json: string;
  tracking_item_id: string;
  tracking_item_title?: string;
  tracking_item_state?: string;
  created_by: string;
  created_at: number;
  reason: string;
  investigation_cause: string;
  investigation_evidence: string;
  source_alert_id?: string;
  silence_until_ms?: number;
  silence_on_fingerprint?: string;
}

export interface AlertTriageCounts {
  untriaged: number;
  triaging: number;
  settled: number;
  escalated: number;
}

export interface AlertCounts {
  total: number;
  active: number;
  open: number;
  acknowledged: number;
  silenced: number;
  resolved: number;
  unsettled: number;
  uninvestigated: number;
  investigating: number;
  investigated: number;
  settled: number;
  escalated?: number;
  active_silences: number;
  triage?: AlertTriageCounts;
}

export interface AlertsData {
  absent: boolean;
  counts: AlertCounts;
  episodes: AlertEpisodeView[];
  active_silences: AlertSilenceView[];
}

export interface AlertDetailData {
  absent: boolean;
  episode: AlertEpisodeView;
  transitions: AlertTransitionView[];
  linked_items: AlertItemLinkView[];
  related_episodes: AlertEpisodeView[];
}

export interface LinkedAlertView {
  alert_id: string;
  rule_id: string;
  subject_key: string;
  state: string;
  settlement_state: string;
  severity: string;
  relation: string;
  linked_at: number;
  linked_by?: string;
  note?: string;
  event_seq: number;
}
