package commands

import (
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// TestOwnGroupFileEchoWaitsForAnswer: the visor echoes an owner's group file
// post before /send-file answers. The echo must reach the UI only after the
// answer, or a client that matches it by file id shows the file twice.
func TestOwnGroupFileEchoWaitsForAnswer(t *testing.T) {
	withLifecycleEnv(t)
	oldGrace := ownFileEchoGrace
	ownFileEchoGrace = 0
	t.Cleanup(func() { ownFileEchoGrace = oldGrace })

	sub, unsub := hub.subscribe()
	defer unsub()

	me, _ := cipher.GenerateKeyPair()
	text, err := encodeGroupFileText(groupFileMeta{ID: "f-own", Name: "voice-20261116-134400.oga", Size: 4500})
	if err != nil {
		t.Fatal(err)
	}
	holdOwnFileEcho("f-own")
	var since time.Time
	deliverGroupMessages([]visorapi.GroupMessage{{
		GroupID: "g-1", SenderPK: me, Text: text, TS: time.Now().UTC(), Arrived: time.Now().UTC(),
	}}, &since)

	select {
	case s := <-sub:
		t.Fatalf("echo reached the UI before the answer: %s", s)
	case <-time.After(200 * time.Millisecond):
	}

	releaseOwnFileEcho("f-own")
	got := waitForString(t, sub, 2*time.Second)
	if !strings.Contains(got, `"file_id":"f-own"`) {
		t.Fatalf("unexpected SSE message %s", got)
	}
}

// TestOtherGroupFileIsNotHeld: a file post nobody here sent goes out at once.
func TestOtherGroupFileIsNotHeld(t *testing.T) {
	withLifecycleEnv(t)
	sub, unsub := hub.subscribe()
	defer unsub()

	peer, _ := cipher.GenerateKeyPair()
	text, err := encodeGroupFileText(groupFileMeta{ID: "f-peer", Name: "voice-1.oga", Size: 8200})
	if err != nil {
		t.Fatal(err)
	}
	var since time.Time
	deliverGroupMessages([]visorapi.GroupMessage{{
		GroupID: "g-1", SenderPK: peer, Text: text, TS: time.Now().UTC(), Arrived: time.Now().UTC(),
	}}, &since)
	got := waitForString(t, sub, 2*time.Second)
	if !strings.Contains(got, `"file_id":"f-peer"`) {
		t.Fatalf("unexpected SSE message %s", got)
	}
}
