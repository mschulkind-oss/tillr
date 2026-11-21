package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestTrunkRewritesResolveRefsStaleBaseAndLandingCalls(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-old")
	ctx := context.Background()
	base := runGit(t, f.root, "rev-parse", "HEAD")
	old := ownerCommit(t, f.root, "old.txt")
	runGit(t, f.root, "reset", "--hard", base)
	replacement := ownerCommit(t, f.root, "replacement.txt")
	runGit(t, f.root, "update-ref", "refs/tillr/green", replacement)
	started, err := json.Marshal(pendingWork{Decision: Decision{ItemID: "item-a"}, BaseSHA: base})
	if err != nil {
		t.Fatal(err)
	}

	fixtureWrite(t, f, func(tx *store.Tx) error {
		const at = "2026-11-20T00:00:00Z"
		if _, err := tx.Exec(`INSERT INTO trunk_rewrites(old_sha,new_sha,recorded_at) VALUES(?,?,?)`, old, replacement, at); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE items SET state='done' WHERE item_id='item-old'`); err != nil {
			return err
		}
		receipt, err := tx.Exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES
			(1,'land.landing','item','item-old','{"run_id":"run-old","result":"landed","base_ref":"refs/heads/main"}')`)
		if err != nil {
			return err
		}
		seq, err := receipt.LastInsertId()
		if err != nil {
			return err
		}
		if err := recordFixtureLanding(tx, "item-old", "run-old", old, int(seq)); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES
			(1,'item.update','item','item-a','{"item_id":"item-a","blocked_by":["item-old"]}'),
			(2,?,'run','run-stale',?),
			(3,?,'call','call-with-event','{"run_id":"run-old","item_id":"item-old"}'),
			(4,?,'call','call-with-event',json_object('commit',?)),
			(5,?,'call','call-without-event','{"run_id":"run-old","item_id":"item-old"}')`, dispatchStartedKind, string(started), callOffered, callLanded, old, callOffered); err != nil {
			return err
		}
		return nil
	})

	t.Run("marker", func(t *testing.T) {
		fixtureRead(t, f, func(r *store.Reader) error {
			planned, err := planRefSweep(r, []RefDisposition{{Ref: land.LandedRefPrefix + "run-old", SHA: replacement}}, nil, map[string]bool{replacement: true}, f.root)
			if err != nil || len(planned) != 1 || !planned[0].Remove {
				t.Fatalf("expected replacement marker to be removable: %+v, %v", planned, err)
			}
			return nil
		})
	})
	t.Run("stale base", func(t *testing.T) {
		fixtureRead(t, f, func(r *store.Reader) error {
			sb, err := runBaseLacks(ctx, r, f.root, "run-stale", replacement)
			if err != nil || !strings.Contains(sb.Lacks, "item-old ("+shortSHA(replacement)+")") {
				t.Fatalf("expected the replacement landing in stale-base evidence: %+v, %v", sb, err)
			}
			return nil
		})
	})
	t.Run("landing calls", func(t *testing.T) {
		if err := l.reconcileLandingCalls(ctx); err != nil {
			t.Fatal(err)
		}
		fixtureRead(t, f, func(r *store.Reader) error {
			for _, id := range []string{"call-with-event", "call-without-event"} {
				var reason string
				if err := r.QueryRow(`SELECT json_extract(payload,'$.reason') FROM events WHERE kind=? AND object_id=?`, callClosed, id).Scan(&reason); err != nil || reason != "proven" {
					t.Errorf("close reason for %s = %q, %v; want proven", id, reason, err)
				}
			}
			return nil
		})
	})
}

func TestCommentDeliveryUsesRewrittenLanding(t *testing.T) {
	f, l, _ := commentFixture(t, "")
	number := 1
	server := stubReview(t, &number)
	defer server.Close()
	c := Candidate{ItemID: takeFixtureComment(t, f, server.URL), RunID: "run-reply", LaneID: "desk-example"}
	old := runGit(t, f.root, "rev-parse", "HEAD")
	const doc = "docs/design/example.md"
	writeForkFile(t, filepath.Join(f.root, doc), "# Rewritten design\n")
	runGit(t, f.root, "commit", "-qam", "docs: rewrite design", "--amend")
	replacement := runGit(t, f.root, "rev-parse", "HEAD")

	durable := run.DurableDir(l.laneDirPath(c.LaneID), c.RunID)
	if err := os.MkdirAll(durable, 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(durable, commentRepliesFile), `{"id":"comment-1","round":0,"summary":"Answer."}`+"\n")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := recordFixtureLanding(tx, c.ItemID, c.RunID, old, 1); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO comment_runs(run_id,item_id,session_id,reason) VALUES(?,?,'fixture','fixture')`, c.RunID, c.ItemID); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO trunk_rewrites(old_sha,new_sha,recorded_at) VALUES(?,?,'2026-11-20T00:00:00Z')`, old, replacement)
		return err
	})
	if err := l.deliverCommentReplies(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.root, ".vantage", "inbox", c.ItemID+".jsonl")); err != nil {
		t.Fatalf("reply was not delivered against the served replacement document: %v", err)
	}
}
