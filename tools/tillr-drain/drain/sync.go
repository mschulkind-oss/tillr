// Architecture and invariants: docs/reference/tillr-queue-persistence.md
package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/bgactors"
	"github.com/mschulkind-oss/tillr-drain/internal/gateland"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
)

// SyncOutcome is what the loop's startup sync did, read from the item.sync record the item
// binary prints. Notes are pre-rendered as `file:line (heading) item: rule: message`, the
// form the item face prints them in, because the loop only reports them.
// SyncSkippedBlock is one unfenced item directive outside the selected roadmap scope.
type SyncSkippedBlock struct {
	File   string `json:"file"`
	ItemID string `json:"item_id"`
}

type SyncOutcome struct {
	// ItemFaults retains attribution instead of making readers parse display text.
	SkippedBlocks  []SyncSkippedBlock `json:"skipped_blocks,omitempty"`
	SkippedCount   int                `json:"skipped_count,omitempty"`
	SkippedFiles   []string           `json:"skipped_files,omitempty"`
	ItemFaults     map[string]string  `json:"item_faults,omitempty"`
	Created        int                `json:"created"`
	Updated        int                `json:"updated"`
	Unchanged      int                `json:"unchanged"`
	Frozen         []string           `json:"frozen,omitempty"`
	Findings       []string           `json:"findings,omitempty"`
	Warnings       int                `json:"warnings"`
	UnsharedOpen   int                `json:"unshared_open"`
	StaleDocBlocks int                `json:"stale_doc_blocks"`
	// Skipped names why no sync ran, for a step set that drives no repository.
	Skipped string `json:"skipped,omitempty"`
}

// Line is the outcome as the loop's one summary line.
func (o SyncOutcome) Line() string {
	if o.Skipped != "" {
		return "sync skipped: " + o.Skipped
	}
	skippedCount := o.SkippedCount
	if skippedCount == 0 && len(o.SkippedBlocks) > 0 {
		skippedCount = len(o.SkippedBlocks)
	}
	skipped := ""
	if skippedCount > 0 {
		skipped = fmt.Sprintf(", skipped %d", skippedCount)
	}
	return fmt.Sprintf("sync: created %d, updated %d, unchanged %d, frozen %d, findings %d, warnings %d%s; unshared_open: %d; stale_doc_blocks: %d",
		o.Created, o.Updated, o.Unchanged, len(o.Frozen), len(o.Findings), o.Warnings, skipped, o.UnsharedOpen, o.StaleDocBlocks)
}

