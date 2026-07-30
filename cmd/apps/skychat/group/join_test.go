// Package group cmd/apps/skychat/group/join_test.go c4-app-chat
// tests for the join-request codec, the transport-identity rule, and the
// approval-queue store.
package group

import (
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/routing"
)

func TestJoinRequestRoundTrip(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	body, err := encodeJoinRequest(NewJoinRequest("gid-1", pk, "let me in"))
	if err != nil {
		t.Fatalf("encodeJoinRequest: %v", err)
	}
	got, err := decodeJoinRequest(body)
	if err != nil {
		t.Fatalf("decodeJoinRequest: %v", err)
	}
	if got.GroupID != "gid-1" || got.RequesterPK != pk || got.Note != "let me in" {
		t.Errorf("round trip lost fields: %+v", got)
	}
}

// An unpatched owner unmarshals any relay frame into RelayMessage. A
// join request must land there with a ZERO sender_pk so it takes the
// existing "empty sender PK" reject path — never mistaken for a chat
// message and published as an empty leaf. This is the whole backward-
// compatibility story, so it is worth pinning.
func TestJoinRequestHasNoSenderPKField(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	body, err := encodeJoinRequest(NewJoinRequest("gid-1", pk, ""))
	if err != nil {
		t.Fatalf("encodeJoinRequest: %v", err)
	}
	var legacy RelayMessage
	if err := json.Unmarshal(body, &legacy); err != nil {
		t.Fatalf("an old owner must at least be able to unmarshal the frame: %v", err)
	}
	if legacy.SenderPK != (cipher.PubKey{}) {
		t.Error("join request populated sender_pk; an old owner would treat it as chat")
	}
	if legacy.Text != "" {
		t.Error("join request populated text; an old owner might publish it")
	}
}

func TestJoinRequestNoteBounded(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	long := make([]byte, maxJoinNoteLen*3)
	for i := range long {
		long[i] = 'x'
	}
	req := NewJoinRequest("gid", pk, string(long))
	if len(req.Note) != maxJoinNoteLen {
		t.Errorf("note len = %d, want it clamped to %d", len(req.Note), maxJoinNoteLen)
	}
	// The receive side must clamp too — a hostile peer builds the frame
	// itself and never calls NewJoinRequest.
	raw, _ := json.Marshal(JoinRequestMsg{
		Kind: frameKindJoinRequest, GroupID: "gid", RequesterPK: pk,
		Note: string(long), TS: time.Now(),
	})
	got, err := decodeJoinRequest(raw)
	if err != nil {
		t.Fatalf("decodeJoinRequest: %v", err)
	}
	if len(got.Note) != maxJoinNoteLen {
		t.Errorf("decoded note len = %d, want clamped to %d", len(got.Note), maxJoinNoteLen)
	}
}

func TestDecodeJoinResponseValidatesStatus(t *testing.T) {
	for _, s := range []JoinStatus{JoinStatusAdmitted, JoinStatusPending, JoinStatusDenied, JoinStatusBanned} {
		body, err := encodeJoinResponse(JoinResponseMsg{GroupID: "g", Status: s})
		if err != nil {
			t.Fatalf("encode %q: %v", s, err)
		}
		if _, err := decodeJoinResponse(body); err != nil {
			t.Errorf("decode %q: %v", s, err)
		}
	}
	bad, _ := json.Marshal(JoinResponseMsg{Kind: frameKindJoinResponse, GroupID: "g", Status: "sideways"})
	if _, err := decodeJoinResponse(bad); err == nil {
		t.Error("unknown status accepted")
	}
	wrongKind, _ := json.Marshal(JoinResponseMsg{Kind: "something-else", Status: JoinStatusAdmitted})
	if _, err := decodeJoinResponse(wrongKind); err == nil {
		t.Error("wrong frame kind accepted")
	}
}

// fakeAddrConn is a net.Conn stub that reports a chosen remote address.
type fakeAddrConn struct {
	net.Conn
	addr net.Addr
}

func (c fakeAddrConn) RemoteAddr() net.Addr { return c.addr }

type plainAddr struct{}

func (plainAddr) Network() string { return "tcp" }
func (plainAddr) String() string  { return "127.0.0.1:1" }

