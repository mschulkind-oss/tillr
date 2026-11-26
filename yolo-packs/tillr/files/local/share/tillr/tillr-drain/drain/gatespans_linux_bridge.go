//go:build linux

package drain

import "github.com/mschulkind-oss/tillr-drain/internal/gatespans"

type heavyProcess = gatespans.HeavyProcess

var readHeavyProcess = gatespans.ReadHeavyProcess
