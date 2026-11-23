package remotesync

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// RemoteSyncReplayKind is the drain event kind for persisted remote-sync replay intents.
const RemoteSyncReplayKind = "drain.remote-sync-replay"

// Publication summarizes a completed remote-sync replay.
type Publication struct {
	Replayed  int
	RemoteTip string
	Base      string
	NewTip    string
}

// RewriteRecord holds the replay rewrite fields serialized in RemoteSyncReplay.
type RewriteRecord struct {
	Tip   string   `json:"Tip"`
	Order []string `json:"Order"`
}

// RemoteSyncReplay is the durable replay intent record.
type RemoteSyncReplay struct {
	Candidate json.RawMessage `json:"Candidate"`
	Rewrite   RewriteRecord   `json:"Rewrite"`
	Tree      string          `json:"Tree"`
	Verdict   land.Verdict    `json:"Verdict"`
	Stage     RemoteSyncState `json:"Stage"`
}

// Publication summarizes the replay's publication outcome.
func (r *RemoteSyncReplay) Publication() Publication {
	return PublicationFor(len(r.Rewrite.Order), r.Stage.RemoteTip, r.Stage.MergeBase, r.Rewrite.Tip)
}

// PublicationFor builds a Publication from its constituent fields.
func PublicationFor(replayed int, remoteTip, mergeBase, tip string) Publication {
	return Publication{
		Replayed:  replayed,
		RemoteTip: remoteTip,
		Base:      mergeBase,
		NewTip:    tip,
	}
}

// WriteRemoteSyncReplay records or clears the replay intent event on trunkRef.
func WriteRemoteSyncReplay(tx *store.Tx, trunkRef, utility string, intent any, at time.Time) error {
	body, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{
		Kind:       RemoteSyncReplayKind,
		ObjectType: "trunk",
		ObjectID:   &trunkRef,
		Payload:    body,
		At:         at,
	})
	return err
}

// ReadRemoteSyncReplayJSON reads the latest raw JSON payload for the replay intent.
func ReadRemoteSyncReplayJSON(ctx context.Context, storePath, trunkRef string) ([]byte, error) {
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	var raw []byte
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT COALESCE((SELECT payload FROM events WHERE kind=? AND object_type='trunk' AND object_id=? ORDER BY seq DESC LIMIT 1), 'null')`, RemoteSyncReplayKind, trunkRef).Scan(&raw)
	})
	return raw, err
}

// ReadRemoteSyncReplay reads and decodes the latest RemoteSyncReplay intent.
func ReadRemoteSyncReplay(ctx context.Context, storePath, trunkRef string) (*RemoteSyncReplay, error) {
	raw, err := ReadRemoteSyncReplayJSON(ctx, storePath, trunkRef)
	if err != nil {
		return nil, err
	}
	var intent *RemoteSyncReplay
	if err := json.Unmarshal(raw, &intent); err != nil {
		return nil, err
	}
	return intent, nil
}
