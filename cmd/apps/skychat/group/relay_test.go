// Package group cmd/apps/skychat/group/relay_test.go
//
// Unit coverage for the owner-side relay path and its member-side ack reader —
// handleRelay, writeAndReadAck, acceptRelayOn — plus the small Session
// accessors that were still at 0%.
//
// The relay is the pre-D1 fallback send path: a member opens a stream to the
// owner's relay listener, writes a RelayMessage, and the owner re-publishes it
// into the CXO feed and acks. Both halves are plain framed JSON over a
// net.Conn, so a net.Pipe drives them end to end with no mesh.
//
// The assertion that carries the most weight is the ALLOWLIST GATE: handleRelay
// re-publishes under a sender PK the *caller* supplies, so a missing membership
// check would let any visor that can reach the relay port inject messages
// attributed to any PK. The rejection tests assert the leaf is absent from the
// feed, not merely that no ack came back.
//
// Ack pairing is the other one: writeAndReadAck must reject an ack whose MsgID
// doesn't match the message it sent, or a member could read a stale ack from a
// previous message as confirmation for the current one.
package group

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skychat/message"
)

// memRelayListener is an in-process net.Listener: dial() hands back one end of
// a pipe and queues the other for Accept.
type memRelayListener struct {
	ch     chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newMemRelayListener() *memRelayListener {
	return &memRelayListener{ch: make(chan net.Conn, 4), closed: make(chan struct{})}
}

func (l *memRelayListener) dial() (net.Conn, error) {
	a, b := net.Pipe()
	select {
	case l.ch <- b:
		return a, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *memRelayListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *memRelayListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *memRelayListener) Addr() net.Addr { return appnet.Addr{Net: appnet.TypeSkynet} }

// waitWG fails the test if wg does not drain within d.
func waitWG(t *testing.T, wg *sync.WaitGroup, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatal("waitgroup did not drain — the accept loop is still running")
	}
}

// skynetRelayAddr is the address shape bindRelaySkynet/SubmitToOwner use.
func skynetRelayAddr(pk cipher.PubKey) appnet.Addr {
	return appnet.Addr{Net: appnet.TypeSkynet, PubKey: pk, Port: routing.Port(60001)}
}

// newRelaySession builds an owner-role session on an in-memory publisher whose
// member allowlist is `members`. ModePublic so relayed text lands as plaintext
// on the leaf (an unset Mode falls through publishAs's switch and would publish
// an empty body).
func newRelaySession(t *testing.T, members []cipher.PubKey) *Session {
	t.Helper()
	me, sk := cipher.GenerateKeyPair()
	s := newRosterSession(t, me, sk, Record{
		ID:      uuid.NewString(),
		OwnerPK: me,
		Role:    RoleOwner,
		Mode:    ModePublic,
	})
	// SetAllowlist is what seeds s.members, which isMember reads.
	if _, err := s.SetAllowlist(append([]cipher.PubKey{me}, members...)); err != nil {
		t.Fatalf("SetAllowlist: %v", err)
	}
	return s
}

// relayLeafPath is the feed path publishAs writes to for the FIRST publish on
// a fresh publisher (seq starts at 1), which is all these tests make.
func relayLeafPath(sender cipher.PubKey, ts time.Time) string {
	return MessagePathPrefix + "/" + sender.Hex() + "/" +
		strconv.FormatInt(ts.UnixNano(), 10) + "/1"
}

// driveRelay runs handleRelay against one end of a pipe, writes rawBody from
// the member end, and returns the ack the owner wrote (if any).
//
// handleRelay writes its ack onto the same unbuffered pipe, so the read must
// happen before waiting for the handler to return — otherwise the ack write
// blocks forever and the test deadlocks.
func driveRelay(t *testing.T, s *Session, rawBody []byte) (ack RelayAck, gotAck bool) {
	t.Helper()
	memberEnd, ownerEnd := net.Pipe()
	defer func() { _ = memberEnd.Close() }() //nolint:errcheck
	defer func() { _ = ownerEnd.Close() }()  //nolint:errcheck

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleRelay(ownerEnd)
	}()

	if err := message.WriteFrame(memberEnd, rawBody); err != nil {
		t.Fatalf("write relay frame: %v", err)
	}

	_ = memberEnd.SetReadDeadline(time.Now().Add(250 * time.Millisecond)) //nolint:errcheck
	payload, err := message.ReadFrame(memberEnd)
	if err == nil {
		if uerr := json.Unmarshal(payload, &ack); uerr != nil {
			t.Fatalf("ack is not a RelayAck: %v (raw %q)", uerr, payload)
		}
		gotAck = true
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleRelay did not return")
	}
	return ack, gotAck
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// --- handleRelay ------------------------------------------------------------

func TestHandleRelay_PublishesAndAcksForMember(t *testing.T) {
	member := mustPK(t)
	s := newRelaySession(t, []cipher.PubKey{member})

	ts := time.Now().UTC().Truncate(time.Nanosecond)
	ack, gotAck := driveRelay(t, s, mustJSON(t, RelayMessage{
		SenderPK: member, Text: "relayed hello", TS: ts, MsgID: "m-1",
	}))

	if !gotAck {
		t.Fatal("a relayed message from a member should be acked")
	}
	if !ack.Ack || ack.MsgID != "m-1" {
		t.Errorf("ack = %+v, want {Ack:true MsgID:m-1}", ack)
	}

	// The ack's contract is "the leaf is committed to the feed" — verify it is.
	body := pubReadable(t, s.pub, relayLeafPath(member, ts))
	var msg Message
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("published leaf is not a Message: %v", err)
	}
	if msg.SenderPK != member {
		t.Errorf("leaf SenderPK = %s, want the relayed sender %s", msg.SenderPK.Hex()[:8], member.Hex()[:8])
	}
	if msg.Text != "relayed hello" {
		t.Errorf("leaf Text = %q, want %q", msg.Text, "relayed hello")
	}
}

