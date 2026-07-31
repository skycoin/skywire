// Package group pkg/skychat/group/joincost.go c4-app-chat
// what it costs to ask to join, and how fast anyone may ask.
//
// # The problem
//
// A skywire public key is free: `cipher.GenerateKeyPair()` and you are
// somebody else. Every gate in this package is per-PK — the ban list, the
// approval queue, the allowlist — so an attacker who can mint identities
// faster than an admin can click "decline" wins by arithmetic. Concretely,
// before this file:
//
//   - An open group auto-admits whoever asks, so N identities meant N
//     members, N feeds every member subscribes to, and N senders. The
//     roster grows without bound and there is no per-PK gate to hit.
//   - A private group queues whoever asks, so N identities meant N rows
//     in every admin's store and N notifications. The queue was the
//     denial-of-service surface: burying real requests under junk costs
//     the attacker nothing and costs the admin their attention.
//   - Banning is per-PK and therefore strictly slower than joining.
//
// No central registry can fix that here, and there is nothing to charge.
// What is available is to make identities cost CPU and to bound the rate
// at which any of them can consume an admin's resources.
//
// # Two mechanisms, deliberately different in kind
//
// **Proof of work** (this file's PoW half) puts a price on each identity.
// A requester must find a nonce whose hash over (group, its own PK, a
// recent timestamp) has N leading zero bits. Verification is one hash;
// production is 2^N. It does not stop an attacker — it converts "free"
// into "measurable", which is the difference between a script and a
// budget. Difficulty is per-group so an operator under attack can raise
// the price without asking anyone to upgrade.
//
// **Rate limiting** (the bucket half) bounds the damage per unit time
// regardless of how many identities exist or how cheap they were. This is
// the part that actually protects the admin: even a perfectly executed
// flood cannot enqueue faster than the bucket refills, and the queue cap
// means it cannot enqueue more than a bounded amount at all.
//
// PoW without the bucket would let a well-funded attacker back in at
// scale; the bucket without PoW would still let free identities occupy
// every slot the bucket allows. Together the attacker pays per identity
// AND cannot convert money into unbounded queue depth.
//
// # What this does not claim
//
// It is not Sybil RESISTANCE. An attacker who wants in badly enough will
// pay, and a distributed one pays in parallel. The goal is proportionality:
// make a flood cost enough, and drip slowly enough, that a human admin's
// decline button keeps up with it.
package group

import (
	"crypto/sha256"
	"encoding/binary"
	"math/bits"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// powDomain separates this hash from every other use of SHA-256 in the
// package, so a proof can never be replayed as a signature preimage or
// vice versa.
const powDomain = "skychat:group-join-pow:v1"

// Difficulty bounds, in leading zero bits.
const (
	// DefaultJoinPoWBits is what a group asks for unless an admin says
	// otherwise. ~2^18 hashes is a few tens of milliseconds on one core —
	// far below the threshold where a person joining a group notices, and
	// enough that ten thousand identities is minutes of CPU rather than a
	// loop that finishes before you look up.
	//
	// It is deliberately not zero. A default of "off" would mean the
	// protection exists only for operators who already knew to turn it on,
	// which in practice means after the flood.
	DefaultJoinPoWBits uint8 = 18

	// MaxJoinPoWBits caps what any group may demand. The difficulty
	// travels in an invite link, i.e. in attacker-supplied text, and a
	// joiner solves it before it can check anything else — an unbounded
	// value would be a way to burn a stranger's CPU by handing them a
	// link. 26 bits is seconds, not hours.
	MaxJoinPoWBits uint8 = 26
)

// joinPoWWindow is how far a proof's timestamp may be from the verifier's
// clock. Bounds precomputation: work done today is worthless tomorrow, so
// an attacker cannot mine a stockpile of identities in advance and spend
// them all at once. Wide enough for ordinary clock skew plus the solve
// time itself.
const joinPoWWindow = 10 * time.Minute

// JoinPoW is a solved proof of work carried by a join request.
type JoinPoW struct {
	// TSUnix is the timestamp the proof is bound to, in Unix seconds.
	// Inside the hash, so changing it invalidates the work.
	TSUnix int64 `json:"ts_unix"`

	// Nonce is the value the requester searched for.
	Nonce uint64 `json:"nonce"`

	// Bits is the difficulty the requester believes was asked of it.
	// Advisory: the verifier checks the hash against ITS OWN requirement
	// and ignores this. Carried so a mismatch is visible in logs when a
	// stale invite is circulating.
	Bits uint8 `json:"bits"`
}

// joinPoWPreimage builds the bytes a proof hashes over.
//
// Every field is load-bearing. The domain separates the construction; the
// group ID stops work for one group being spent on another; the
// requester's PK stops one solved proof being shared across an attacker's
// whole identity pool — which would otherwise reduce the cost of N
// identities to the cost of one; and the timestamp bounds precomputation.
func joinPoWPreimage(groupID string, requester cipher.PubKey, tsUnix int64, nonce uint64) []byte {
	buf := make([]byte, 0, len(powDomain)+1+len(groupID)+1+33+8+8)
	buf = append(buf, powDomain...)
	buf = append(buf, '|')
	buf = append(buf, groupID...)
	buf = append(buf, '|')
	buf = append(buf, requester[:]...)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(tsUnix)) //nolint:gosec
	buf = append(buf, ts[:]...)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], nonce)
	buf = append(buf, n[:]...)
	return buf
}

