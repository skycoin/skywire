// Package group cmd/apps/skychat/group/admission_targets_test.go
//
// Unit coverage for the multi-admin admission fan-out's pure half: which
// PKs a joiner will ask, in what order, and what the invite carries to
// tell it. The wire half (a founder-less group that still admits) needs a
// transport and lives in admission_integration_test.go.
package group

import (
	"errors"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestAdmissionOrder(t *testing.T) {
	founder, b, c, self := pkN(1), pkN(2), pkN(3), pkN(4)

	// Founder first, then the rest in the order given.
	got := admissionOrder(founder, self, []cipher.PubKey{b, c})
	want := []cipher.PubKey{founder, b, c}
	if len(got) != len(want) {
		t.Fatalf("got %d targets, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("target %d = %s, want %s", i, got[i], want[i])
		}
	}

	// Zero keys, duplicates (across lists as well as within one) and self
	// are all dropped: an invite naming us is normal, and dialing our own
	// relay would waste a round trip on an answer we hold locally.
	got = admissionOrder(founder, self, []cipher.PubKey{{}, b, self, founder}, []cipher.PubKey{b, c})
	want = []cipher.PubKey{founder, b, c}
	if len(got) != len(want) {
		t.Fatalf("dedupe: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("dedupe: target %d = %s, want %s", i, got[i], want[i])
		}
	}

	// A zero founder is not a candidate, but the rest still are — that is
	// the shape a Record built by hand in a test can have.
	if got := admissionOrder(cipher.PubKey{}, self, []cipher.PubKey{b}); len(got) != 1 || got[0] != b {
		t.Errorf("zero founder: got %v, want [%s]", got, b)
	}

	// Truncated at the cap so a hostile invite can't turn one paste into
	// an unbounded fan-out of dials.
	long := make([]cipher.PubKey, maxAdmissionTargets+5)
	for i := range long {
		long[i] = pkN(byte(i))
	}
	if got := admissionOrder(founder, self, long); len(got) != maxAdmissionTargets {
		t.Errorf("cap: got %d targets, want %d", len(got), maxAdmissionTargets)
	}

	// Nothing to ask at all — the caller has to notice rather than dial
	// the zero PK.
	if got := admissionOrder(cipher.PubKey{}, self, nil); len(got) != 0 {
		t.Errorf("empty: got %v, want none", got)
	}
	if got := admissionOrder(self, self); len(got) != 0 {
		t.Errorf("self-only: got %v, want none", got)
	}
}

// An invite is the only thing a joiner has before its first round trip,
// so what it names is what decides whether a dead founder is fatal.
func TestInviteAdmissionTargets(t *testing.T) {
	founder, b, joiner := pkN(1), pkN(2), pkN(3)

	inv := Invite{OwnerPK: founder, Admins: []cipher.PubKey{b}}
	got := inv.AdmissionTargets(joiner)
	if len(got) != 2 || got[0] != founder || got[1] != b {
		t.Fatalf("got %v, want [founder %s, admin %s]", got, founder, b)
	}

	// A link minted before Invite.Admins existed resolves to exactly the
	// old behavior: the founder, alone.
	legacy := Invite{OwnerPK: founder}
	if got := legacy.AdmissionTargets(joiner); len(got) != 1 || got[0] != founder {
		t.Errorf("legacy link: got %v, want [%s]", got, founder)
	}
}

// Once queued for approval, the re-ask has to reach the same set — the
// founder being the one PK we're trying not to depend on.
func TestRecordAdmissionTargets(t *testing.T) {
	founder, invited, learned, self := pkN(1), pkN(2), pkN(3), pkN(4)

	r := Record{
		OwnerPK:      founder,
		AdmissionPKs: []cipher.PubKey{invited},
		Admins:       []cipher.PubKey{founder, learned},
	}
	got := r.AdmissionTargets(self)
	want := []cipher.PubKey{founder, invited, learned}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("target %d = %s, want %s", i, got[i], want[i])
		}
	}
}

