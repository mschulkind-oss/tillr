package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestReplyConfirmationFromReview(t *testing.T) {
	f, l, _ := commentFixture(t, "")
	ctx := context.Background()
	calls, fail := 0, false
	nullReview := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if nullReview {
			fmt.Fprint(w, "null")
			return
		}
		if r.URL.Path != "/api/r/fixture/review" || r.URL.Query().Get("path") != "docs/design/example.md" {
			t.Errorf("request %s", r.URL)
		}
		if fail {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, `{"comments":[{"id":"answered","reactions":[{"actor":"agent","kind":"addressed","answers_round":2}]},{"id":"read","reactions":[{"actor":"agent","kind":"noted","answers_round":2}]},{"id":"wrong-round","reactions":[{"actor":"agent","kind":"addressed","answers_round":1}]}]}`)
	}))
	defer server.Close()
	l.cfg.VantageURL, l.cfg.VantageRepo = server.URL, "fixture"
	body := ""
	for _, id := range []string{"answered", "deleted", "read", "wrong-round"} {
		raw, _ := json.Marshal(commentReply{ID: id, Round: 2, Summary: "answer"})
		body += string(raw) + "\n"
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec("INSERT INTO comment_deliveries(run_id,item_id,document,blob_id,body,written) VALUES('r','i','docs/design/example.md','blob',?,1)", body)
		return err
	})
	inbox := filepath.Join(f.root, ".vantage/inbox/i.jsonl")
	if err := os.MkdirAll(filepath.Dir(inbox), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, inbox, body)
	if err := l.confirmCommentReplies(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("asked server while inbox exists")
	}
	assertReplyState(t, f, "answered", "written")
	if err := os.Remove(inbox); err != nil {
		t.Fatal(err)
	}
	fail = true
	if err := l.confirmCommentReplies(ctx); err != nil {
		t.Fatal(err)
	}
	assertReplyState(t, f, "answered", "written")
	fail = false
	nullReview = true
	if err := l.confirmCommentReplies(ctx); err != nil {
		t.Fatal(err)
	}
	assertReplyState(t, f, "answered", "written")
	nullReview = false
	if err := l.confirmCommentReplies(ctx); err != nil {
		t.Fatal(err)
	}
	assertReplyState(t, f, "answered", "delivered")
	assertReplyState(t, f, "deleted", "dropped")
	assertReplyState(t, f, "read", "not confirmed")
	assertReplyState(t, f, "wrong-round", "not confirmed")
	fail = true
	if err := l.confirmCommentReplies(ctx); err != nil {
		t.Fatal(err)
	}
	assertReplyState(t, f, "read", "not confirmed")
}

func assertReplyState(t *testing.T, f refusalLoopFixture, id, want string) {
	t.Helper()
	st, err := store.OpenRead(context.Background(), f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	err = st.ReadTx(context.Background(), func(r *store.Reader) error {
		var state string
		err := r.QueryRow("SELECT state FROM comment_reply_states WHERE run_id='r' AND comment_id=?", id).Scan(&state)
		if err == nil && state != want {
			t.Errorf("%s: %s, want %s", id, state, want)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRepliesWithoutServerStayWritten(t *testing.T) {
	f, l, _ := commentFixture(t, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO comment_deliveries(run_id,item_id,document,blob_id,body,written) VALUES('r','i','docs/design/example.md','blob','{"id":"answered","round":0,"summary":"answer"}',1)`)
		return err
	})
	if err := l.confirmCommentReplies(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertReplyState(t, f, "answered", "written")
}

func TestFirstStagingRecordsCommentArrivals(t *testing.T) {
	f, l, _ := commentFixture(t, "")
	ctx := context.Background()
	last := time.Unix(1000, 0).UTC()
	lifetime := 60
	prior := &lastDesigner{SessionID: "11111111-1111-4111-8111-111111111111", LastRequest: last.Format(time.RFC3339Nano), CacheLifetimeSeconds: &lifetime}
	var ids []string
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for i, ts := range []float64{1020, 1100, 900, 1020} {
			doc := "docs/design/example.md"
			if i > 0 {
				doc = fmt.Sprintf("docs/design/example%d.md", i)
				writeForkFile(t, filepath.Join(f.root, doc), "# Example\n")
			}
			comment := item.ReviewComment{ID: fmt.Sprint(i), Comment: "Why?", CreatedAt: ts}
			if i == 0 {
				comment.CreatedAt = 900
				comment.EditedAt = 1010
				comment.Reactions = []json.RawMessage{json.RawMessage(`{"actor":"reviewer","kind":"needs_clarification","timestamp":1015,"edited_at":1020}`), json.RawMessage(`{"actor":"agent","kind":"noted","timestamp":1200}`)}
			}
			if i == 1 {
				comment.CreatedAt = 900
				comment.EditedAt = 1100
			}
			id, err := item.TakeComments(ctx, tx, f.root, doc, []item.ReviewComment{comment}, last)
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	for i, id := range ids {
		d := Decision{ItemID: id, LaneID: "desk", DesignerReason: "resumed-warm", DesignerSessionID: prior.SessionID, Designer: prior}
		if i == 3 {
			d.Designer = nil
			d.DesignerSessionID = ""
			d.DesignerReason = "no-designer"
		}
		d.RunRequest = l.cfg.Policy.Request(id, "desk", fmt.Sprintf("r%d", i), "fixture")
		spec := StageSpec{}
		if err := l.stageComment(ctx, &d, &spec); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			d.RunRequest.RunID = "retry"
			d.Designer = nil
			d.DesignerReason = "transcript-missing"
			if err := l.stageComment(ctx, &d, &spec); err != nil {
				t.Fatal(err)
			}
		}
	}
	st, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	err = st.ReadTx(ctx, func(r *store.Reader) error {
		var total, inside, after, absent int
		if err := r.QueryRow(`SELECT count(*),sum(CASE WHEN seconds_since_request<=cache_lifetime_seconds THEN 1 ELSE 0 END),sum(CASE WHEN seconds_since_request>cache_lifetime_seconds THEN 1 ELSE 0 END),sum(CASE WHEN session_id='' THEN 1 ELSE 0 END) FROM comment_arrivals`).Scan(&total, &inside, &after, &absent); err != nil {
			return err
		}
		if total != 4 || inside != 2 || after != 1 || absent != 1 {
			t.Errorf("counts %d %d %d %d", total, inside, after, absent)
		}
		var first float64
		if err := r.QueryRow("SELECT seconds_since_request FROM comment_arrivals WHERE item_id=?", ids[0]).Scan(&first); err != nil {
			return err
		}
		if first != 20 {
			t.Errorf("last reviewer edit: %v", first)
		}
		var seconds float64
		var session, reason string
		var cache int
		if err := r.QueryRow("SELECT seconds_since_request,session_id,reason,cache_lifetime_seconds FROM comment_arrivals WHERE item_id=?", ids[2]).Scan(&seconds, &session, &reason, &cache); err != nil {
			return err
		}
		if seconds != 0 || session != prior.SessionID || reason != "resumed-warm" || cache != 60 {
			t.Errorf("arrival %v %s %s %d", seconds, session, reason, cache)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
