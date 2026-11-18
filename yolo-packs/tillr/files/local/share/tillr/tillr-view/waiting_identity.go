package main

import (
	"fmt"

	"github.com/mschulkind-oss/tillr-land/identityhistory"
	"github.com/mschulkind-oss/tillr/store"
)

func waitingPublicationHolds(r *store.Reader, storePath string) ([]waitingRow, error) {
	rows, err := waitingPatchMoves(r, storePath)
	if err != nil {
		return nil, err
	}
	history, err := identityhistory.ReadIdentityHistory(r)
	if err != nil {
		return nil, err
	}
	for _, commit := range history.Pending {
		rows = append(rows, waitingRow{
			Observation: "trunk identity " + commit.SHA,
			Class:       "needs you", Where: "trunk", ClassSource: "store",
			Why:   fmt.Sprintf("%s: %s author %s; committer %s", identityhistory.ReasonForeignIdentity, commit.SHA, commit.Author, commit.Committer),
			Mover: "re-author the commit, or record its id with tillr-drain accept-identity",
		})
	}
	return rows, nil
}
