package main

import (
	"context"
	"fmt"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func orderFace(args []string) int {
	fs := newFlagSet("order")
	path := fs.String("store", "", "the store whose order is read")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *path == "" {
		return fail(item.Usagef("order needs --store <path>"))
	}
	ctx := context.Background()
	s, err := store.OpenRead(ctx, *path)
	if err != nil {
		return fail(openFailure(err))
	}
	defer s.Close()
	var text string
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		text, err = item.OrderRecord(r)
		return err
	}); err != nil {
		return fail(err)
	}
	fmt.Print(text)
	return record.ExitOK
}
