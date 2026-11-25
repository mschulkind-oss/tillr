package main

import (
	"os"

	"github.com/mschulkind-oss/tillr/tillrdocs"
)

func main() {
	os.Exit(tillrdocs.Main(os.Args[1:], os.Stdout, os.Stderr))
}
