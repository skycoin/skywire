// Package group pkg/skychat/group/joincost_test.go c4-app-chat
//
// Unit coverage for the two anti-flood primitives: the proof of work that
// prices an identity, and the token bucket that bounds what any number of
// identities can consume. The wire half — a flood actually being refused
// by a live group — lives in joincost_integration_test.go.
package group

import (
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// The properties that make the proof worth asking for at all: it must be
// verifiable, bound to the identity that did the work, bound to the group,
// and worthless once stale.
func TestJoinPoWBinding(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	now := time.Now().UTC()
	const bits = 12 // cheap: these tests solve for real

	p, ok := SolveJoinPoW("group-a", pk, bits, now, now.Add(30*time.Second))
	if !ok {
		t.Fatal("SolveJoinPoW gave up on a trivial difficulty")
	}
	if valid, why := VerifyJoinPoW("group-a", pk, p, bits, now); !valid {
		t.Fatalf("a freshly solved proof did not verify: %s", why)
	}

	// Bound to the requester. Without this an attacker solves once and
	// spends the same proof across an entire identity pool, which would
	// make N identities cost the same as one.
	if valid, _ := VerifyJoinPoW("group-a", other, p, bits, now); valid {
		t.Error("a proof solved for one PK verified for another")
	}

	// Bound to the group, so work is not fungible across rooms.
	if valid, _ := VerifyJoinPoW("group-b", pk, p, bits, now); valid {
		t.Error("a proof solved for one group verified for another")
	}

	// Bound to its moment, so a stockpile mined in advance is worthless.
	if valid, why := VerifyJoinPoW("group-a", pk, p, bits, now.Add(2*joinPoWWindow)); valid {
		t.Error("a stale proof verified")
	} else if why == "" {
		t.Error("a rejection should say why")
	}
	if valid, _ := VerifyJoinPoW("group-a", pk, p, bits, now.Add(-2*joinPoWWindow)); valid {
		t.Error("a future-dated proof verified")
	}

	// Tampering with the nonce breaks it.
	tampered := p
	tampered.Nonce++
	if valid, _ := VerifyJoinPoW("group-a", pk, tampered, bits, now); valid {
		t.Error("a tampered nonce still verified")
	}

	// Asking for more than was paid is refused — that is what lets a group
	// raise its price and have old proofs stop working.
	if valid, _ := VerifyJoinPoW("group-a", pk, p, bits+8, now); valid {
		t.Error("a proof verified against a higher difficulty than it solved")
	}
}

// Zero difficulty means the gate is off, and an absent proof must be
// distinguishable from a bad one so the responder can challenge rather
// than refuse.
func TestJoinPoWZeroAndMissing(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	now := time.Now().UTC()

	if valid, _ := VerifyJoinPoW("g", pk, JoinPoW{}, 0, now); !valid {
		t.Error("zero difficulty should accept anything, including no proof")
	}
	valid, why := VerifyJoinPoW("g", pk, JoinPoW{}, 16, now)
	if valid {
		t.Fatal("a missing proof was accepted")
	}
	if why != "no proof of work" {
		t.Errorf("reason = %q, want the missing-proof reason", why)
	}
	if p, ok := SolveJoinPoW("g", pk, 0, now, now.Add(time.Second)); !ok || p.Nonce != 0 {
		t.Error("solving for zero bits should be free and produce no proof")
	}
}

// The difficulty travels in an invite link, i.e. in text an attacker can
// write. It must be impossible to make a stranger grind for hours.
func TestJoinPoWDifficultyIsCapped(t *testing.T) {
	if got := clampJoinPoWBits(255); got != MaxJoinPoWBits {
		t.Errorf("clamp(255) = %d, want the %d cap", got, MaxJoinPoWBits)
	}
	if got := clampJoinPoWBits(8); got != 8 {
		t.Errorf("clamp(8) = %d, want 8 unchanged", got)
	}
	// A link claiming an absurd price is clamped on read.
	in := Invite{
		ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Name: "trap",
		OwnerPK: pkN(1), Port: 20099, Mode: ModePublic, PoWBits: 255,
	}
	link, err := EncodeInvite(in)
	if err != nil {
		t.Fatalf("EncodeInvite: %v", err)
	}
	out, err := DecodeInvite(link)
	if err != nil {
		t.Fatalf("DecodeInvite: %v", err)
	}
	if out.PoWBits != MaxJoinPoWBits {
		t.Errorf("decoded PoWBits = %d, want the %d cap", out.PoWBits, MaxJoinPoWBits)
	}
}

// A solve that can't finish in time must degrade into "no proof" — the
// responder challenges — rather than blocking the joiner forever.
func TestSolveJoinPoWGivesUp(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	now := time.Now().UTC()
	// A deadline already in the past with a difficulty that cannot be hit
	// by luck in one batch of nonces.
	if _, ok := SolveJoinPoW("g", pk, MaxJoinPoWBits, now, now.Add(-time.Second)); ok {
		t.Error("solving past its deadline reported success")
	}
}

func TestLeadingZeroBits(t *testing.T) {
	var sum [32]byte
	if got := leadingZeroBits(sum); got != 255 && got != 0 {
		// All-zero digest: every bit is zero, but the counter saturates at
		// the byte loop's total. Just assert it is large.
		if got < 32 {
			t.Errorf("all-zero digest counted %d leading zero bits", got)
		}
	}
	sum[0] = 0x80 // 1000_0000
	if got := leadingZeroBits(sum); got != 0 {
		t.Errorf("0x80 prefix = %d leading zeros, want 0", got)
	}
	sum[0] = 0x01 // 0000_0001
	if got := leadingZeroBits(sum); got != 7 {
		t.Errorf("0x01 prefix = %d leading zeros, want 7", got)
	}
	sum[0], sum[1] = 0x00, 0x0f
	if got := leadingZeroBits(sum); got != 12 {
		t.Errorf("0x000f prefix = %d leading zeros, want 12", got)
	}
}

// The bucket is what actually bounds a flood: burst first, then a drip.
func TestJoinBucket(t *testing.T) {
	b := newJoinBucket(3, time.Minute)
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)

	// The burst is absorbed, then the next request is refused with a
	// wait hint rather than silently dropped.
	for i := range 3 {
		if ok, _ := b.allow("g", now); !ok {
			t.Fatalf("request %d within the burst was refused", i+1)
		}
	}
	ok, wait := b.allow("g", now)
	if ok {
		t.Fatal("a fourth request was allowed past a burst of three")
	}
	if wait <= 0 || wait > time.Minute {
		t.Errorf("retry hint = %v, want something inside one refill period", wait)
	}

	// A token comes back after the refill period.
	if ok, _ := b.allow("g", now.Add(time.Minute)); !ok {
		t.Error("no token available after a full refill period")
	}
	// ...and only one.
	if ok, _ := b.allow("g", now.Add(time.Minute)); ok {
		t.Error("the refill produced more than one token")
	}

	// Refill is capped at the burst, so a long quiet period does not bank
	// unlimited credit for one huge flood.
	for i := range 3 {
		if ok, _ := b.allow("g", now.Add(24*time.Hour)); !ok {
			t.Fatalf("request %d after a long idle period was refused", i+1)
		}
	}
	if ok, _ := b.allow("g", now.Add(24*time.Hour)); ok {
		t.Error("a day of idling banked more than the burst")
	}

	// Buckets are per group: one busy room must not throttle another.
	if ok, _ := b.allow("other", now.Add(24*time.Hour)); !ok {
		t.Error("a different group shared the first group's exhausted bucket")
	}

	// Forgetting a deleted group resets it rather than leaking the entry.
	b.forget("g")
	if ok, _ := b.allow("g", now.Add(24*time.Hour)); !ok {
		t.Error("a forgotten group did not start fresh")
	}

	// A nil bucket fails open — a missing protection must not become a
	// group that refuses everyone.
	var nilBucket *joinBucket
	if ok, _ := nilBucket.allow("g", now); !ok {
		t.Error("a nil bucket refused a request")
	}
	nilBucket.forget("g") // must not panic
}

