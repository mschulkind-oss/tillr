// Architecture and invariants: docs/reference/tillr-drain-successor.md
package storelog

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr/record"
)

// WriteLoopResultRecord encodes and writes one loop result envelope to
// drainmodel.LoopResultFile(root, firstGeneration, lastGeneration), archiving any prior
// zero-generation file and reporting I/O errors to report without failing the caller.
func WriteLoopResultRecord(root, firstGeneration, lastGeneration string, at time.Time, result any, report io.Writer) {
	if report == nil {
		report = io.Discard
	}
	path := drainmodel.LoopResultFile(root, firstGeneration, lastGeneration)
	payload, err := json.Marshal(result)
	if err != nil {
		reportRecordFailure(report, path, drainmodel.Internalf("encoding the loop's result: %v", err))
		return
	}
	env := record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          drainmodel.LoopResultKind,
		ID:            "loop-" + strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "drain-"), ".json"),
		Producer:      record.Producer{System: record.System, Utility: drainmodel.Utility, Version: drainmodel.Version},
		CreatedAt:     at,
		Payload:       payload,
	}
	body, err := record.Encode(env)
	if err != nil {
		reportRecordFailure(report, path, drainmodel.Internalf("encoding the loop's result record: %v", err))
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		reportRecordFailure(report, path, drainmodel.Externalf("creating %s: %v", filepath.Dir(path), err))
		return
	}
	if firstGeneration == "" && lastGeneration == "" {
		if prior, err := os.ReadFile(path); err == nil {
			archive := filepath.Join(filepath.Dir(path), fmt.Sprintf("drain-0-0-%d.json", at.UnixNano()))
			if err := os.WriteFile(archive, prior, 0o644); err != nil {
				reportRecordFailure(report, archive, drainmodel.Externalf("archiving %s: %v", path, err))
			}
		} else if !os.IsNotExist(err) {
			reportRecordFailure(report, path, drainmodel.Externalf("reading %s: %v", path, err))
		}
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		reportRecordFailure(report, path, drainmodel.Externalf("writing %s: %v", path, err))
	}
}

func reportRecordFailure(report io.Writer, path string, err error) {
	fmt.Fprintf(report, "loop: the loop's result was not recorded at %s: %v\n", path, err)
}
