package main

import (
	"os"

	"github.com/mschulkind-oss/tillr/packmirror"
)

func main() {
	os.Exit(packmirror.Main(os.Args[1:], os.Stdout, os.Stderr))
}