// Identity comes from the authenticated transport, never the envelope.
// Without this an attacker could submit requests naming third parties:
// junk on an admin's queue, and unsolicited membership in an open group.
func TestRequesterPKUsesTransportIdentity(t *testing.T) {
	realPK, _ := cipher.GenerateKeyPair()
	otherPK, _ := cipher.GenerateKeyPair()

	t.Run("dmsg addr wins", func(t *testing.T) {
		c := fakeAddrConn{addr: dmsg.Addr{PK: realPK, Port: 42}}
		got, err := requesterPK(c, JoinRequestMsg{RequesterPK: realPK})
		if err != nil {
			t.Fatalf("requesterPK: %v", err)
		}
		if got != realPK {
			t.Errorf("got %s, want the authenticated %s", got, realPK)
		}
	})

	t.Run("skynet addr wins", func(t *testing.T) {
		c := fakeAddrConn{addr: appnet.Addr{Net: appnet.TypeSkynet, PubKey: realPK, Port: routing.Port(42)}}
		got, err := requesterPK(c, JoinRequestMsg{})
		if err != nil {
			t.Fatalf("requesterPK: %v", err)
		}
		if got != realPK {
			t.Errorf("got %s, want %s", got, realPK)
		}
	})

	t.Run("mismatched claim refused", func(t *testing.T) {
		c := fakeAddrConn{addr: dmsg.Addr{PK: realPK, Port: 42}}
		_, err := requesterPK(c, JoinRequestMsg{RequesterPK: otherPK})
		if !errors.Is(err, ErrJoinIdentityMismatch) {
			t.Errorf("err = %v, want ErrJoinIdentityMismatch", err)
		}
	})

	t.Run("unauthenticated transport refused", func(t *testing.T) {
		c := fakeAddrConn{addr: plainAddr{}}
		_, err := requesterPK(c, JoinRequestMsg{RequesterPK: realPK})
		if !errors.Is(err, ErrJoinNoTransportIdentity) {
			t.Errorf("err = %v, want ErrJoinNoTransportIdentity", err)
		}
	})
}

func TestErrForStatus(t *testing.T) {
	if err := errForStatus(JoinStatusDenied, "nope"); !errors.Is(err, ErrJoinDenied) {
		t.Errorf("denied → %v, want wrapping ErrJoinDenied", err)
	}
	if err := errForStatus(JoinStatusBanned, ""); !errors.Is(err, ErrJoinBanned) {
		t.Errorf("banned → %v, want ErrJoinBanned", err)
	}
	if err := errForStatus(JoinStatusAdmitted, ""); err != nil {
		t.Errorf("admitted → %v, want nil", err)
	}
	if err := errForStatus(JoinStatusPending, ""); err != nil {
		t.Errorf("pending → %v, want nil", err)
	}
}

func TestErrIsTerminalJoin(t *testing.T) {
	if !errIsTerminalJoin(ErrJoinDenied) || !errIsTerminalJoin(ErrJoinBanned) {
		t.Error("explicit refusals must stop the retry loop")
	}
	// A transport failure is retryable — treating it as terminal would
	// strand a requester whose group was merely offline.
	if errIsTerminalJoin(ErrJoinNoResponse) {
		t.Error("a no-response transport failure must stay retryable")
	}
}

// A private invite must round-trip WITHOUT a key. That is the whole
// point of key-on-approval: the link is coordinates, not a secret. The
// codec used to hard-require 32 bytes for private mode, which would
// have made a keyless link unparseable.
func TestPrivateInviteRoundTripsWithoutKey(t *testing.T) {
	owner, _ := cipher.GenerateKeyPair()
	link, err := EncodeInvite(Invite{
		ID: "g1", Name: "room", OwnerPK: owner, Port: 8080, Mode: ModePrivate,
	})
	if err != nil {
		t.Fatalf("EncodeInvite without a key: %v", err)
	}
	got, err := DecodeInvite(link)
	if err != nil {
		t.Fatalf("DecodeInvite without a key: %v", err)
	}
	if len(got.AESKey) != 0 {
		t.Errorf("keyless invite came back with a key: %x", got.AESKey)
	}
	if got.Mode != ModePrivate {
		t.Errorf("Mode = %q, want %q", got.Mode, ModePrivate)
	}

	// A legacy link that still carries a key stays valid.
	legacy, err := EncodeInvite(Invite{
		ID: "g1", Name: "room", OwnerPK: owner, Port: 8080, Mode: ModePrivate,
		AESKey: make([]byte, 32),
	})
	if err != nil {
		t.Fatalf("EncodeInvite with a legacy key: %v", err)
	}
	if _, err := DecodeInvite(legacy); err != nil {
		t.Errorf("legacy keyed invite no longer parses: %v", err)
	}

	// A wrong-sized key is still an error either way.
	if _, err := EncodeInvite(Invite{
		ID: "g1", OwnerPK: owner, Port: 1, Mode: ModePrivate, AESKey: []byte{1, 2, 3},
	}); err == nil {
		t.Error("a short AES key was accepted")
	}
}

func TestSortJoinRequestsNewestFirst(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	reqs := []JoinRequest{
		{PK: a, AskedAt: base},
		{PK: b, AskedAt: base.Add(time.Hour)},
	}
	sortJoinRequests(reqs)
	if !reqs[0].AskedAt.After(reqs[1].AskedAt) {
		t.Error("queue is not newest-first")
	}
	// Same instant → deterministic order by PK, so the two Store
	// backends (bbolt key order vs Go map order) agree.
	tie := []JoinRequest{{PK: b, AskedAt: base}, {PK: a, AskedAt: base}}
	sortJoinRequests(tie)
	if tie[0].PK.Hex() > tie[1].PK.Hex() {
		t.Error("tie-break is not stable by PK")
	}
}

