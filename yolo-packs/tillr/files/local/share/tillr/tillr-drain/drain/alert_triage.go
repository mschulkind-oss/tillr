package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

const defaultMaxConcurrentTriage = 2

func (l *Loop) maxConcurrentTriage() int {
	if l.cfg.MaxConcurrentTriage > 0 {
		return l.cfg.MaxConcurrentTriage
	}
	return defaultMaxConcurrentTriage
}

func (l *Loop) activeTriageCount() int {
	l.triageMu.Lock()
	defer l.triageMu.Unlock()
	return len(l.triageActive)
}

func (l *Loop) isTriageRunActive(runID string) bool {
	l.triageMu.Lock()
	defer l.triageMu.Unlock()
	_, ok := l.triageActive[runID]
	return ok
}

func (l *Loop) allocateTriageSlot() string {
	l.triageMu.Lock()
	defer l.triageMu.Unlock()
	limit := l.maxConcurrentTriage()
	for i := 1; i <= limit; i++ {
		slot := fmt.Sprintf("triage-%d", i)
		if !l.triageSlots[slot] {
			l.triageSlots[slot] = true
			return slot
		}
	}
	return ""
}

func (l *Loop) releaseTriageSlot(slot string) {
	if slot == "" {
		return
	}
	l.triageMu.Lock()
	delete(l.triageSlots, slot)
	l.triageMu.Unlock()
}

func (l *Loop) registerActiveTriage(runID, slot string) {
	l.triageMu.Lock()
	l.triageActive[runID] = slot
	l.triageSlots[slot] = true
	l.triageMu.Unlock()
}

func (l *Loop) releaseTriageRun(runID, slot string) {
	l.triageMu.Lock()
	delete(l.triageActive, runID)
	if slot != "" {
		delete(l.triageSlots, slot)
	}
	l.triageMu.Unlock()
}

func (l *Loop) signalTriageWake() {
	if l.triageWake == nil {
		return
	}
	select {
	case l.triageWake <- struct{}{}:
	default:
	}
}

func (l *Loop) consumeTriageWake() bool {
	if l.triageWake == nil {
		return false
	}
	select {
	case <-l.triageWake:
		return true
	default:
		return false
	}
}

func (l *Loop) hasTriageSeat() bool {
	p := l.currentPolicy()
	return p != nil && strings.TrimSpace(p.Seats[item.SeatTriage]) != ""
}

func (l *Loop) triageRuleIDs(ctx context.Context) map[string]bool {
	var fileRules []watch.Rule
	if l.cfg.WatchRules != nil {
		fileRules = l.cfg.WatchRules
	} else {
		var shippedFS fs.FS
		var candidates []string
		if backendsDir, err := run.ResolveBackendsDir(); err == nil {
			candidates = append(candidates, filepath.Join(filepath.Dir(backendsDir), "..", "tillr-watch", "rules"))
		}
		if l.cfg.Root != "" {
			candidates = append(candidates, filepath.Join(l.cfg.Root, "tools", "tillr-watch", "rules"))
		}
		for _, candidate := range candidates {
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				shippedFS = os.DirFS(candidate)
				break
			}
		}
		repoDir := l.cfg.WatchRulesDir
		if repoDir == "" && l.cfg.Root != "" {
			repoDir = filepath.Join(l.cfg.Root, "config", "tillr", "watch")
		}
		if loaded, err := watch.LoadRuleLayersWithOptions(shippedFS, repoDir, watch.RuleLoadOptions{AllowUnregisteredKinds: true}); err == nil {
			fileRules = loaded
		} else if shippedFS != nil || (repoDir != "" && dirExists(repoDir)) {
			l.reportf("loop: alert triage could not load watch rules: %v\n", err)
		}
	}
	effective := fileRules
	if l.cfg.Store != "" {
		if rdStore, err := store.OpenRead(ctx, l.cfg.Store); err == nil {
			_ = rdStore.ReadTx(ctx, func(rd *store.Reader) error {
				if merged, err := watch.ReadEffectiveRules(rd, fileRules); err == nil {
					effective = merged
				}
				return nil
			})
			_ = rdStore.Close()
		}
	}
	out := map[string]bool{}
	for _, r := range effective {
		if r.Enabled && r.Delivery.Receiver == "triage" {
			out[r.ID] = true
		}
	}
	return out
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func (l *Loop) hasEligibleTriageEpisodes(ctx context.Context) bool {
	if l.cfg.Store == "" {
		return false
	}
	ruleIDs := l.triageRuleIDs(ctx)
	if len(ruleIDs) == 0 {
		return false
	}
	rdStore, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false
	}
	defer rdStore.Close()
	var eligible []watch.EligibleTriageEpisode
	_ = rdStore.ReadTx(ctx, func(rd *store.Reader) error {
		eligible, err = watch.ReadEligibleTriageEpisodes(rd, ruleIDs)
		return err
	})
	return len(eligible) > 0
}

