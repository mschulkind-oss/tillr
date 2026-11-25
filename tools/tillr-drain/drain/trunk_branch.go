package drain

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"

	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

const trunkBranchMetaKey = "trunk.branch"

func trunkBranchFor(root string) string {
	if root == "" {
		return settings.DefaultTrunkBranch
	}
	s, err := settings.Read(root)
	if err != nil {
		return settings.DefaultTrunkBranch
	}
	return s.TrunkBranch()
}

func (l *Loop) trunkRef() string {
	if l != nil {
		if l.cfg.TrunkBranch != "" {
			return l.cfg.TrunkBranch
		}
		if l.cfg.PostLanding.Trunk.Branch != "" {
			return l.cfg.PostLanding.Trunk.Branch
		}
		if l.cfg.Root != "" {
			return trunkBranchFor(l.cfg.Root)
		}
	}
	return settings.DefaultTrunkBranch
}

// CheckTrunkBranchStore refuses to use a store that recorded trunk landings on a different
// branch than configuredBranch, naming both branches and the operator remedy.
func CheckTrunkBranchStore(ctx context.Context, storePath, configuredBranch string) error {
	if storePath == "" || configuredBranch == "" {
		return nil
	}
	if _, err := os.Stat(storePath); os.IsNotExist(err) {
		return nil
	}
	s, err := store.Open(ctx, storePath, false)
	if err != nil {
		return err
	}
	defer s.Close()

	var recorded string
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var hasMeta int
		if err := r.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name='meta'`).Scan(&hasMeta); err != nil {
			return err
		}
		if hasMeta > 0 {
			var val string
			err := r.QueryRow(`SELECT value FROM meta WHERE key = ?`, trunkBranchMetaKey).Scan(&val)
			if err == nil && val != "" {
				recorded = val
				return nil
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}

		var hasLandings int
		if err := r.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name='landings'`).Scan(&hasLandings); err != nil {
			return err
		}
		if hasLandings == 0 {
			return nil
		}
		var baseRef string
		err := r.QueryRow(`
			SELECT COALESCE(json_extract(e.payload, '$.base_ref'), '')
			FROM landings l
			LEFT JOIN events e ON e.seq = l.event_seq
			WHERE l.result = 'landed'
			  AND COALESCE(json_extract(e.payload, '$.base_ref'), '') NOT LIKE 'refs/heads/patch/%'
			  AND COALESCE(json_extract(e.payload, '$.base_ref'), '') NOT LIKE 'patch/%'
			ORDER BY l.rowid DESC
			LIMIT 1
		`).Scan(&baseRef)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		recorded = strings.TrimPrefix(baseRef, "refs/heads/")
		if recorded == "" {
			recorded = settings.DefaultTrunkBranch
		}
		return nil
	})
	if err != nil {
		return err
	}
	if recorded != "" && recorded != configuredBranch {
		return Usagef("store %s recorded landings on branch %q, but .tillr.toml configures trunk.branch = %q; revert .tillr.toml to %q or start a fresh store for %q",
			storePath, recorded, configuredBranch, recorded, configuredBranch)
	}
	return nil
}

// RecordTrunkBranchInStore records trunk.branch in meta if not already set.
func RecordTrunkBranchInStore(ctx context.Context, storePath, branch string) error {
	if storePath == "" || branch == "" {
		return nil
	}
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT OR IGNORE INTO meta(key, value) VALUES(?, ?)`, trunkBranchMetaKey, branch)
		return err
	})
}
