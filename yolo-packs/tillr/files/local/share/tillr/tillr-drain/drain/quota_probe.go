package drain

import (
	"context"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// A continuation or restart tests its recorded backend, not the seat's choices.
func (l *Loop) recordedBackendOpen(ctx context.Context, backend string) (bool, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, err
	}
	defer s.Close()
	open := false
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		shut, err := readQuotaShutBackends(r, l.cfg.Now(), l.currentPolicy())
		open = err == nil && backend != "" && !shut[backend]
		return err
	})
	return open, err
}

// readQuotaShutBackends is every backend a dispatch must skip: shut by quota or
// closed by its latest infrastructure ending.
func readQuotaShutBackends(r *store.Reader, at time.Time, policy *Policy) (map[string]bool, error) {
	return readQuotaShutBackendsFrom(r, at, policy, nil)
}

func readQuotaShutBackendsFrom(r *store.Reader, at time.Time, policy *Policy, byRun map[string]*pendingWork) (map[string]bool, error) {
	shut, err := readQuotaOnlyShut(r, at, policy)
	if err != nil {
		return nil, err
	}
	closed, err := readInfrastructureClosedFrom(r, byRun)
	if err != nil {
		return nil, err
	}
	for name := range closed {
		shut[name] = true
	}
	return shut, nil
}

// readDispatchShut is the shut set a claim re-reads. A retry the owner authorized
// is the one dispatch to a closed backend, so it is refused by quota only.
func readDispatchShut(r *store.Reader, at time.Time, policy *Policy, d Decision) (map[string]bool, error) {
	return readDispatchShutFrom(r, at, policy, d, nil)
}

func readDispatchShutFrom(r *store.Reader, at time.Time, policy *Policy, d Decision, byRun map[string]*pendingWork) (map[string]bool, error) {
	if d.Action == ActionRetry {
		return readQuotaOnlyShut(r, at, policy)
	}
	return readQuotaShutBackendsFrom(r, at, policy, byRun)
}
