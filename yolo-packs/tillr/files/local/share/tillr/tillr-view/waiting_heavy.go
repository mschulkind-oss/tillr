package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
)

func heavyProofWaitingRow(proof heavy.HeavyStatus, now time.Time) waitingRow {
	sha := proof.GreenCommit
	if len(sha) > 12 {
		sha = sha[:12]
	}
	age := heavy.ProofAge(proof, now)
	label := "proved " + age + " ago"
	if age == "unknown" {
		label = "proof age unknown"
	}
	return waitingRow{ItemID: "trunk-proof", Class: "waits on work", Where: "trunk", Why: fmt.Sprintf("green commit %s; %s; heavy=%s", sha, label, proof.Result), Mover: "the heavy runner; prove the current tip", ClassSource: "rule"}
}

func heavyWaitingRows(ctx context.Context, root, storePath string, rows []waitingRow) ([]waitingRow, error) {
	// Record-only fixtures and nonrepository roots have no Git proof surface.
	if _, err := os.Stat(filepath.Join(root, ".git")); os.IsNotExist(err) {
		return rows, nil
	} else if err != nil {
		return nil, err
	}
	green, err := heavy.Green(ctx, root)
	if err == nil && green == "" {
		// No proof yet is not a broken proof reader, including repositories
		// that have not created a trunk branch.
		return rows, nil
	}
	var proof heavy.HeavyStatus
	if err == nil {
		proof, err = heavy.ReadStatus(ctx, root, storePath)
	}
	if err != nil {
		return append(rows, waitingRow{ItemID: "trunk-proof", Class: "needs you", Where: "trunk", Why: "heavy proof unavailable: " + err.Error(), Mover: "the owner; repair the proof reader", ClassSource: "rule"}), nil
	}
	if proof.GreenCommit != "" {
		rows = append(rows, heavyProofWaitingRow(proof, time.Now()))
	}
	return rows, nil
}
