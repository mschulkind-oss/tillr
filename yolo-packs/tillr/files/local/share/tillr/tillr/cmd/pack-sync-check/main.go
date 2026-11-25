package main

import (
	"os"

	"github.com/mschulkind-oss/tillr/packsync"
)

func main() {
	os.Exit(packsync.Main(os.Args[1:], os.Stdout, os.Stderr))
}
