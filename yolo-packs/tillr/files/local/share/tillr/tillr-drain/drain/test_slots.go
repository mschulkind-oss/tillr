package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	TestSlotsFreeWithWaitersKind = "drain.test-slots-free-with-waiters"
	TestSlotsClearedKind         = "drain.test-slots-cleared"
)

func init() {
	if err := event.Register(TestSlotsFreeWithWaitersKind, Utility); err != nil {
		panic(err)
	}
	if err := event.Register(TestSlotsClearedKind, Utility); err != nil {
		panic(err)
	}
}

// TestSlotsFreeWithWaitersFact is the payload of drain.test-slots-free-with-waiters.
type TestSlotsFreeWithWaitersFact struct {
	LockDir           string `json:"lock_dir"`
	FreeSlots         int    `json:"free_slots"`
	TotalSlots        int    `json:"total_slots"`
	EligibleFreeSlots int    `json:"eligible_free_slots"`
	Waiters           int    `json:"waiters"`
	Observations      int    `json:"observations"`
}

// TestSlotsClearedFact is the payload of drain.test-slots-cleared.
type TestSlotsClearedFact struct {
	LockDir   string `json:"lock_dir"`
	FreeSlots int    `json:"free_slots"`
	Waiters   int    `json:"waiters"`
}

// TestSlotsLimits configures active slot counts per pool. Zero uses the
// runner's default core-derived limits.
type TestSlotsLimits struct {
	Ordinary int
	Gate     int
	Proof    int
}

func defaultTestSlotsLimits(cpus int) TestSlotsLimits {
	cpus = max(1, cpus)
	ordinary := max(1, (cpus*3)/8)
	spareAfterOrdinary := max(0, cpus-ordinary)
	gate := min(spareAfterOrdinary, 6, (ordinary+1)/2)
	spareAfterGate := max(0, spareAfterOrdinary-gate)
	proof := min(spareAfterGate, 4, (ordinary+2)/3)
	return TestSlotsLimits{
		Ordinary: ordinary,
		Gate:     gate,
		Proof:    proof,
	}
}

func defaultTestSlotsLockDir() string {
	if override := os.Getenv("GO_TEST_SHARDS_LOCK_DIR"); override != "" {
		return override
	}
	if goCache := os.Getenv("GOCACHE"); goCache != "" && goCache != "off" {
		return filepath.Join(goCache, "go-test-shards-locks")
	}
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "go-build", "go-test-shards-locks")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".cache", "go-build", "go-test-shards-locks")
}

type testSlotsSnapshot struct {
	TotalSlots        int
	FreeSlots         int
	EligibleFreeSlots int
	Waiters           int
}

// TestSlotsObserver tracks consecutive observations of free Go test slots
// coexisting with live waiters and records episode transitions in the store.
type TestSlotsObserver struct {
	Store   string
	LockDir string
	Limits  TestSlotsLimits
	Now     func() time.Time

	mu       sync.Mutex
	loaded   map[string]bool
	standing map[string]bool
	streak   map[string]int
}

func (o *TestSlotsObserver) now() time.Time {
	if o.Now != nil {
		return o.Now().UTC()
	}
	return time.Now().UTC()
}

// Observe inspects the Go test slot pool once and appends an event when free
// eligible slots and live waiters coexist across consecutive observations, or
// clears a standing episode when the condition ends.
func (o *TestSlotsObserver) Observe(ctx context.Context) error {
	if o.Store == "" {
		return nil
	}
	lockDir := o.LockDir
	if lockDir == "" {
		lockDir = defaultTestSlotsLockDir()
	}
	if lockDir == "" {
		return nil
	}
	limits := o.Limits
	if limits == (TestSlotsLimits{}) {
		limits = defaultTestSlotsLimits(runtime.NumCPU())
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	if o.loaded == nil {
		o.loaded = map[string]bool{}
		o.standing = map[string]bool{}
		o.streak = map[string]int{}
	}
	if !o.loaded[lockDir] {
		standing, loaded, err := readStandingTestSlots(ctx, o.Store, lockDir)
		if err != nil {
			return err
		}
		if loaded {
			o.loaded[lockDir] = true
			o.standing[lockDir] = standing
		}
	}

	snap, err := inspectTestSlots(lockDir, limits)
	if err != nil {
		return err
	}

	if snap.Waiters > 0 && snap.EligibleFreeSlots > 0 {
		o.streak[lockDir]++
		if o.streak[lockDir] >= 2 && !o.standing[lockDir] {
			body, err := json.Marshal(TestSlotsFreeWithWaitersFact{
				LockDir:           lockDir,
				FreeSlots:         snap.FreeSlots,
				TotalSlots:        snap.TotalSlots,
				EligibleFreeSlots: snap.EligibleFreeSlots,
				Waiters:           snap.Waiters,
				Observations:      o.streak[lockDir],
			})
			if err != nil {
				return err
			}
			if err := appendTestSlotsEvent(ctx, o.Store, TestSlotsFreeWithWaitersKind, lockDir, body, o.now()); err != nil {
				return err
			}
			o.standing[lockDir] = true
			o.loaded[lockDir] = true
		}
		return nil
	}

	o.streak[lockDir] = 0
	if o.standing[lockDir] {
		body, err := json.Marshal(TestSlotsClearedFact{
			LockDir:   lockDir,
			FreeSlots: snap.FreeSlots,
			Waiters:   snap.Waiters,
		})
		if err != nil {
			return err
		}
		if err := appendTestSlotsEvent(ctx, o.Store, TestSlotsClearedKind, lockDir, body, o.now()); err != nil {
			return err
		}
		o.standing[lockDir] = false
	}
	return nil
}

func readStandingTestSlots(ctx context.Context, storePath, lockDir string) (standing, loaded bool, err error) {
	if _, statErr := os.Stat(storePath); os.IsNotExist(statErr) {
		return false, false, nil
	} else if statErr != nil {
		return false, false, statErr
	}
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return false, false, err
	}
	defer s.Close()
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var marker int
		qErr := r.QueryRow(`SELECT 1
FROM events
WHERE kind = ?
  AND coalesce(json_extract(payload, '$.lock_dir'), '') = ?
  AND seq > COALESCE((
    SELECT seq FROM events
    WHERE kind = ?
      AND coalesce(json_extract(payload, '$.lock_dir'), '') = ?
    ORDER BY seq DESC LIMIT 1
  ), 0)
ORDER BY seq DESC LIMIT 1`,
			TestSlotsFreeWithWaitersKind, lockDir,
			TestSlotsClearedKind, lockDir,
		).Scan(&marker)
		if errors.Is(qErr, sql.ErrNoRows) {
			return nil
		}
		if qErr != nil {
			return qErr
		}
		standing = true
		return nil
	})
	if err != nil {
		return false, false, err
	}
	return standing, true, nil
}

func appendTestSlotsEvent(ctx context.Context, storePath, kind, lockDir string, body []byte, at time.Time) error {
	s, err := store.Open(ctx, storePath, false)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       kind,
			ObjectType: "slot-pool",
			ObjectID:   &lockDir,
			Payload:    body,
			At:         at,
		})
		return err
	})
}

func (l *Loop) observeTestSlots(ctx context.Context) error {
	if l.nonLanding || l.cfg.Store == "" {
		return nil
	}
	l.testSlots.Store = l.cfg.Store
	l.testSlots.Now = l.cfg.Now
	return l.testSlots.Observe(ctx)
}
