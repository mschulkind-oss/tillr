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
	readConns           int
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

// WithReadConnections lets a read-only handle (OpenRead) hold up to n connections, so reads
// overlap. SQLite's WAL gives each connection its own snapshot and never blocks a reader on
// a writer. Every answer still runs inside one ReadTx, which is one transaction on one
// connection, so it describes one moment whatever else is read beside it. The default is one
// connection, which is what every short-lived face wants. The option does not apply to a
// writable handle, whose single connection serializes the append-only triggers and the
// migration.
func WithReadConnections(n int) OpenOption {
	return func(c *openConfig) error {
		if n < 1 {
			return fmt.Errorf("store: a handle needs at least one read connection, got %d", n)
		}
		c.readConns = n
		return nil
	}
}

func storeOpenConfig(options []OpenOption) (openConfig, error) {
	c := openConfig{busyTimeout: DefaultBusyTimeout, readConns: 1}
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