func (l *Loop) fileTriageDefect(ctx context.Context, s *store.Store, alertID, dedupKey, title, words string) string {
	var itemID string
	err := s.WriteTx(ctx, func(tx *store.Tx) error {
		env, err := item.FileRequest(ctx, tx, item.RequestFiling{
			Words:       []byte(words),
			Title:       title,
			Seat:        item.SeatBuild,
			Workflow:    item.SingleWorkerWorkflow,
			Alerts:      []string{alertID},
			FiledBy:     "tillr-triage",
			DedupKey:    dedupKey,
			Ordinary:    true,
			Destination: item.Destination{To: "last"},
			At:          l.cfg.Now().UTC(),
			By:          "tillr-triage",
			Via:         "tillr-drain",
		})
		if err != nil {
			return err
		}
		var payload struct {
			ItemID string `json:"item_id"`
		}
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			return err
		}
		itemID = payload.ItemID
		return nil
	})
	if err != nil {
		l.reportf("loop: alert triage could not file defect item for %s: %v\n", alertID, err)
		return ""
	}
	return itemID
}

func (l *Loop) recoverStaleTriageClaims(ctx context.Context) {
	if l.cfg.Store == "" || ctx.Err() != nil {
		return
	}
	rdStore, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return
	}
	var claims []watch.ActiveTriageClaim
	_ = rdStore.ReadTx(ctx, func(rd *store.Reader) error {
		claims, err = watch.ReadActiveTriageClaims(rd)
		return err
	})
	if err != nil || len(claims) == 0 {
		_ = rdStore.Close()
		return
	}
	type staleClaim struct {
		claim  watch.ActiveTriageClaim
		reason string
	}
	var stale []staleClaim
	_ = rdStore.ReadTx(ctx, func(rd *store.Reader) error {
		for _, c := range claims {
			if l.isTriageRunActive(c.ActiveRunID) {
				continue
			}
			env, found, qErr := run.StatusRecord(rd, c.ActiveRunID)
			if qErr != nil {
				continue
			}
			var rec run.Record
			if found {
				_ = json.Unmarshal(env.Payload, &rec)
			}
			var leased int
			_ = rd.QueryRow(`SELECT COUNT(*) FROM lanes WHERE run_id = ? AND state = ?`, c.ActiveRunID, run.LaneStateLeased).Scan(&leased)
			if !found {
				stale = append(stale, staleClaim{claim: c, reason: fmt.Sprintf("triage run %s is missing from runs table", c.ActiveRunID)})
			} else if rec.State != "running" {
				stale = append(stale, staleClaim{claim: c, reason: fmt.Sprintf("triage run %s is terminal (%s)", c.ActiveRunID, rec.State)})
			} else if leased == 0 {
				stale = append(stale, staleClaim{claim: c, reason: fmt.Sprintf("triage run %s has no live lane lease", c.ActiveRunID)})
			}
		}
		return nil
	})
	_ = rdStore.Close()
	if len(stale) == 0 {
		return
	}

	wStore, err := store.Open(ctx, l.cfg.Store, false)
	if err != nil {
		l.reportf("loop: alert triage stale claim recovery could not open store: %v\n", err)
		return
	}
	defer wStore.Close()

	eval := watch.Evaluator{Store: wStore, Clock: l.cfg.Now}
	for _, sc := range stale {
		dedupKey := "triage-stale-claim:" + sc.claim.RuleID
		title := fmt.Sprintf("Fix stale alert triage claim for rule %s", sc.claim.RuleID)
		words := fmt.Sprintf("Alert %s (rule %s, subject %s) had stale active_triage_run_id %s: %s.\n",
			sc.claim.AlertID, sc.claim.RuleID, sc.claim.SubjectKey, sc.claim.ActiveRunID, sc.reason)
		itemID := l.fileTriageDefect(ctx, wStore, sc.claim.AlertID, dedupKey, title, words)
		outcomeKind := ""
		if itemID != "" {
			outcomeKind = "fix-fault"
		}
		if err := eval.RecordTriageFailure(ctx, sc.claim.AlertID, watch.TriageFailureOptions{
			RunID:        sc.claim.ActiveRunID,
			Finding:      sc.reason,
			ActionNeeded: fmt.Sprintf("inspect stale triage claim %s for alert %s (%s)", sc.claim.ActiveRunID, sc.claim.AlertID, sc.claim.RuleID),
			ItemID:       itemID,
			OutcomeKind:  outcomeKind,
		}); err != nil {
			l.reportf("loop: alert triage stale claim escalation failed for %s: %v\n", sc.claim.AlertID, err)
		}
	}
}

