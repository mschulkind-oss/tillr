package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr-drain/faults"
	"github.com/mschulkind-oss/tillr-drain/holdrules"

	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-drain/quota"
	"github.com/mschulkind-oss/tillr-drain/spend"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
)

// waitingRow is shared by the terminal and HTTP faces. Since is an absolute UTC
// time, or the explicit absence of a decision; it is not the item's update time.
type waitingRow struct {
	Alarm        *alarms.Alarm `json:"alarm,omitempty"`
	backendReset time.Time
	Quota        *quota.QuotaStatus `json:"quota,omitempty"`
	GraphChanges []string           `json:"graph_changes,omitempty"`
	Place        int                `json:"place,omitempty"`
	ItemID       string             `json:"item_id"`
	RunID        string             `json:"run_id,omitempty"`
	Backend      string             `json:"backend,omitempty"`
	LaneID       string             `json:"lane_id,omitempty"`
	Document     string             `json:"document"`
	// Observation identifies a repository condition, never an item or design document.
	Observation string          `json:"observation,omitempty"`
	Class       string          `json:"class"`
	Position    string          `json:"position,omitempty"`
	Where       string          `json:"where"`
	Why         string          `json:"why"`
	Mover       string          `json:"mover"`
	Since       string          `json:"since"`
	Waited      string          `json:"waited"`
	ClassSource string          `json:"class_source"`
	LaneStatus  string          `json:"lane_status,omitempty"`
	LaneReason  string          `json:"lane_reason,omitempty"`
	SplitParent *whySplitParent `json:"split_parent,omitempty"`
	SplitPart   *whySplitPart   `json:"split_part,omitempty"`
}

// waitingRule is the view's copy of a holdrules.Rule. The rules themselves live in
// tillr-drain/holdrules, beside the reasons they describe: adding a hold reason means
// adding its row there, and the drain module's own tests fail until it exists. This
// file keeps only the presentation of a resolved rule.
type waitingRule struct{ class, where, mover string }

func waitingRuleFor(reason string) waitingRule {
	rule := holdrules.For(reason)
	return waitingRule{rule.Class, rule.Where, rule.Mover}
}

func waitingFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("waiting")
	path := fs.String("store", "", "the store to read (required)")
	asJSON := fs.Bool("json", false, "print the waiting rows as JSON instead of a table")
	history := fs.Bool("history", false, "read recorded decisions instead of the current planner (also supports older stores)")
	policyRef := fs.String("policy", "", "the loop's policy file; absent means no-policy")
	lanesArg := fs.String("lanes", "", "comma-separated lanes offered by the loop (default: all store lanes)")
	root := fs.String("root", ".", "the directory a relative design_doc resolves against")
	questionsCmd := fs.String("questions-cmd", "", questionsCmdHelp)
	plannerFlag := fs.String("tillr-drain", "", "the tillr-drain whose `why` answers (default: the one beside this executable, then PATH); it must be this program's build")
	nowFlag := fs.String("now", "", "the clock quota windows and lease times are measured against, RFC 3339 (default: now)")
	if code := parseFlags(fs, args); code != record.ExitOK {
		return record.Envelope{}, code
	}
	if *path == "" {
		return record.Envelope{}, usagef("waiting needs --store <path>")
	}
	now := time.Now().UTC()
	if *nowFlag != "" {
		parsed, err := time.Parse(time.RFC3339, *nowFlag)
		if err != nil {
			return record.Envelope{}, usagef("--now %q is not an RFC 3339 timestamp: %v", *nowFlag, err)
		}
		now = parsed.UTC()
	}
	ctx := context.Background()
	questions := &vantage.VantageQuestions{Root: *root, Command: commandFields(*questionsCmd), Context: ctx}
	var rows []waitingRow
	var active []alarms.Alarm
	var err error
	if *history {
		rows, err = readWaitingAt(ctx, *path, questions, now)
	} else {
		var raw json.RawMessage
		raw, rows, err = askPlannerAt(ctx, *plannerFlag, buildInfo("none"), *path, *root, *policyRef, *lanesArg, *nowFlag, commandFields(*questionsCmd), nil)
		if err == nil {
			var answer struct {
				Alarms []alarms.Alarm `json:"alarms"`
			}
			err = json.Unmarshal(raw, &answer)
			active = answer.Alarms
		}
	}
	if err == nil && !*history {
		s, e := store.OpenRead(ctx, *path)
		if e != nil {
			err = e
		} else {
			err = s.ReadTx(ctx, func(r *store.Reader) error {
				quotas, e := waitingBackendQuotasWithRoot(r, now, *root)
				if e != nil {
					return e
				}
				rows = mergeQuotaWaiting(rows, quotas)
				extra, e := waitingCommentIntake(r)
				if e != nil {
					return e
				}
				rows = append(rows, extra...)
				lanes, e := waitingLaneConditions(r)
				rows = append(rows, lanes...)
				if e != nil {
					return e
				}
				moves, e := waitingPatchMoves(r, *path)
				if e != nil {
					return e
				}
				rows = append(rows, moves...)
				return addGraphRevisionHistory(r, rows)
			})
			s.Close()
		}
	}
	if err == nil {
		rows, err = policyWaitingRows(*root, rows)
	}
	if err == nil {
		rows, err = heavyWaitingRows(ctx, *root, *path, rows)
	}
	if err == nil {
		rows = appendRepeatedStopWaiting(waitingLoopDir(*path, *root), rows)
	}
	if err != nil {
		var mismatch schemaMismatch
		if errors.As(err, &mismatch) {
			fmt.Fprintf(os.Stderr, "tillr-view: %v\n", err)
			return record.Envelope{}, record.ExitIncompatible
		}
		return record.Envelope{}, exitFor(err)
	}
	s, err := store.OpenRead(ctx, *path)
	if err != nil {
		return record.Envelope{}, exitFor(err)
	}
	declared, err := definition.ForStore(ctx, s)
	if err == nil {
		err = s.ReadTx(ctx, func(r *store.Reader) error {
			var err error
			if *history {
				active, err = readAlarms(r, *root, *policyRef, now)
				if err != nil {
					return err
				}
			}
			return nil
		})
	}
	s.Close()
	if err != nil {
		return record.Envelope{}, exitFor(err)
	}
	for i := range rows {
		normalizeWaitingMover(&rows[i])
	}
	sortWaitingDocuments(rows)
	if *asJSON {
		err = json.NewEncoder(os.Stdout).Encode(alarmWaitingRows(active, rows))
	} else {
		window, readErr := spend.Read(ctx, *path, now)
		if readErr != nil {
			return record.Envelope{}, exitFor(readErr)
		}
		err = alarms.Print(os.Stdout, active)
		if err == nil {
			_, err = fmt.Fprintf(os.Stdout, "definition: %s\n%s\n", declared.Name, window)
		}
		var laneRows []laneRow
		if err == nil {
			laneRows, err = readWaitingLaneCapacityRows(ctx, *path, filepath.Join(filepath.Dir(*path), "loop"))
		}
		if err == nil {
			_, err = fmt.Fprintln(os.Stdout, laneCapacityLine(laneRows))
		}
		if err == nil {
			err = printWaiting(os.Stdout, rows)
		}
	}
	if err != nil {
		return record.Envelope{}, exitFor(err)
	}
	return record.Envelope{}, record.ExitOK
}

