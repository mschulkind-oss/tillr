//go:build linux

package drain

import "github.com/mschulkind-oss/tillr-drain/heavyrun"

type heavyProcess = heavyrun.HeavyProcess

var readHeavyProcess = heavyrun.ReadHeavyProcess