func (l *Loop) dispatchAlertTriage(ctx context.Context) {
	if l.cfg.Store == "" || ctx.Err() != nil {
		return
	}
	policy := l.currentPolicy()
	if policy == nil {
		return
	}
	backendName := strings.TrimSpace(policy.Seats[item.SeatTriage])
	if backendName == "" {
		return
	}
	if _, ok := policy.Backends[backendName]; !ok {
		return
	}
	ruleIDs := l.triageRuleIDs(ctx)
	if len(ruleIDs) == 0 {
		return
	}

	rdStore, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return
	}
	var eligible []watch.EligibleTriageEpisode
	var shut map[string]bool
	readErr := rdStore.ReadTx(ctx, func(rd *store.Reader) error {
		var qErr error
		eligible, qErr = watch.ReadEligibleTriageEpisodes(rd, ruleIDs)
		if qErr != nil {
			return qErr
		}
		if len(eligible) > 0 {
			shut, qErr = readQuotaShutBackends(rd, l.cfg.Now().UTC(), policy)
		}
		return qErr
	})
	_ = rdStore.Close()
	if readErr != nil {
		l.reportf("loop: alert triage read failed: %v\n", readErr)
		return
	}
	if len(eligible) == 0 {
		return
	}

	wStore, err := store.Open(ctx, l.cfg.Store, false)
	if err != nil {
		l.reportf("loop: alert triage dispatch could not open store: %v\n", err)
		return
	}
	defer wStore.Close()

	eval := watch.Evaluator{Store: wStore, Clock: l.cfg.Now}
	now := l.cfg.Now().UTC()

	for _, ep := range eligible {
		if ctx.Err() != nil {
			return
		}
		if shut[backendName] {
			finding := fmt.Sprintf("triage backend %s is closed (quota-shut)", backendName)
			action := fmt.Sprintf("reopen backend %s or wait for its quota window, then inspect alert %s (%s)", backendName, ep.AlertID, ep.RuleID)
			if err := eval.RecordTriageFailure(ctx, ep.AlertID, watch.TriageFailureOptions{
				Finding:      finding,
				ActionNeeded: action,
			}); err != nil {
				l.reportf("loop: alert triage closed-backend escalation failed for %s: %v\n", ep.AlertID, err)
			}
			continue
		}

		avail := l.maxConcurrentTriage() - l.activeTriageCount()
		if avail <= 0 {
			return
		}
		slot := l.allocateTriageSlot()
		if slot == "" {
			return
		}

		seq := l.triageSeq.Add(1)
		runID := fmt.Sprintf("triage-%s-%d-%d", ep.AlertID, now.UnixMilli(), seq)
		if err := eval.ClaimTriage(ctx, ep.AlertID, runID); err != nil {
			l.releaseTriageSlot(slot)
			if !errors.Is(err, watch.ErrTriageAlreadyClaimed) && !errors.Is(err, watch.ErrTriageNotEligible) {
				l.reportf("loop: alert triage claim failed for %s: %v\n", ep.AlertID, err)
			}
			continue
		}

		req := *policy.Request(ep.AlertID, slot, runID, backendName)
		req.Seat = item.SeatTriage
		req.WorkingDir = "work"

		if err := l.stageTriageBrief(ctx, wStore, ep, slot, runID, backendName, req.TimeoutMS, policy); err != nil {
			l.releaseTriageSlot(slot)
			dedupKey := "triage-stage-failed:" + ep.RuleID
			itemID := l.fileTriageDefect(ctx, wStore, ep.AlertID, dedupKey,
				fmt.Sprintf("Fix alert triage brief staging failure for %s", ep.RuleID),
				fmt.Sprintf("Failed to stage triage brief for alert %s (%s): %v\n", ep.AlertID, ep.RuleID, err))
			outcomeKind := ""
			if itemID != "" {
				outcomeKind = "fix-fault"
			}
			_ = eval.RecordTriageFailure(ctx, ep.AlertID, watch.TriageFailureOptions{
				RunID:        runID,
				Finding:      fmt.Sprintf("triage brief staging failed: %v", err),
				ActionNeeded: fmt.Sprintf("inspect triage staging for alert %s (%s)", ep.AlertID, ep.RuleID),
				ItemID:       itemID,
				OutcomeKind:  outcomeKind,
			})
			continue
		}

		l.registerActiveTriage(runID, slot)
		l.triageWG.Add(1)
		go func(ep watch.EligibleTriageEpisode, slot, runID string, req run.Request) {
			defer l.triageWG.Done()
			rec, _, runErr := l.cfg.Steps.Run(ctx, req)
			l.finishAlertTriage(ctx, ep, slot, runID, rec, runErr)
		}(ep, slot, runID, req)
	}
}

