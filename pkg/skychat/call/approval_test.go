package call

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// awaitDialEnd waits for the call callID placed by m to end, and returns how.
func awaitDialEnd(t *testing.T, m *Manager, callID string) DialState {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		for _, d := range m.Dialing() {
			if d.CallID == callID && d.State.Ended() {
				return d.State
			}
		}
		select {
		case <-deadline:
			t.Fatalf("call %s never ended, dialing = %+v", callID, m.Dialing())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestApprovalDeclinesCallerNotAccepted pins that with approval on a caller
// not let in is declined without ringing and listed as refused, and that
// with approval off the same caller gets through.
func TestApprovalDeclinesCallerNotAccepted(t *testing.T) {
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
	mgrB.SetApproval(true, []cipher.PubKey{pkA})

	mgrC := NewManager(Config{LocalPK: pkC, Dial: dialB})
	callID := mgrC.Dial(pkB, 3*time.Second)
	if got := awaitDialEnd(t, mgrC, callID); got != DialDeclined {
		t.Fatalf("stranger's call ended %q, want %q", got, DialDeclined)
	}
	if got := mgrB.Active(); len(got) != 0 {
		t.Fatalf("B active calls = %v, want none", got)
	}
	refused := mgrB.Refused()
	if len(refused) != 1 || refused[0].Peer != pkC || refused[0].CallID != callID || refused[0].Reason != RefusedNotAccepted {
		t.Fatalf("refused = %+v, want C's call as %s", refused, RefusedNotAccepted)
	}

	mgrA := NewManager(Config{LocalPK: pkA, Dial: dialB})
	sessA, err := mgrA.Call(ctx, pkB)
	if err != nil {
		t.Fatalf("accepted caller A: %v", err)
	}
	if err := mgrA.Hangup(sessA.CallID); err != nil {
		t.Fatal(err)
	}
	waitNoCalls(t, mgrB)

	mgrB.SetApproval(false, nil)
	sessC, err := mgrC.Call(ctx, pkB)
	if err != nil {
		t.Fatalf("approval off, C calls B: %v", err)
	}
	if err := mgrC.Hangup(sessC.CallID); err != nil {
		t.Fatal(err)
	}
}

func waitNoCalls(t *testing.T, m *Manager) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for len(m.Active()) != 0 {
		select {
		case <-deadline:
			t.Fatalf("calls still active: %v", m.Active())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