func readWaiting(ctx context.Context, path string, questions vantage.QuestionSource) ([]waitingRow, error) {
	return readWaitingAt(ctx, path, questions, time.Now().UTC())
}

func readWaitingAt(ctx context.Context, path string, questions vantage.QuestionSource, now time.Time) ([]waitingRow, error) {
	r, err := store.OpenRead(ctx, path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var out []waitingRow
	err = r.ReadTx(ctx, func(tx *store.Reader) error {
		version, err := tx.SchemaVersion()
		if err != nil {
			return err
		}
		if !schemaSupported(version) {
			return schemaMismatch{version: version}
		}
		out, err = assembleWaitingAt(tx, questions, path, now)
		return err
	})
	return out, err
}

func (s *server) waitingPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	now := s.clock().UTC()
	if rawNow := req.URL.Query().Get("now"); rawNow != "" {
		parsed, err := time.Parse(time.RFC3339, rawNow)
		if err != nil {
			return nil, failure(codeBadQuery, "now %q is not an RFC 3339 timestamp: %v", rawNow, err)
		}
		now = parsed.UTC()
	}
	rows, err := assembleWaitingAt(r, s.vantage.source(req.Context()), s.storePath, now)
	if err == nil {
		rows, err = policyWaitingRows(s.root, rows)
	}
	if err == nil {
		rows, err = heavyWaitingRows(req.Context(), s.root, s.storePath, rows)
	}
	if err == nil {
		loopDir := ""
		if s.obs != nil {
			loopDir = s.obs.loopDir
		}
		if loopDir == "" {
			loopDir = waitingLoopDir(s.storePath, s.root)
		}
		rows = appendRepeatedStopWaiting(loopDir, rows)
	}
	if err != nil {
		return nil, failure(codeStoreUnreadable, "waiting could not read the store: %v", err)
	}
	sortWaitingDocuments(rows)
	active, err := readAlarms(r, s.root, s.policyRef, now)
	if err != nil {
		return nil, unreadable(err)
	}
	return alarmWaitingRows(active, rows), nil
}

func waitingLoopDir(storePath, root string) string {
	beside := filepath.Join(filepath.Dir(storePath), "loop")
	if matches, err := filepath.Glob(filepath.Join(beside, "drain-*.json")); err == nil && len(matches) > 0 {
		return beside
	}
	if root != "" && root != "." {
		underRoot := filepath.Join(root, ".tillr", "loop")
		if matches, err := filepath.Glob(filepath.Join(underRoot, "drain-*.json")); err == nil && len(matches) > 0 {
			return underRoot
		}
	}
	return beside
}

func appendRepeatedStopWaiting(loopDir string, rows []waitingRow) []waitingRow {
	rs := (&observations{loopDir: loopDir}).repeatedLoopStop()
	if rs == nil {
		return rows
	}
	why := fmt.Sprintf("loop exited %d times in a row (%s)", rs.Count, rs.Reason)
	if rs.Detail != "" {
		why += ": " + rs.Detail
	}
	row := waitingRow{
		Observation: "loop:repeated-exit",
		Class:       "needs you",
		Where:       "loop",
		Why:         why,
		Mover:       rs.Next,
		Since:       time.UnixMilli(rs.FirstAtMS).UTC().Format(time.RFC3339Nano),
		ClassSource: "rule",
	}
	normalizeWaitingMover(&row)
	return append(rows, row)
}

// One pinned read transaction supplies both callers. Optional utility tables are
// absent on older stores; SQL errors other than absence always refuse the read.
func assembleWaiting(r *store.Reader, questions vantage.QuestionSource, storePath string) ([]waitingRow, error) {
	return assembleWaitingAt(r, questions, storePath, time.Now().UTC())
}

