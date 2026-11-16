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

// TestGroupInbox_WaitArrivedAfter: a waiter wakes on the next arrival, a
// replay does not wake it, and it gives up after the wait.
func TestGroupInbox_WaitArrivedAfter(t *testing.T) {
	g := newGroupInbox(0)
	pk, _ := cipher.GenerateKeyPair()
	now := time.Now().UTC()
	g.deliver("g", pk, skychatgroup.Message{SenderPK: pk, Text: "a", TS: now})
	cursor := g.snapshotArrivedAfter(time.Time{})[0].Arrived

	if got := g.waitArrivedAfter(time.Time{}, time.Hour); len(got) != 1 {
		t.Fatalf("waiting with messages after since got %d, want 1 at once", len(got))
	}

	sub := g.subscribe(4)
	defer g.unsubscribe(sub)
	go func() {
		time.Sleep(50 * time.Millisecond)
		g.deliver("g", pk, skychatgroup.Message{SenderPK: pk, Text: "a", TS: now}) // replay
		time.Sleep(50 * time.Millisecond)
		g.deliver("g", pk, skychatgroup.Message{SenderPK: pk, Text: "b", TS: now.Add(-time.Minute)})
	}()
	got := g.waitArrivedAfter(cursor, 5*time.Second)
	if len(got) != 1 || got[0].Text != "b" {
		t.Fatalf("wait got %+v, want the late message b", got)
	}
	if n := len(sub.ch); n != 1 {
		t.Fatalf("subscriber got %d messages, want 1, the replay not fanned out", n)
	}

	tp := time.Now()
	if got := g.waitArrivedAfter(got[0].Arrived, 50*time.Millisecond); len(got) != 0 {
		t.Fatalf("wait with nothing new got %+v", got)
	}
	if time.Since(tp) < 50*time.Millisecond {
		t.Fatal("did not wait")
	}
}
