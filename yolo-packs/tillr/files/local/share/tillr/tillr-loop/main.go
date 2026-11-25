package main

import (
	"os"

	"github.com/mschulkind-oss/tillr-loop/loop"
)

func main() {
	os.Exit(loop.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