func assembleWaitingAt(r *store.Reader, questions vantage.QuestionSource, storePath string, now time.Time) ([]waitingRow, error) {
	items, err := readOpenItems(r)
	if err != nil && !isMissingTable(err) {
		return nil, err
	}
	openItemIDs := make([]string, len(items))
	for i, it := range items {
		openItemIDs[i] = it.ItemID
	}
	conflicts, err := waitingConflictsForItems(r, openItemIDs)
	if err != nil && !isMissingTable(err) {
		return nil, err
	}
	runs, err := readRunsForItems(r, openItemIDs)
	if err != nil && !isMissingTable(err) {
		return nil, err
	}
	lanes, err := readLanesIn(r, waitingLoopDir(storePath, ""))
	if err != nil && !isMissingTable(err) {
		return nil, err
	}
	seenRun := map[string]bool{}
	var openRunIDs []string
	addRunID := func(id string) {
		if id != "" && !seenRun[id] {
			seenRun[id] = true
			openRunIDs = append(openRunIDs, id)
		}
	}
	for _, run := range runs {
		addRunID(run.RunID)
	}
	for _, lane := range lanes {
		addRunID(lane.RunID)
		for _, nodeRunID := range lane.ActiveNodes {
			addRunID(nodeRunID)
		}
	}
	allocations, err := readAllocationsFor(r, openRunIDs)
	if err != nil {
		return nil, err
	}
	runs = append(runs, liveRuns(lanes, runs, allocations)...)
	candidates, err := readCandidatesForItems(r, openItemIDs)
	if err != nil && !isMissingTable(err) {
		return nil, err
	}
	candidateIDs := make([]string, len(candidates))
	for i, c := range candidates {
		candidateIDs[i] = c.CandidateID
	}
	attempts, err := readAttemptsForCandidates(r, candidateIDs)
	if err != nil && !isMissingTable(err) {
		return nil, err
	}
	states := map[string]string{}
	for _, item := range items {
		states[item.ItemID] = item.State
	}
	splits, err := readSplitsForItems(r, openItemIDs)
	if err != nil {
		return nil, err
	}
	partIndex := splitPartIndex(splits)
	var splitMeta map[string]splitItemMeta
	var closingStates map[string]splitClosingState
	if len(splits) > 0 {
		parentIDs := make([]string, 0, len(splits))
		metaSeen := map[string]bool{}
		var metaIDs []string
		for parentID, st := range splits {
			parentIDs = append(parentIDs, parentID)
			if !metaSeen[parentID] {
				metaSeen[parentID] = true
				metaIDs = append(metaIDs, parentID)
			}
			for _, p := range st.Parts {
				partID := splitPartID(parentID, p.Label)
				if !metaSeen[partID] {
					metaSeen[partID] = true
					metaIDs = append(metaIDs, partID)
				}
			}
		}
		splitMeta, err = readSplitItemMetaFor(r, metaIDs)
		if err != nil {
			return nil, err
		}
		closingStates, err = readSplitClosingStatesFor(r, parentIDs)
		if err != nil {
			return nil, err
		}
		for id, meta := range splitMeta {
			if _, ok := states[id]; !ok {
				states[id] = meta.State
			}
		}
	}
	var waiterIDs []string
	for _, item := range items {
		if depID, ok := strings.CutPrefix(item.Waiter, "item:"); ok && depID != "" {
			waiterIDs = append(waiterIDs, depID)
		}
	}
	if err := ensureItemStates(r, states, waiterIDs); err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Rank != items[j].Rank {
			if items[i].Rank == "" {
				return false
			}
			if items[j].Rank == "" {
				return true
			}
			return items[i].Rank < items[j].Rank
		}
		return items[i].ItemID < items[j].ItemID
	})
	itemFaults, err := faults.ReadForItems(r, openItemIDs, openRunIDs)
	if err != nil {
		return nil, err
	}
	obstacleHolds, _, err := obstacles.ReadForItems(r, openItemIDs, openRunIDs)
	if err != nil {
		return nil, err
	}
	places := itemPlaces(items)
	out := []waitingRow{}
	for _, item := range items {
		if waitingTerminal(item.State) {
			continue
		}
		row := waitingRow{Place: places[item.ItemID], ItemID: item.ItemID, Class: "waits on the loop", Where: "queue", Why: "not yet planned", Mover: "the next pass", Since: "no pass yet", ClassSource: "rule"}
		var latestRun *storeRun
		var active *storeRun
		for i := range runs {
			run := &runs[i]
			if run.ItemID != item.ItemID {
				continue
			}
			if latestRun == nil || waitingRunLater(*run, *latestRun) {
				latestRun = run
			}
			if run.State == liveRunState {
				active = run
			}
		}
		if latestRun != nil {
			row.Where = "lane"
		}
		for _, candidate := range candidates {
			if candidate.ItemID != item.ItemID {
				continue
			}
			row.Where = waitingFurtherStage(row.Where, "gate")
			for _, attempt := range attempts {
				if attempt.CandidateID == candidate.CandidateID {
					row.Where = waitingFurtherStage(row.Where, "land")
				}
			}
		}
		stage, err := waitingRecordedStage(r, item.ItemID, latestRun)
		if err != nil {
			return nil, err
		}
		row.Where = waitingFurtherStage(row.Where, stage)
		var stoppedAt int64
		var stopped bool
		var stoppedRun string
		if latestRun != nil {
			stoppedAt, stopped, err = waitingStoppedRun(r, latestRun.RunID)
			if err != nil {
				return nil, err
			}
			if stopped {
				stoppedRun = latestRun.RunID
				if active != nil && active.RunID == stoppedRun {
					active = nil
				}
			}
		}
		last, since, deferredBy, found, err := waitingDecision(r, item.ItemID, stoppedRun, stoppedAt)
		if err != nil {
			return nil, err
		}
		if last.Reason == "owner-only-owed" && item.OwnerOwed == nil {
			// A verdict lifts the hold even before the next planner pass.
			found = false
		}
		if found {
			rule := waitingRuleFor(last.Reason)
			row.Class, row.Why, row.Mover = rule.class, last.Reason, rule.mover
			if strings.TrimSpace(row.Why) == "" {
				row.Why = "reason not recorded"
			}
			row.Where = waitingFurtherStage(row.Where, rule.where)
			row.Since = time.UnixMilli(since).UTC().Format(time.RFC3339Nano)
			if deferredBy != "" {
				row.Mover = deferredBy
				if last.Reason == alarms.Closed || last.Reason == alarms.Window {
					row.Mover = "operator; " + deferredBy + ": " + strings.TrimPrefix(rule.mover, "operator; ")
				}
			}
			if last.Reason == "question-open" {
				var excluded []string
				for other, conflict := range conflicts {
					file, id, ok := strings.Cut(conflict.QuestionRef, "#")
					if other != item.ItemID && conflict.Status == "question" && ok && file == item.DesignDoc {
						excluded = append(excluded, id)
					}
				}
				answer := readQuestions(questions, item.DesignDoc, excluded...)
				if answer.State == questionsOK && len(answer.Questions) > 0 {
					row.Mover = "owner; answer " + item.DesignDoc + "#" + answer.Questions[0].QuestionID
					row.Why += ": " + answer.Questions[0].Text
				} else if answer.State != "" && answer.State != questionsOK {
					row.Mover = answer.State
				}
			}
			if last.Reason == "blocked" {
				edges, err := readEdges(r, item.ItemID)
				if err != nil && !isMissingTable(err) {
					return nil, err
				}
				if err := ensureEdgeStates(r, states, edges); err != nil {
					return nil, err
				}
				var blockers, closedBlockers []string
				for _, edge := range edges {
					state := states[edge.BlockedBy]
					if edge.Kind == edgeKindAfter {
						if terminalItemStates[state] {
							continue
						}
						blockers = append(blockers, edge.BlockedBy)
						continue
					}
					if state == "landed" || state == "resolved-elsewhere" {
						continue
					}
					if state == "closed" {
						closedBlockers = append(closedBlockers, edge.BlockedBy)
						continue
					}
					blockers = append(blockers, edge.BlockedBy)
				}
				if len(closedBlockers) > 0 {
					allDefectiveSibling := false
					if pm, isPart := partIndex[item.ItemID]; isPart {
						allDefectiveSibling = true
						for _, b := range closedBlockers {
							bm, ok := partIndex[b]
							if !ok || bm.ParentID != pm.ParentID || bm.Part.Required == nil || !*bm.Part.Required || len(bm.Part.ReplacedBy) > 0 || !isDefectiveClose(states[b], splitMeta[b].CloseReason) {
								allDefectiveSibling = false
								break
							}
						}
						if allDefectiveSibling {
							cs := closingStates[pm.ParentID]
							if cs.Held != nil {
								row.Class, row.Mover = splitClosingHoldPresentation(cs.Held)
								row.Why = cs.Held.Reason
							} else {
								row.Class = "waits on work"
								row.Mover = "design"
								row.Why = "waiting on design seat to replace defective required part (" + strings.Join(closedBlockers, ", ") + ")"
							}
						}
					}
					if !allDefectiveSibling {
						row.Class = "needs you"
						if len(blockers) > 0 {
							row.Why = fmt.Sprintf("blocked: waiting on %s; %s closed without landing", strings.Join(blockers, ", "), strings.Join(closedBlockers, ", "))
						} else {
							row.Why = fmt.Sprintf("blocked: %s closed without landing", strings.Join(closedBlockers, ", "))
						}
						row.Mover = fmt.Sprintf("operator; remove the dependency or reopen %s", strings.Join(closedBlockers, ", "))
					}
				} else if len(blockers) > 0 {
					row.Why = "blocked: waiting on " + strings.Join(blockers, ", ")
					row.Mover = strings.Join(blockers, ", ")
				}
			}
		}
		if found && (last.Reason == "eval-failed" || last.Reason == "judge-unreadable" || last.Reason == "words-missing" || last.Reason == "words-altered" || last.Reason == "idea-no-outcome") {
			var detail string
			err := r.QueryRow(`SELECT json_extract(payload,'$.detail') FROM events
 WHERE kind='drain.refusal' AND json_valid(payload) AND json_extract(payload,'$.item_id')=?
 AND json_extract(payload,'$.step')=? ORDER BY seq DESC LIMIT 1`, item.ItemID, last.Reason).Scan(&detail)
			if err != nil && !errors.Is(err, sql.ErrNoRows) && !isMissingTable(err) {
				return nil, err
			}
			if detail != "" {
				row.Why += ": " + detail
			}
		}
		if found && last.Reason == "gate-stall-rerun" {
			detail, err := waitingGateStall(r, item.ItemID)
			if err != nil {
				return nil, err
			}
			if detail != "" {
				row.Why += ": " + detail
			}
		}
		if c, ok := conflicts[item.ItemID]; ok && item.State == "blocked" && c.Status != "closed" && c.Status != "answered" {
			row.Class, row.Mover, row.Why = "waits on work", c.WaitingOn, c.ConflictID+": "+c.Text
			row.Since = time.UnixMilli(c.Since).UTC().Format(time.RFC3339Nano)
			if c.WaitingOn == "owner" {
				row.Class, row.Mover = "needs you", "operator; route the conflict into a document question"
				if c.BodyRef == "" {
					row.Why += "; no document exists; name or triage one for " + item.ItemID
				}
			}
		}
		if c := conflicts[item.ItemID]; item.State == "blocked" && item.Waiter != "" && c.TriageVersion == "" {
			row.Class, row.Mover, row.Why = "waits on work", item.Waiter, item.WaitingReason
			if strings.TrimSpace(row.Why) == "" {
				row.Why = "waiting reason not recorded"
			}
			row.ClassSource = "store"
			row.Since = time.UnixMilli(item.UpdatedAtMS).UTC().Format(time.RFC3339Nano)
			if item.Waiter == "owner" {
				row.Class, row.Mover = "needs you", "operator; resolve the recorded hold"
				if c := conflicts[item.ItemID]; c.ConflictID != "" {
					row.Mover = "operator; route the conflict into a document question"
					if c.BodyRef == "" {
						row.Why += "; no document exists; name or triage one for " + item.ItemID
					}
				}
			} else if depID, ok := strings.CutPrefix(item.Waiter, "item:"); ok && states[depID] == "closed" {
				row.Class = "needs you"
				row.Why = depID + " closed without landing"
				if strings.TrimSpace(item.WaitingReason) != "" {
					row.Why += ": " + item.WaitingReason
				}
				row.Mover = "operator; remove the dependency or reopen " + depID
			}
			if st, isSplitParent := splits[item.ItemID]; isSplitParent {
				sp := &whySplitParent{}
				var closedRequired, defectiveRequired, unlandedRequired []string
				for _, p := range st.Parts {
					if len(p.ReplacedBy) > 0 {
						continue
					}
					sp.TotalParts++
					partID := splitPartID(item.ItemID, p.Label)
					req := p.Required != nil && *p.Required
					if req {
						sp.RequiredParts++
					}
					partState := states[partID]
					if partState == "" {
						partState = splitMeta[partID].State
					}
					switch {
					case partState == "landed":
						sp.Landed++
						if req {
							sp.RequiredLanded++
						}
					case waitingTerminal(partState):
						sp.Closed++
						if req {
							if isDefectiveClose(partState, splitMeta[partID].CloseReason) {
								defectiveRequired = append(defectiveRequired, partID)
							} else {
								closedRequired = append(closedRequired, partID)
							}
							unlandedRequired = append(unlandedRequired, partID)
						}
					default:
						if req {
							unlandedRequired = append(unlandedRequired, partID)
						}
						sp.Queued++
					}
				}
				sp.ProgressSummary = fmt.Sprintf("%d of %d required parts landed", sp.RequiredLanded, sp.RequiredParts)
				row.SplitParent = sp
				summary := formatSplitCounts(sp.TotalParts, sp.Landed, sp.Working, sp.Held, sp.Queued, sp.Closed)
				cs := closingStates[item.ItemID]
				switch {
				case len(closedRequired) > 0:
					id := closedRequired[0]
					row.Class = "needs you"
					row.Mover = "owner; required part did not land: " + id
					row.Why = summary + "; required part did not land: " + id + " (" + states[id] + ")"
				case len(defectiveRequired) > 0:
					id := defectiveRequired[0]
					if cs.Held != nil {
						row.Class, row.Mover = splitClosingHoldPresentation(cs.Held)
						row.Why = summary + "; " + cs.Held.Reason
					} else {
						row.Class = "waits on work"
						row.Mover = "design"
						row.Why = summary + "; required part closed as defective: " + id + "; waiting on design seat"
						if cs.RefusedReason != "" {
							row.Why += "; prior extension refused: " + cs.RefusedReason
						}
					}
				case sp.RequiredLanded == sp.RequiredParts:
					if cs.Held != nil {
						row.Class, row.Mover = splitClosingHoldPresentation(cs.Held)
						row.Why = summary + "; " + cs.Held.Reason
					} else if cs.Finding != "" {
						row.Class = "waits on work"
						row.Mover = "design"
						row.Why = summary + "; " + cs.Finding
						if cs.RefusedReason != "" {
							row.Why += "; prior extension refused: " + cs.RefusedReason
						}
					} else {
						row.Class = "waits on work"
						row.Mover = "review"
						row.Why = summary + "; required parts have landed; waiting on the closing check"
					}
				default:
					row.Class = "waits on work"
					row.Why = summary
					if len(unlandedRequired) > 0 {
						row.Mover = strings.Join(unlandedRequired, ", ")
					}
				}
			}
		} else if item.State == "blocked" && !found && row.Why == "not yet planned" {
			row.Class, row.Mover, row.Why = "waits on work", "waiter not recorded", "waiter not recorded"
		} else if !found && item.State == "in-queue" {
			edges, err := readEdges(r, item.ItemID)
			if err != nil && !isMissingTable(err) {
				return nil, err
			}
			if err := ensureEdgeStates(r, states, edges); err != nil {
				return nil, err
			}
			var blockers, closedBlockers []string
			for _, edge := range edges {
				state := states[edge.BlockedBy]
				if edge.Kind == edgeKindAfter {
					if terminalItemStates[state] {
						continue
					}
					blockers = append(blockers, edge.BlockedBy)
					continue
				}
				if state == "landed" || state == "resolved-elsewhere" {
					continue
				}
				if state == "closed" {
					closedBlockers = append(closedBlockers, edge.BlockedBy)
					continue
				}
				blockers = append(blockers, edge.BlockedBy)
			}
			if len(closedBlockers) > 0 {
				allDefectiveSibling := false
				if pm, isPart := partIndex[item.ItemID]; isPart {
					allDefectiveSibling = true
					for _, b := range closedBlockers {
						bm, ok := partIndex[b]
						if !ok || bm.ParentID != pm.ParentID || bm.Part.Required == nil || !*bm.Part.Required || len(bm.Part.ReplacedBy) > 0 || !isDefectiveClose(states[b], splitMeta[b].CloseReason) {
							allDefectiveSibling = false
							break
						}
					}
					if allDefectiveSibling {
						cs := closingStates[pm.ParentID]
						if cs.Held != nil {
							row.Class, row.Mover = splitClosingHoldPresentation(cs.Held)
							row.Why = cs.Held.Reason
						} else {
							row.Class = "waits on work"
							row.Mover = "design"
							row.Why = "waiting on design seat to replace defective required part (" + strings.Join(closedBlockers, ", ") + ")"
						}
					}
				}
				if !allDefectiveSibling {
					row.Class = "needs you"
					if len(blockers) > 0 {
						row.Why = fmt.Sprintf("blocked: waiting on %s; %s closed without landing", strings.Join(blockers, ", "), strings.Join(closedBlockers, ", "))
					} else {
						row.Why = fmt.Sprintf("blocked: %s closed without landing", strings.Join(closedBlockers, ", "))
					}
					row.Mover = fmt.Sprintf("operator; remove the dependency or reopen %s", strings.Join(closedBlockers, ", "))
				}
			} else if len(blockers) > 0 {
				row.Class = "waits on work"
				row.Why = "blocked: waiting on " + strings.Join(blockers, ", ")
				row.Mover = strings.Join(blockers, ", ")
			}
		}
		fixer := ""
		if found && last.Reason == "trunk-broken" {
			fixer = deferredBy
		}
		if strings.HasPrefix(item.Waiter, "item:fixer-main-") {
			fixer = strings.TrimPrefix(item.Waiter, "item:")
		}
		if fixer != "" {
			detail, owner, err := waitingTrunkFixer(r, fixer, conflicts)
			if err != nil {
				return nil, err
			}
			row.Why += ": " + detail
			row.Mover = detail
			if owner {
				row.Class = "needs you"
			}
		}
		if found && last.Action == "hold" && latestRun != nil && active == nil {
			cause, err := waitingEndCause(r, latestRun.RunID)
			if err != nil {
				return nil, err
			}
			// Preserve the seat, blocker, or owner that can move the item; the
			// run's termination explains the hold but does not replace its mover.
			row.Mover += "; " + cause
		}
		if latestRun != nil {
			hold, since, err := waitingRepairHold(r, item.ItemID, latestRun.RunID)
			if err != nil {
				return nil, err
			}
			if hold != nil {
				row.Class, row.Where = "needs you", "land"
				row.Why, row.Mover = "conflict-repair-staging-failed: "+hold.Cause, hold.NextAction
				row.Since = time.UnixMilli(since).UTC().Format(time.RFC3339Nano)
			}
		}
		if obsHold, held := obstacleHolds[item.ItemID]; held {
			if !(item.State == "in-queue" && found && (last.Action == "dispatch" || last.Action == "retry") && last.DispatchedMS >= obsHold.AtMS) {
				rule := waitingRuleFor(obsHold.ReasonCode())
				row.Class, row.Where = rule.class, rule.where
				row.Why = obsHold.WhyText()
				row.Mover = obsHold.MoverText()
				row.Since = time.UnixMilli(obsHold.AtMS).UTC().Format(time.RFC3339Nano)
				row.ClassSource = "store"
			}
		}
		if fault, held := itemFaults[item.ItemID]; held {
			rule := waitingRuleFor(fault.Reason)
			row.Class, row.Where = "needs you", rule.where
			row.Why = fmt.Sprintf("%s: %s", fault.Reason, fault.Detail)
			if fault.RunID != "" {
				row.Why += "; run " + fault.RunID
			}
			if fault.LaneID != "" {
				row.Why += "; lane " + fault.LaneID
			}
			row.Mover = fault.ClearCommand(storePath)
			row.Since = time.UnixMilli(fault.AtMS).UTC().Format(time.RFC3339Nano)
		}
		if active != nil {
			row.Class, row.Where, row.Mover = "moving", "lane", active.RunID
			if c := conflicts[item.ItemID]; c.TriageVersion != "" && c.WaitingOn == "design" {
				row.Mover = "triage on the design seat; " + active.RunID
			}
		}
		if stopped && (!found || last.DispatchedMS < stoppedAt) {
			row.Class, row.Where = "waits on the loop", "lane"
			row.Why, row.Mover = "run ended by a stop", latestRun.RunID
			row.Since = time.UnixMilli(stoppedAt).UTC().Format(time.RFC3339Nano)
			row.ClassSource = "rule"
		}
		if row.Class == "waits on work" && strings.HasPrefix(row.Why, "trunk-path-dirty: ") {
			started, err := time.Parse(time.RFC3339Nano, row.Since)
			if err != nil {
				return nil, err
			}
			elapsed := now.Sub(started).Round(time.Second)
			if elapsed < 0 {
				elapsed = 0
			}
			row.Waited = elapsed.String()
		}
		activeNodes := false
		for _, lane := range lanes {
			if len(lane.ActiveNodes) == 0 {
				continue
			}
			if allocated, ok := allocations[lane.ActiveNodes[0]]; ok && allocated.ItemID == item.ItemID {
				row.Class, row.Where = "waits on work", "lane"
				activeNodes = true
				row.Why = fmt.Sprintf("%d nodes running", len(lane.ActiveNodes))
				row.Mover = "the item's nodes to finish"
			}
		}
		if c := conflicts[item.ItemID]; c.Status == "question" {
			decorateOwnerQuestionWaiting(&row, c, questions)
		}
		_, hasFault := itemFaults[item.ItemID]
		if item.State == "in-queue" && item.OwnerOwed != nil && active == nil && !activeNodes && !hasFault {
			rule := waitingRuleFor("owner-only-owed")
			row.Class, row.Where, row.ClassSource = rule.class, rule.where, "store"
			row.Why, row.Mover = item.OwnerOwed.Explanation(), item.OwnerOwed.Mover()
			row.Since = "no pass yet"
			if found && last.Reason == "owner-only-owed" {
				row.Since = time.UnixMilli(since).UTC().Format(time.RFC3339Nano)
			}
		}
		out = append(out, row)
	}
	addConflictHistory(out, conflicts)
	if err := addHeldLaneHistory(r, out); err != nil {
		return nil, err
	}
	if err := addPreservedHistory(r, out); err != nil {
		return nil, err
	}
	windows, err := waitingBackendWindows(r, now)
	if err != nil {
		return nil, err
	}
	quotas, err := waitingBackendQuotasWithRoot(r, now, waitingRoot(storePath, questions))
	if err != nil {
		return nil, err
	}
	observed := map[string]bool{}
	for _, q := range quotas {
		observed[q.ItemID] = true
	}
	filtered := windows[:0]
	for _, w := range windows {
		if !observed[w.ItemID] {
			filtered = append(filtered, w)
		}
	}
	windows = append(filtered, quotas...)
	extra, err := waitingCommentIntake(r)
	if err != nil {
		return nil, err
	}
	laneRows, err := waitingLaneConditions(r)
	if err != nil {
		return nil, err
	}
	moves, err := waitingPatchMoves(r, storePath)
	if err != nil {
		return nil, err
	}
	out = append(out, moves...)
	all, err := appendWaitingDesigns(r, append(append(mergeQuotaWaiting(out, windows), extra...), laneRows...))
	if err != nil {
		return nil, err
	}
	if err := addGraphRevisionHistory(r, all); err != nil {
		return nil, err
	}
	for i := range all {
		normalizeWaitingMover(&all[i])
	}
	return appendPostLandingWaiting(r, all)
}

