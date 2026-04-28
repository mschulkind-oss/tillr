// Package orchestrator runs the post-reset Stage 0 daemon loop:
// poll the queue, dispatch up to N parallel `claude -p` invocations
// per persona, capture their JSON output, and apply the structural
// side effects (append context, comment on feature, file follow-ups,
// update feature status).
//
// The orchestrator is the *tool* enforcement of the persona
// lifecycle (Principle Zero): personas don't have to remember to
// call `tillr persona append` — the orchestrator does it for them
// based on schema-validated JSON the spawned agent emits.
package orchestrator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/models"
	"github.com/mschulkind-oss/tillr/internal/persona"
)

// Config bundles tunables. Defaults applied if zero values.
//
// MVP scope (2026-04-28): no metering. claude -p runs unmetered.
// We rely on the user's Claude MAX subscription quota and on the
// dispatcher being slow enough (PollInterval) to be observable.
// Re-add caps as features when there's evidence they're needed.
type Config struct {
	ProjectRoot     string
	MaxParallelism  int           // default 1
	PollInterval    time.Duration // default 5s
	StaleRunMinutes int           // running > this → marked errored on startup; default 60
	ClaudeBinary    string        // default "claude"
	DryRun          bool          // if true, use stub spawner that doesn't actually invoke claude
	WorkerTimeout   time.Duration // hard kill after this (safety net, not metering); default 60 min
}

func (c *Config) applyDefaults() {
	if c.MaxParallelism <= 0 {
		c.MaxParallelism = 1
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 5 * time.Second
	}
	if c.StaleRunMinutes <= 0 {
		c.StaleRunMinutes = 60
	}
	if c.ClaudeBinary == "" {
		c.ClaudeBinary = "claude"
	}
	if c.WorkerTimeout <= 0 {
		c.WorkerTimeout = 60 * time.Minute
	}
}

// LoadConfigFromDB reads any orchestrator.* keys from the config
// table and overlays them on top of the given default config.
func LoadConfigFromDB(database *sql.DB, base Config) (Config, error) {
	all, err := db.ConfigList(database)
	if err != nil {
		return base, err
	}
	if v, ok := all["orchestrator.max-parallelism"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			base.MaxParallelism = n
		}
	}
	if v, ok := all["orchestrator.poll-interval-sec"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			base.PollInterval = time.Duration(n) * time.Second
		}
	}
	if v, ok := all["orchestrator.claude-binary"]; ok {
		base.ClaudeBinary = v
	}
	if v, ok := all["orchestrator.worker-timeout-min"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			base.WorkerTimeout = time.Duration(n) * time.Minute
		}
	}
	if v, ok := all["orchestrator.dry-run"]; ok {
		base.DryRun = v == "true" || v == "1"
	}
	base.applyDefaults()
	return base, nil
}

// Daemon is the long-lived orchestrator process.
type Daemon struct {
	db  *sql.DB
	cfg Config

	mu      sync.Mutex
	running map[int64]*Worker // run_id → worker

	// spawn is indirected so tests can inject a fake.
	spawn SpawnFunc

	logger *log.Logger
}

// NewDaemon constructs a Daemon with the given config. If spawn is
// nil, the real claude-CLI spawner is used.
func NewDaemon(database *sql.DB, cfg Config, spawn SpawnFunc) *Daemon {
	cfg.applyDefaults()
	if spawn == nil {
		spawn = ClaudeSpawn
	}
	return &Daemon{
		db:      database,
		cfg:     cfg,
		running: make(map[int64]*Worker),
		spawn:   spawn,
		logger:  log.New(log.Writer(), "[orchestrator] ", log.LstdFlags),
	}
}

// Run blocks until ctx is cancelled. On startup, marks any
// stale-running rows as errored. Then enters the poll loop.
func (d *Daemon) Run(ctx context.Context) error {
	if n, err := db.MarkStaleRunsErrored(d.db, d.cfg.StaleRunMinutes); err != nil {
		d.logger.Printf("warning: marking stale runs: %v", err)
	} else if n > 0 {
		d.logger.Printf("marked %d stale runs as errored", n)
	}

	d.logger.Printf("starting (max-parallelism=%d, poll=%s, dry-run=%v)",
		d.cfg.MaxParallelism, d.cfg.PollInterval, d.cfg.DryRun)

	tick := time.NewTicker(d.cfg.PollInterval)
	defer tick.Stop()

	d.tickOnce(ctx) // first poll immediately

	for {
		select {
		case <-ctx.Done():
			d.logger.Printf("shutting down; waiting for %d workers", d.activeCount())
			d.waitWorkers()
			d.logger.Print("stopped")
			return nil
		case <-tick.C:
			d.tickOnce(ctx)
		}
	}
}

