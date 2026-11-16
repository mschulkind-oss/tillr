package land

import (
	"errors"
	"fmt"

	"github.com/mschulkind-oss/tillr/record"
)

// One constructor per exit code, so a face's exit is fixed where the error is made and
// not reconstructed at main. The constructors live in this package rather than in main
// because this is where the errors are made: a malformed candidate, a repository that
// cannot be read and a failed write are all decided here, and main only prints the
// diagnostic and answers the code.
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

// Externalf is the exit-4 constructor: an external dependency prevented a decision. A
// git command that could not run is this, not a refusal: a refusal is a decision the
// utility reached, and it exits 1 with a record saying why.
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