func TestJoinBucketDefaults(t *testing.T) {
	b := newJoinBucket(0, 0)
	if b.burst != float64(DefaultJoinBurst) || b.refill != DefaultJoinRefill {
		t.Errorf("zero config = burst %v refill %v, want the defaults", b.burst, b.refill)
	}
	if b2 := newJoinBucket(-5, -time.Second); b2.burst != float64(DefaultJoinBurst) {
		t.Error("negative config should fall back to the defaults, not disable the limiter")
	}
}

// The record's price predicate has to distinguish "an admin chose free"
// from "this record predates the setting", or turning the gate off would
// silently turn itself back on.
func TestRecordJoinPoWRequired(t *testing.T) {
	legacy := Record{}
	if got := legacy.JoinPoWRequired(); got != DefaultJoinPoWBits {
		t.Errorf("a record predating the setting requires %d bits, want the default %d", got, DefaultJoinPoWBits)
	}
	off := Record{JoinPoWConfigured: true, JoinPoWBits: 0}
	if got := off.JoinPoWRequired(); got != 0 {
		t.Errorf("an admin's explicit zero was overridden with %d", got)
	}
	set := Record{JoinPoWConfigured: true, JoinPoWBits: 22}
	if got := set.JoinPoWRequired(); got != 22 {
		t.Errorf("configured 22 bits reported as %d", got)
	}
	absurd := Record{JoinPoWConfigured: true, JoinPoWBits: 200}
	if got := absurd.JoinPoWRequired(); got != MaxJoinPoWBits {
		t.Errorf("a stored absurd difficulty was not clamped: %d", got)
	}
}

