package drain

import (
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestConflictSingleDesignBackendCannotFallOntoBuild(t *testing.T) {
	for _, listed := range []bool{false, true} {
		t.Run(map[bool]string{false: "string", true: "one-element-list"}[listed], func(t *testing.T) {
			f, l := designQuotaFixture(t)
			if listed {
				l.cfg.Policy.SeatLists = map[string][]string{"design": {"fixture"}}
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`INSERT INTO backend_windows VALUES ('fixture', 'weekly', 1, 'rejected', ?, 'refused')`, l.cfg.Now().Add(time.Hour).UnixMilli())
				return err
			})
			d := mustPlan(t, l)[0].decision
			if d.Action != ActionHold || d.Reason != ReasonBackendWindow || d.RunRequest != nil {
				t.Fatalf("single shut design backend escaped to the build backend: %+v", d)
			}
		})
	}
}