// leadingZeroBits counts the zero bits at the front of a digest.
func leadingZeroBits(sum [32]byte) uint8 {
	var n uint8
	for _, b := range sum {
		if b != 0 {
			return n + uint8(bits.LeadingZeros8(b)) //nolint:gosec
		}
		n += 8
	}
	return n
}

// joinPoWStrength returns how many leading zero bits a proof actually
// achieves.
func joinPoWStrength(groupID string, requester cipher.PubKey, p JoinPoW) uint8 {
	return leadingZeroBits(sha256.Sum256(joinPoWPreimage(groupID, requester, p.TSUnix, p.Nonce)))
}

// VerifyJoinPoW checks a proof against the difficulty this group requires
// right now. Returns the reason when it fails, for the log and for the
// challenge sent back.
//
// The requester's PK comes from the AUTHENTICATED transport, never from
// the envelope (see join.go), so the proof is bound to an identity the
// requester actually holds rather than one it claims.
func VerifyJoinPoW(groupID string, requester cipher.PubKey, p JoinPoW, requiredBits uint8, now time.Time) (bool, string) {
	if requiredBits == 0 {
		return true, ""
	}
	if p.Nonce == 0 && p.TSUnix == 0 {
		return false, "no proof of work"
	}
	ts := time.Unix(p.TSUnix, 0)
	if d := now.Sub(ts); d > joinPoWWindow || d < -joinPoWWindow {
		// Stale or future-dated. Both are the same defect: work that was
		// not done for this moment.
		return false, "proof of work is outside the accepted time window"
	}
	if got := joinPoWStrength(groupID, requester, p); got < requiredBits {
		return false, "proof of work is too weak"
	}
	return true, ""
}

// SolveJoinPoW searches for a nonce meeting bits, giving up when ctx-less
// deadline passes. Returns the proof and whether it succeeded.
//
// Deliberately simple and single-threaded: at the difficulties this
// package allows it finishes in well under a second, and burning every
// core of a joiner's machine to shave milliseconds off a once-per-group
// operation would be the wrong trade.
func SolveJoinPoW(groupID string, requester cipher.PubKey, bits uint8, at time.Time, deadline time.Time) (JoinPoW, bool) {
	if bits == 0 {
		return JoinPoW{}, true
	}
	if bits > MaxJoinPoWBits {
		bits = MaxJoinPoWBits
	}
	p := JoinPoW{TSUnix: at.Unix(), Bits: bits}
	// Start at 1: a zero nonce with a zero timestamp is the "no proof"
	// sentinel VerifyJoinPoW checks for.
	for nonce := uint64(1); ; nonce++ {
		p.Nonce = nonce
		if joinPoWStrength(groupID, requester, p) >= bits {
			return p, true
		}
		// Check the clock rarely — time.Now() costs more than the hash.
		if nonce%4096 == 0 && !deadline.IsZero() && time.Now().After(deadline) {
			return JoinPoW{}, false
		}
	}
}

