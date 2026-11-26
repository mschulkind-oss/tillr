package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/preserved"
	"github.com/mschulkind-oss/tillr/event"
)

// ForkLeftoverKind records that a run ended leaving uncommitted work or
// unretained commits in its lane's fork checkout. Nothing is cleared
// automatically; the operator inspects, preserves, and resets by hand.
const ForkLeftoverKind = "drain.fork-leftover"

// ForkLeftover is drain.fork-leftover's payload.
type ForkLeftover struct {
	Checkout         string   `json:"checkout"`
	LaneID           string   `json:"lane_id"`
	RunID            string   `json:"run_id"`
	ItemID           string   `json:"item_id"`
	Ref              string   `json:"ref"`
	Holds            string   `json:"holds"`
	UncommittedFiles []string `json:"uncommitted_files,omitempty"`
	UncommittedCount int      `json:"uncommitted_count,omitempty"`
	UnretainedHead   string   `json:"unretained_head,omitempty"`
	DeliveryTip      string   `json:"delivery_tip,omitempty"`
}

const (
	maxLeftoverPayloadFiles = 20
	maxLeftoverHoldsFiles   = 5
)

func init() {
	if err := event.Register(ForkLeftoverKind, Utility); err != nil {
		panic(err)
	}
}

// reportForkCheckoutLeftover inspects the lane's fork checkout after a run's
// examination finishes and records one drain.fork-leftover event if the
// checkout still holds uncommitted work or unretained commits that staging
// would later refuse. It never modifies or clears the checkout.
func (l *Loop) reportForkCheckoutLeftover(ctx context.Context, d Decision, checkout, ref string, at time.Time) {
	runID := ""
	if d.RunRequest != nil {
		runID = d.RunRequest.RunID
	}
	leftover, err := l.inspectForkCheckoutLeftover(ctx, d.LaneID, runID, d.ItemID, checkout, ref)
	if err != nil {
		l.reportf("loop: checking fork checkout %s after %s: %v\n", checkout, runID, err)
		return
	}
	if leftover == nil {
		return
	}
	objectType, objectID := "run", runID
	if objectID == "" {
		objectType, objectID = "lane", d.LaneID
	}
	if err := appendJudgment(ctx, l.cfg.Store, ForkLeftoverKind, objectType, objectID, *leftover, at); err != nil {
		l.reportf("loop: recording %s for %s after %s: %v\n", ForkLeftoverKind, checkout, runID, err)
		return
	}
	l.reportf("loop: run %s left %s in fork checkout %s (lane %s); not cleared automatically\n",
		leftover.RunID, leftover.Holds, leftover.Checkout, leftover.LaneID)
}

// inspectForkCheckoutLeftover checks checkout without mutating it, matching the
// uncommitted-work and unretained-commit conditions that refreshForkCheckout
// refuses at staging.
func (l *Loop) inspectForkCheckoutLeftover(ctx context.Context, lane, runID, itemID, checkout, ref string) (*ForkLeftover, error) {
	absCheckout, err := filepath.Abs(checkout)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(absCheckout); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if err := l.checkForkRepository(ctx, absCheckout); err != nil {
		return nil, nil
	}

	status, err := l.git(ctx, absCheckout, "status", "--porcelain=v1", "-z")
	if err != nil {
		return nil, err
	}
	var uncommittedFiles []string
	var uncommittedCount int
	if status != "" {
		parsed := parsePorcelainZ(status)
		uncommittedCount = len(parsed)
		if uncommittedCount == 0 {
			uncommittedCount = 1
		}
		if len(parsed) > maxLeftoverPayloadFiles {
			uncommittedFiles = parsed[:maxLeftoverPayloadFiles]
		} else {
			uncommittedFiles = parsed
		}
	}

	unretainedHead, deliveryTip, err := l.checkForkUnretainedHead(ctx, absCheckout, ref)
	if err != nil {
		return nil, err
	}

	if uncommittedCount == 0 && unretainedHead == "" {
		return nil, nil
	}
	return &ForkLeftover{
		Checkout:         absCheckout,
		LaneID:           lane,
		RunID:            runID,
		ItemID:           itemID,
		Ref:              ref,
		Holds:            formatForkLeftoverHolds(uncommittedFiles, uncommittedCount, unretainedHead, deliveryTip),
		UncommittedFiles: uncommittedFiles,
		UncommittedCount: uncommittedCount,
		UnretainedHead:   unretainedHead,
		DeliveryTip:      deliveryTip,
	}, nil
}