// syncNote is one finding, warning or frozen definition in the item.sync payload.
type syncNote struct {
	File    string `json:"file"`
	Heading string `json:"heading"`
	Line    int    `json:"line"`
	ItemID  string `json:"item_id"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

func (n syncNote) String() string {
	where := fmt.Sprintf("%s:%d", n.File, n.Line)
	if n.Heading != "" {
		where += " (" + n.Heading + ")"
	}
	if n.ItemID != "" {
		where += " " + n.ItemID
	}
	return fmt.Sprintf("%s: %s: %s", where, n.Rule, n.Message)
}

// syncPayload is the part of the item.sync payload the loop reads.
type syncPayload struct {
	SkippedBlocks  []SyncSkippedBlock `json:"skipped_blocks"`
	SkippedCount   int                `json:"skipped_count"`
	SkippedFiles   []string           `json:"skipped_files"`
	CreatedCount   int                `json:"created_count"`
	UpdatedCount   int                `json:"updated_count"`
	UnchangedCount int                `json:"unchanged_count"`
	Frozen         []syncNote         `json:"frozen"`
	Findings       []syncNote         `json:"findings"`
	Warnings       []syncNote         `json:"warnings"`
	UnsharedOpen   int                `json:"unshared_open"`
	StaleDocBlocks int                `json:"stale_doc_blocks"`
}

// SetSyncRoadmaps replaces the roadmap list used on subsequent Sync calls.
func (s *ProcessSteps) SetSyncRoadmaps(roadmaps []string) {
	s.syncRoadmapsMu.Lock()
	defer s.syncRoadmapsMu.Unlock()
	if len(roadmaps) == 0 {
		s.SyncRoadmaps = nil
		return
	}
	s.SyncRoadmaps = append([]string(nil), roadmaps...)
}

func (s *ProcessSteps) syncRoadmaps() []string {
	s.syncRoadmapsMu.RLock()
	defer s.syncRoadmapsMu.RUnlock()
	return append([]string(nil), s.SyncRoadmaps...)
}

// Sync runs `tillr-item sync` over the loop's repository and store. Exit 0 and exit 1 are
// both a sync that ran: 1 says a document carried a finding, and the documents without one
// were written. Any other exit, or a binary that cannot be started, is a sync that could not
// run, and the loop treats it as the fault it is.
func (s *ProcessSteps) Sync(ctx context.Context) (SyncOutcome, error) {
	if s.ItemBin == "" {
		return SyncOutcome{}, Usagef("the loop was given no item binary: it cannot read the queue from the documents")
	}
	if s.Root == "" {
		return SyncOutcome{Skipped: "the step set names no repository root"}, nil
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	args := []string{"sync", "--store", s.Store, "--root", s.Root,
		"--now", now().UTC().Format(time.RFC3339), "--json"}
	for _, roadmap := range s.syncRoadmaps() {
		args = append(args, "--roadmap", roadmap)
	}
	if len(s.QuestionsCmd) > 0 {
		args = append(args, "--questions-cmd", strings.Join(s.QuestionsCmd, " "))
	}
	cmd := exec.CommandContext(ctx, s.ItemBin, args...)
	var stderr stderrTail
	cmd.Stderr = &stderr
	if os.Getenv("TILLR_SQLITE_TRACE") == "1" {
		traces := &sqliteTraceForwarder{Output: os.Stderr}
		cmd.Stderr = io.MultiWriter(&stderr, traces)
		defer traces.Flush()
	}
	out, err := cmd.Output()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit) && exit.ExitCode() == record.ExitNegative:
	case errors.As(err, &exit) && exit.ExitCode() == record.ExitIncompatible:
		// The store the loop was handed cannot be opened: the same fault the first tick would
		// answer, so it keeps the class the loop's face promises for it.
		return SyncOutcome{}, Incompatiblef("%s sync could not open the store %s: %s", s.ItemBin, s.Store, strings.TrimSpace(stderr.String()))
	case errors.As(err, &exit) && exit.ExitCode() == record.ExitUsage:
		return SyncOutcome{}, Usagef("%s sync could not run: %s", s.ItemBin, strings.TrimSpace(stderr.String()))
	default:
		return SyncOutcome{}, Externalf("%s sync could not run: %v: %s", s.ItemBin, err, strings.TrimSpace(stderr.String()))
	}
	var env struct {
		Kind    string      `json:"kind"`
		Payload syncPayload `json:"payload"`
	}
	if err := json.Unmarshal(out, &env); err != nil || env.Kind != "item.sync" {
		return SyncOutcome{}, Externalf("%s sync answered no item.sync record (%v): %s", s.ItemBin, err, strings.TrimSpace(string(out)))
	}
	p := env.Payload
	outcome := SyncOutcome{
		Created: p.CreatedCount, Updated: p.UpdatedCount, Unchanged: p.UnchangedCount,
		SkippedBlocks: p.SkippedBlocks, SkippedCount: p.SkippedCount,
		SkippedFiles: p.SkippedFiles,
		Warnings:     len(p.Warnings), UnsharedOpen: p.UnsharedOpen, StaleDocBlocks: p.StaleDocBlocks,
	}
	for _, n := range p.Frozen {
		outcome.Frozen = append(outcome.Frozen, n.String())
	}
	for _, n := range p.Findings {
		outcome.Findings = append(outcome.Findings, n.String())
	}
	for _, n := range append(p.Frozen, p.Findings...) {
		if n.ItemID != "" {
			if outcome.ItemFaults == nil {
				outcome.ItemFaults = map[string]string{}
			}
			if outcome.ItemFaults[n.ItemID] != "" {
				outcome.ItemFaults[n.ItemID] += "; "
			}
			outcome.ItemFaults[n.ItemID] += n.String()
		}
	}
	if len(p.SkippedFiles) > 0 {
		if err := s.attributeSkippedDefinitions(ctx, p, &outcome); err != nil {
			return SyncOutcome{}, err
		}
	}
	return outcome, nil
}

// Item sync writes whole files atomically. A finding against A therefore leaves
// B stale too when both definitions live in the same skipped document.
func (s *ProcessSteps) attributeSkippedDefinitions(ctx context.Context, p syncPayload, outcome *SyncOutcome) error {
	canonical := func(file string) string {
		if !filepath.IsAbs(file) {
			file = filepath.Join(s.Root, file)
		}
		return filepath.Clean(file)
	}
	skipped := map[string]string{}
	for _, file := range p.SkippedFiles {
		detail := "definition sync skipped " + file
		for _, n := range p.Findings {
			if canonical(n.File) == canonical(file) {
				detail += "; " + n.String()
			}
		}
		skipped[canonical(file)] = detail
	}
	st, err := store.OpenRead(ctx, s.Store)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT item_id, body_ref FROM items`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, ref string
			if err := rows.Scan(&id, &ref); err != nil {
				return err
			}
			file, _, _ := strings.Cut(ref, "#")
			if detail, held := skipped[canonical(file)]; held {
				if outcome.ItemFaults == nil {
					outcome.ItemFaults = map[string]string{}
				}
				outcome.ItemFaults[id] = detail
			}
		}
		return rows.Err()
	})
}

