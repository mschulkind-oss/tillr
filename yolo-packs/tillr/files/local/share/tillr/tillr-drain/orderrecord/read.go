// Package orderrecord reads the store's hand-placement receipt and its tracked
// rebuild file without writing either. The loop and observatory share this read.
package orderrecord

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

// Status is the receipt shown beside Items. ChangedAtMS dates the last hand
// placement, not an import or a landing. An unset record setting omits it.
type Status struct {
	Recorded         bool  `json:"recorded"`
	HandEventSeq     int64 `json:"hand_event_seq"`
	RecordedEventSeq int64 `json:"recorded_event_seq"`
	ChangedAtMS      int64 `json:"changed_at_ms,omitempty"`
}

func receipt(body string) (int64, error) {
	line, _, _ := strings.Cut(body, "\n")
	const prefix = "# last_hand_move_event_seq: "
	if !strings.HasPrefix(line, prefix) {
		return 0, fmt.Errorf("order record lacks its last hand move receipt")
	}
	seq, err := strconv.ParseInt(strings.TrimPrefix(line, prefix), 10, 64)
	if err != nil || seq < 0 {
		return 0, fmt.Errorf("order record has an invalid last hand move receipt: %q", line)
	}
	return seq, nil
}

// Read reads the trunk object, never a possibly dirty checkout, and returns the
// receipt comments for a generated record. It imports no write-side utility:
// importing item would register its schemas and materializer in the view process.
// The hand-placement predicate matches item.OrderRecord, including no-op ideas.
func Read(ctx context.Context, r *store.Reader, root string) (*Status, string, error) {
	cfg, err := settings.Read(root)
	if err != nil {
		return nil, "", err
	}
	if cfg.Order.Record == "" {
		return nil, "", nil
	}
	var seq int64
	if err := r.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM events WHERE
 (kind='item.update' AND COALESCE(json_extract(payload,'$.placed_via'),'') NOT IN ('','sync'))
 OR (kind='item.add' AND object_id IN (SELECT item_id FROM idea_intake))`).Scan(&seq); err != nil {
		return nil, "", err
	}
	body := fmt.Sprintf("# last_hand_move_event_seq: %d\n", seq)
	state := &Status{HandEventSeq: seq}
	fingerprint := ""
	const fingerprintPrefix = "# hand_move_event_sha256: "
	if seq != 0 {
		var kind, id, payload string
		if err := r.QueryRow("SELECT ts,kind,object_id,payload FROM events WHERE seq=?", seq).Scan(&state.ChangedAtMS, &kind, &id, &payload); err != nil {
			return nil, "", err
		}
		// Event sequence numbers are local to one store. Bind the receipt to
		// the event's contents too, so a rebuilt store's low sequences are
		// never mistaken for moves recorded by the old store.
		fingerprint = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d\n%d\n%s\n%s\n%s", seq, state.ChangedAtMS, kind, id, payload))))
		first, rest, _ := strings.Cut(body, "\n")
		body = first + "\n" + fingerprintPrefix + fingerprint + "\n" + rest
	}
	recordedFingerprint := ""
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("reading order record with git %s: %w: %s", strings.Join(args, " "), err, out)
		}
		return string(out), nil
	}
	entry, err := git("ls-tree", "main", "--", cfg.Order.Record)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(entry) != "" {
		fields := strings.Fields(entry)
		if len(fields) < 4 || (fields[0] != "100644" && fields[0] != "100755") {
			// A directory, symlink or submodule is not a receipt. The loop
			// will refuse replacements it cannot safely make, not the read.
			state.Recorded = seq == 0
			return state, body, nil
		}
		record, err := git("show", "main:"+cfg.Order.Record)
		if err != nil {
			return nil, "", err
		}
		for _, line := range strings.Split(record, "\n") {
			if strings.HasPrefix(line, fingerprintPrefix) {
				recordedFingerprint = strings.TrimPrefix(line, fingerprintPrefix)
			}
		}
		// A hand-edited record is stale, not a failed status read.
		state.RecordedEventSeq, _ = receipt(record)
	}
	// No hand move after a rebuild means no pending record. A legacy record
	// without a fingerprint is refreshed once after the next hand move.
	state.Recorded = seq == 0 || (seq == state.RecordedEventSeq && fingerprint == recordedFingerprint)
	return state, body, nil
}
