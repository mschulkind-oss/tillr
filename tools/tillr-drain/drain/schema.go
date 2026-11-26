// Package drain is the drain utility's contract: the two record kinds it speaks, the
// tick it consumes and the decisions it answers with. It is a package rather than part
// of main because a main package cannot be imported, and the conformance case that pins
// what drain registers has to run in a test process.
package drain

import (
	// The blank imports are the ITEM-PKG precondition made structural. plan writes
	// nothing, but it reads items, lanes and runs, and a store drain opens must carry
	// those tables: the substrate composes its migration list from what was registered
	// at package load, so a store created in drain's process has item's and run's tables
	// only because their packages are linked here. The same imports register their
	// record→row mappings, which is what lets a fixture be seeded in drain's test
	// process. A face that remembered to import them would be a face that could forget.
	_ "github.com/mschulkind-oss/tillr-item/item"
	_ "github.com/mschulkind-oss/tillr-run/run"
)