// Window rows are optional on old stores, and disappear at (not after) reset.
// The waiting face reads the durable hold; it never guesses from a run's failure.
func waitingRoot(storePath string, questions vantage.QuestionSource) string {
	if vq, ok := questions.(*vantage.VantageQuestions); ok && vq != nil && vq.Root != "" {
		return vq.Root
	}
	if storePath != "" && filepath.Base(filepath.Dir(storePath)) == ".tillr" {
		return filepath.Dir(filepath.Dir(storePath))
	}
	return ""
}

func waitingBackendWindows(r *store.Reader, now time.Time) ([]waitingRow, error) {
	rows, err := r.Query(`SELECT backend, kind, reset_at FROM backend_windows WHERE reset_at > ? ORDER BY backend`, now.UnixMilli())
	if isMissingTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []waitingRow
	for rows.Next() {
		var backend, kind string
		var reset int64
		if err := rows.Scan(&backend, &kind, &reset); err != nil {
			return nil, err
		}
		reopens := time.UnixMilli(reset).UTC().Format(time.RFC3339)
		out = append(out, waitingRow{ItemID: "backend:" + backend, Class: "alarm", Where: "backend", Why: kind + " reopens " + reopens, Mover: waitingRuleFor(alarms.Window).mover, Since: "—", ClassSource: "store", backendReset: time.UnixMilli(reset).UTC()})
	}
	return out, rows.Err()
}

