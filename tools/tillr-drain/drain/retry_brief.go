package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type answeredOwnerQuestion struct {
	RunID    string
	Question string
	Answer   string
	By       string
	At       string
	Via      string
}

type recordedRetryReason struct {
	SupersedesRunID string
	Reason          string
	By              string
	At              string
}

// isFiledRequest reports whether itemID was filed as a request with stored request words
// and no document block.
func isFiledRequest(r *store.Reader, itemID string) (bool, error) {
	words, err := item.RequestWords(r, itemID)
	if err != nil {
		return false, err
	}
	return words != nil, nil
}

// acceptedBuildRetryCommand returns the exact `tillr drain retry` command AuthorizeRetry
// accepts for itemID's latest finished run (including any required --acknowledge flag),
// falling back to fallbackRunID when the store has no finished run row yet.
func acceptedBuildRetryCommand(r *store.Reader, itemID, fallbackRunID string) (string, error) {
	var (
		rec      run.Record
		exitCode sql.NullInt64
		refs     string
	)
	err := r.QueryRow(
		`SELECT run_id, backend, state, exit_code, artifact_refs
		   FROM runs WHERE item_id = ? AND state IN (?, ?, ?)
		  ORDER BY started_at DESC, event_seq DESC, rowid DESC LIMIT 1`,
		itemID, run.StateExited, run.StateFailedToStart, run.StateTimedOut,
	).Scan(&rec.RunID, &rec.Backend, &rec.State, &exitCode, &refs)
	if errors.Is(err, sql.ErrNoRows) || rec.RunID == "" {
		return retryCommand(itemID, fallbackRunID, ""), nil
	}
	if err != nil {
		return "", readFailure(err)
	}
	if exitCode.Valid {
		code := int(exitCode.Int64)
		rec.ExitCode = &code
	}
	var artifactRefs []run.ArtifactRef
	_ = json.Unmarshal([]byte(refs), &artifactRefs)
	judged, err := runJudged(r, rec.RunID)
	if err != nil {
		return "", err
	}
	class := ClassifyAttempt(rec, logTail(artifactRefs))
	return retryCommand(itemID, rec.RunID, acceptedAck(judged, class)), nil
}

// workerConflictReport isolates the worker's own report from loop-appended notes or design
// turns inside a conflict's text.
func workerConflictReport(text string) string {
	if strings.HasPrefix(text, IdenticalRepeatPrefix) || strings.HasPrefix(text, "Second failed attempt:") {
		if _, blocked, ok := strings.Cut(text, "\n\nBlocked finding:\n"); ok {
			text = blocked
		}
	}
	for _, marker := range []string{
		"\n\nDesign run ",
		"\n\nOwner question: ",
		"\n\nNo item block reference",
		"\n\nThe backend policy",
		"\n\nDeliver the answer",
		"\n\nThis item is a filed request",
		"\n\nQuestion for the owner",
		"\n\nStale base:",
	} {
		if before, _, ok := strings.Cut(text, marker); ok {
			text = before
		}
	}
	return strings.TrimSpace(text)
}

func collapseSingleLine(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func firstParagraph(s string) string {
	s = strings.TrimSpace(s)
	if before, _, ok := strings.Cut(s, "\n\n"); ok {
		s = before
	}
	return collapseSingleLine(s)
}

// quoteBriefLines formats untrusted worker, operator, or owner text as a Markdown
// blockquote so embedded headings or template braces cannot alter brief structure.
func quoteBriefLines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.Trim(s, "\n")
	if s == "" {
		return "> (empty)"
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + line
		}
	}
	return strings.Join(lines, "\n")
}