// The invite's admin list is built by whichever admin issued the link.
func TestInviteAdmins(t *testing.T) {
	founder, b, c := pkN(1), pkN(2), pkN(3)
	r := Record{OwnerPK: founder, Admins: []cipher.PubKey{founder, b, c}}

	// Issued by the founder: the founder travels as OwnerPK, so it must
	// not be repeated in the list.
	got := inviteAdmins(r, founder)
	if len(got) != 2 || got[0] != b || got[1] != c {
		t.Fatalf("founder-issued: got %v, want [%s %s]", got, b, c)
	}

	// Issued by a non-founder admin: that admin comes first. It is
	// demonstrably alive, it is the one likely to be watching for the
	// request, and being first means it is never what the cap trims.
	got = inviteAdmins(r, b)
	if len(got) != 2 || got[0] != b || got[1] != c {
		t.Fatalf("admin-issued: got %v, want [%s %s]", got, b, c)
	}

	// Room is left for the founder, which the joiner adds back at the head
	// from OwnerPK.
	many := Record{OwnerPK: founder}
	for i := 0; i < maxAdmissionTargets+3; i++ {
		many.Admins = append(many.Admins, pkN(byte(i)))
	}
	if got := inviteAdmins(many, founder); len(got) != maxAdmissionTargets-1 {
		t.Errorf("cap: got %d admins in link, want %d", len(got), maxAdmissionTargets-1)
	}

	// A founder-only group names nobody else — the link is exactly what it
	// was before this field existed.
	solo := Record{OwnerPK: founder, Admins: []cipher.PubKey{founder}}
	if got := inviteAdmins(solo, founder); len(got) != 0 {
		t.Errorf("solo founder: got %v, want none", got)
	}
}

// The admin list has to survive the link's encode/decode round trip, and
// a link without it has to stay decodable.
func TestInviteRoundTripCarriesAdmins(t *testing.T) {
	in := Invite{
		ID:      "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		Name:    "ops",
		OwnerPK: pkN(1),
		Admins:  []cipher.PubKey{pkN(2), pkN(3)},
		Port:    20002,
		Mode:    ModePublic,
	}
	link, err := EncodeInvite(in)
	if err != nil {
		t.Fatalf("EncodeInvite: %v", err)
	}
	out, err := DecodeInvite(link)
	if err != nil {
		t.Fatalf("DecodeInvite: %v", err)
	}
	if len(out.Admins) != len(in.Admins) {
		t.Fatalf("Admins: got %v, want %v", out.Admins, in.Admins)
	}
	for i := range in.Admins {
		if out.Admins[i] != in.Admins[i] {
			t.Errorf("admin %d = %s, want %s", i, out.Admins[i], in.Admins[i])
		}
	}
}

// "Ask someone else" must not read as "no" anywhere in the requester's
// decision logic — that distinction is the whole point of the status.
func TestJoinStatusUnavailableIsNotADecision(t *testing.T) {
	if JoinStatusUnavailable.IsTerminal() {
		t.Error("unavailable must not be terminal — another admin may still admit")
	}
	if JoinStatusUnavailable.IsDecision() {
		t.Error("unavailable is an answer about the responder, not a decision about the requester")
	}
	for _, s := range []JoinStatus{JoinStatusAdmitted, JoinStatusPending, JoinStatusDenied, JoinStatusBanned} {
		if !s.IsDecision() {
			t.Errorf("%q should count as a decision", s)
		}
	}
	if errForStatus(JoinStatusUnavailable, "whatever") != nil {
		t.Error("unavailable must not map onto a terminal join error")
	}
}

// The status also has to survive the wire codec, or a responder saying
// "ask someone else" would look like a malformed frame.
func TestDecodeJoinResponseAcceptsUnavailable(t *testing.T) {
	body, err := encodeJoinResponse(JoinResponseMsg{
		GroupID: "g", Status: JoinStatusUnavailable, Reason: "demoted",
	})
	if err != nil {
		t.Fatalf("encodeJoinResponse: %v", err)
	}
	got, err := decodeJoinResponse(body)
	if err != nil {
		t.Fatalf("decodeJoinResponse: %v", err)
	}
	if got.Status != JoinStatusUnavailable || got.Reason != "demoted" {
		t.Errorf("round trip: got %+v", got)
	}
}

func TestSendJoinRequestAnyNoCandidates(t *testing.T) {
	_, err := SendJoinRequestAny(t.Context(), nil, "g", nil, 20003, pkN(1), "", 0)
	if !errors.Is(err, ErrJoinNoAdmin) {
		t.Errorf("got %v, want ErrJoinNoAdmin", err)
	}
}

// The last candidate must still get a full response window, or adding an
// admin to an invite would silently start timing joins out mid-answer.
func TestJoinAttemptBudget(t *testing.T) {
	one := joinAttemptBudget(1)
	if one < joinResponseReadTimeout {
		t.Errorf("single-candidate budget %v is shorter than one response window %v", one, joinResponseReadTimeout)
	}
	if joinAttemptBudget(0) != one {
		t.Error("a zero count should be treated as one candidate, not as no budget")
	}
	four := joinAttemptBudget(4)
	if want := one + 3*joinCandidateStagger; four != want {
		t.Errorf("four-candidate budget = %v, want %v", four, want)
	}
	if lastStart := 3 * joinCandidateStagger; four-lastStart < joinResponseReadTimeout {
		t.Errorf("last candidate gets %v to answer, less than the %v response window",
			four-lastStart, joinResponseReadTimeout)
	}
}
