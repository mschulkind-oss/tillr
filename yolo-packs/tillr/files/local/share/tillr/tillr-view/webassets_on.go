//go:build observatory

package main

import (
	"embed"
	"io/fs"
)

// The built front end, embedded so `just observatory` starts one process with no Node
// runtime to view a drain (design §5.3). The tag exists because web/dist is a build
// output: embedding it unconditionally would make `go build ./...` and every Go test in
// this module depend on `npm run build` having run first, which is exactly the gate cost
// the path-scoped frontend check exists to avoid.
//
//go:embed all:web/dist
var embeddedWeb embed.FS

func webAssets() fs.FS {
	sub, err := fs.Sub(embeddedWeb, "web/dist")
	if err != nil {
		// The pattern above guarantees the directory exists; a failure here is a
		// build-time impossibility, not a runtime condition to render.
		return nil
	}
	return sub
}
