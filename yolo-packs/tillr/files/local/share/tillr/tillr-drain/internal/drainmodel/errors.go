package drainmodel

import (
	"errors"
	"fmt"

	"github.com/mschulkind-oss/tillr-drain/fixer"
	"github.com/mschulkind-oss/tillr-drain/heavyrun"
	"github.com/mschulkind-oss/tillr/record"
)

func WrapFixerError(err error) error {
	if err == nil {
		return nil
	}
	var ce *fixer.CodeError
	if errors.As(err, &ce) {
		return &ExitError{Code: ce.Code, Msg: ce.Msg}
	}
	return err
}

// One constructor per exit code, so a face's exit is fixed where the error is made and
// not reconstructed at main. The constructors live in this package rather than in main
// because this is where the errors are made: a malformed tick, an unreadable policy and
// a failed read are all decided here, and main only prints the diagnostic and answers
// the code.
//
// There is no exit-1 constructor: plan always produces a decision for every item, and
// once's tick has no negative decision to make — a tick that dispatches nothing is a
// successful tick. A tick it cannot read is exit 2 and a store it cannot read is exit 3.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// Usagef is the exit-2 constructor: a usage error or malformed/unsupported input, with
// nothing written and no record on stdout.
func Usagef(format string, args ...any) error {
	return &ExitError{Code: record.ExitUsage, Msg: fmt.Sprintf(format, args...)}
}

// Incompatiblef is the exit-3 constructor: the store, its schema or the record version
// is not one this binary can work with.
func Incompatiblef(format string, args ...any) error {
	return &ExitError{Code: record.ExitIncompatible, Msg: fmt.Sprintf(format, args...)}
}

// Externalf is the exit-4 constructor: an external dependency could not be reached, which
// for this utility is the runner the face hands its run requests to. It is distinct from a
// negative decision — the face did not decide against the item, it could not hand the
// request over — and every write the tick made is rolled back.
func Externalf(format string, args ...any) error {
	return &ExitError{Code: record.ExitExternal, Msg: fmt.Sprintf(format, args...)}
}

// Internalf is the exit-5 constructor: a bug, not an outcome.
func Internalf(format string, args ...any) error {
	return &ExitError{Code: record.ExitInternal, Msg: fmt.Sprintf(format, args...)}
}

// ItemContentError is a refusal the item's own record caused: a value the brief cannot
// carry, or a field it does not have. It is a staging refusal and not a judgment about the
// work — nobody ran anything — so it is not a verdict either.
//
// It is the one class of staging refusal the loop holds per item instead of backing off.
// The distinction is the point: a template that cannot be rendered is the machine's fault
// and the loop waits it out, while a record whose title carries a `{{` will refuse the same
// way on every retry, and backing the whole queue off for it stalls work that is fine.
//
// The item and the field are carried beside the message so the refusal names what an
// operator has to edit — the record, not the renderer — without them having to read the
// renderer to find out. The embedded ExitError keeps the exit code the external class has
// for a face that answers only one item (`drain once`), where the refusal is still a
// failure.
type ItemContentError struct {
	*ExitError
	// ItemID is the item whose own record refused, and Field is the field by the name the
	// store's record uses (`title`, `body_ref`, `criteria`, `allowed_paths`, `item_id`).
	ItemID string
	Field  string
}

func (e *ItemContentError) Unwrap() error { return e.ExitError }

// ItemContentf is the item-content constructor: the record value named by field is the
// reason the brief cannot be rendered, so the refusal names the item and the field.
func ItemContentf(itemID, field, format string, args ...any) error {
	return &ItemContentError{
		ExitError: &ExitError{Code: record.ExitExternal, Msg: fmt.Sprintf(format, args...)},
		ItemID:    itemID,
		Field:     field,
	}
}

// IsItemContent answers whether err is a refusal the item's own content caused. It is what
// the loop classifies on: true is a per-item hold, false is the machinery's fault.
func IsItemContent(err error) bool {
	var e *ItemContentError
	return errors.As(err, &e)
}

// ExitCode answers the code an error was made with, and exit 5 for an error that
// carries none: an unclassified error is a bug, not an outcome.
func ExitCode(err error) int {
	var e *ExitError
	if errors.As(err, &e) {
		return e.Code
	}
	var hce *heavyrun.CodeError
	if errors.As(err, &hce) {
		return hce.Code
	}
	var fce *fixer.CodeError
	if errors.As(err, &fce) {
		return fce.Code
	}
	var sqliteErr interface{ Code() int }
	if errors.As(err, &sqliteErr) {
		if code := sqliteErr.Code() & 0xff; code == 5 || code == 15 {
			return record.ExitIncompatible
		}
	}
	return record.ExitInternal
}