// startSyncActor runs the document and comment sync background actor outside
// the scheduling pass (D-SN3, Section 5.7), woken by docs.changed (relay and
// inotify on tracked Markdown files) + resync and reconciled every
// StorePollInterval (15s default).
func (l *Loop) startSyncActor(ctx context.Context, result *LoopResult) func() {
	actorCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	l.actors.Ensure()
	l.mu.Lock()
	l.actors.Sync.Active = true
	timer := l.actors.Sync.Timer
	l.mu.Unlock()

	var watcher bgactors.DocsWatcher
	var watcherDone chan struct{}
	if l.notifyEnabled() && l.cfg.Root != "" {
		if w := bgactors.NewDocsWatcher(l.cfg.Root); w != nil {
			watcher = w
			watcherDone = make(chan struct{})
			go func() {
				defer close(watcherDone)
				debounce := time.NewTimer(time.Hour)
				if !debounce.Stop() {
					select {
					case <-debounce.C:
					default:
					}
				}
				defer debounce.Stop()
				pending := false
				for {
					select {
					case <-actorCtx.Done():
						return
					case _, ok := <-w.Events():
						if !ok {
							return
						}
						if !pending {
							pending = true
							debounce.Reset(50 * time.Millisecond)
						}
					case <-debounce.C:
						if pending {
							pending = false
							l.signalSyncWake()
						}
					}
				}
			}()
		}
	}

	go func() {
		defer close(done)
		defer func() {
			l.mu.Lock()
			l.actors.Sync.Active = false
			l.mu.Unlock()
		}()
		wait := l.cfg.StorePollInterval
		if wait <= 0 {
			wait = DefaultFallbackPollInterval
		}
		gateland.RunLoop(actorCtx, wait, timer, func() <-chan struct{} {
			if !l.notifyEnabled() {
				return nil
			}
			return l.actors.SyncWakeChan()
		}, func() {
			if actorCtx.Err() != nil || l.isRestartReplaced() {
				return
			}
			if stopped, err := l.stopRequested(actorCtx); stopped || err != nil {
				return
			}
			beforeSeq, _ := l.readEventHeadSeq(actorCtx)
			if err := l.runSyncStep(actorCtx, result, false); err != nil {
				l.reportActorError(actorCtx, "sync", err)
				return
			}
			if watcher != nil {
				l.updateDocsWatcherDirs(actorCtx, watcher)
			}
			afterSeq, _ := l.readEventHeadSeq(actorCtx)
			if afterSeq != beforeSeq {
				l.actors.DocsGen.Add(1)
				l.signalPassWake()
			}
		})
	}()

	return func() {
		cancel()
		if watcher != nil {
			watcher.Close()
			<-watcherDone
		}
		<-done
	}
}

func (l *Loop) runSyncStep(ctx context.Context, result *LoopResult, startup bool) error {
	l.actors.SyncMu.Lock()
	defer l.actors.SyncMu.Unlock()
	l.actors.Sync.Runs.Add(1)

	if err := l.refreshPolicy(); err != nil {
		return err
	}
	if !startup {
		if err := l.refreshQuotas(ctx); err != nil {
			return err
		}
	}
	if err := l.intakeComments(ctx); err != nil {
		return err
	}
	if err := l.confirmCommentReplies(ctx); err != nil {
		return err
	}
	if err := l.flushCommentDeliveries(ctx); err != nil {
		return err
	}
	if !startup {
		var end func()
		if !l.actorsActive() {
			end = l.startPassPhase("refresh_documents")
		}
		_, err := l.refreshDocuments(ctx, result)
		if end != nil {
			end()
		}
		if err != nil {
			return err
		}
	}
	if changed, err := l.refreshCommentReadiness(ctx); err != nil {
		return err
	} else if changed {
		l.actors.DocsGen.Add(1)
		l.signalPassWake()
	}
	if changed, err := l.refreshSyncPlannerCaches(ctx); err != nil {
		return err
	} else if changed {
		l.actors.DocsGen.Add(1)
		l.signalPassWake()
	}
	if err := l.retireDesigners(ctx); err != nil {
		return err
	}
	if !l.nonLanding {
		if err := l.releaseGeneratedTriage(ctx); err != nil {
			return err
		}
		if err := l.settleUnansweredDesignRuns(ctx); err != nil {
			return err
		}
		if err := l.settleMisreadDesignAnswers(ctx); err != nil {
			return err
		}
	}
	return nil
}

