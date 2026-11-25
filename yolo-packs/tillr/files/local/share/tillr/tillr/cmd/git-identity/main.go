package main

import (
	"os"

	"github.com/mschulkind-oss/tillr/gitidentity"
)

func main() {
	os.Exit(gitidentity.MainCheck(os.Args[1:], os.Stdout, os.Stderr))
}
