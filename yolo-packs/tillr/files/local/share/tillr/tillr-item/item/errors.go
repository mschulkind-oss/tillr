package item

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

// One constructor per exit code, so a face's exit is fixed where the error is made
// and not reconstructed at main. A face returns the error; main prints it and answers
// the code the constructor chose. The constructors live in this package rather than in
// main because this is where the errors are made: a malformed record, a refused
// transition and a failed write are all decided here.

// ExitError is an error that carries the exit code it answers with and, for a negative
// decision, the rule the rejection record names and the item it is about. The rule is
// the discriminator: only Rejectionf sets one, so a face can tell "the answer is no"
// from "I could not answer" without a second type.
type ExitError struct {
	Code   int
	Rule   string
	ItemID string
	Msg    string
}

func (e *ExitError) Error() string { return e.Msg }

// Usagef is the exit-2 constructor: a usage error or malformed input, with nothing
// written and no record on stdout.
func Usagef(format string, args ...any) error {
	return &ExitError{Code: record.ExitUsage, Msg: fmt.Sprintf(format, args...)}
}

// Rejectionf is the exit-1 constructor: the utility ran and the answer is no. It names
// the rule the rejection record carries and the item the record is about, so the face
// that prints the record re-derives neither.
func Rejectionf(rule, itemID, format string, args ...any) error {
	return &ExitError{
		Code:   record.ExitNegative,
		Rule:   rule,
		ItemID: itemID,
		Msg:    fmt.Sprintf(format, args...),
	}
}

// Incompatiblef is the exit-3 constructor: the store, its schema or the record version
// is not one this binary can work with.
func Incompatiblef(format string, args ...any) error {
	return &ExitError{Code: record.ExitIncompatible, Msg: fmt.Sprintf(format, args...)}
}

// Externalf is the exit-4 constructor: something outside this utility that it needs did
// not answer — `git`, for the sync face that reads which documents are tracked.
func Externalf(format string, args ...any) error {
	return &ExitError{Code: record.ExitExternal, Msg: fmt.Sprintf(format, args...)}
}

// Internalf is the exit-5 constructor: a bug, not an outcome.
func Internalf(format string, args ...any) error {
	return &ExitError{Code: record.ExitInternal, Msg: fmt.Sprintf(format, args...)}
}

// ExitCode answers the code an error was made with, and exit 5 for an error that
// carries none: an unclassified error is a bug, not an outcome.
func ExitCode(err error) int {
	var e *ExitError
	if errors.As(err, &e) {
		return e.Code
	}
	return record.ExitInternal
}

// RejectionOf answers the error as a rejection, and false for an error that is not a
// negative decision.
func RejectionOf(err error) (*ExitError, bool) {
	var e *ExitError
	if errors.As(err, &e) && e.Rule != "" {
		return e, true
	}
	return nil, false
}

// RejectionEnvelope is the record a negative decision carries: the rule it refused
// under, the item it is about, and the reason in prose.
func RejectionEnvelope(rej *ExitError, at time.Time) record.Envelope {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]string{
		"item_id": rej.ItemID,
		"rule":    rej.Rule,
		"reason":  rej.Msg,
	}); err != nil {
		// The payload is a map of strings this file builds, so an encoding failure is a
		// bug rather than an outcome.
		panic(err)
	}
	body := bytes.TrimSuffix(buf.Bytes(), []byte{'\n'})
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		panic(err)
	}
	id := fmt.Sprintf("rejection-%x", suffix)
	if rej.ItemID != "" {
		id = fmt.Sprintf("rejection-%s-%x", rej.ItemID, suffix)
	}
	return record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          RejectionKind,
		ID:            id,
		Producer:      record.Producer{System: record.System, Utility: Utility, Version: Version},
		CreatedAt:     at.UTC(),
		Payload:       body,
	}
}
