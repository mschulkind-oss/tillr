package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// adapterPauseKind answers the pause kind req's adapter declares in the pause-kind file
// beside it, or "" when it declares none. A relative adapter path names the repository's
// file, as the backend policy spells it.
// The only error is a gathered write's miss (gathered.go); an unreadable file declares none.
func adapterPauseKind(ctx context.Context, root string, req *run.Request) (string, error) {
	if req == nil || len(req.Command) == 0 {
		return "", nil
	}
	adapter := req.Command[0]
	if !filepath.IsAbs(adapter) {
		adapter = filepath.Join(root, adapter)
	}
	path := filepath.Join(filepath.Dir(adapter), "pause-kind")
	raw, err := gather(ctx, "read "+path, func(context.Context) (string, error) {
		raw, err := os.ReadFile(path)
		return string(raw), err
	})
	if isGatherMiss(err) {
		return "", err
	}
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(raw), nil
}

// recordResumedBuild copies the last dispatch fact, changing only its build hash.
// A legacy run with no fact has no historical inputs to copy.
func (l *Loop) recordResumedBuild(ctx context.Context, req run.Request) error {
	hash, err := l.build.Hash()
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		facts, err := readInputDecisions(tx.Reader())
		if err != nil {
			return err
		}
		d := facts[req.RunID]
		inputs := RunInputs{}
		if d.Inputs != nil {
			inputs = *d.Inputs
		}
		inputs.TillrBuild = hash
		d.Inputs = &inputs
		return appendEffect(tx, inputRecordedKind, req.RunID, d, l.cfg.Now())
	})
}
