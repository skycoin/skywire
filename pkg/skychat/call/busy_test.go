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
	if got := awaitDialEnd(t, mgrC, callID); got != DialBusy {
		t.Fatalf("C's call ended %q, want %q", got, DialBusy)
	}
	if _, err := mgrC.Call(ctx, pkB); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("C calls B again: err = %v, want busy", err)
	}
	if refused := mgrB.Refused(); len(refused) != 2 || refused[0].CallID != callID || refused[0].Peer != pkC || refused[0].Reason != RefusedBusy {
		t.Fatalf("refused = %+v, want both of C's calls as %s", refused, RefusedBusy)
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
