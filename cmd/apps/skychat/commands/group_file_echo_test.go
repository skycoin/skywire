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
	text, err := encodeGroupFileText(groupFileMeta{ID: "f-peer", Name: "photo.png", Size: 8200})
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

// TestGroupClipIsPulledOnArrival: a recorded voice or video message from
// another member is requested from its sender as it arrives, once, while an
// ordinary attachment or an old clip waits for the user to ask.
func TestGroupClipIsPulledOnArrival(t *testing.T) {
	withLifecycleEnv(t)
	type pull struct {
		peer cipher.PubKey
		id   string
	}
	pulls := make(chan pull, 8)
	old := clipPull
	clipPull = func(peer cipher.PubKey, id, _ string) { pulls <- pull{peer, id} }
	t.Cleanup(func() { clipPull = old })

	peer, _ := cipher.GenerateKeyPair()
	msg := func(id, name string, ts time.Time) visorapi.GroupMessage {
		text, err := encodeGroupFileText(groupFileMeta{ID: id, Name: name, Size: 8200})
		if err != nil {
			t.Fatal(err)
		}
		return visorapi.GroupMessage{GroupID: "g-1", SenderPK: peer, Text: text, TS: ts, Arrived: time.Now().UTC()}
	}
	now := time.Now().UTC()
	t.Cleanup(func() { clearRequestedFile("clip-1") })
	var since time.Time
	deliverGroupMessages([]visorapi.GroupMessage{
		msg("clip-1", "voice-20261116-134400.oga", now),
		msg("clip-1", "voice-20261116-134400.oga", now),
		msg("doc-1", "report.pdf", now),
		msg("clip-old", "video-20261101-090000.mp4", now.Add(-2*clipPullMaxAge)),
	}, &since)

	select {
	case p := <-pulls:
		if p.peer != peer || p.id != "clip-1" {
			t.Fatalf("pulled %s from %s", p.id, p.peer)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the voice message was not pulled")
	}
	select {
	case p := <-pulls:
		t.Fatalf("unexpected pull of %s", p.id)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestRecordedClip(t *testing.T) {
	for name, want := range map[string]bool{
		"voice-20261116-134400.oga": true,
		"video-20261116-134400.mp4": true,
		"video-sq-20261116.mp4":     true,
		"voice-memo.oga":            false,
		"photo.png":                 false,
		"video-.mp4":                false,
	} {
		if got := recordedClip(name); got != want {
			t.Errorf("recordedClip(%q) = %v, want %v", name, got, want)
		}
	}
}
