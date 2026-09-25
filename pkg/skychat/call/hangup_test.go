// Package call pkg/skychat/call/hangup_test.go
package call

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// neverDial is the Dial of a side that only ever answers.
func neverDial(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return nil, io.EOF }

// waitNoActive blocks until m lists no live call, failing after a second.
func waitNoActive(t *testing.T, m *Manager, who string) {
	const within = time.Second
	t.Helper()
	deadline := time.After(within)
	for len(m.Active()) > 0 {
		select {
		case <-deadline:
			t.Fatalf("%s still lists %v as live after %s", who, m.Active(), within)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// session is the live session for id, or nil.
func (m *Manager) session(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[id]
}

// drainFrames reads n media frames off a raw conn, the way a peer's recv loop
// would.
func drainFrames(c net.Conn, n int) error {
	for i := 0; i < n; i++ {
		var hdr [2]byte
		if _, err := io.ReadFull(c, hdr[:]); err != nil {
			return err
		}
		if _, err := io.ReadFull(c, make([]byte, binary.BigEndian.Uint16(hdr[:]))); err != nil {
			return err
		}
	}
	return nil
}

// TestPeerHangsUpAtOnce: a hangup on one side ends the call on the other
// within a second, whichever side it was — not after the reconnect budget.
//
// The media conn is one stream on a shared dmsg session, so the peer closing
// it and the session dying under it look the same: EOF. Resumption took every
// EOF for the second and spent thirty seconds (the caller) or sixty (the
// callee) re-dialing a peer that had hung up, which is what a phone reported
// as "the other side keeps the line". The farewell ahead of the close
// (byeFrame) is what tells the two apart.
func TestPeerHangsUpAtOnce(t *testing.T) {
	for _, tc := range []struct {
		name          string
		callerHangsUp bool
	}{{"caller hangs up", true}, {"callee hangs up", false}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			pkA, _ := cipher.GenerateKeyPair()
			pkB, _ := cipher.GenerateKeyPair()

			lis := newMemListener()
			defer lis.Close() //nolint:errcheck
			dialer := &recordingDialer{lis: lis}
			sink := &captureSink{}
			mgrB := NewManager(Config{
				LocalPK:    pkB,
				Dial:       neverDial,
				OnIncoming: func(Sig) bool { return true },
				NewSink:    func() Sink { return sink },
			})
			go mgrB.Serve(ctx, lis)
			mgrA := NewManager(Config{
				LocalPK:   pkA,
				Dial:      dialer.dial,
				NewSource: func() Source { return &toneSource{} },
			})

			sessA, err := mgrA.Call(ctx, pkB)
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			waitFrames(t, sink, 5, 3*time.Second, "before the hangup")
			sessB := mgrB.session(sessA.CallID)
			if sessB == nil {
				t.Fatalf("the callee has no session for %s", sessA.CallID)
			}
			// Both sides run this code, so both offered resumption: a bare
			// close here WOULD have started a reconnect.
			if sessA.resume == nil || !sessB.awaitResume {
				t.Fatal("resumption was not armed on both sides; the test would prove nothing")
			}

			hanger, other, otherSess := mgrA, mgrB, sessB
			if !tc.callerHangsUp {
				hanger, other, otherSess = mgrB, mgrA, sessA
			}
			if err := hanger.Hangup(sessA.CallID); err != nil {
				t.Fatalf("Hangup: %v", err)
			}
			waitNoActive(t, other, "the side hung up on")
			waitNoActive(t, hanger, "the side that hung up")
			if r := otherSess.EndReason(); r != "peer hung up" {
				t.Fatalf("the side hung up on ended for %q, want \"peer hung up\"", r)
			}
			if got := dialer.count(); got != 1 {
				t.Fatalf("the hangup was taken for a transport failure: %d conns dialed, want 1", got)
			}
		})
	}
}

// TestResumeRefusalIsFinal: a peer that answers a resume with a decline has
// dropped the call or cannot take one, and asking it again for the rest of
// the budget only delays the end it has already announced.
func TestResumeRefusalIsFinal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()

	lis := newMemListener()
	defer lis.Close() //nolint:errcheck
	mgrB := NewManager(Config{LocalPK: pkB, Dial: neverDial})
	go mgrB.Serve(ctx, lis)
	mgrA := NewManager(Config{
		LocalPK: pkA,
		Dial:    func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return lis.dial() },
	})

	start := time.Now()
	_, err := mgrA.resumeOutbound(ctx, "0000000000000000", pkB)
	if !errors.Is(err, ErrResumeRefused) {
		t.Fatalf("resumeOutbound = %v, want ErrResumeRefused", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("a refused resume was retried for %s; the budget is %s", took, ResumeBudget)
	}
}

// TestNoResumeWithPeerThatPredatesIt: a peer built before resumption never
// re-dials and never takes a re-dial, so arming resumption against one only
// turns its hangup — a bare close, all that build can do — into a wait for a
// reconnect that cannot come. Such a peer sends no Resume flag, and a call
// with it ends on the close the way every call did before.
func TestNoResumeWithPeerThatPredatesIt(t *testing.T) {
	t.Run("callee predates it", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pkA, _ := cipher.GenerateKeyPair()
		pkB, _ := cipher.GenerateKeyPair()

		lis := newMemListener()
		defer lis.Close() //nolint:errcheck
		dialer := &recordingDialer{lis: lis}
		// The old callee: accepts without a Resume flag, takes a few frames,
		// and hangs up with nothing but a close.
		go func() {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			inv, err := readSig(c)
			if err != nil {
				return
			}
			if err := writeSig(c, Sig{Type: SigAccept, CallID: inv.CallID, FromPK: pkB, Codec: "pcm"}); err != nil {
				return
			}
			_ = drainFrames(c, 5) //nolint:errcheck
			_ = c.Close()         //nolint:errcheck
		}()
		mgrA := NewManager(Config{
			LocalPK:   pkA,
			Dial:      dialer.dial,
			NewSource: func() Source { return &toneSource{} },
		})
		sess, err := mgrA.Call(ctx, pkB)
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if sess.resume != nil {
			t.Fatal("resumption was armed against a peer that never offered it")
		}
		waitNoActive(t, mgrA, "the caller")
		if got := dialer.count(); got != 1 {
			t.Fatalf("re-dialed a peer that cannot take a resume: %d conns, want 1", got)
		}
	})

	t.Run("caller predates it", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pkA, _ := cipher.GenerateKeyPair()
		pkB, _ := cipher.GenerateKeyPair()

		lis := newMemListener()
		defer lis.Close() //nolint:errcheck
		mgrB := NewManager(Config{
			LocalPK:    pkB,
			Dial:       neverDial,
			OnIncoming: func(Sig) bool { return true },
		})
		go mgrB.Serve(ctx, lis)

		// The old caller: invites without a Resume flag.
		c, err := lis.dial()
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		const callID = "0123456789abcdef"
		if err := writeSig(c, Sig{Type: SigInvite, CallID: callID, FromPK: pkA, Codec: "pcm"}); err != nil {
			t.Fatalf("invite: %v", err)
		}
		reply, err := readSig(c)
		if err != nil || reply.Type != SigAccept {
			t.Fatalf("reply = %+v, %v; want SigAccept", reply, err)
		}
		if err := drainFrames(c, 5); err != nil {
			t.Fatalf("no media from the callee: %v", err)
		}
		if sess := mgrB.session(callID); sess == nil || sess.awaitResume {
			t.Fatal("the callee armed resumption against a caller that never offered it")
		}
		_ = c.Close() //nolint:errcheck // the old caller's hangup
		waitNoActive(t, mgrB, "the callee")
	})
}