// TestHandleRelay_RejectsNonMember is the authorization gate. handleRelay
// republishes under a caller-supplied sender PK, so without the allowlist check
// anyone able to reach the relay port could inject messages attributed to any
// PK. Asserts the leaf never lands, not just that no ack came back.
func TestHandleRelay_RejectsNonMember(t *testing.T) {
	member := mustPK(t)
	stranger := mustPK(t)
	s := newRelaySession(t, []cipher.PubKey{member})

	ts := time.Now().UTC()
	_, gotAck := driveRelay(t, s, mustJSON(t, RelayMessage{
		SenderPK: stranger, Text: "injected", TS: ts, MsgID: "m-evil",
	}))

	if gotAck {
		t.Error("a non-member's relay message must not be acked")
	}
	if _, ok := s.pub.Get(relayLeafPath(stranger, ts)); ok {
		t.Error("a non-member's relay message must not reach the feed")
	}
}

// TestHandleRelay_RejectsEmptySenderPK — the zero PK is not a member, and an
// unauthenticated stream must not be able to publish under it.
func TestHandleRelay_RejectsEmptySenderPK(t *testing.T) {
	member := mustPK(t)
	s := newRelaySession(t, []cipher.PubKey{member})

	ts := time.Now().UTC()
	_, gotAck := driveRelay(t, s, mustJSON(t, RelayMessage{
		SenderPK: cipher.PubKey{}, Text: "anon", TS: ts, MsgID: "m-anon",
	}))

	if gotAck {
		t.Error("a relay message with an empty sender PK must not be acked")
	}
	if _, ok := s.pub.Get(relayLeafPath(cipher.PubKey{}, ts)); ok {
		t.Error("a relay message with an empty sender PK must not reach the feed")
	}
}

func TestHandleRelay_RejectsMalformedPayload(t *testing.T) {
	member := mustPK(t)
	s := newRelaySession(t, []cipher.PubKey{member})

	if _, gotAck := driveRelay(t, s, []byte("{not json")); gotAck {
		t.Error("a malformed relay payload must not be acked")
	}
}

