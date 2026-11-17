package drain

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const commentReplyStateKind = "drain.comment-reply-state"

func recordCommentArrivals(tx *store.Tx, d Decision) error {
	var staged bool
	if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM comment_arrivals WHERE item_id=?)", d.ItemID).Scan(&staged); err != nil {
		return err
	}
	if staged {
		return nil
	}
	batch, err := item.ReadCommentBatch(tx.Reader(), d.ItemID)
	if err != nil || batch == nil {
		return err
	}
	for _, c := range batch.Comments {
		session := ""
		var lifetime *int
		var seconds *float64
		if d.Designer != nil {
			lifetime = d.Designer.CacheLifetimeSeconds
		}
		if d.Designer != nil && strings.HasPrefix(d.DesignerReason, "resumed-") {
			session = d.Designer.SessionID
			if last, err := time.Parse(time.RFC3339Nano, d.Designer.LastRequest); err == nil {
				changed := math.Max(c.CreatedAt, c.EditedAt)
				for _, raw := range c.Reactions {
					var turn struct {
						Actor     string  `json:"actor"`
						Timestamp float64 `json:"timestamp"`
						EditedAt  float64 `json:"edited_at"`
					}
					if err := json.Unmarshal(raw, &turn); err != nil {
						return err
					}
					if turn.Actor == "reviewer" {
						changed = math.Max(changed, math.Max(turn.Timestamp, turn.EditedAt))
					}
				}
				delta := math.Max(0, changed-float64(last.UnixNano())/1e9)
				seconds = &delta
			}
		}
		if _, err := tx.Exec("INSERT INTO comment_arrivals(item_id,comment_id,session_id,reason,cache_lifetime_seconds,seconds_since_request) VALUES(?,?,?,?,?,?) ON CONFLICT(item_id,comment_id) DO NOTHING", d.ItemID, c.ID, session, d.DesignerReason, lifetime, seconds); err != nil {
			return err
		}
	}
	return nil
}

// confirmCommentReplies observes review only after the inbox is gone. Written is
// not an acknowledgment, and an unavailable review never changes a recorded state.
func (l *Loop) confirmCommentReplies(ctx context.Context) error {
	st, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer st.Close()
	configured := l.cfg.VantageURL != "" && l.cfg.VantageRepo != ""
	var deliveries []commentDelivery
	states := map[string]map[string]string{}
	err = st.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec("INSERT INTO comment_confirmation(id,configured) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET configured=excluded.configured", configured); err != nil {
			return err
		}
		rows, err := tx.Reader().Query("SELECT run_id,item_id,document,blob_id,body FROM comment_deliveries WHERE written=1 ORDER BY run_id")
		if err != nil {
			return err
		}
		for rows.Next() {
			var d commentDelivery
			if err := rows.Scan(&d.RunID, &d.ItemID, &d.Document, &d.BlobID, &d.Body); err != nil {
				rows.Close()
				return err
			}
			deliveries = append(deliveries, d)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		rows, err = tx.Reader().Query("SELECT run_id,comment_id,state FROM comment_reply_states")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var run, id, state string
			if err := rows.Scan(&run, &id, &state); err != nil {
				return err
			}
			if states[run] == nil {
				states[run] = map[string]string{}
			}
			states[run][id] = state
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	for _, d := range deliveries {
		scanner := bufio.NewScanner(strings.NewReader(d.Body))
		scanner.Buffer(make([]byte, 4096), 8<<20)
		var replies []commentReply
		pending := false
		for scanner.Scan() {
			var reply commentReply
			if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil {
				return err
			}
			replies = append(replies, reply)
			state := states[d.RunID][reply.ID]
			if state != "delivered" && state != "dropped" {
				pending = true
			}
		}
		if err := scanner.Err(); err != nil {
			return err
		}
		if !pending {
			continue
		}
		reviewAvailable := false
		review := map[string]item.ReviewComment{}
		_, inboxErr := os.Stat(filepath.Join(l.cfg.Root, ".vantage", "inbox", d.ItemID+".jsonl"))
		if inboxErr != nil && !os.IsNotExist(inboxErr) {
			return inboxErr
		}
		if configured && os.IsNotExist(inboxErr) {
			comments, err := item.ReadReview(ctx, l.cfg.VantageURL, l.cfg.VantageRepo, d.Document)
			if err != nil {
				l.reportf("loop: replies not confirmed for %s: %v\n", d.ItemID, err)
			} else if comments != nil {
				reviewAvailable = true
				for _, c := range comments {
					review[c.ID] = c
				}
			}
		}
		err = st.WriteTx(ctx, func(tx *store.Tx) error {
			for _, reply := range replies {
				state := states[d.RunID][reply.ID]
				if state == "delivered" || state == "dropped" {
					continue
				}
				if state == "" {
					state = "written"
				}
				if reviewAvailable {
					state = "not confirmed"
					c, found := review[reply.ID]
					if !found {
						state = "dropped"
					} else {
						for _, raw := range c.Reactions {
							var turn struct {
								Actor        string `json:"actor"`
								Kind         string `json:"kind"`
								AnswersRound *int   `json:"answers_round"`
								Nonce        string `json:"nonce"`
							}
							if err := json.Unmarshal(raw, &turn); err != nil {
								return err
							}
							if turn.Actor == "agent" && turn.Kind != "noted" && turn.AnswersRound != nil && *turn.AnswersRound == reply.Round {
								state = "delivered"
								break
							}
						}
					}
				}
				if state == states[d.RunID][reply.ID] {
					continue
				}
				body, err := json.Marshal(map[string]string{"run_id": d.RunID, "comment_id": reply.ID, "state": state})
				if err != nil {
					return err
				}
				if _, err := tx.Append(event.Event{Kind: commentReplyStateKind, ObjectType: "item", ObjectID: &d.ItemID, Payload: body, At: l.cfg.Now().UTC()}); err != nil {
					return err
				}
				if _, err := tx.Exec("INSERT INTO comment_reply_states(run_id,comment_id,state) VALUES(?,?,?) ON CONFLICT(run_id,comment_id) DO UPDATE SET state=excluded.state", d.RunID, reply.ID, state); err != nil {
					return fmt.Errorf("record reply state: %w", err)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