// itemGuidanceBrief renders any answered owner questions and operator retry reasons
// recorded for itemID so every subsequent node and judge brief carries them verbatim.
func itemGuidanceBrief(ctx context.Context, storePath, itemID, worktree string) (string, error) {
	if storePath == "" || itemID == "" {
		return "", nil
	}
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return "", err
	}
	defer s.Close()

	var (
		questionByRun     = map[string]string{}
		questionOrder     []string
		conflictByID      = map[string]item.Conflict{}
		conflictOrder     []string
		vantageAnsweredAt = map[string]time.Time{}
		buildRetries      []struct {
			RetryID         string
			SupersedesRunID string
			RequestedBy     string
			Reason          string
			RequestedAt     time.Time
		}
		designRetries []Retry
		itemDecisions []struct {
			DecisionID string
			Text       string
			DecidedBy  string
			DecidedAt  time.Time
		}
	)

	recordQuestion := func(runID, q string) {
		q = strings.TrimSpace(q)
		if runID == "" || q == "" {
			return
		}
		if _, seen := questionByRun[runID]; !seen {
			questionOrder = append(questionOrder, runID)
		}
		questionByRun[runID] = q
	}

	err = s.ReadTx(ctx, func(r *store.Reader) error {
		cRows, err := r.Query(
			`SELECT payload, ts FROM events WHERE kind = 'item.conflict' AND object_id = ? ORDER BY seq ASC`,
			itemID,
		)
		if err != nil {
			return readFailure(err)
		}
		for cRows.Next() {
			var (
				payload []byte
				ts      int64
				c       item.Conflict
			)
			if err := cRows.Scan(&payload, &ts); err != nil {
				cRows.Close()
				return readFailure(err)
			}
			if json.Unmarshal(payload, &c) != nil || c.ConflictID == "" {
				continue
			}
			if _, seen := conflictByID[c.ConflictID]; !seen {
				conflictOrder = append(conflictOrder, c.ConflictID)
			}
			conflictByID[c.ConflictID] = c
			report := workerConflictReport(c.Text)
			if obs := parseObstacle(report); obs.Kind == ObstacleOwner && obs.Field("QUESTION") != "" {
				recordQuestion(c.RunID, obs.Field("QUESTION"))
			} else if q := loopOwnerQuestion(c.Text); q != "" {
				recordQuestion(c.RunID, q)
			}
			if c.QuestionRef != "" && c.AnswerHead != "" && (c.Status == "answered" || c.Status == "closed") {
				if _, ok := vantageAnsweredAt[c.ConflictID]; !ok {
					vantageAnsweredAt[c.ConflictID] = time.UnixMilli(ts).UTC()
				}
			}
		}
		if err := cRows.Err(); err != nil {
			cRows.Close()
			return readFailure(err)
		}
		cRows.Close()

		rRows, err := r.Query(
			`SELECT retry_id, supersedes_run_id, requested_by, reason, requested_at
			   FROM retries WHERE item_id = ? ORDER BY requested_at ASC, rowid ASC`,
			itemID,
		)
		if err != nil {
			if !strings.Contains(err.Error(), "no such table: retries") {
				return readFailure(err)
			}
		} else {
			for rRows.Next() {
				var (
					retryID, supersedes, by, reason string
					atMS                            int64
				)
				if err := rRows.Scan(&retryID, &supersedes, &by, &reason, &atMS); err != nil {
					rRows.Close()
					return readFailure(err)
				}
				buildRetries = append(buildRetries, struct {
					RetryID         string
					SupersedesRunID string
					RequestedBy     string
					Reason          string
					RequestedAt     time.Time
				}{
					RetryID:         retryID,
					SupersedesRunID: supersedes,
					RequestedBy:     by,
					Reason:          reason,
					RequestedAt:     time.UnixMilli(atMS).UTC(),
				})
			}
			if err := rRows.Err(); err != nil {
				rRows.Close()
				return readFailure(err)
			}
			rRows.Close()
		}

		for _, br := range buildRetries {
			if _, ok := questionByRun[br.SupersedesRunID]; ok {
				continue
			}
			var refsRaw string
			if err := r.QueryRow(`SELECT artifact_refs FROM runs WHERE run_id = ?`, br.SupersedesRunID).Scan(&refsRaw); err != nil {
				continue
			}
			var refs []run.ArtifactRef
			if json.Unmarshal([]byte(refsRaw), &refs) != nil {
				continue
			}
			for _, ref := range refs {
				if ref.Kind != "report" || ref.Ref == "" {
					continue
				}
				raw, err := os.ReadFile(ref.Ref)
				if err != nil {
					continue
				}
				if obs := parseObstacle(string(raw)); obs.Kind == ObstacleOwner && obs.Field("QUESTION") != "" {
					recordQuestion(br.SupersedesRunID, obs.Field("QUESTION"))
				}
			}
		}

		dRows, err := r.Query(
			`SELECT payload, ts FROM events WHERE kind = ? AND json_extract(payload, '$.item_id') = ? ORDER BY seq ASC`,
			DesignRetryKind, itemID,
		)
		if err != nil {
			return readFailure(err)
		}
		for dRows.Next() {
			var (
				payload []byte
				ts      int64
				dr      Retry
			)
			if err := dRows.Scan(&payload, &ts); err != nil {
				dRows.Close()
				return readFailure(err)
			}
			if json.Unmarshal(payload, &dr) != nil {
				continue
			}
			if dr.RequestedAt.IsZero() {
				dr.RequestedAt = time.UnixMilli(ts).UTC()
			} else {
				dr.RequestedAt = dr.RequestedAt.UTC()
			}
			designRetries = append(designRetries, dr)
		}
		if err := dRows.Err(); err != nil {
			dRows.Close()
			return readFailure(err)
		}
		dRows.Close()

		decRows, err := r.Query(
			`SELECT decision_id, text, COALESCE(scope_ref, ''), decided_by, decided_at
			   FROM decisions WHERE scope_kind = 'items' AND status = 'active'
			  ORDER BY decided_at ASC, rowid ASC`,
		)
		if err != nil {
			if !strings.Contains(err.Error(), "no such table: decisions") {
				return readFailure(err)
			}
		} else {
			for decRows.Next() {
				var (
					decID, text, scopeRef, by string
					atMS                      int64
				)
				if err := decRows.Scan(&decID, &text, &scopeRef, &by, &atMS); err != nil {
					decRows.Close()
					return readFailure(err)
				}
				if !scopeRefNamesItem(scopeRef, itemID) {
					continue
				}
				itemDecisions = append(itemDecisions, struct {
					DecisionID string
					Text       string
					DecidedBy  string
					DecidedAt  time.Time
				}{
					DecisionID: decID,
					Text:       text,
					DecidedBy:  by,
					DecidedAt:  time.UnixMilli(atMS).UTC(),
				})
			}
			if err := decRows.Err(); err != nil {
				decRows.Close()
				return readFailure(err)
			}
			decRows.Close()
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	var (
		answered     []answeredOwnerQuestion
		retries      []recordedRetryReason
		answeredRuns = map[string]bool{}
	)

	for _, br := range buildRetries {
		at := br.RequestedAt.UTC().Format(time.RFC3339)
		if q := questionByRun[br.SupersedesRunID]; q != "" {
			answered = append(answered, answeredOwnerQuestion{
				RunID:    br.SupersedesRunID,
				Question: q,
				Answer:   br.Reason,
				By:       br.RequestedBy,
				At:       at,
				Via:      "tillr drain retry on " + br.SupersedesRunID,
			})
			answeredRuns[br.SupersedesRunID] = true
			continue
		}
		retries = append(retries, recordedRetryReason{
			SupersedesRunID: br.SupersedesRunID,
			Reason:          br.Reason,
			By:              br.RequestedBy,
			At:              at,
		})
	}

	for _, dr := range designRetries {
		at := dr.RequestedAt.UTC().Format(time.RFC3339)
		c := conflictByID[dr.ReraisesConflict]
		if q := questionByRun[c.RunID]; q != "" {
			answered = append(answered, answeredOwnerQuestion{
				RunID:    c.RunID,
				Question: q,
				Answer:   dr.Reason,
				By:       dr.RequestedBy,
				At:       at,
				Via:      "tillr drain retry on " + dr.SupersedesRunID,
			})
			answeredRuns[c.RunID] = true
			continue
		}
		retries = append(retries, recordedRetryReason{
			SupersedesRunID: dr.SupersedesRunID,
			Reason:          dr.Reason,
			By:              dr.RequestedBy,
			At:              at,
		})
	}

	for _, cid := range conflictOrder {
		c := conflictByID[cid]
		at, ok := vantageAnsweredAt[cid]
		if !ok {
			continue
		}
		file, qid, _ := strings.Cut(c.QuestionRef, "#")
		var docTitle, docAns, docBy string
		if worktree != "" && file != "" && qid != "" && !filepath.IsAbs(file) && !strings.HasPrefix(filepath.Clean(file), "..") {
			if raw, readErr := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(file))); readErr == nil {
				docTitle, docAns, docBy = extractVantageAnswer(raw, qid)
			}
		}
		q := questionByRun[c.RunID]
		if q == "" {
			q = docTitle
		}
		if q == "" {
			if title, ok := item.OwnerDecision(ownerReport(c)); ok {
				q = title
			}
		}
		if q == "" {
			q = c.QuestionRef
		}
		if docAns == "" {
			docAns = fmt.Sprintf("Answered in %s at commit %s.", c.QuestionRef, shortSHA(c.AnswerHead))
		}
		if docBy == "" {
			docBy = "owner"
		}
		answered = append(answered, answeredOwnerQuestion{
			RunID:    c.RunID,
			Question: q,
			Answer:   docAns,
			By:       docBy,
			At:       at.UTC().Format(time.RFC3339),
			Via:      c.QuestionRef + " at " + shortSHA(c.AnswerHead),
		})
		if c.RunID != "" {
			answeredRuns[c.RunID] = true
		}
	}

	for _, dec := range itemDecisions {
		if dec.DecidedBy != item.DecidedByOwner {
			continue
		}
		for _, runID := range questionOrder {
			if answeredRuns[runID] {
				continue
			}
			answered = append(answered, answeredOwnerQuestion{
				RunID:    runID,
				Question: questionByRun[runID],
				Answer:   dec.Text,
				By:       dec.DecidedBy,
				At:       dec.DecidedAt.UTC().Format(time.RFC3339),
				Via:      "decision " + dec.DecisionID,
			})
			answeredRuns[runID] = true
			break
		}
	}

	var b strings.Builder
	if len(answered) > 0 {
		b.WriteString("\n## Answered owner questions\n\n")
		b.WriteString("Do not ask an answered question again; build from each recorded answer below.\n")
		for _, a := range answered {
			runLabel := ""
			if a.RunID != "" {
				runLabel = fmt.Sprintf(" (run `%s`)", a.RunID)
			}
			fmt.Fprintf(&b, "\n- **Question**%s:\n%s\n\n  **Answer** (by `%s` at `%s`; %s):\n%s\n",
				runLabel, quoteBriefLines(a.Question), a.By, a.At, a.Via, quoteBriefLines(a.Answer))
		}
	}
	if len(retries) > 0 {
		b.WriteString("\n## Retry reasons\n\n")
		b.WriteString("Build from each operator retry reason recorded for this item:\n")
		for _, r := range retries {
			fmt.Fprintf(&b, "\n- **Retry of `%s`** (by `%s` at `%s`):\n%s\n",
				r.SupersedesRunID, r.By, r.At, quoteBriefLines(r.Reason))
		}
	}
	return b.String(), nil
}

