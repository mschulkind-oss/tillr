// Package record holds the envelope every tillr record carries and the exit-code
// contract its utilities answer with.
package record

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// System is the producer system name every record of this family carries. A record
// naming another system is not this system's record.
const System = "tillr"

// SupportedVersion is the record format version this package reads and writes.
const SupportedVersion = 1

// ErrUnsupportedVersion is returned for a record whose record_version this package
// does not know. It is a named error so a caller refuses rather than retries.
var ErrUnsupportedVersion = errors.New("record: unsupported record version")

// ErrForeignSystem is returned for a record whose producer names another system.
var ErrForeignSystem = errors.New("record: record from another system")

// Envelope is the common shape of every record: who produced it, what it is about,
// and the kind-specific payload.
type Envelope struct {
	RecordVersion  int             `json:"record_version"`
	Kind           string          `json:"kind"`
	ID             string          `json:"id"`
	Producer       Producer        `json:"producer"`
	CreatedAt      time.Time       `json:"created_at"`
	Subject        *Subject        `json:"subject,omitempty"`
	CorrelationID  string          `json:"correlation_id,omitempty"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	Payload        json.RawMessage `json:"payload"`
}

// Producer names who emitted a record.
type Producer struct {
	System  string `json:"system"`
	Utility string `json:"utility"`
	Version string `json:"version"`
}

// Subject names the thing a record is about, where it is about one.
type Subject struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Decode reads one record, refusing a version it does not know and a record from
// another system.
func Decode(raw []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return Envelope{}, fmt.Errorf("record: decoding envelope: %w", err)
	}
	if env.RecordVersion != SupportedVersion {
		return Envelope{}, fmt.Errorf("%w: %d", ErrUnsupportedVersion, env.RecordVersion)
	}
	if env.Producer.System != System {
		return Envelope{}, fmt.Errorf("%w: %q", ErrForeignSystem, env.Producer.System)
	}
	return env, nil
}

// Encode writes one record as JSON, with created_at normalized to UTC so the same
// record encodes identically wherever it is produced.
func Encode(env Envelope) ([]byte, error) {
	env.CreatedAt = env.CreatedAt.UTC()
	raw, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("record: encoding envelope: %w", err)
	}
	return raw, nil
}
