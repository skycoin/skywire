// Package arfeed pkg/deployment/ar/arfeed/reach.go c4-net-discovery
//
// Reachability: which visors can actually be transported, per transport type.
//
// A binding at the address resolver says a visor LISTENS for a type. It does
// not say anyone can get through to it: a public visor can accept stcpr while
// its router drops inbound UDP, and a sudph binding outlives the UDP control
// connection the AR needs to make both sides punch at once. Autoconnect needs
// the difference, and it cannot learn it by waiting for a peer to succeed
// first — nothing would be tried.
//
// The chicken-and-egg is broken per type by whatever can make the first
// attempt without a peer:
//
//   - stcpr, squicr, swtr — a listener behind a router either admits inbound
//     traffic or it does not, whoever sends it. The AR probes the bound address
//     itself: a TCP connect for stcpr, and for the QUIC-based types one packet
//     with an unknown QUIC version, which a QUIC listener always answers with a
//     version negotiation. The result is the AR's, recorded as Open / Closed.
//   - sudph — success is pairwise: it depends on the NAT behavior of BOTH
//     sides, and it needs the receiver's live UDP control connection at the AR
//     (Live) for the simultaneous open. Each visor measures its own NAT class
//     with STUN (NAT); a pair is tried only when the two classes can punch.
//   - webrtc — ICE, also pairwise; the same NAT-class rule applies.
//
// Confirmations from real use sit on top: each visor states when it last
// ACCEPTED an inbound transport of each type (Inbound). A confirmation ranks a
// peer first; it never gates one, so nothing depends on a peer having
// succeeded before.
package arfeed

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
)

// ReachVersion is the frame version of a reach bucket leaf.
const ReachVersion = 1

// ReachPathPrefix is the tree prefix of the reach feed. One leaf per bucket,
// reach/<h>, where <h> is the key's first hex digit after the 02/03 prefix.
const ReachPathPrefix = "reach/"

// ReachBuckets is the number of reach leaves: one per hex digit.
const ReachBuckets = 16

// Transport type names as they appear in a Reach record. They are the
// canonical wire strings of the types, so a record can be read without the
// transport package.
const (
	TypeSTCPR  = "stcpr"
	TypeSUDPH  = "sudph"
	TypeQUIC   = "squicr"
	TypeWT     = "swtr"
	TypeWEBRTC = "webrtc"
)

// NAT classes, as measured by the visor's own STUN probe.
const (
	NATOpen           = "open"            // no NAT: public address on the interface
	NATFullCone       = "full-cone"       // any host may send once a mapping exists
	NATRestricted     = "restricted"      // only hosts the visor sent to (any port)
	NATPortRestricted = "port-restricted" // only the exact host:port the visor sent to
	NATSymmetric      = "symmetric"       // a new mapping per destination
	NATUDPFirewall    = "udp-firewall"    // public address, but filtered like port-restricted
	NATBlocked        = "blocked"         // no UDP at all
)

// InboundRecent is how long an accepted inbound transport counts as a
// confirmation.
const InboundRecent = 24 * time.Hour

// InboundGranularity is the resolution Inbound times are published at. Coarse
// on purpose: a visor accepting transports all day must not rewrite its record
// every time it does.
const InboundGranularity = time.Hour

// ReachDecl is what a visor states about itself, published as the "reach"
// leaf of its AR-bind feed.
type ReachDecl struct {
	// NAT is the visor's STUN-measured NAT class; empty when not measured.
	NAT string `json:"nat,omitempty"`
	// Accepts lists the types the visor serves inbound.
	Accepts []string `json:"acc,omitempty"`
	// Inbound maps a type to the time (unix seconds, rounded down to
	// InboundGranularity) the visor last accepted an inbound transport of it.
	Inbound map[string]int64 `json:"in,omitempty"`
}

// Reach is one peer's record in the reach feed: the visor's own declaration
// plus what the AR itself observed.
type Reach struct {
	ReachDecl
	// Open lists the types whose bound address answered the AR's probe.
	Open []string `json:"open,omitempty"`
	// Closed lists the types whose bound address did not.
	Closed []string `json:"closed,omitempty"`
	// Live is true while the visor holds a sudph UDP control connection at
	// the AR — without it the AR cannot ask it to punch.
	Live bool `json:"udp,omitempty"`
	// Bound lists the types the visor has a binding for at the AR.
	Bound []string `json:"bound,omitempty"`
}

// Verdict is how likely a transport of one type to a peer is to work.
// Ordered: a higher verdict is tried first.
type Verdict int

const (
	// Unreachable means the attempt is known to fail; it is not tried.
	Unreachable Verdict = iota
	// Untested means nothing is known either way — an old visor that states
	// nothing, or a probe that has not run yet. Tried within a small budget.
	Untested
	// Capable means everything measurable says it should work.
	Capable
	// Confirmed means it has worked: the AR's probe got through, or the peer
	// recently accepted an inbound transport of this type.
	Confirmed
)

