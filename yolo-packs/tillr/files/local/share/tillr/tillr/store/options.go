package store

import (
	"fmt"
	"time"
)

// DefaultBusyTimeout is how long SQLite waits for another connection's lock.
const DefaultBusyTimeout = 5 * time.Second

type openConfig struct {
	busyTimeout         time.Duration
	preserveJournalMode bool
}

// OpenOption configures one store handle, including its read-only sidecar.
type OpenOption func(*openConfig) error

// WithBusyTimeout sets SQLite's lock wait for this open. Zero disables waiting;
// positive values must be whole milliseconds, SQLite's unit for this setting.
func WithBusyTimeout(wait time.Duration) OpenOption {
	return func(c *openConfig) error {
		if wait < 0 || wait%time.Millisecond != 0 {
			return fmt.Errorf("store: busy timeout must be a non-negative whole number of milliseconds, got %s", wait)
		}
		c.busyTimeout = wait
		return nil
	}
}

func storeOpenConfig(options []OpenOption) (openConfig, error) {
	c := openConfig{busyTimeout: DefaultBusyTimeout}
	for _, option := range options {
		if option == nil {
			return c, fmt.Errorf("store: a nil open option is not valid")
		}
		if err := option(&c); err != nil {
			return c, err
		}
	}
	return c, nil
}