func scopeRefNamesItem(scopeRef, itemID string) bool {
	for _, part := range strings.Split(scopeRef, ",") {
		if strings.TrimSpace(part) == itemID {
			return true
		}
	}
	return false
}

var (
	vantageAnswerByRe   = regexp.MustCompile(`<!--\s*vantage:\s*answer\b[^>]*\bby="([^"]+)"`)
	vantageNextItemRe   = regexp.MustCompile(`^\s*(?:[0-9]+\.|[-*])\s+(?:💬|✅)\s+\*\*OQ-`)
	vantageTopHeadingRe = regexp.MustCompile(`^#{1,6}\s+`)
)

// extractVantageAnswer extracts a Vantage question's title and owner answer from raw
// Markdown without spawning an external indexer.
func extractVantageAnswer(raw []byte, questionID string) (title, answer, by string) {
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	start := -1
	headerMarker := "**" + questionID + ":"
	directiveMarker := "id=" + questionID
	for i, line := range lines {
		if strings.Contains(line, headerMarker) {
			start = i
			_, rest, _ := strings.Cut(line, headerMarker)
			rest = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "**"))
			title = rest
			break
		}
		if strings.Contains(line, "vantage:") && strings.Contains(line, "question") && strings.Contains(line, directiveMarker) {
			start = i
			break
		}
	}
	if start < 0 {
		// Check whether the question was compacted into a decision ledger line.
		for _, line := range lines {
			if strings.Contains(line, questionID) {
				return "", strings.TrimSpace(line), "owner"
			}
		}
		return "", "", ""
	}

	var block []string
	for i := start + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if vantageTopHeadingRe.MatchString(line) || strings.HasPrefix(trimmed, "<!-- tillr:") || vantageNextItemRe.MatchString(line) {
			break
		}
		if m := vantageAnswerByRe.FindStringSubmatch(line); m != nil {
			by = m[1]
		}
		block = append(block, line)
	}

	// Prefer explicit **Answer:** / <!-- vantage: answer --> block, ignoring the
	// unfilled template placeholder (`_(empty — fill in when decided)_`).
	isPlaceholder := func(s string) bool {
		return strings.Contains(s, "empty — fill in when decided") || strings.Contains(s, "empty - fill in when decided")
	}
	var ansLines []string
	var curLines []string
	capturing := false
	flush := func() {
		if len(curLines) > 0 {
			ansLines = append([]string(nil), curLines...)
			curLines = nil
		}
		capturing = false
	}
	for _, line := range block {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "**Answer") || (strings.Contains(trimmed, "vantage:") && strings.Contains(trimmed, "answer")) {
			flush()
			capturing = true
			if strings.HasPrefix(trimmed, "**Answer") {
				_, rest, ok := strings.Cut(trimmed, ":")
				if ok {
					rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), "**"))
					if rest != "" && !isPlaceholder(rest) {
						curLines = append(curLines, rest)
					} else if isPlaceholder(rest) {
						capturing = false
					}
				}
			}
			continue
		}
		if capturing {
			if strings.HasPrefix(trimmed, "<!--") {
				continue
			}
			cleaned := strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
			if cleaned != "" && !isPlaceholder(cleaned) {
				curLines = append(curLines, cleaned)
			} else if len(curLines) > 0 || isPlaceholder(cleaned) {
				flush()
			}
		}
	}
	flush()
	if len(ansLines) > 0 {
		return title, strings.Join(ansLines, "\n"), by
	}
	for _, line := range block {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Ruled ") || strings.HasPrefix(trimmed, "> Ruled ") {
			return title, strings.TrimSpace(strings.TrimPrefix(trimmed, ">")), by
		}
	}
	return title, "", by
}
