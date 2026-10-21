// Package call pkg/skychat/call/block_test.go
package call

import (
	"context"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestBlockedCallerRingsUnseen: a blocked caller is told the call is ringing,
// without our tone, as for any unanswered call, while nothing rings here.
func TestBlockedCallerRingsUnseen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	lis := newMemListener()
	defer lis.Close() //nolint:errcheck
	rang := make(chan Sig, 1)
	mgrB := NewManager(Config{LocalPK: pkB, Dial: neverDial, ManualAnswer: true, Ring: func(inv Sig) { rang <- inv }})
	_ = mgrB.SetRingback([]byte("tone"), "audio/mpeg") //nolint:errcheck
	mgrB.SetBlocked([]cipher.PubKey{pkA})
	go mgrB.Serve(ctx, lis)

	c, err := lis.dial()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeSig(c, Sig{Type: SigInvite, CallID: "blocked", FromPK: pkA, Codec: "pcm", Progress: true}); err != nil {
		t.Fatal(err)
	}
	first, err := readSig(c)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if first.Type != SigRinging || first.Tone != "" {
		t.Fatalf("the blocked caller got %s tone=%q, want a plain ringing", sigTypeName(first.Type), first.Tone)
	}
	select {
	case inv := <-rang:
		t.Fatalf("rang here for a blocked caller: %+v", inv)
	case <-time.After(200 * time.Millisecond):
	}
	if got := mgrB.Incoming(); len(got) != 0 {
		t.Fatalf("Incoming = %+v, want the blocked call hidden", got)
	}
	_ = c.Close() //nolint:errcheck

	// Unblocked, the same caller rings again.
	mgrB.SetBlocked(nil)
	c2, err := lis.dial()
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close() //nolint:errcheck
	if err := writeSig(c2, Sig{Type: SigInvite, CallID: "again", FromPK: pkA, Codec: "pcm"}); err != nil {
		t.Fatal(err)
	}
	if inv := awaitRing(t, rang); inv.CallID != "again" {
		t.Fatalf("rang for %q, want again", inv.CallID)
	}
}
