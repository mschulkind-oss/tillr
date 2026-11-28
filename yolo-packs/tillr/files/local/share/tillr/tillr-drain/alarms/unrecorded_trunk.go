package alarms

import "github.com/mschulkind-oss/tillr/event"

const (
	UnrecordedTrunkCommitKind  = "drain.unrecorded-trunk-commit"
	UnrecordedTrunkClearedKind = "drain.unrecorded-trunk-cleared"
)

func init() {
	if err := event.Register(UnrecordedTrunkCommitKind, "drain"); err != nil {
		panic(err)
	}
	if err := event.Register(UnrecordedTrunkClearedKind, "drain"); err != nil {
		panic(err)
	}
}