// TestHandleRelay_FillsZeroTimestamp — a member that sends no TS still gets
// published, stamped with the owner's clock. The ack is the authoritative
// signal: handleRelay only writes it after publishAs has returned, so an ack
// here proves the zero TS did not fail the publish.
func TestHandleRelay_FillsZeroTimestamp(t *testing.T) {
	member := mustPK(t)
	s := newRelaySession(t, []cipher.PubKey{member})

	ack, gotAck := driveRelay(t, s, mustJSON(t, RelayMessage{
		SenderPK: member, Text: "no timestamp", MsgID: "m-nots",
	}))
	if !gotAck || !ack.Ack {
		t.Fatal("a zero-TS message from a member should still publish and ack")
	}
	if ack.MsgID != "m-nots" {
		t.Errorf("ack MsgID = %q, want m-nots", ack.MsgID)
	}
	// A zero TS would have produced the path .../-6795364578871345152/1
	// (time.Time{}.UnixNano()); the fill-in means that path must NOT exist.
	if _, ok := s.pub.Get(relayLeafPath(member, time.Time{})); ok {
		t.Error("the zero timestamp was published verbatim instead of being filled in")
	}
}

// TestHandleRelay_NoAckForEmptyMsgID — a pre-ack member sends no MsgID and
// cannot parse a response, so writing one would be wasted bytes on a stream it
// is about to close.
func TestHandleRelay_NoAckForEmptyMsgID(t *testing.T) {
	member := mustPK(t)
	s := newRelaySession(t, []cipher.PubKey{member})

	ts := time.Now().UTC()
	_, gotAck := driveRelay(t, s, mustJSON(t, RelayMessage{
		SenderPK: member, Text: "legacy member", TS: ts,
	}))
	if gotAck {
		t.Error("no ack should be written when the member sent no MsgID")
	}
	// But the message must still be published — the member just can't confirm it.
	if _, ok := s.pub.Get(relayLeafPath(member, ts)); !ok {
		t.Error("a pre-ack member's message must still reach the feed")
	}
}

func TestHandleRelay_ReadErrorReturns(t *testing.T) {
	member := mustPK(t)
	s := newRelaySession(t, []cipher.PubKey{member})

	memberEnd, ownerEnd := net.Pipe()
	_ = memberEnd.Close() //nolint:errcheck // hang up before writing anything

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleRelay(ownerEnd)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleRelay should return promptly when the stream is closed")
	}
	_ = ownerEnd.Close() //nolint:errcheck
}

// --- writeAndReadAck --------------------------------------------------------

// ackResponder replies to one framed message on c with the given RelayAck.
// Returns the payload it read so the caller can assert what went out.
func ackResponder(t *testing.T, c net.Conn, reply *RelayAck, garbage []byte) <-chan []byte {
	t.Helper()
	out := make(chan []byte, 1)
	go func() {
		payload, err := message.ReadFrame(c)
		if err != nil {
			close(out)
			return
		}
		out <- payload
		switch {
		case garbage != nil:
			_ = message.WriteFrame(c, garbage) //nolint:errcheck
		case reply != nil:
			b, _ := json.Marshal(reply)  //nolint:errcheck
			_ = message.WriteFrame(c, b) //nolint:errcheck
		default:
			_ = c.Close() //nolint:errcheck // hang up without acking
		}
	}()
	return out
}

func TestWriteAndReadAck_HappyPath(t *testing.T) {
	sender, peer := net.Pipe()
	defer func() { _ = sender.Close() }() //nolint:errcheck
	defer func() { _ = peer.Close() }()   //nolint:errcheck

	sent := ackResponder(t, peer, &RelayAck{Ack: true, MsgID: "m-1"}, nil)
	body := []byte(`{"hello":"relay"}`)

	if err := writeAndReadAck(sender, body, "m-1"); err != nil {
		t.Fatalf("writeAndReadAck: %v", err)
	}
	if got := <-sent; string(got) != string(body) {
		t.Errorf("peer received %q, want %q", got, body)
	}
}