// clampJoinPoWBits normalizes a difficulty from an untrusted source (an
// invite link, a challenge from another visor, an operator's typo).
func clampJoinPoWBits(b uint8) uint8 {
	if b > MaxJoinPoWBits {
		return MaxJoinPoWBits
	}
	return b
}

// --- rate limiting ---------------------------------------------------------

// Join-rate defaults. A token is one request that gets to consume real
// resources — an admission or a queue insertion. Everything else (a
// re-ask from a PK already queued, a banned PK, a member re-syncing) is
// answered without spending one, so an honest requester polling on its
// 30s timer never runs the bucket down.
const (
	// DefaultJoinBurst is how many requests a group absorbs back-to-back.
	// Sized for a real event — a team of ten joining a new room at once —
	// rather than for the steady state.
	DefaultJoinBurst = 10

	// DefaultJoinRefill is how long one token takes to come back. Six an
	// hour after the burst is spent: slow enough that a flood is a
	// trickle, fast enough that a genuine queue of newcomers clears in an
	// evening rather than a week.
	DefaultJoinRefill = 10 * time.Minute

	// DefaultMaxPendingJoins caps how many undecided requests a group
	// stores. The queue is an admin's attention, and attention is the
	// resource being attacked: an unbounded queue means the flood wins by
	// making the real requests unfindable. At the cap, new requests are
	// refused with a retryable answer rather than displacing what is
	// already there — a queued PK that has been waiting has already been
	// paid for, and dropping it to make room for the newest arrival would
	// hand the attacker exactly the eviction primitive they want.
	DefaultMaxPendingJoins = 64
)

// joinBucket is a per-group token bucket over join attempts that cost
// something.
//
// In memory and per-visor: it resets on restart and each admin keeps its
// own. Both are deliberate. Persisting it would mean a disk write on
// every refused request — turning a cheap refusal into an expensive one,
// which is the wrong shape for an anti-flood measure — and a restart
// costs the attacker the same burst it costs everyone else. Per-visor
// because each admin is protecting its own queue and its own attention;
// there is nothing to agree on.
type joinBucket struct {
	mu     sync.Mutex
	tokens map[string]float64
	last   map[string]time.Time
	burst  float64
	refill time.Duration
}

func newJoinBucket(burst int, refill time.Duration) *joinBucket {
	if burst <= 0 {
		burst = DefaultJoinBurst
	}
	if refill <= 0 {
		refill = DefaultJoinRefill
	}
	return &joinBucket{
		tokens: make(map[string]float64),
		last:   make(map[string]time.Time),
		burst:  float64(burst),
		refill: refill,
	}
}

// allow spends a token for id if one is available. Returns whether it was
// spent and, when it was not, how long until the next one.
func (b *joinBucket) allow(id string, now time.Time) (bool, time.Duration) {
	if b == nil {
		// A Manager built without one — hand-constructed in a test, or a
		// caller predating this field. Fail OPEN: the limiter is a
		// protection, and a missing protection must not turn into a group
		// that refuses everyone.
		return true, 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	last, seen := b.last[id]
	if !seen {
		// First contact: a full bucket, minus this request.
		b.tokens[id] = b.burst - 1
		b.last[id] = now
		return true, 0
	}
	// Refill for elapsed time, capped at burst.
	if elapsed := now.Sub(last); elapsed > 0 {
		b.tokens[id] += elapsed.Seconds() / b.refill.Seconds()
		if b.tokens[id] > b.burst {
			b.tokens[id] = b.burst
		}
		b.last[id] = now
	}
	if b.tokens[id] >= 1 {
		b.tokens[id]--
		return true, 0
	}
	missing := 1 - b.tokens[id]
	return false, time.Duration(missing * float64(b.refill))
}

// forget drops a group's bucket state. Called when a group is deleted so
// the map doesn't grow with dead IDs.
func (b *joinBucket) forget(id string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	delete(b.tokens, id)
	delete(b.last, id)
	b.mu.Unlock()
}