// Run timestamps may share a second. Finish order and the store's event sequence
// distinguish those runs before an ID fallback; attempt IDs are not chronological.
func waitingRunLater(a, b storeRun) bool {
	if a.StartedAtMS != b.StartedAtMS {
		return a.StartedAtMS > b.StartedAtMS
	}
	if a.FinishedAtMS != b.FinishedAtMS {
		return a.FinishedAtMS > b.FinishedAtMS
	}
	if a.EventSeq != b.EventSeq {
		return a.EventSeq > b.EventSeq
	}
	return a.RunID > b.RunID
}

// waitingEndCause reads the last run's observed termination, never inferring it
// from state. An older store may lack the column and remains read-only here.
func waitingEndCause(r *store.Reader, runID string) (string, error) {
	var cause sql.NullString
	err := r.QueryRow(`SELECT end_cause FROM runs WHERE run_id = ?`, runID).Scan(&cause)
	if isMissingColumn(err) || errors.Is(err, sql.ErrNoRows) {
		return "end cause not recorded", nil
	}
	if err != nil {
		return "", err
	}
	switch cause.String {
	case "", "end cause not recorded":
		return "end cause not recorded", nil
	case "normal-exit":
		return "normal exit", nil
	case "time-limit":
		return "time limit", nil
	case "lease-reclaimed":
		return "lease reclaimed", nil
	case "failed-to-start":
		return "failure to start", nil
	default:
		return cause.String, nil
	}
}

