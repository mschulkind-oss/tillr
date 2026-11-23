package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"

	"github.com/mschulkind-oss/tillr/store"
)

// A client branches on `error.code`, so the codes are this API's closed vocabulary and
// they live here, once, named as constants. Each names a condition with its own remedy
// rather than a class of failures: a store that is not there and a store newer than this
// binary are two different answers to an operator, and one `store_unreadable` for both is
// how a missing file gets diagnosed as a schema problem.
//
// The spelling is kebab-case: a code travels in URLs and log lines as well as in the JSON,
// and none of them is a payload field, whose names here are snake_case.
const (
	codeWordsEmpty           = "words-empty"
	codeWordsNotText         = "words-not-text"
	codeWordsTooLarge        = "words-too-large"
	codeIntakeNotConfigured  = "intake-not-configured"
	codeIntakeStoreFailed    = "intake-store-failed"
	codeReadOnly             = "read-only"
	codeOriginRefused        = "origin-refused"
	codeContentType          = "content-type"
	codeTooLarge             = "too-large"
	codeBadRequest           = "bad-request"
	codeUnknownItem          = "unknown-item"
	codeNotInOrder           = "not-in-order"
	codeStoreBusy            = "store-busy"
	codeWriterUnavailable    = "writer-unavailable"
	codeStoreAbsent          = "store-absent"
	codeStoreUnreadable      = "store-unreadable"
	codeSchemaTooNew         = "schema-too-new"
	codeSchemaUninitialized  = "schema-uninitialized"
	codeTableAbsent          = "table-absent"
	codeItemNotFound         = "item-not-found"
	codeRunNotFound          = "run-not-found"
	codeLandingNotFound      = "landing-not-found"
	codeCheckNotFound        = "check-not-found"
	codeEntryNotFound        = "entry-not-found"
	codeBadQuery             = "bad-query"
	codeNoDesignDoc          = "no-design-doc"
	codeDesignDocMissing     = "design-doc-missing"
	codeDesignDocOutsideRoot = "design-doc-outside-root"
	codeDesignDocUnreadable  = "design-doc-unreadable"
	codeUnknownKind          = "unknown-kind"
	codeUnknownRoute         = "unknown-route"
	codeUnknownTable         = "unknown-table"
	codeRowNotFound          = "row-not-found"
	// The planner (`tillr-drain why`) is a separate program. Not finding it, or finding one
	// of another build, is neither a store condition nor a statement about the drain.
	codePlannerUnavailable = "planner-unavailable"
	codePlannerMismatch    = "planner-mismatch"
)

// apiCode is one code and the status it answers with.
type apiCode struct {
	Code   string
	Status int
}

// apiCodes is the whole vocabulary with its statuses. Every code above appears here
// exactly once (TestEveryErrorCodeHasAStatus), so a code cannot be minted without deciding
// what it answers:
//
//	503  the store this face serves cannot answer at all — absent, unreadable, or a schema
//	     this binary does not read. The condition is about the store, not the request, and
//	     the same request will work once the store does.
//	404  the route or the subject is not here.
//	500  the store answered, and something it points at could not be read.
//	400  the request named something outside this face's closed set.
var apiCodes = []apiCode{
	{codeWordsEmpty, http.StatusUnprocessableEntity},
	{codeWordsNotText, http.StatusUnprocessableEntity},
	{codeWordsTooLarge, http.StatusUnprocessableEntity},
	{codeIntakeNotConfigured, http.StatusConflict},
	{codeIntakeStoreFailed, http.StatusInternalServerError},
	{codeReadOnly, http.StatusMethodNotAllowed},
	{codeOriginRefused, http.StatusForbidden},
	{codeContentType, http.StatusUnsupportedMediaType},
	{codeTooLarge, http.StatusRequestEntityTooLarge},
	{codeBadRequest, http.StatusBadRequest},
	{codeUnknownItem, http.StatusNotFound},
	{codeNotInOrder, http.StatusConflict},
	{codeStoreBusy, http.StatusServiceUnavailable},
	{codeWriterUnavailable, http.StatusServiceUnavailable},
	{codeStoreAbsent, http.StatusServiceUnavailable},
	{codeStoreUnreadable, http.StatusServiceUnavailable},
	{codeSchemaTooNew, http.StatusServiceUnavailable},
	{codeSchemaUninitialized, http.StatusServiceUnavailable},
	{codeTableAbsent, http.StatusServiceUnavailable},
	{codeItemNotFound, http.StatusNotFound},
	{codeRunNotFound, http.StatusNotFound},
	{codeLandingNotFound, http.StatusNotFound},
	{codeCheckNotFound, http.StatusNotFound},
	{codeEntryNotFound, http.StatusNotFound},
	{codeBadQuery, http.StatusBadRequest},
	{codeNoDesignDoc, http.StatusNotFound},
	{codeDesignDocMissing, http.StatusNotFound},
	{codeDesignDocOutsideRoot, http.StatusInternalServerError},
	{codeDesignDocUnreadable, http.StatusInternalServerError},
	{codeUnknownKind, http.StatusBadRequest},
	{codeUnknownRoute, http.StatusNotFound},
	{codeUnknownTable, http.StatusNotFound},
	{codeRowNotFound, http.StatusNotFound},
	{codePlannerUnavailable, http.StatusServiceUnavailable},
	{codePlannerMismatch, http.StatusServiceUnavailable},
}

