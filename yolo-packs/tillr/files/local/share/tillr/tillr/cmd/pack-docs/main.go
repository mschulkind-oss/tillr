package main

import (
	"os"

	"github.com/mschulkind-oss/tillr/packdocs"
)

func main() {
	os.Exit(packdocs.Main(os.Args[1:], os.Stdout, os.Stderr))
}