func (d *Daemon) tickOnce(ctx context.Context) {
	avail := d.cfg.MaxParallelism - d.activeCount()
	if avail <= 0 {
		return
	}

	personas, err := persona.List(d.cfg.ProjectRoot)
	if err != nil {
		d.logger.Printf("error listing personas: %v", err)
		return
	}

	project, err := db.GetProject(d.db)
	if err != nil {
		d.logger.Printf("error loading project: %v", err)
		return
	}

	for _, p := range personas {
		if avail <= 0 {
			break
		}
		feat, err := db.ClaimNextFeature(d.db, project.ID, p.Name)
		if err != nil {
			d.logger.Printf("claim error for %s: %v", p.Name, err)
			continue
		}
		if feat == nil {
			continue
		}
		d.startWorker(ctx, p, feat)
		avail--
	}
}

func (d *Daemon) startWorker(ctx context.Context, p persona.Persona, feat *models.Feature) {
	runID, err := db.StartOrchestratorRun(d.db, feat.ID, p.Name, "claude")
	if err != nil {
		d.logger.Printf("start run error: %v", err)
		// Roll back claim
		_, _ = db.SetFeatureStatus(d.db, feat.ID, "draft")
		return
	}

	w := &Worker{
		RunID:     runID,
		Persona:   p,
		Feature:   feat,
		Cfg:       d.cfg,
		DB:        d.db,
		Spawn:     d.spawn,
		startedAt: time.Now(),
		logger:    d.logger,
	}

	d.mu.Lock()
	d.running[runID] = w
	d.mu.Unlock()

	d.logger.Printf("→ run %d  feature #%d  persona %s", runID, feat.ID, p.Name)

	go func() {
		defer func() {
			d.mu.Lock()
			delete(d.running, runID)
			d.mu.Unlock()
		}()
		w.Execute(ctx)
	}()
}

func (d *Daemon) activeCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.running)
}

func (d *Daemon) waitWorkers() {
	for {
		if d.activeCount() == 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// ActiveRuns returns the list of currently in-flight runs (snapshot).
func (d *Daemon) ActiveRuns() []models.OrchestratorRun {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]models.OrchestratorRun, 0, len(d.running))
	for _, w := range d.running {
		out = append(out, models.OrchestratorRun{
			ID:        w.RunID,
			FeatureID: w.Feature.ID,
			Persona:   w.Persona.Name,
			StartedAt: w.startedAt,
			Result:    "running",
			Model:     "claude",
		})
	}
	return out
}

// ErrAlreadyRunning is returned when an orchestrator daemon is
// already running in the project (detected via pidfile).
var ErrAlreadyRunning = errors.New("orchestrator already running")

// ErrNotRunning is returned when no daemon is running.
var ErrNotRunning = errors.New("orchestrator not running")

// PidFile path under swarf/.
func PidFile(projectRoot string) string {
	return projectRoot + "/swarf/orchestrator.pid"
}

// LogFile path under swarf/.
func LogFile(projectRoot string) string {
	return projectRoot + "/swarf/orchestrator.log"
}

// Status is the return shape of `tillr orchestrator status`.
type Status struct {
	Running    bool                     `json:"running"`
	PID        int                      `json:"pid,omitempty"`
	StartedAt  string                   `json:"started_at,omitempty"`
	Active     []models.OrchestratorRun `json:"active"`
	RecentRuns []models.OrchestratorRun `json:"recent_runs"`
	Config     Config                   `json:"config"`
}