// statusFor answers the status one code pairs with. A code outside apiCodes is a bug in
// this package rather than a caller's mistake, and 500 is what a bug answers.
func statusFor(code string) int {
	for _, c := range apiCodes {
		if c.Code == code {
			return c.Status
		}
	}
	return http.StatusInternalServerError
}

// failure builds one refusal. The message is one sentence a human can act on: it names
// what was looked for and where, because "not found" alone leaves the operator nothing to
// change.
func failure(code, format string, args ...any) *apiFailure {
	return &apiFailure{status: statusFor(code),
		Error: apiErrorBody{Code: code, Message: fmt.Sprintf(format, args...)}}
}

// itemNotFound and runNotFound are the two subjects a route names by id. They are separate
// constructors rather than one taking a kind string, so a route cannot answer the wrong
// code for the subject it looked up.
func itemNotFound(itemID string) *apiFailure {
	return failure(codeItemNotFound,
		"the store carries no item %q: read /api/v1/snapshot for the items it does carry", itemID)
}

func runNotFound(runID string) *apiFailure {
	return failure(codeRunNotFound,
		"the store carries no run %q: read /api/v1/snapshot for the runs it does carry", runID)
}

// storeVerdict is the store's condition when it is not one this face can read: the code a
// client branches on, the state a client renders, and the sentence an operator acts on. A
// readable store has none.
type storeVerdict struct {
	code  string
	state string
	note  string
}

// storeVerdictFor names the condition a failed open answered. A missing file and a file
// this process may not read are different remedies, so they are different codes; anything
// else is the store being unreadable.
func storeVerdictFor(path string, err error) *storeVerdict {
	if errors.Is(err, fs.ErrNotExist) {
		return &storeVerdict{code: codeStoreAbsent, state: "absent",
			note: fmt.Sprintf("%s is not there: name an existing store with --store, or create one with the utilities that write it", path)}
	}
	if store.NeedsRecovery(err) {
		return &storeVerdict{code: codeStoreUnreadable, state: stateUnreadable,
			note: fmt.Sprintf("%s carries an uncommitted writer's transaction (SQLITE_READONLY_ROLLBACK, 776): run tillr-store recover --store %q, then Read it again; this face never writes the store", path, path)}
	}
	return &storeVerdict{code: codeStoreUnreadable, state: stateUnreadable, note: err.Error()}
}

// schemaVerdict names the condition a store's schema version is in. Version 0 is no
// migration row at all — whatever file this is, it is not a store these utilities created —
// and a version this binary does not know is a store a later writer owns, which a read-only
// face cannot migrate forward to read.
func schemaVerdict(version int) *storeVerdict {
	if version == 0 {
		return &storeVerdict{code: codeSchemaUninitialized, state: "schema_uninitialized",
			note: "this store has applied no migration: it is not a store these utilities created, and an empty board would be an answer nobody measured"}
	}
	return &storeVerdict{code: codeSchemaTooNew, state: "schema_too_new",
		note: fmt.Sprintf("this store's schema is version %d and this binary reads up to %d: a read-only face cannot migrate it forward",
			version, store.SchemaVersion)}
}
