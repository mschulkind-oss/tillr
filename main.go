// Thin wrapper so go-to-wheel (PyPI packaging) can build from the module root.
// The real entry point is cmd/tillr/main.go.
package main

import "github.com/mschulkind-oss/tillr/internal/cli"

func main() {
	cli.Execute()
}
