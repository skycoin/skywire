package visor

import (
	"testing"
	"time"

	"github.com/skycoin/skywire/cmd/apps/skychat/pairing"
	"github.com/skycoin/skywire/pkg/cipher"
)

func TestPairInboxDeliverAndSnapshot(t *testing.T) {
	inbox := newPairInbox(8)
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()

	t1 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(1 * time.Second)
	t3 := t1.Add(2 * time.Second)

	inbox.deliver(pkA, pairing.Message{Text: "hi", TS: t1})
	inbox.deliver(pkB, pairing.Message{Text: "yo", TS: t2})
	inbox.deliver(pkA, pairing.Message{Text: "still here", TS: t3})

	all := inbox.snapshotAfter(time.Time{})
	if len(all) != 3 {
		t.Fatalf("snapshotAfter(zero): got %d msgs, want 3", len(all))
	}
	if all[0].Text != "hi" || all[1].Text != "yo" || all[2].Text != "still here" {
		t.Errorf("messages out of order: %+v", all)
	}

	since := t1.Add(500 * time.Millisecond)
	after := inbox.snapshotAfter(since)
	if len(after) != 2 {
		t.Fatalf("snapshotAfter(t1+500ms): got %d, want 2", len(after))
	}
	if after[0].Text != "yo" {
		t.Errorf("first after-snapshot = %q, want yo", after[0].Text)
	}
}

func TestPairInboxRingEvictsOldest(t *testing.T) {
	inbox := newPairInbox(3)
	pk, _ := cipher.GenerateKeyPair()

	base := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		inbox.deliver(pk, pairing.Message{
			Text: "msg",
			TS:   base.Add(time.Duration(i) * time.Second),
		})
	}

	all := inbox.snapshotAfter(time.Time{})
	if len(all) != 3 {
		t.Fatalf("ring should cap at 3, got %d", len(all))
	}
	// Oldest two messages (i=0, i=1) should have been evicted.
	want := base.Add(2 * time.Second)
	if !all[0].TS.Equal(want) {
		t.Errorf("oldest in ring should be %v, got %v", want, all[0].TS)
	}
}

func TestPairInboxSnapshotIsCopy(t *testing.T) {
	// Mutating a snapshot must not affect the inbox state.
	inbox := newPairInbox(8)
	pk, _ := cipher.GenerateKeyPair()
	inbox.deliver(pk, pairing.Message{Text: "x", TS: time.Now().UTC()})

	first := inbox.snapshotAfter(time.Time{})
	first[0].Text = "tampered"

	second := inbox.snapshotAfter(time.Time{})
	if second[0].Text != "x" {
		t.Errorf("snapshot mutation leaked into inbox: %q", second[0].Text)
	}
}

func TestPairInboxEmpty(t *testing.T) {
	inbox := newPairInbox(8)
	got := inbox.snapshotAfter(time.Time{})
	if len(got) != 0 {
		t.Errorf("empty inbox should yield 0 messages, got %d", len(got))
	}
}

func TestPairInboxSinceFuture(t *testing.T) {
	inbox := newPairInbox(8)
	pk, _ := cipher.GenerateKeyPair()
	inbox.deliver(pk, pairing.Message{Text: "x", TS: time.Now().UTC()})

	future := time.Now().UTC().Add(time.Hour)
	got := inbox.snapshotAfter(future)
	if len(got) != 0 {
		t.Errorf("since-in-future should yield 0 messages, got %d", len(got))
	}
}
