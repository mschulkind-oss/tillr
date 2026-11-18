package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/patchmoves"
	"github.com/mschulkind-oss/tillr/store"
)

func waitingPatchMoves(r *store.Reader, storePath string) ([]waitingRow, error) {
	holds, err := patchmoves.Pending(r)
	if isMissingTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]waitingRow, 0, len(holds))
	for _, h := range holds {
		out = append(out, waitingRow{
			Observation: fmt.Sprintf("patch/%s observation %d", h.Fork(), h.Observation),
			Class:       "needs you", Where: "publication", ClassSource: "store",
			Why:   fmt.Sprintf("patch/%s observed by run %s: %s -> %s (observation %d); publication held", h.Fork(), strings.Join(h.Runs, ", "), endpoint(h.Old), endpoint(h.New), h.Observation),
			Mover: h.Remedy(storePath), Since: time.UnixMilli(h.AtMS).UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

func endpoint(sha string) string {
	if sha == "" {
		return "absent"
	}
	return sha
}