func TestStoreJoinRequestLifecycle(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "groups.db"), testStoreSK())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer st.Close() //nolint:errcheck

	pk, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()

	if _, found, err := st.GetJoinRequest("g1", pk); err != nil || found {
		t.Fatalf("empty store: found=%v err=%v", found, err)
	}

	req := JoinRequest{GroupID: "g1", PK: pk, Note: "hi", AskedAt: time.Now().UTC(), Status: JoinStatusPending}
	if err := st.PutJoinRequest(req); err != nil {
		t.Fatalf("PutJoinRequest: %v", err)
	}
	if err := st.PutJoinRequest(JoinRequest{GroupID: "g1", PK: other, AskedAt: time.Now().UTC(), Status: JoinStatusPending}); err != nil {
		t.Fatalf("PutJoinRequest other: %v", err)
	}
	// A different group's queue must not leak into g1's — the composite
	// key's prefix scan is what keeps them apart.
	if err := st.PutJoinRequest(JoinRequest{GroupID: "g2", PK: pk, AskedAt: time.Now().UTC(), Status: JoinStatusPending}); err != nil {
		t.Fatalf("PutJoinRequest g2: %v", err)
	}

	got, found, err := st.GetJoinRequest("g1", pk)
	if err != nil || !found {
		t.Fatalf("GetJoinRequest: found=%v err=%v", found, err)
	}
	if got.Note != "hi" || !got.IsPending() {
		t.Errorf("unexpected request: %+v", got)
	}

	list, err := st.ListJoinRequests("g1")
	if err != nil {
		t.Fatalf("ListJoinRequests: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("g1 queue has %d entries, want 2", len(list))
	}

	// A decision overwrites in place rather than adding a row.
	req.Status = JoinStatusDenied
	req.DecidedAt = time.Now().UTC()
	if err := st.PutJoinRequest(req); err != nil {
		t.Fatalf("PutJoinRequest decision: %v", err)
	}
	list, _ = st.ListJoinRequests("g1")
	if len(list) != 2 {
		t.Errorf("queue grew to %d on a decision update, want 2", len(list))
	}

	if err := st.DeleteJoinRequests("g1"); err != nil {
		t.Fatalf("DeleteJoinRequests: %v", err)
	}
	if list, _ = st.ListJoinRequests("g1"); len(list) != 0 {
		t.Errorf("g1 queue not cleared: %d left", len(list))
	}
	if list, _ = st.ListJoinRequests("g2"); len(list) != 1 {
		t.Errorf("clearing g1 also removed g2's queue: %d left", len(list))
	}
}

func TestStoreModerationRoundTrip(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "groups.db"), testStoreSK())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer st.Close() //nolint:errcheck

	owner, _ := cipher.GenerateKeyPair()
	victim, _ := cipher.GenerateKeyPair()
	rec := Record{ID: "g1", OwnerPK: owner, Mode: ModePublic, Kind: KindPublic, Members: []cipher.PubKey{owner}}
	if err := st.Put(rec); err != nil {
		t.Fatalf("Put: %v", err)
	}

	at := time.Now().UTC().Truncate(time.Millisecond)
	st2 := ModState{
		Banned:        []cipher.PubKey{victim},
		Muted:         []cipher.PubKey{victim},
		MutedSince:    map[string]time.Time{victim.Hex(): at},
		ReadOnly:      true,
		ReadOnlySince: at,
	}
	if err := st.SetModeration("g1", st2); err != nil {
		t.Fatalf("SetModeration: %v", err)
	}
	got, ok, err := st.Get("g1")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if !got.IsBanned(victim) || !got.IsMuted(victim) || !got.ReadOnly {
		t.Errorf("moderation state did not persist: %+v", got)
	}
	if since := got.MuteEffectiveFrom(victim); !since.Equal(at) {
		t.Errorf("MutedSince = %v, want %v", since, at)
	}
	if !got.ReadOnlySince.Equal(at) {
		t.Errorf("ReadOnlySince = %v, want %v", got.ReadOnlySince, at)
	}
}

// A record persisted before Kind existed must come back normalized, so
// no caller ever has to handle the zero value.
func TestStoreNormalizesKindOnRead(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "groups.db"), testStoreSK())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer st.Close() //nolint:errcheck

	owner, _ := cipher.GenerateKeyPair()
	if err := st.Put(Record{ID: "legacy", OwnerPK: owner, Mode: ModePrivate}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := st.Get("legacy")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Kind != KindPrivate {
		t.Errorf("Get did not normalize Kind: %q", got.Kind)
	}
	if got.JoinPolicy() != JoinApproval {
		t.Errorf("legacy private group is not approval-gated: %q", got.JoinPolicy())
	}
	all, err := st.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 || all[0].Kind != KindPrivate {
		t.Errorf("List did not normalize Kind: %+v", all)
	}
}