// The event sequence orders stop/resume segments, even within one millisecond.
func waitingStoppedRun(r *store.Reader, runID string) (int64, bool, error) {
	var kind string
	var at int64
	err := r.QueryRow(`SELECT kind, ts FROM events WHERE object_type = 'run' AND object_id = ?
  AND kind IN ('drain.orphaned', 'run.resume') ORDER BY seq DESC LIMIT 1`, runID).Scan(&kind, &at)
	if isMissingTable(err) || errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil || kind != "drain.orphaned" {
		return 0, false, err
	}
	return at, true, nil
}

func waitingTerminal(state string) bool {
	return state == "landed" || state == "closed" || state == "resolved-elsewhere"
}

// Each decision query returns at most one row for one displayed item. SQL finds
// the latest answer, last interruption, and first answer after that interruption;
// no history slice or arbitrary time window can truncate the exact streak.
func waitingDecision(r *store.Reader, itemID, stoppedRun string, stoppedAt int64) (storeDispatch, int64, string, bool, error) {
	var last storeDispatch
	var mover string
	// Old stores retain audit history for identity holds. Skip them for both the
	// current decision and its streak, without discarding an earlier valid hold.
	where := ` WHERE item_id = ? AND reason <> 'judge-grant-absent'
		AND NOT (action = 'hold' AND (reason = 'trunk-foreign-identity' OR reason GLOB 'trunk-foreign-identity: *'))`
	args := []any{itemID}
	if stoppedRun != "" {
		// A launch tied with (or recorded after) its stop cannot supersede the
		// stop. Remove it before choosing a decision so an equally timed hold
		// wins regardless of lexical decision ID order. Retry also launches runs.
		where += ` AND NOT (COALESCE(run_id, '') = ? AND action IN ('dispatch', 'retry') AND dispatched_at >= ?)`
		args = append(args, stoppedRun, stoppedAt)
	}
	selectLatest := func(column string) error {
		return r.QueryRow(`SELECT decision_id, action, reason, dispatched_at, `+column+`
   FROM dispatches`+where+` ORDER BY dispatched_at DESC, decision_id DESC LIMIT 1`, args...).
			Scan(&last.DecisionID, &last.Action, &last.Reason, &last.DispatchedMS, &mover)
	}
	err := selectLatest(`COALESCE(deferred_by, '')`)
	if isMissingColumn(err) {
		err = selectLatest(`''`)
	}
	if isMissingTable(err) || errors.Is(err, sql.ErrNoRows) {
		return last, 0, "", false, nil
	}
	if err != nil {
		return last, 0, "", false, err
	}
	var brokenAt int64
	var brokenID string
	err = r.QueryRow(`SELECT dispatched_at, decision_id FROM dispatches`+where+`
  AND (action != ? OR reason != ?)
  ORDER BY dispatched_at DESC, decision_id DESC LIMIT 1`, append(args, last.Action, last.Reason)...).Scan(&brokenAt, &brokenID)
	var since int64
	if errors.Is(err, sql.ErrNoRows) {
		err = r.QueryRow(`SELECT dispatched_at FROM dispatches`+where+`
   ORDER BY dispatched_at, decision_id LIMIT 1`, args...).Scan(&since)
	} else if err == nil {
		err = r.QueryRow(`SELECT dispatched_at FROM dispatches`+where+`
   AND (dispatched_at, decision_id) > (?, ?)
   ORDER BY dispatched_at, decision_id LIMIT 1`, append(args, brokenAt, brokenID)...).Scan(&since)
	}
	return last, since, mover, true, err
}

