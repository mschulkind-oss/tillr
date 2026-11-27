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

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
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
