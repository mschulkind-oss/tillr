package drain

import "github.com/mschulkind-oss/tillr/buildstamp"

func init() {
	supportRevision = buildstamp.Read().SupportRevision
}