var waitingStages = []string{"queue", "lane", "fence", "judge", "gate", "land", "trunk"}

func waitingFurtherStage(a, b string) string {
	for i := len(waitingStages) - 1; i >= 0; i-- {
		if waitingStages[i] == a || waitingStages[i] == b {
			return waitingStages[i]
		}
	}
	return "queue"
}

// Current writers leave gate/refusal events and landings, not candidate rows.
// Aggregate stage evidence in SQL so event history is not copied into the view.
func waitingRecordedStage(r *store.Reader, itemID string, run *storeRun) (string, error) {
	stage, runID := "queue", ""
	if run != nil {
		runID = run.RunID
		var step int
		err := r.QueryRow(`SELECT COALESCE(MAX(CASE
   WHEN kind = 'gate.verdict' THEN 4
   WHEN kind = 'drain.refusal' THEN CASE json_extract(CASE WHEN json_valid(payload) THEN payload ELSE '{}' END, '$.step')
    WHEN 'no-op' THEN 1 WHEN 'fence' THEN 2
    WHEN 'words-missing' THEN 2 WHEN 'words-altered' THEN 2 WHEN 'idea-no-outcome' THEN 2
    WHEN 'eval-failed' THEN 3 WHEN 'judge-unreadable' THEN 3 WHEN 'gate' THEN 4
    WHEN 'land' THEN 5 WHEN 'conflict' THEN 5 ELSE 0 END
   ELSE 0 END), 0) FROM events
   WHERE object_type = 'run' AND object_id = ? AND kind IN ('gate.verdict','drain.refusal')`, runID).Scan(&step)
		if err != nil && !isMissingTable(err) {
			return "", err
		}
		stage = waitingStages[step]
	}
	var result string
	err := r.QueryRow(`SELECT result FROM landings WHERE item_id = ? AND (? = '' OR run_id = ?)
  ORDER BY event_seq DESC, landing_id DESC LIMIT 1`, itemID, runID, runID).Scan(&result)
	if err != nil && !isMissingTable(err) && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err == nil {
		if result == "landed" {
			stage = "trunk"
		} else {
			stage = waitingFurtherStage(stage, "land")
		}
	}
	return stage, nil
}

// decorateOwnerQuestionWaiting uses the conflict record for state and the index for the title.
func decorateOwnerQuestionWaiting(row *waitingRow, c waitingConflict, source vantage.QuestionSource) {
	file, id, _ := strings.Cut(c.QuestionRef, "#")
	row.Class = "needs you"
	if c.QuestionRemovedHead != "" {
		row.Mover = "operator; restore " + c.QuestionRef + " under the item's block and land it"
		row.Why = "question-removed: " + c.QuestionRef + ": the question was removed without a ruling"
		return
	}
	row.Mover = "owner; answer " + c.QuestionRef
	row.Why = "question-open: " + c.QuestionRef
	if source == nil || file == "" || id == "" {
		return
	}
	doc, err := source.Document(file)
	if err != nil {
		return
	}
	for _, q := range doc.Questions {
		if q.ID != id {
			continue
		}
		row.Why = "question-open: " + questionText(q)
		return
	}
}

// normalizeWaitingMover keeps question answers separate from operational acts,
// including rows read from older stores that named only an owner.
func normalizeWaitingMover(row *waitingRow) {
	if row.Observation == "post-landing:push" && (row.Class == "needs you" ||
		strings.HasPrefix(row.Mover, "the heavy runner;") || strings.HasPrefix(row.Mover, "sync-remote-")) {
		return // Structured push rows already name their mover.
	}
	if row.Class == "ready" || row.Class == "moving" || row.Class == "done" {
		return
	}
	if strings.HasPrefix(row.Why, "owner-only-owed: ") {
		return
	}
	if strings.HasPrefix(row.Why, "obstacle-owner:") && strings.HasPrefix(row.Mover, "owner;") {
		return
	}
	if strings.HasPrefix(row.Mover, "owner; answer ") && strings.Contains(row.Mover, "#OQ-") {
		return
	}
	if row.SplitParent != nil && strings.HasPrefix(row.Mover, "owner;") {
		return
	}
	for _, actor := range []string{"drain;", "design seat;", "operator;"} {
		if strings.HasPrefix(row.Mover, actor) {
			return
		}
	}
	actor := "drain"
	if row.Mover == "design" || row.Mover == "plan" || strings.HasPrefix(row.Mover, "design;") || strings.HasPrefix(row.Mover, "plan;") || strings.Contains(row.Mover, "design seat") {
		actor = "design seat"
	}
	if row.Class == "needs you" || row.Class == "needs a cause" || strings.HasPrefix(row.Why, "trunk-path-dirty:") {
		actor = "operator"
	}
	action := row.Mover
	for _, prefix := range []string{"the owner; ", "owner; ", "owner: ", "the operator; ", "operator; ", "design seat; ", "design; ", "plan; ", "drain; "} {
		action = strings.TrimPrefix(action, prefix)
	}
	switch action {
	case "owner", "the owner", "operator", "the operator":
		action = "resolve the recorded hold"
	case "design", "plan":
		action = "resolve the recorded planning hold"
	}
	row.Mover = actor + "; " + action
}