// refreshCommentReadiness evaluates checkCommentDispatch for queued/blocked
// comment items outside the scheduling pass and caches readiness for the pass.
func (l *Loop) refreshCommentReadiness(ctx context.Context) (bool, error) {
	if l.cfg.Store == "" {
		return false, nil
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, err
	}
	defer s.Close()

	var checks []plannedApplication
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		declared, err := definition.ForReader(r)
		if err != nil {
			return err
		}
		if !holdEnabled(declared.Parsed, ReasonVantageUnavailable) {
			return nil
		}
		rows, err := readItems(r)
		if err != nil {
			return err
		}
		folds, err := readVersionFolds(r)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if !item.IsCommentItem(row.ItemID) || (row.State != StateInQueue && row.State != StateBlocked) {
				continue
			}
			d := Decision{ItemID: row.ItemID, Action: ActionHold}
			version, err := inputVersionFrom(r, d, folds)
			if err != nil {
				return err
			}
			checks = append(checks, plannedApplication{
				decision:      d,
				version:       version,
				definition:    &declared.Parsed,
				definitionSHA: declared.SHA256,
			})
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	readyMap := make(map[string]bool, len(checks))
	at := l.cfg.Now().UTC()
	closedAny := false
	for _, p := range checks {
		if l.planStillActive(p) {
			continue
		}
		ready, closed, err := l.checkCommentDispatch(ctx, p, at)
		if err != nil {
			return false, err
		}
		readyMap[p.decision.ItemID] = ready
		closedAny = closedAny || closed
	}
	changed := l.actors.SetCachedCommentReady(readyMap)
	return changed || closedAny, nil
}

// refreshSyncPlannerCaches preloads Vantage questions and document-sharing
// prerequisites outside the scheduling pass so pure passes invoke zero git or
// Vantage subprocesses.
func (l *Loop) refreshSyncPlannerCaches(ctx context.Context) (bool, error) {
	if l.cfg.Root == "" {
		return false, nil
	}
	var preloaded *VantageQuestions
	tip := l.cachedTrunkTip()
	if tip != "" {
		if saved, ok := LoadSavedQuestionsIndex(l.cfg.Root, tip, l.cfg.QuestionsCmd, true); ok {
			preloaded = saved
		}
	}
	if preloaded == nil {
		if raw, err := vantage.RunIndexCommand(ctx, l.cfg.Root, l.cfg.QuestionsCmd); err == nil {
			idx := vantage.ParseIndex(raw, vantage.IndexCommandName(l.cfg.QuestionsCmd))
			if tip != "" {
				if _, perr := idx.Questions(""); perr == nil {
					_ = SaveQuestionsIndex(l.cfg.Root, tip, l.cfg.QuestionsCmd, raw)
				}
			}
			preloaded = vantage.Preloaded(l.cfg.Root, idx)
		} else {
			preloaded = vantage.Preloaded(l.cfg.Root, vantage.IndexError(err))
		}
	}
	l.actors.SetCachedVantageQuestions(preloaded)

	if l.cfg.Store == "" {
		return false, nil
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, err
	}
	defer s.Close()

	next := bgactors.DocPrereqsCache{Valid: true}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		declared, err := definition.ForReader(r)
		if err != nil {
			return err
		}
		if hasPrerequisite(declared.Parsed, definition.PrerequisiteDesignDocument) && declared.Parsed.Completes == "landing" {
			proseRewrite := l.proseRewriteSettings()
			unshared, rewriteHolds, rewriteOwnerHolds, err := documentPrerequisites(r, l.cfg.Root, &proseRewrite, preloaded)
			if err != nil {
				return err
			}
			next.Unshared = unshared
			next.RewriteHolds = rewriteHolds
			next.RewriteOwnerHolds = rewriteOwnerHolds
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return l.actors.SetCachedDocPrereqs(next), nil
}

func (l *Loop) updateDocsWatcherDirs(ctx context.Context, w bgactors.DocsWatcher) {
	if l.cfg.Store == "" || l.cfg.Root == "" {
		return
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return
	}
	defer s.Close()
	dirs := map[string]bool{}
	_ = s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := readItems(r)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.DesignDoc == "" {
				continue
			}
			path := row.DesignDoc
			if !filepath.IsAbs(path) {
				path = filepath.Join(l.cfg.Root, path)
			}
			dirs[filepath.Dir(path)] = true
		}
		return nil
	})
	w.UpdateDocs(dirs)
}