func (v Verdict) String() string {
	switch v {
	case Unreachable:
		return "unreachable"
	case Untested:
		return "untested"
	case Capable:
		return "capable"
	case Confirmed:
		return "confirmed"
	}
	return fmt.Sprintf("verdict(%d)", int(v))
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// PunchCompatible reports whether two NAT classes can hole-punch to each
// other with a simultaneous open. An empty class is unknown and assumed
// compatible; blocked is compatible with nothing.
//
// A symmetric NAT picks a new external port per destination, so its peer
// cannot know where to send. That is fatal only when the peer also filters by
// port: another symmetric NAT, or a port-restricted one (a UDP firewall
// filters the same way). Open, full-cone and address-restricted peers admit it.
func PunchCompatible(a, b string) bool {
	if a == NATBlocked || b == NATBlocked {
		return false
	}
	portFiltered := func(c string) bool {
		return c == NATSymmetric || c == NATPortRestricted || c == NATUDPFirewall
	}
	if a == NATSymmetric && portFiltered(b) {
		return false
	}
	if b == NATSymmetric && portFiltered(a) {
		return false
	}
	return true
}

// InboundWithin reports whether the peer accepted an inbound transport of the
// type within InboundRecent of now.
func (r *Reach) InboundWithin(t string, now time.Time) bool {
	if r == nil {
		return false
	}
	at, ok := r.Inbound[t]
	if !ok || at <= 0 {
		return false
	}
	// The published time is rounded down, so allow one granule of slack.
	return now.Sub(time.Unix(at, 0)) <= InboundRecent+InboundGranularity
}

// Verdict says how likely a transport of type t from a visor with NAT class
// selfNAT to this peer is to work. r nil means the reach feed has no record
// for the peer at all.
func (r *Reach) Verdict(t, selfNAT string, now time.Time) Verdict {
	if r == nil {
		return Untested
	}
	if r.InboundWithin(t, now) {
		return Confirmed
	}
	// A visor that states what it accepts and leaves t out does not serve it.
	// An empty list is a visor that states nothing — unknown, not a refusal.
	if len(r.Accepts) > 0 && !has(r.Accepts, t) {
		return Unreachable
	}
	switch t {
	case TypeSTCPR, TypeQUIC, TypeWT:
		switch {
		case has(r.Open, t):
			return Confirmed
		case has(r.Closed, t):
			return Unreachable
		case len(r.Bound) > 0 && !has(r.Bound, t):
			// Nothing to dial: the AR resolves no address for it.
			return Unreachable
		}
		return Untested
	case TypeSUDPH:
		if !has(r.Bound, t) || !r.Live {
			return Unreachable
		}
		if !PunchCompatible(selfNAT, r.NAT) {
			return Unreachable
		}
		if r.NAT == "" {
			return Untested
		}
		return Capable
	case TypeWEBRTC:
		if !PunchCompatible(selfNAT, r.NAT) {
			return Unreachable
		}
		if r.NAT == "" || !has(r.Accepts, t) {
			return Untested
		}
		return Capable
	}
	return Untested
}

// ReachBucket returns the bucket index of a key: its first hex digit after the
// 02/03 compression prefix, which is uniformly distributed.
func ReachBucket(pk cipher.PubKey) int {
	h := pk.Hex()
	c := h[KeyOffset]
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	}
	return 0
}

// ReachPath returns the tree path of a reach bucket.
func ReachPath(bucket int) string {
	return fmt.Sprintf("%s%x", ReachPathPrefix, bucket)
}

// EncodeReachBucket encodes one reach bucket leaf. Keys are sorted so equal
// content encodes to equal bytes — the feed is content-addressed, and an
// unchanged bucket must cost nothing to republish.
func EncodeReachBucket(peers map[cipher.PubKey]*Reach) ([]byte, error) {
	pks := make([]string, 0, len(peers))
	idx := make(map[string]*Reach, len(peers))
	for pk, r := range peers {
		if r == nil {
			continue
		}
		h := pk.Hex()
		pks = append(pks, h)
		idx[h] = r
	}
	sort.Strings(pks)
	payload := make([]byte, 0, 16+len(pks)*128)
	payload = append(payload, '{')
	for i, h := range pks {
		body, err := json.Marshal(idx[h])
		if err != nil {
			return nil, err
		}
		if i > 0 {
			payload = append(payload, ',')
		}
		payload = append(payload, '"')
		payload = append(payload, h...)
		payload = append(payload, '"', ':')
		payload = append(payload, body...)
	}
	payload = append(payload, '}')
	return cxoutils.FrameGzip(ReachVersion, payload), nil
}

// DecodeReachBucket decodes one reach bucket leaf.
func DecodeReachBucket(blob []byte) (map[cipher.PubKey]*Reach, error) {
	version, body, ok := cxoutils.UnframeGzip(blob)
	if !ok {
		return nil, errors.New("arfeed: empty reach bucket body")
	}
	if version != ReachVersion {
		return nil, fmt.Errorf("%w: got %d want %d", ErrBadVersion, version, ReachVersion)
	}
	raw := make(map[string]*Reach)
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make(map[cipher.PubKey]*Reach, len(raw))
	for h, r := range raw {
		var pk cipher.PubKey
		if err := pk.UnmarshalText([]byte(h)); err != nil {
			continue
		}
		out[pk] = r
	}
	return out, nil
}
