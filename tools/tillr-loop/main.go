package main

import (
	"os"

	"github.com/mschulkind-oss/tillr-loop/loop"
	"github.com/mschulkind-oss/tillr/store"
)

func main() {
	defer store.FlushSQLiteTraceAtExit()
	code := loop.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	store.FlushSQLiteTraceAtExit()
	os.Exit(code)
}
