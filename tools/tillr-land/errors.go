package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/record"
)

// The exit-code constructors live in the land package, because that is where the errors
// are made: a face's exit is fixed where the error is made, not reconstructed at main.
// This file holds the two things main does with them — print the diagnostic and answer
// the code — and the one classification main makes itself.

// exitCode answers the code an error was made with.
func exitCode(err error) int { return land.ExitCode(err) }

// fail prints one error's diagnostic and answers its exit code. A face that answered
// `--help` already wrote its flags to stdout, so it answers 0 and prints nothing.
func fail(err error) int {
	if errors.Is(err, errHelp) {
		return record.ExitOK
	}
	fmt.Fprintf(os.Stderr, "tillr-land: %v\n", err)
	return exitCode(err)
}

// openFailure is the exit for a store this process could not open at all. A missing
// store or one it may not read is the caller's mistake about the path, not a schema
// mismatch: exit 3 means "incompatible", and answering it for a typo sends the caller
// looking for a migration that is not the problem.
func openFailure(err error) error {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		return land.Usagef("%v", err)
	}
	return land.Incompatiblef("%v", err)
}