// Spawned implements the contract: given the inputs, produce the
// effects synchronously. Used by both real daemon dispatch and tests.
type Spawned struct {
	ExitCode     int     `json:"exit_code"`
	DurationMS   int64   `json:"duration_ms"`
	CostUSD      float64 `json:"cost_usd"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	SessionID    string  `json:"session_id"`
	Model        string  `json:"model"`

	// Result is the parsed structured output the agent emitted (per
	// the schema in schema.go). May be zero-value if the run failed
	// before producing valid JSON.
	Result Result `json:"result"`

	// Stderr captures any error output for diagnostics.
	Stderr string `json:"stderr,omitempty"`

	// SpawnError is set if the spawn itself failed (binary not
	// found, schema validation failed, etc.).
	SpawnError string `json:"spawn_error,omitempty"`
}

// SpawnFunc abstracts the actual `claude -p` invocation.
type SpawnFunc func(ctx context.Context, opts SpawnOpts) Spawned

// SpawnOpts captures all inputs to a single claude -p run.
type SpawnOpts struct {
	ProjectRoot        string
	Persona            string // matches .claude/agents/<name>.md
	ContextFilePath    string // absolute path to swarf/agents/<name>/context.md (or empty)
	FeatureID          int64
	FeatureTitle       string
	FeatureDescription string
	WorkerTimeout      time.Duration
	ClaudeBinary       string
	JSONSchema         string
	WorktreeName       string

	// In dry-run / test mode, the spawner returns a pre-canned result.
	DryRunResult *Spawned
}

// Worker drives one (persona, feature) dispatch end-to-end.
type Worker struct {
	RunID   int64
	Persona persona.Persona
	Feature *models.Feature
	Cfg     Config
	DB      *sql.DB
	Spawn   SpawnFunc

	startedAt time.Time
	logger    *log.Logger
}

// Execute runs claude -p, applies side effects, finishes the run row.
func (w *Worker) Execute(ctx context.Context) {
	worktreeName := fmt.Sprintf("feature-%d", w.Feature.ID)

	opts := SpawnOpts{
		ProjectRoot:        w.Cfg.ProjectRoot,
		Persona:            w.Persona.Name,
		ContextFilePath:    w.Cfg.ProjectRoot + "/" + w.Persona.ContextPath,
		FeatureID:          w.Feature.ID,
		FeatureTitle:       w.Feature.Title,
		FeatureDescription: w.Feature.Description,
		WorkerTimeout:      w.Cfg.WorkerTimeout,
		ClaudeBinary:       w.Cfg.ClaudeBinary,
		JSONSchema:         PersonaJSONSchema,
		WorktreeName:       worktreeName,
	}

	spawned := w.Spawn(ctx, opts)
	w.applySideEffects(spawned)
}

// applySideEffects is the Principle-Zero core: regardless of whether
// the persona's prompt remembered to call any tillr CLI commands, the
// orchestrator persists the structured output as comments, context
// appends, follow-up features, and final feature status. Tools,
// not instructions.
func (w *Worker) applySideEffects(s Spawned) {
	durationMS := time.Since(w.startedAt).Milliseconds()
	resultStatus := "completed"
	errMsg := s.SpawnError
	summary := s.Result.Summary

	switch {
	case s.SpawnError != "":
		resultStatus = "error"
	case s.ExitCode != 0:
		resultStatus = "error"
		if errMsg == "" {
			errMsg = fmt.Sprintf("non-zero exit (code=%d)", s.ExitCode)
		}
	case s.Result.Result == "blocked":
		resultStatus = "blocked"
	case s.Result.Result == "needs_review":
		resultStatus = "needs_review"
	case s.Result.Result == "completed":
		resultStatus = "completed"
	}

	// 1. Append context entry to the persona's context file
	//    (orchestrator owns this — Principle Zero).
	if s.Result.ContextEntry != "" {
		entrySummary := summary
		if entrySummary == "" {
			entrySummary = fmt.Sprintf("Feature #%d: %s", w.Feature.ID, w.Feature.Title)
		}
		if err := persona.Append(w.Cfg.ProjectRoot, w.Persona.Name, entrySummary, s.Result.ContextEntry); err != nil {
			w.logger.Printf("run %d: append context error: %v", w.RunID, err)
		}
	}

	// 2. Add a comment on the feature with the run's summary.
	if summary != "" {
		_, err := db.AddComment(w.DB, &models.Comment{
			EntityType: "feature",
			EntityID:   strconv.FormatInt(w.Feature.ID, 10),
			AuthorType: "agent",
			AuthorRole: w.Persona.Name,
			Body:       summary,
		})
		if err != nil {
			w.logger.Printf("run %d: comment error: %v", w.RunID, err)
		}
	}

	// 3. File follow-up features.
	project, err := db.GetProject(w.DB)
	if err == nil {
		for _, fu := range s.Result.FollowUpFeatures {
			if fu.Title == "" {
				continue
			}
			if _, err := db.AddFeature(w.DB, project.ID, fu.Title, fu.Description, fu.Persona); err != nil {
				w.logger.Printf("run %d: follow-up file error: %v", w.RunID, err)
			}
		}
	}

	// 4. Transition feature status.
	finalStatus := "done"
	switch resultStatus {
	case "blocked":
		finalStatus = "blocked"
	case "needs_review":
		finalStatus = "human-qa"
	case "error":
		finalStatus = "error"
	}
	if _, err := db.SetFeatureStatus(w.DB, w.Feature.ID, finalStatus); err != nil {
		w.logger.Printf("run %d: set status error: %v", w.RunID, err)
	}

	// 5. Finish the orchestrator_runs row.
	if err := db.FinishOrchestratorRun(
		w.DB, w.RunID, s.ExitCode, durationMS, s.CostUSD,
		s.InputTokens, s.OutputTokens, s.SessionID,
		resultStatus, errMsg, summary,
	); err != nil {
		w.logger.Printf("run %d: finish error: %v", w.RunID, err)
	}

	w.logger.Printf("← run %d  feature #%d  persona %s  result=%s  cost=$%.4f  duration=%dms",
		w.RunID, w.Feature.ID, w.Persona.Name, resultStatus, s.CostUSD, durationMS)
}
