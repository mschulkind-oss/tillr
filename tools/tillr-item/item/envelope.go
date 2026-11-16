package item

import (
	"encoding/json"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

// envelopeOf builds one record of this utility's, with the injected clock. Every face
// that answers a record builds it here, so the producer, the version and the clock
// normalization have one spelling rather than one per face.
func envelopeOf(kind, id string, at time.Time, payload any) (record.Envelope, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return record.Envelope{}, Internalf("encoding the %s record: %v", kind, err)
	}
	return record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          kind,
		ID:            id,
		Producer:      record.Producer{System: record.System, Utility: Utility, Version: Version},
		CreatedAt:     at.UTC(),
		Payload:       body,
	}, nil
}
