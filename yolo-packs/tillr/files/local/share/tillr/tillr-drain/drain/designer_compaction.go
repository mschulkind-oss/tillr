package drain

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// The journal identifies the previous request checkpoint, not a dispatch attempt.
// Once intent exists, absence of a completed checkpoint is ambiguous: never pay again.
type compactionJournal struct {
	SessionID  string              `json:"session_id"`
	Checkpoint string              `json:"checkpoint"`
	Transcript string              `json:"transcript"`
	Offset     int64               `json:"offset"`
	RunID      string              `json:"run_id"`
	ItemID     string              `json:"item_id"`
	Receipt    *designerCompaction `json:"receipt,omitempty"`
}

func designerCompactionFile(l *Loop, session, checkpoint string) string {
	return filepath.Join(l.cfg.LaneRoot, "compactions", session, checkpoint+".json")
}

func writeCompactionJournal(path string, j compactionJournal, exclusive bool) error {
	raw, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	name := path
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if !exclusive {
		name = path + ".next"
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(name, flags, 0644)
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if !exclusive {
		return os.Rename(name, path)
	}
	return nil
}

func recoverCompaction(j *compactionJournal) error {
	f, err := os.Open(j.Transcript)
	if err != nil {
		return fmt.Errorf("ambiguous compaction completion: %w", err)
	}
	defer f.Close()
	if _, err = f.Seek(j.Offset, io.SeekStart); err != nil {
		return err
	}
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 64<<10), 16<<20)
	count := 0
	for scan.Scan() {
		var e struct {
			Type              string `json:"type"`
			TokensBefore      *int   `json:"tokensBefore"`
			ContextTokenFacts struct {
				After struct {
					Tokens *int `json:"tokens"`
				} `json:"after"`
			} `json:"contextTokenFacts"`
			Usage struct {
				TotalTokens *int64 `json:"totalTokens"`
				Cost        struct {
					Total *float64 `json:"total"`
				} `json:"cost"`
			} `json:"usage"`
		}
		if json.Unmarshal(scan.Bytes(), &e) != nil || e.Type != "compaction" {
			continue
		}
		count++
		if e.TokensBefore != nil && e.ContextTokenFacts.After.Tokens != nil {
			j.Receipt = &designerCompaction{TokensBefore: e.TokensBefore, TokensAfter: e.ContextTokenFacts.After.Tokens, Tokens: e.Usage.TotalTokens, CostUSD: e.Usage.Cost.Total}
		}
	}
	if err = scan.Err(); err != nil {
		return err
	}
	if count != 1 || j.Receipt == nil {
		return fmt.Errorf("ambiguous compaction completion for session %s at %s: transcript has %d completed checkpoints with no unique receipt; refusing another paid compaction", j.SessionID, j.Checkpoint, count)
	}
	return nil
}

func (l *Loop) ensureDesignerCompaction(ctx context.Context, d Decision) (*compactionJournal, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	var previous *lastDesigner
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		batch, e := item.ReadCommentBatch(r, d.ItemID)
		if e != nil {
			return e
		}
		if batch == nil {
			return fmt.Errorf("comment item %s has no batch", d.ItemID)
		}
		previous, e = readDesigner(r, batch.Document)
		return e
	})
	s.Close()
	if err != nil {
		return nil, err
	}
	if previous == nil || previous.SessionID != d.DesignerSessionID {
		return nil, fmt.Errorf("designer checkpoint changed before compaction")
	}
	if err = checkRunID(previous.RunID); err != nil {
		return nil, err
	}
	if !run.IsSessionID(previous.SessionID) {
		return nil, fmt.Errorf("invalid designer session id")
	}
	path := designerCompactionFile(l, previous.SessionID, previous.RunID)
	raw, err := os.ReadFile(path)
	var j compactionJournal
	if err == nil {
		if err = json.Unmarshal(raw, &j); err != nil {
			return nil, fmt.Errorf("ambiguous compaction journal: %w", err)
		}
		if j.SessionID != previous.SessionID || j.Checkpoint != previous.RunID {
			return nil, fmt.Errorf("ambiguous compaction journal identity")
		}
		if j.Receipt == nil {
			// A receipt written before the journal was completed is recoverable too.
			receipt, readErr := os.ReadFile(filepath.Join(RunDir(l.cfg.Root, j.RunID), "compaction.json"))
			if readErr == nil {
				j.Receipt = &designerCompaction{}
				if err = json.Unmarshal(receipt, j.Receipt); err != nil {
					return nil, err
				}
			} else if os.IsNotExist(readErr) {
				if err = recoverCompaction(&j); err != nil {
					return nil, err
				}
			} else {
				return nil, readErr
			}
			if err = writeCompactionJournal(path, j, false); err != nil {
				return nil, err
			}
		}
	} else if os.IsNotExist(err) {
		info, e := os.Stat(previous.Transcript)
		if e != nil {
			return nil, e
		}
		j = compactionJournal{SessionID: previous.SessionID, Checkpoint: previous.RunID, Transcript: previous.Transcript, Offset: info.Size(), RunID: d.RunRequest.RunID, ItemID: d.ItemID}
		if err = writeCompactionJournal(path, j, true); err != nil {
			return nil, err
		}
		var runBin string
		if ps, ok := l.cfg.Steps.(*ProcessSteps); ok {
			runBin = ps.RunBin
		}
		compactBin, compactPrefix := compactCommand(d.RunRequest, runBin)
		args := append([]string{}, compactPrefix...)
		args = append(args, d.RunRequest.Command[1:]...)
		args = append(args, "--session-id", d.DesignerSessionID)
		cmd := exec.CommandContext(ctx, compactBin, args...)
		cmd.Dir = l.laneTarget(d.LaneID)
		cmd.Env = append(os.Environ(), "BOARD_DRAIN_RUN="+d.RunRequest.RunID, "BOARD_DRAIN_SESSION="+d.DesignerSessionID, "TILLR_COMPACTION_JOURNAL="+path)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		raw, err = cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("compact designer %s (completion may be ambiguous): %w: %s", d.DesignerSessionID, err, stderr.String())
		}
		j.Receipt = &designerCompaction{}
		if err = json.Unmarshal(raw, j.Receipt); err != nil {
			return nil, err
		}
		if j.Receipt.TokensBefore == nil || j.Receipt.TokensAfter == nil {
			return nil, fmt.Errorf("compact omitted token measurements")
		}
		if err = writeCompactionJournal(path, j, false); err != nil {
			return nil, err
		}
		l.reportf("loop: compact %s: %d -> %d estimated tokens, %s\n", d.DesignerSessionID, *j.Receipt.TokensBefore, *j.Receipt.TokensAfter, spendWords(&run.Usage{Tokens: j.Receipt.Tokens, CostUSD: j.Receipt.CostUSD}))
	} else {
		return nil, err
	}
	if j.Receipt == nil || j.Receipt.TokensBefore == nil || j.Receipt.TokensAfter == nil {
		return nil, fmt.Errorf("ambiguous compaction receipt")
	}
	raw, err = json.Marshal(j.Receipt)
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(RunDir(l.cfg.Root, d.RunRequest.RunID), "compaction.json"), raw, 0644); err != nil {
		return nil, err
	}
	return &j, nil
}
