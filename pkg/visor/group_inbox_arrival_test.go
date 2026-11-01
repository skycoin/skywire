package visor

import (
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	skychatgroup "github.com/skycoin/skywire/pkg/skychat/group"
)

// TestGroupInbox_PollFindsLateHistory: a joiner's backfill arrives after
// newer messages but carries older TSs, and a poll that already saw the
// newer ones must still return it.
func TestGroupInbox_PollFindsLateHistory(t *testing.T) {
	g := newGroupInbox(0)
	pk, _ := cipher.GenerateKeyPair()
	now := time.Now().UTC()
	g.deliver("g", pk, skychatgroup.Message{SenderPK: pk, Text: "new", TS: now})
	first := g.snapshotArrivedAfter(time.Time{})
	if len(first) != 1 {
		t.Fatalf("first poll got %d messages, want 1", len(first))
	}
	cursor := first[0].Arrived

	g.deliver("g", pk, skychatgroup.Message{SenderPK: pk, Text: "old", TS: now.Add(-time.Hour)})
	late := g.snapshotArrivedAfter(cursor)
	if len(late) != 1 || late[0].Text != "old" {
		t.Fatalf("poll after the cursor got %+v, want the late old message", late)
	}
	if !late[0].Arrived.After(cursor) {
		t.Fatalf("Arrived %v is not after the cursor %v", late[0].Arrived, cursor)
	}
	if again := g.snapshotArrivedAfter(late[0].Arrived); len(again) != 0 {
		t.Fatalf("polling past the newest arrival returned %d messages", len(again))
	}

	// A reconnecting feed replays both. They are not news.
	g.deliver("g", pk, skychatgroup.Message{SenderPK: pk, Text: "new", TS: now})
	g.deliver("g", pk, skychatgroup.Message{SenderPK: pk, Text: "old", TS: now.Add(-time.Hour)})
	if again := g.snapshotArrivedAfter(late[0].Arrived); len(again) != 0 {
		t.Fatalf("a replay reached the poll again: %+v", again)
	}
}