// TestWriteAndReadAck_EmptyMsgIDSkipsRead — with no MsgID there is nothing to
// pair an ack to, so the function must return as soon as the write lands rather
// than blocking for a response that will never come.
func TestWriteAndReadAck_EmptyMsgIDSkipsRead(t *testing.T) {
	sender, peer := net.Pipe()
	defer func() { _ = sender.Close() }() //nolint:errcheck
	defer func() { _ = peer.Close() }()   //nolint:errcheck

	// The peer reads the body but never replies.
	sent := ackResponder(t, peer, nil, nil)

	done := make(chan error, 1)
	go func() { done <- writeAndReadAck(sender, []byte("body"), "") }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("writeAndReadAck with no MsgID = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("writeAndReadAck blocked waiting for an ack it should not expect")
	}
	<-sent
}

func TestWriteAndReadAck_NoAckCases(t *testing.T) {
	cases := []struct {
		name    string
		reply   *RelayAck
		garbage []byte
	}{
		{"peer hangs up without acking", nil, nil},
		{"ack is not JSON", nil, []byte("not-an-ack")},
		{"ack refuses", &RelayAck{Ack: false, MsgID: "m-1"}, nil},
		{"ack is for a different message", &RelayAck{Ack: true, MsgID: "m-OTHER"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sender, peer := net.Pipe()
			defer func() { _ = sender.Close() }() //nolint:errcheck
			defer func() { _ = peer.Close() }()   //nolint:errcheck

			ackResponder(t, peer, c.reply, c.garbage)

			err := writeAndReadAck(sender, []byte("body"), "m-1")
			if err != ErrRelayNoAck { //nolint:errorlint // sentinel is returned verbatim
				t.Errorf("err = %v, want ErrRelayNoAck", err)
			}
		})
	}
}

func TestWriteAndReadAck_WriteFailureIsReported(t *testing.T) {
	sender, peer := net.Pipe()
	_ = peer.Close()   //nolint:errcheck
	_ = sender.Close() //nolint:errcheck

	err := writeAndReadAck(sender, []byte("body"), "m-1")
	if err == nil {
		t.Fatal("writing to a closed conn should error")
	}
	if err == ErrRelayNoAck { //nolint:errorlint // sentinel comparison is intentional
		t.Error("a write failure must be distinguishable from a missing ack")
	}
}

// TestRelayRoundTrip drives the real member half against the real owner half
// over one pipe — the exchange as it happens on the wire.
func TestRelayRoundTrip(t *testing.T) {
	member := mustPK(t)
	s := newRelaySession(t, []cipher.PubKey{member})

	memberEnd, ownerEnd := net.Pipe()
	defer func() { _ = memberEnd.Close() }() //nolint:errcheck
	defer func() { _ = ownerEnd.Close() }()  //nolint:errcheck

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleRelay(ownerEnd)
	}()

	ts := time.Now().UTC()
	msgID := newRelayMsgID()
	body := mustJSON(t, RelayMessage{SenderPK: member, Text: "round trip", TS: ts, MsgID: msgID})

	if err := writeAndReadAck(memberEnd, body, msgID); err != nil {
		t.Fatalf("member side: %v", err)
	}
	<-done

	if _, ok := s.pub.Get(relayLeafPath(member, ts)); !ok {
		t.Error("an acked round trip must leave the message on the feed")
	}
}

func TestNewRelayMsgID_Unique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := newRelayMsgID()
		if len(id) != 16 {
			t.Fatalf("msg id %q is %d chars, want 16 hex", id, len(id))
		}
		if seen[id] {
			t.Fatalf("duplicate relay msg id %q — ack pairing would be ambiguous", id)
		}
		seen[id] = true
	}
}

// --- acceptRelayOn ----------------------------------------------------------

// TestAcceptRelayOn_ServesThenExitsOnClose covers the accept loop: it must
// dispatch an inbound stream to handleRelay and unwind cleanly when the
// listener closes.
func TestAcceptRelayOn_ServesThenExitsOnClose(t *testing.T) {
	member := mustPK(t)
	s := newRelaySession(t, []cipher.PubKey{member})
	s.relayCtx, s.relayCancel = context.WithCancel(context.Background())
	defer s.relayCancel()

	lis := newMemRelayListener()
	s.relayWG.Add(1)
	go s.acceptRelayOn(lis, "test")

	conn, err := lis.dial()
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	ts := time.Now().UTC()
	msgID := "m-accept"
	body := mustJSON(t, RelayMessage{SenderPK: member, Text: "via accept loop", TS: ts, MsgID: msgID})
	if err := writeAndReadAck(conn, body, msgID); err != nil {
		t.Fatalf("relay through the accept loop: %v", err)
	}
	_ = conn.Close() //nolint:errcheck

	if _, ok := s.pub.Get(relayLeafPath(member, ts)); !ok {
		t.Error("the accept loop should have dispatched the stream to handleRelay")
	}

	// Closing the listener ends the loop.
	_ = lis.Close() //nolint:errcheck
	waitWG(t, &s.relayWG, 5*time.Second)
}