// checkForkUnretainedHead answers (head, tip, nil) when checkout's HEAD holds
// commits that neither ref's tip, a local run/preserved ref, nor patch-history
// contains. It never fetches into or modifies checkout.
func (l *Loop) checkForkUnretainedHead(ctx context.Context, checkout, ref string) (string, string, error) {
	tipOut, err := l.git(ctx, l.cfg.Root, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", "", err
	}
	tip := strings.TrimSpace(tipOut)
	headOut, err := l.git(ctx, checkout, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	head := strings.TrimSpace(headOut)
	if head == tip {
		return "", "", nil
	}

	// Check whether head is already an ancestor of tip without mutating checkout.
	if _, err := l.git(ctx, checkout, "cat-file", "-e", tip+"^{commit}"); err == nil {
		undelivered, err := l.git(ctx, checkout, "rev-list", "--max-count=1", tip+".."+head)
		if err != nil {
			return "", "", err
		}
		if strings.TrimSpace(undelivered) == "" {
			return "", "", nil
		}
	} else if _, err := l.git(ctx, l.cfg.Root, "--no-replace-objects", "cat-file", "-e", head+"^{commit}"); err == nil {
		undelivered, err := l.git(ctx, l.cfg.Root, "--no-replace-objects", "rev-list", "--max-count=1", tip+".."+head)
		if err != nil {
			return "", "", err
		}
		if strings.TrimSpace(undelivered) == "" {
			return "", "", nil
		}
	}

	refs, err := l.git(ctx, checkout, "for-each-ref", "--contains", head, "--count=1", "--format=%(refname)",
		"refs/tillr/runs/", "refs/tillr/runs-long/", preserved.Prefix)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(refs) != "" {
		return "", "", nil
	}
	oldSeries, err := wasDeliveredForkHead(ctx, l.git, l.cfg.Root, ref, head)
	if err != nil {
		return "", "", err
	}
	if oldSeries {
		return "", "", nil
	}
	return head, tip, nil
}

func parsePorcelainZ(out string) []string {
	parts := strings.Split(out, "\x00")
	var paths []string
	for i := 0; i < len(parts); i++ {
		entry := parts[i]
		if len(entry) < 4 {
			continue
		}
		xy := entry[:2]
		path := entry[3:]
		if path != "" {
			paths = append(paths, path)
		}
		if strings.ContainsAny(xy, "RC") && i+1 < len(parts) {
			i++
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

func formatForkLeftoverHolds(files []string, totalFiles int, unretainedHead, deliveryTip string) string {
	var parts []string
	if totalFiles > 0 {
		switch {
		case len(files) == 0:
			parts = append(parts, "uncommitted work")
		case totalFiles == 1:
			parts = append(parts, fmt.Sprintf("uncommitted work (1 file: %s)", files[0]))
		case totalFiles <= maxLeftoverHoldsFiles && len(files) == totalFiles:
			parts = append(parts, fmt.Sprintf("uncommitted work (%d files: %s)", totalFiles, strings.Join(files, ", ")))
		default:
			shown := files
			if len(shown) > maxLeftoverHoldsFiles {
				shown = shown[:maxLeftoverHoldsFiles]
			}
			more := totalFiles - len(shown)
			parts = append(parts, fmt.Sprintf("uncommitted work (%d files: %s, and %d more)", totalFiles, strings.Join(shown, ", "), more))
		}
	}
	if unretainedHead != "" {
		parts = append(parts, fmt.Sprintf("unretained commits at %s (delivery tip %s)", shortLeftoverSHA(unretainedHead), shortLeftoverSHA(deliveryTip)))
	}
	return strings.Join(parts, " and ")
}

func shortLeftoverSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
