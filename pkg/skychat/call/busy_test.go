package call

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestInviteDuringCallGetsBusy pins that a third party calling someone who is
// already on a call is told busy, and the call in progress stays up.
func TestInviteDuringCallGetsBusy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	pkC, _ := cipher.GenerateKeyPair()

	lis := newMemListener()
	defer lis.Close() //nolint:errcheck
	dialB := func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return lis.dial() }

	mgrB := NewManager(Config{
		LocalPK:    pkB,
		Dial:       func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return nil, io.EOF },
		OnIncoming: func(Sig) bool { return true },
	})
	go mgrB.Serve(ctx, lis)

	mgrA := NewManager(Config{LocalPK: pkA, Dial: dialB})
	sessA, err := mgrA.Call(ctx, pkB)
	if err != nil {
		t.Fatalf("A calls B: %v", err)
	}
	defer mgrA.Hangup(sessA.CallID) //nolint:errcheck

	mgrC := NewManager(Config{LocalPK: pkC, Dial: dialB})
	callID := mgrC.Dial(pkB, 3*time.Second)
	deadline := time.After(3 * time.Second)
	for {
		dials := mgrC.Dialing()
		if len(dials) == 1 && dials[0].CallID == callID && dials[0].State.Ended() {
			if dials[0].State != DialBusy {
				t.Fatalf("C's call ended %q, want %q", dials[0].State, DialBusy)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("C's call never ended, dialing = %+v", dials)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err := mgrC.Call(ctx, pkB); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("C calls B again: err = %v, want busy", err)
	}

	if got := mgrB.Active(); len(got) != 1 || got[0] != sessA.CallID {
		t.Fatalf("B active calls = %v, want only %s", got, sessA.CallID)
	}
	if got := mgrA.Active(); len(got) != 1 {
		t.Fatalf("A active calls = %v, want 1", got)
	}

	// A redial from the peer already on the call is not refused as busy.
	sessA2, err := mgrA.Call(ctx, pkB)
	if err != nil {
		t.Fatalf("A redials B: %v", err)
	}
	defer mgrA.Hangup(sessA2.CallID) //nolint:errcheck
}
