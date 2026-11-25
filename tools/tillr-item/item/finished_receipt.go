package item

import (
	"regexp"
	"slices"
	"strings"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

var commitPointerShape = regexp.MustCompile(`^[0-9a-fA-F]+$`)

// checkStateReceipt refuses arguments that this move cannot record. Explicit empty
// flags count as supplied too; omitted optional flags do not require a receipt.
func checkStateReceipt(r *store.Reader, d definition.Definition, id, from, to string, receipt MoveReceipt) error {
	entering := d.IsFinished(to)
	leaving := from != to && d.IsFinished(from)
	releasing := from == d.Blocked && to == d.Ready
	supplied := func(flag, value string) bool { return value != "" || slices.Contains(receipt.Flags, flag) }
	for _, f := range []struct {
		name, value string
		stored      bool
	}{
		{"reason", receipt.Reason, entering || leaving || to == d.Blocked || releasing},
		{"by", receipt.By, entering || leaving || releasing},
		{"resolves-via", receipt.ResolvesVia, entering},
		// Internal callers may carry waiting metadata through an unblock. Only
		// an explicitly supplied CLI --waiter is an unused flag on that move.
		{"waiter", "", to == d.Blocked},
	} {
		if supplied(f.name, f.value) && !f.stored {
			return Rejectionf("flag-not-stored", id, "--%s is not stored on a move from %q to %q", f.name, from, to)
		}
	}
	// A same-state retry does not replace or validate a replacement receipt.
	if from == to || !supplied("resolves-via", receipt.ResolvesVia) {
		return nil
	}
	kind, value, ok := strings.Cut(receipt.ResolvesVia, ":")
	if !ok || strings.TrimSpace(value) == "" {
		return Usagef("--resolves-via needs item:<id>, commit:<sha>, or doc:<path>")
	}
	switch kind {
	case "item":
		if value == id {
			return Rejectionf("pointer-unknown", id, "--resolves-via must name another item")
		}
		var exists bool
		if err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM items WHERE item_id = ?)`, value).Scan(&exists); err != nil {
			return Internalf("checking resolution pointer: %v", err)
		}
		if !exists {
			return Rejectionf("pointer-unknown", id, "--resolves-via names item %q, which this store does not hold", value)
		}
	case "commit":
		if !commitPointerShape.MatchString(value) {
			return Usagef("--resolves-via commit pointer needs a hexadecimal SHA")
		}
	case "doc":
		// Document and commit pointers are evidence references, not local existence probes.
	default:
		return Usagef("--resolves-via needs item:<id>, commit:<sha>, or doc:<path>")
	}
	return nil
}
