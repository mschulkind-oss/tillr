package main

import (
	"errors"
	"fmt"

	"github.com/mschulkind-oss/tillr/record"
)

// exitError is an error that already knows which exit code it answers with. A face's
// exit is fixed where the error is made, not at `main`: a caller reading the code and
// the message together cannot find them disagreeing.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }

func (e *exitError) Unwrap() error { return e.err }

// usagef is a usage error or a malformed input record: exit 2. The face made no
// decision, so it emits no record.
func usagef(format string, args ...any) error {
	return &exitError{code: record.ExitUsage, err: fmt.Errorf(format, args...)}
}

// incompatiblef is an input this face cannot read: exit 3. A registry whose digest does
// not match its declarations is this, not a usage error -- the caller's invocation was
// well formed and the file is the thing that is wrong.
func incompatiblef(format string, args ...any) error {
	return &exitError{code: record.ExitIncompatible, err: fmt.Errorf(format, args...)}
}

// externalf is a check that could not be run: exit 4. It is never a failed check -- a
// candidate that was not judged must not be read as a candidate that was rejected.
func externalf(format string, args ...any) error {
	return &exitError{code: record.ExitExternal, err: fmt.Errorf(format, args...)}
}

// internalf is a bug rather than an outcome: exit 5.
func internalf(format string, args ...any) error {
	return &exitError{code: record.ExitInternal, err: fmt.Errorf(format, args...)}
}

// exitCode is the code an error answers with. An error that named none is a bug, so it
// is exit 5 rather than a code a caller could read as a verdict.
func exitCode(err error) int {
	var named *exitError
	if errors.As(err, &named) {
		return named.code
	}
	return record.ExitInternal
}
