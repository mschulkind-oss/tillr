package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// syncResidue is the item writer's last whole sync observation. Reading its table
// directly keeps status independent of the writer's package and of document availability.
type syncResidue struct {
	SyncedAt           string
	UnsharedOpenIDs    []string
	UnsharedOpenOldest string
	StaleDocBlockIDs   []string
}

func readSyncResidue(r *store.Reader) (*syncResidue, error) {
	var s syncResidue
	var open, stale string
	err := r.QueryRow(`SELECT synced_at,unshared_open_ids,unshared_open_oldest,stale_doc_block_ids FROM item_sync_residue WHERE singleton=1`).Scan(&s.SyncedAt, &open, &s.UnsharedOpenOldest, &stale)
	if errors.Is(err, sql.ErrNoRows) || isMissingTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := time.Parse(time.RFC3339Nano, s.SyncedAt); err != nil {
		return nil, fmt.Errorf("sync residue time: %w", err)
	}
	if s.UnsharedOpenOldest != "" {
		if _, err := time.Parse(time.RFC3339Nano, s.UnsharedOpenOldest); err != nil {
			return nil, fmt.Errorf("sync residue oldest write: %w", err)
		}
	}
	if err := json.Unmarshal([]byte(open), &s.UnsharedOpenIDs); err != nil {
		return nil, fmt.Errorf("sync residue open ids: %w", err)
	}
	if err := json.Unmarshal([]byte(stale), &s.StaleDocBlockIDs); err != nil {
		return nil, fmt.Errorf("sync residue stale ids: %w", err)
	}
	return &s, nil
}

func statusText(p statusPayload) string {
	var b strings.Builder
	for _, alarm := range p.Alarms {
		fmt.Fprintln(&b, alarm)
	}
	fmt.Fprintf(&b, "status at %s\n", p.GeneratedAt.UTC().Format(time.RFC3339))
	for _, it := range p.Items {
		fmt.Fprintf(&b, "item: %s [%s] %s\n", it.ItemID, it.State, it.Title)
	}
	if p.Item != nil {
		fmt.Fprintf(&b, "item: %s [%s] %s\n", p.Item.ItemID, p.Item.State, p.Item.Title)
	}
	if p.Found != nil && !*p.Found {
		fmt.Fprintf(&b, "item: %s not found\n", p.Subject.ID)
	}
	fmt.Fprintf(&b, "candidates: %d\nruns: %d\n", len(p.Candidates), len(p.Runs))
	if p.QuestionsState != "" {
		fmt.Fprintf(&b, "questions: %s (%d)\n", p.QuestionsState, len(p.Questions))
	}
	if line := p.Push.text(); line != "" {
		fmt.Fprintln(&b, line)
	}
	if p.HeavyFault != "" {
		fmt.Fprintf(&b, "heavy status unavailable: %s\n", p.HeavyFault)
	}
	if p.UnsharedOpen == nil || p.StaleDocBlocks == nil || p.SyncedAt == "" {
		b.WriteString("no sync has been recorded\n")
	} else {
		fmt.Fprintf(&b, "sync at %s\n", p.SyncedAt)
		age := ""
		if oldest, err := time.Parse(time.RFC3339Nano, p.UnsharedOpenOldest); err == nil {
			minutes := int64(p.GeneratedAt.Sub(oldest) / time.Minute)
			if minutes < 0 {
				minutes = 0
			}
			age = fmt.Sprintf(" (oldest %dm)", minutes)
		}
		fmt.Fprintf(&b, "unshared_open: %d%s\nstale_doc_blocks: %d\n", *p.UnsharedOpen, age, *p.StaleDocBlocks)
	}
	return b.String()
}
