//go:build !observatory

package main

import "io/fs"

// webAssets is empty without the `observatory` build tag: this binary serves the API and
// answers 404 for the UI, which is the honest answer when no front end was compiled into
// it. `--web-dist` points at a directory instead.
func webAssets() fs.FS {
	return nil
}