// --- Session accessors ------------------------------------------------------

func TestSessionIDAndPort(t *testing.T) {
	me, sk := cipher.GenerateKeyPair()
	id := uuid.NewString()
	s := newRosterSession(t, me, sk, Record{ID: id, OwnerPK: me, Role: RoleOwner})
	s.port = 60123

	if s.ID() != id {
		t.Errorf("ID() = %q, want %q", s.ID(), id)
	}
	if s.Port() != 60123 {
		t.Errorf("Port() = %d, want 60123", s.Port())
	}
}

func TestSessionIsSubscriberAlive(t *testing.T) {
	me, sk := cipher.GenerateKeyPair()

	// Owner role has no legacy subscriber — reported alive so a
	// subscriber-health field never reads "down" for a publisher-only visor.
	owner := newRosterSession(t, me, sk, Record{ID: uuid.NewString(), OwnerPK: me, Role: RoleOwner})
	if !owner.IsSubscriberAlive() {
		t.Error("an owner-role session (no legacy subscriber) should report alive")
	}
	owner.closed.Store(true)
	if !owner.IsSubscriberAlive() {
		t.Error("the nil-sub owner short-circuit runs before the closed check")
	}

	// Member role: health is a pure function of the legacy subscriber's
	// last-inbound, which is what /status surfaces as subscriber_alive.
	newMember := func(t *testing.T) *Session {
		t.Helper()
		mPK, mSK := cipher.GenerateKeyPair()
		s := newRosterSession(t, mPK, mSK, Record{ID: uuid.NewString(), OwnerPK: me, Role: RoleMember})
		sub, err := treestore.NewSubscriberOnNode(s.pub.Node(), me, treestore.SubConfig{Logger: s.log})
		if err != nil {
			t.Fatalf("NewSubscriberOnNode: %v", err)
		}
		t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck
		s.sub = sub
		return s
	}

	// Never observed an inbound → not alive (still converging or wedged).
	fresh := newMember(t)
	if fresh.IsSubscriberAlive() {
		t.Error("a member session that never saw an inbound should not report alive")
	}

	// Recent inbound → alive.
	live := newMember(t)
	live.lastInboundNs.Store(time.Now().UnixNano())
	if !live.IsSubscriberAlive() {
		t.Error("a member session with a recent inbound should report alive")
	}

	// Inbound older than the stale threshold → not alive.
	stale := newMember(t)
	stale.lastInboundNs.Store(time.Now().Add(-subscriberStaleThreshold - time.Minute).UnixNano())
	if stale.IsSubscriberAlive() {
		t.Errorf("an inbound older than %v should report not alive", subscriberStaleThreshold)
	}

	// A closed session is never alive, however fresh its last inbound.
	closed := newMember(t)
	closed.lastInboundNs.Store(time.Now().UnixNano())
	closed.closed.Store(true)
	if closed.IsSubscriberAlive() {
		t.Error("a closed session must never report alive")
	}
}

// --- dialSkynetRelay --------------------------------------------------------

// TestDialSkynetRelay_NoNetworkerErrors — the skynet networker is registered by
// the visor's router init; with none registered the dial must fail fast so the
// caller falls back to dmsg instead of hanging on a 15s dial.
func TestDialSkynetRelay_NoNetworkerErrors(t *testing.T) {
	start := time.Now()
	_, err := dialSkynetRelay(t.Context(), skynetRelayAddr(mustPK(t)))
	if err == nil {
		t.Fatal("dialSkynetRelay should error when no skynet networker is registered")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("failed in %v — the no-networker check should short-circuit before the dial timeout", elapsed)
	}
}