func printWaiting(w io.Writer, rows []waitingRow) error {
	owner, drain, design, operator := 0, 0, 0, 0
	for _, row := range rows {
		if row.Class == "ready" || row.Class == "moving" || row.Class == "done" {
			continue
		}
		switch {
		case row.Observation == "post-landing:push" && row.Class == "needs you":
			if strings.HasPrefix(row.Mover, "owner;") {
				owner++
			} else {
				operator++
			}
		case (strings.HasPrefix(row.Mover, "owner; answer ") && strings.Contains(row.Mover, "#OQ-")) || (strings.HasPrefix(row.Why, "obstacle-owner:") && strings.HasPrefix(row.Mover, "owner;")):
			owner++
		case strings.HasPrefix(row.Mover, "design seat;") || strings.Contains(row.Mover, "design seat"):
			design++
		case strings.HasPrefix(row.Mover, "operator;") || row.Class == "needs you" || row.Class == "needs a cause":
			operator++
		default:
			drain++
		}
	}
	if _, err := fmt.Fprintf(w, "summary: owner-waiting %d; next [drain=%d, design seat=%d, operator=%d]\n", owner, drain, design, operator); err != nil {
		return err
	}
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "#\titem_id\tclass\twhere\twhy\tmover\tsince"); err != nil {
		return err
	}
	for _, row := range rows {
		place := placeText(row.Place)
		// Quote control characters supplied by store text so one item stays one row.
		why := row.Why
		for _, change := range row.GraphChanges {
			why += "; " + change
		}
		if row.Waited != "" {
			why += " (waited " + row.Waited + ")"
		}
		identity := row.ItemID
		if row.SplitPart != nil && row.Document == "" && row.Observation == "" {
			if row.SplitPart.ParentTitle != "" {
				identity = fmt.Sprintf("%s (%s · part %s)", row.ItemID, row.SplitPart.ParentTitle, row.SplitPart.PartLabel)
			} else {
				identity = fmt.Sprintf("%s (part %s)", row.ItemID, row.SplitPart.PartLabel)
			}
		}
		if row.Document != "" {
			identity = row.Document
		}
		if row.Observation != "" {
			identity = row.Observation
		}
		class := row.Class
		if class == "moving" && row.Position == "running" {
			class += " (running)"
		}
		cells := []string{place, identity, class, row.Where, why, row.Mover, row.Since}
		for i, cell := range cells {
			cells[i] = strings.NewReplacer("\t", "\\t", "\n", "\\n", "\r", "\\r").Replace(cell)
		}
		if _, err := fmt.Fprintln(table, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return table.Flush()
}

// The durable wait carries the check's observations and the concurrency change
// that will wake it; the face never supplies a clock-based retry of its own.
func waitingGateStall(r *store.Reader, itemID string) (string, error) {
	var raw string
	err := r.QueryRow(`SELECT payload FROM events WHERE kind='drain.gate-stall' AND json_extract(payload,'$.candidate.item_id')=? ORDER BY seq DESC LIMIT 1`, itemID).Scan(&raw)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var hold struct {
		GatesInFlight int             `json:"gates_in_flight"`
		Observations  json.RawMessage `json:"observations"`
	}
	if err := json.Unmarshal([]byte(raw), &hold); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s; %d gates in flight at stall", hold.Observations, hold.GatesInFlight), nil
}

// Report the latest result of each action; a successful retry clears its warning.
// Structured push results name a mover; older outcomes keep the detail-based row.
func appendPostLandingWaiting(r *store.Reader, out []waitingRow) ([]waitingRow, error) {
	rows, err := r.Query(`WITH actions AS (
    SELECT seq, json_extract(payload,'$.action') AS action, object_id, json_extract(payload,'$.detail') AS detail, COALESCE(json_extract(payload,'$.what'),'') AS what,
      COALESCE(json_extract(payload,'$.kind'),'') AS state, payload, ts
    FROM events WHERE kind = 'drain.post-landing-action'
   ), latest AS (SELECT max(seq) AS seq FROM actions GROUP BY action)
   SELECT action, COALESCE(object_id,''), COALESCE(detail,''), what, state, payload, ts FROM actions JOIN latest USING(seq)
   WHERE detail <> '' OR (action = 'push' AND state <> '') ORDER BY action`)
	if isMissingTable(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var action, tip, detail, what, state, payload string
		var ts int64
		if err := rows.Scan(&action, &tip, &detail, &what, &state, &payload, &ts); err != nil {
			return nil, err
		}
		if action == "push" && state != "" {
			if state == "push-ok" || state == "remote-sync-cleared" {
				continue
			}
			ev, err := parsePushEvent(payload, tip, ts)
			if err != nil {
				return nil, err
			}
			if class, mover, why, ok := ev.pushRow(); ok {
				out = append(out, waitingRow{Observation: "post-landing:push", Class: class, Where: "push", Why: why, Mover: mover, Since: ev.since(), ClassSource: "store"})
				continue
			}
		}
		if detail == "" {
			continue
		}
		why := "push failed"
		if action == "roll" {
			why = "running build is behind the trunk"
		}
		mover := "the next relevant landing"
		if what == "green" {
			why = "push of the green commit failed"
			mover = "the next green advance"
		}
		out = append(out, waitingRow{Observation: "post-landing:" + action, Class: "waits on work", Where: action,
			Why: why + " at " + tip + ": " + strings.Join(strings.Fields(detail), " "), Mover: mover, Since: "—", ClassSource: "store"})
	}
	return out, rows.Err()
}

func readWaitingLaneCapacityRows(ctx context.Context, path, loop string) ([]laneRow, error) {
	db, err := store.OpenRead(ctx, path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var rows []laneRow
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		version, err := r.SchemaVersion()
		if err != nil {
			return err
		}
		if !schemaSupported(version) {
			return schemaMismatch{version: version}
		}
		rows, err = assembleWaitingLaneCapacityRows(r, loop)
		return err
	})
	return rows, err
}

func assembleWaitingLaneCapacityRows(r *store.Reader, loop string) ([]laneRow, error) {
	lanes, err := readLanesIn(r, loop)
	if err != nil && !isMissingTable(err) {
		return nil, err
	}
	configured, _ := readConfiguredSessionLanes(r)
	heldByLane, err := readHeldByLaneForOpenItems(r)
	if err != nil {
		return nil, err
	}
	lanes = ensureConfiguredStoreLanes(lanes, configured, heldByLane)
	seenRun := map[string]bool{}
	var leasedRunIDs []string
	for _, lane := range lanes {
		if lane.State != laneStateLeased {
			continue
		}
		runID := lane.RunID
		if len(lane.ActiveNodes) > 0 {
			runID = lane.ActiveNodes[0]
		}
		if runID != "" && !seenRun[runID] {
			seenRun[runID] = true
			leasedRunIDs = append(leasedRunIDs, runID)
		}
	}
	runs, err := readRunsByIDs(r, leasedRunIDs)
	if err != nil {
		return nil, err
	}
	rows := make([]laneRow, 0, len(lanes))
	for _, lane := range lanes {
		row := laneRow{LaneID: lane.LaneID, State: "idle", RunningNodes: len(lane.ActiveNodes)}
		var run storeRun
		if lane.State == laneStateLeased {
			if len(lane.ActiveNodes) > 0 {
				run = runs[lane.ActiveNodes[0]]
			} else {
				run = runs[lane.RunID]
			}
		}
		row.State = laneWorkState(lane, run)
		rows = append(rows, row)
	}
	applyLaneGroups(rows, configured, heldByLane)
	return rows, nil
}

func sqlPlaceholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func ensureEdgeStates(r *store.Reader, states map[string]string, edges []storeEdge) error {
	if len(edges) == 0 {
		return nil
	}
	ids := make([]string, len(edges))
	for i, e := range edges {
		ids[i] = e.BlockedBy
	}
	return ensureItemStates(r, states, ids)
}

func ensureItemStates(r *store.Reader, states map[string]string, ids []string) error {
	var missing []string
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		if _, ok := states[id]; ok {
			continue
		}
		seen[id] = true
		missing = append(missing, id)
	}
	if len(missing) == 0 {
		return nil
	}
	const chunk = 400
	for start := 0; start < len(missing); start += chunk {
		end := start + chunk
		if end > len(missing) {
			end = len(missing)
		}
		batch := missing[start:end]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		rows, err := r.Query(`SELECT item_id, state FROM items WHERE item_id IN (`+sqlPlaceholders(len(batch))+`)`, args...)
		if err != nil {
			if isMissingTable(err) {
				return nil
			}
			return err
		}
		for rows.Next() {
			var id, state string
			if err := rows.Scan(&id, &state); err != nil {
				rows.Close()
				return err
			}
			states[id] = state
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return nil
}