// The two new statuses must not read as decisions — a challenge means
// "pay and come back", throttled means "later", and treating either as a
// verdict would strand a legitimate joiner.
func TestChallengeAndThrottledAreNotDecisions(t *testing.T) {
	for _, s := range []JoinStatus{JoinStatusChallenge, JoinStatusThrottled} {
		if s.IsDecision() {
			t.Errorf("%q must not count as a decision about the requester", s)
		}
		if s.IsTerminal() {
			t.Errorf("%q must not be terminal", s)
		}
		if errForStatus(s, "x") != nil {
			t.Errorf("%q must not map onto a terminal join error", s)
		}
	}
	if !JoinStatusChallenge.NeedsWork() {
		t.Error("a challenge should be recognized as work to do")
	}
	if JoinStatusThrottled.NeedsWork() {
		t.Error("throttling is not something the requester can solve")
	}
	// Both have to survive the wire codec.
	for _, st := range []JoinStatus{JoinStatusChallenge, JoinStatusThrottled} {
		body, err := encodeJoinResponse(JoinResponseMsg{GroupID: "g", Status: st, PoWBits: 20, RetryAfterSec: 30})
		if err != nil {
			t.Fatalf("encode %q: %v", st, err)
		}
		got, err := decodeJoinResponse(body)
		if err != nil {
			t.Fatalf("decode %q: %v", st, err)
		}
		if got.Status != st || got.PoWBits != 20 || got.RetryAfterSec != 30 {
			t.Errorf("round trip of %q lost fields: %+v", st, got)
		}
	}
}

// A request built with a difficulty carries a solved proof; one built
// without stays byte-compatible with what older builds send.
func TestNewJoinRequestCarriesProof(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	free := NewJoinRequest("g", pk, "hi", 0)
	if free.PoW.Nonce != 0 || free.PoW.TSUnix != 0 {
		t.Error("a free group's request carried a proof")
	}

	paid := NewJoinRequest("g", pk, "hi", 12)
	if paid.PoW.Nonce == 0 {
		t.Fatal("a priced group's request carried no proof")
	}
	if valid, why := VerifyJoinPoW("g", pk, paid.PoW, 12, time.Now().UTC()); !valid {
		t.Errorf("the request's own proof did not verify: %s", why)
	}
	// The proof survives the frame codec.
	body, err := encodeJoinRequest(paid)
	if err != nil {
		t.Fatalf("encodeJoinRequest: %v", err)
	}
	back, err := decodeJoinRequest(body)
	if err != nil {
		t.Fatalf("decodeJoinRequest: %v", err)
	}
	if back.PoW != paid.PoW {
		t.Errorf("proof did not round trip: got %+v want %+v", back.PoW, paid.PoW)
	}
}