func (l *Loop) resolveTriageTemplatePath() string {
	if l.cfg.TriageTemplate != "" {
		return l.cfg.TriageTemplate
	}
	if stager, ok := l.cfg.Stage.(*LaneStager); ok && stager != nil && stager.Template != "" {
		candidate := filepath.Join(filepath.Dir(stager.Template), "brief-template-triage.md")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if l.cfg.Root != "" {
		candidate := filepath.Join(l.cfg.Root, "tools", "tillr-run", "backends", "brief-template-triage.md")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if backendsDir, err := run.ResolveBackendsDir(); err == nil {
		return filepath.Join(backendsDir, "brief-template-triage.md")
	}
	return "brief-template-triage.md"
}

func (l *Loop) stageTriageBrief(
	ctx context.Context,
	s *store.Store,
	ep watch.EligibleTriageEpisode,
	slot, runID, backendName string,
	timeoutMS int,
	policy *Policy,
) error {
	tmplPath := l.resolveTriageTemplatePath()
	rawTmpl, err := os.ReadFile(tmplPath)
	if err != nil {
		return fmt.Errorf("read triage template %s: %w", tmplPath, err)
	}

	laneDir := filepath.Join(l.cfg.LaneRoot, slot)
	targetDir := filepath.Join(laneDir, "work")
	durableDir := run.DurableDir(laneDir, runID)
	for _, dir := range []string{laneDir, targetDir, durableDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create triage dir %s: %w", dir, err)
		}
	}

	corrCtx := "(none)"
	_ = s.ReadTx(ctx, func(rd *store.Reader) error {
		if c, qErr := watch.ReadTriageCorrelationContext(rd, ep.AlertID, l.cfg.Now()); qErr == nil && c != "" {
			corrCtx = c
		}
		return nil
	})

	epMeta := fmt.Sprintf(
		"- **alert_id:** %s\n- **rule_id:** %s\n- **subject_key:** %s\n- **state:** %s\n- **severity:** %s\n- **condition_fingerprint:** %s\n- **repeat_count:** %d\n- **reopen_count_for_item:** %d\n- **tracking_item_id:** %s\n- **variables:** `%s`",
		ep.AlertID, ep.RuleID, ep.SubjectKey, ep.State, ep.Severity, ep.ConditionFingerprint,
		ep.ObservationCount, ep.ReopenCountForItem, ep.TrackingItemID, ep.VariablesJSON,
	)

	timeoutText := wallWords(timeoutMS)

	replacements := []string{
		"{{ALERT_ID}}", ep.AlertID,
		"{{RULE_ID}}", ep.RuleID,
		"{{RUN_ID}}", runID,
		"{{BACKEND}}", backendName,
		"{{STORE_PATH}}", l.cfg.Store,
		"{{REPO_ROOT}}", l.cfg.Root,
		"{{TARGET}}", targetDir,
		"{{DURABLE_DIR}}", durableDir,
		"{{DATE}}", l.cfg.Now().UTC().Format("2006-01-02"),
		"{{TIMEOUT_SECONDS}}", timeoutText,
		"{{SEAT_LINES}}", seatLines(policy),
		"{{EPISODE_METADATA}}", epMeta,
		"{{TIMELINE_BUNDLE}}", ep.BundleJSON,
		"{{CORRELATION_CONTEXT}}", corrCtx,
	}
	rendered := strings.NewReplacer(replacements...).Replace(string(rawTmpl))

	briefPath := filepath.Join(laneDir, "brief.md")
	if err := os.WriteFile(briefPath, []byte(rendered), 0o644); err != nil {
		return fmt.Errorf("write triage brief %s: %w", briefPath, err)
	}
	if l.cfg.Root != "" {
		archiveDir := RunDir(l.cfg.Root, runID)
		if err := os.MkdirAll(archiveDir, 0o755); err == nil {
			_ = os.WriteFile(filepath.Join(archiveDir, "brief.md"), []byte(rendered), 0o644)
		}
	}
	return nil
}

func (l *Loop) finishAlertTriage(
	ctx context.Context,
	ep watch.EligibleTriageEpisode,
	slot, runID string,
	rec run.Record,
	runErr error,
) {
	defer l.signalTriageWake()
	defer l.releaseTriageRun(runID, slot)

	if ctx.Err() != nil && (runErr != nil || errors.Is(runErr, context.Canceled)) {
		return
	}

	wStore, err := store.Open(ctx, l.cfg.Store, false)
	if err != nil {
		l.reportf("loop: alert triage finish could not open store for %s: %v\n", ep.AlertID, err)
		return
	}
	defer wStore.Close()

	var st watch.TriageEpisodeState
	var found bool
	var itemOpen, silenced bool
	readErr := wStore.ReadTx(ctx, func(rd *store.Reader) error {
		var qErr error
		st, found, qErr = watch.ReadTriageEpisodeState(rd, ep.AlertID)
		if qErr != nil || !found {
			return qErr
		}
		if st.TrackingItemID != "" {
			itemOpen = isTrackingItemOpen(rd, st.TrackingItemID)
			silenced, qErr = watch.HasActiveSilenceForItem(rd, st.RuleID, st.VariablesJSON, st.TrackingItemID, l.cfg.Now())
		}
		return qErr
	})
	if readErr != nil {
		l.reportf("loop: alert triage finish could not read episode %s: %v\n", ep.AlertID, readErr)
		return
	}

	eval := watch.Evaluator{Store: wStore, Clock: l.cfg.Now}

	timedOut := rec.EndCause == run.EndTimeLimit || rec.State == run.StateTimedOut
	nonZeroExit := rec.ExitCode == nil || *rec.ExitCode != 0
	unrecorded := !found || st.ActiveTriageRunID != "" || (st.TriageState != "settled" && st.TriageState != "escalated")

	if runErr != nil || nonZeroExit || timedOut || unrecorded {
		var reason string
		switch {
		case runErr != nil:
			reason = fmt.Sprintf("triage run %s failed: %v", runID, runErr)
		case timedOut:
			reason = fmt.Sprintf("triage run %s timed out", runID)
		case rec.ExitCode == nil:
			reason = fmt.Sprintf("triage run %s ended without an exit code (state=%s)", runID, rec.State)
		case *rec.ExitCode != 0:
			reason = fmt.Sprintf("triage run %s exited with code %d", runID, *rec.ExitCode)
		default:
			reason = fmt.Sprintf("triage run %s exited 0 without recording a triage outcome", runID)
		}

		var defectItemID, outcomeKind string
		if runErr != nil || nonZeroExit || timedOut {
			dedupKey := "triage-run-failed:" + ep.RuleID
			defectItemID = l.fileTriageDefect(ctx, wStore, ep.AlertID, dedupKey,
				fmt.Sprintf("Fix alert triage failure for rule %s", ep.RuleID),
				fmt.Sprintf("Triage run %s for alert %s (%s) failed: %s\n", runID, ep.AlertID, ep.RuleID, reason))
			if defectItemID != "" {
				outcomeKind = "fix-fault"
			}
		}

		if err := eval.RecordTriageFailure(ctx, ep.AlertID, watch.TriageFailureOptions{
			RunID:        runID,
			Finding:      reason,
			ActionNeeded: fmt.Sprintf("inspect failed triage run %s for alert %s (%s)", runID, ep.AlertID, ep.RuleID),
			ItemID:       defectItemID,
			OutcomeKind:  outcomeKind,
		}); err != nil {
			l.reportf("loop: alert triage failure escalation failed for %s: %v\n", ep.AlertID, err)
		}
		return
	}

	// Repeat-flood escalation backstop (D-AT11, §6.1 item 2).
	if st.TriageState == "settled" && st.TriageOutcome == "linked" &&
		st.TrackingItemID != "" && st.ReopenCountForItem >= 3 && itemOpen && !silenced {
		outcomeKind := st.TriageOutcomeKind
		if outcomeKind != "fix-fault" && outcomeKind != "fix-rule" {
			outcomeKind = "fix-fault"
		}
		finding := strings.TrimSpace(st.TriageFinding)
		if finding == "" {
			finding = fmt.Sprintf("linked to %s", st.TrackingItemID)
		}
		finding = fmt.Sprintf("%s; reopen_count_for_item=%d >= 3 without an active silence", finding, st.ReopenCountForItem)
		actionNeeded := fmt.Sprintf("decide whether to silence repeat alerts: tillr watch silence %s --store %s --by operator-session --item %s --reason \"repeat flood (%d reopens) while %s is open\" --until-change",
			ep.AlertID, l.cfg.Store, st.TrackingItemID, st.ReopenCountForItem, st.TrackingItemID)
		if err := eval.RecordTriage(ctx, ep.AlertID, watch.RecordTriageOptions{
			RunID:        runID,
			Outcome:      "escalated",
			OutcomeKind:  outcomeKind,
			ItemID:       st.TrackingItemID,
			Finding:      finding,
			EscalateTo:   "operator-session",
			ActionNeeded: actionNeeded,
		}); err != nil {
			l.reportf("loop: alert triage repeat-flood escalation failed for %s: %v\n", ep.AlertID, err)
		}
	}
}

func isTrackingItemOpen(rd *store.Reader, itemID string) bool {
	var state string
	if err := rd.QueryRow(`SELECT state FROM items WHERE item_id = ?`, itemID).Scan(&state); err != nil {
		return false
	}
	return state != "landed" && state != "closed" && state != "abandoned" && state != "deleted" && state != "resolved-elsewhere"
}
