// Package arfeed pkg/deployment/ar/arfeed/arfeed.go c4-net-discovery
//
// Wire shape of the address-resolver's CXO bindings feed — the tree layout,
// the record type and the codec, shared by the publisher
// (pkg/deployment/ar/api) and any reader. It depends on nothing but the
// VisorData type and the CXO frame helpers, so a visor can import it without
// dragging in the AR service's HTTP stack.
//
// # Why this tree is shaped the way it is
//
// The feed exists to be read with CXO's Preview (pkg/cxo/node.Conn.Preview):
// fetch the publisher's latest Root over an already-open connection, walk ONLY
// the branches the lookup needs, and hold nothing afterwards. That pays off
// only if the tree is keyed by the thing being looked up. The dmsg-discovery
// clients-by-server feed is the counter-example — keyed by SERVER, so
// resolving one client means pulling that server's whole batch.
//
// Three properties of treestore and registry.Refs decide the layout, and each
// one was measured rather than assumed:
//
//  1. A level's children are individual objects, one network fetch each. So
//     SCANNING a level of N children costs N round-trips.
//  2. treestore encodes each level's children in sorted Name order
//     (publisher.go sortedNames -> encodeNode), so a reader can address a
//     child by INDEX and skip the scan entirely.
//  3. Refs is a Merkle tree of degree 16. Addressing element i inside a level
//     of more than 16 children walks the branch nodes preceding it, so a
//     256-wide level costs between 1 and ~16 EXTRA fetches depending on where
//     the key lands. A level of at most 16 children is a single Refs node:
//     one fetch, whatever the index.
//
// Hence two levels of one hex character each, 16 wide apiece:
//
//	<pk-hex[2]>/<pk-hex[3]>    // FrameGzip(v1, JSON map pk-hex -> PeerBindings)
//
// 256 buckets, every one of them always published (empty ones included) so
// both levels stay dense and index-addressable. A lookup is then a FIXED
// handful of object fetches — schema registry, root TreeNode, root Refs node,
// level-1 entry, level-2 TreeNode, its Refs node, the bucket entry — at any
// network size and for any key. Widening to 4096 buckets later is one more
// level and three more fetches.
//
// # The offset matters
//
// The bucket is named from hex[2:4], not hex[0:2]. A skywire public key is a
// COMPRESSED secp256k1 point, so its leading byte is always 0x02 or 0x03:
// bucketing on the first two hex characters puts the entire network into two
// buckets and silently turns each "one indexed fetch" into a whole-network
// download. Measured on the 1711 peers the production AR held on 2026-09-06:
// hex[0:2] gives 2 buckets of 886 and 825; hex[2:4] gives 255 occupied buckets
// with a maximum occupancy of 17. The offset skips the parity byte and starts
// at the first byte of the X coordinate, which is uniform.
//
// Bucket occupancy is the tuning knob. At ~1.7k bound peers a bucket holds
// around seven records, a few hundred bytes gzipped, so a lookup transfers the
// target plus a handful of incidental neighbors. An order of magnitude more
// peers wants a third level — a wire-format change, which is what the leading
// version byte is for: an old reader rejects the frame cleanly and falls back
// to HTTP rather than misparsing it.
package arfeed

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// Version is the wire-format version byte of a bucket leaf body. Bump it on
// any breaking change to the bucket encoding OR to the tree layout; readers
// reject other values rather than misparsing them.
const Version = 1

// Levels is the depth of the bucket tree. Each level is named by one hex
// character of the peer's public key.
const Levels = 2

// Fanout is the number of children at each level — one hex character. It is
// deliberately not larger: registry.Refs has degree 16, so a level of at most
// 16 children is a single Refs node and an indexed fetch there costs one
// round-trip regardless of the index.
const Fanout = 16

// KeyOffset is the index of the first hex character of a peer's public key
// used to name its path. It is one byte in, not zero, because a compressed
// secp256k1 key's leading byte is always 0x02 or 0x03 — see the package doc.
const KeyOffset = 2

// BucketCount is the number of buckets the publisher always materializes.
const BucketCount = 1 << (4 * Levels)

// ErrBadVersion is returned by DecodeBucket for a frame whose version byte is
// not Version. Callers treat it as "I cannot read this", not as an error worth
// retrying.
var ErrBadVersion = errors.New("arfeed: unsupported bucket frame version")

// PeerBindings is one peer's address-resolver record: whichever of the four
// bindable transport types the AR currently holds for it. Fields are pointers
// so an absent binding is absent from the JSON rather than an empty object,
// and the field order is fixed by the struct so a re-encode of unchanged
// content produces identical bytes — a wire no-op on CXO's content-addressed
// store.
type PeerBindings struct {
	STCPR *addrresolver.VisorData `json:"stcpr,omitempty"`
	SUDPH *addrresolver.VisorData `json:"sudph,omitempty"`
	QUIC  *addrresolver.VisorData `json:"squicr,omitempty"`
	WT    *addrresolver.VisorData `json:"swtr,omitempty"`
}

// Empty reports whether the record carries no binding at all, in which case
// the publisher drops the peer from its bucket.
func (b *PeerBindings) Empty() bool {
	return b == nil || (b.STCPR == nil && b.SUDPH == nil && b.QUIC == nil && b.WT == nil)
}

// Get returns the binding for one transport type, or nil.
func (b *PeerBindings) Get(t types.Type) *addrresolver.VisorData {
	if b == nil {
		return nil
	}
	switch types.NormalizeType(t) {
	case types.STCPR:
		return b.STCPR
	case types.SUDPH:
		return b.SUDPH
	case types.QUIC:
		return b.QUIC
	case types.WT:
		return b.WT
	default:
		return nil
	}
}

// Set installs the binding for one transport type; a nil value clears it.
// Reports whether the type was a bindable one.
func (b *PeerBindings) Set(t types.Type, v *addrresolver.VisorData) bool {
	switch types.NormalizeType(t) {
	case types.STCPR:
		b.STCPR = v
	case types.SUDPH:
		b.SUDPH = v
	case types.QUIC:
		b.QUIC = v
	case types.WT:
		b.WT = v
	default:
		return false
	}
	return true
}

// Segments returns the per-level names of pk's bucket, outermost first.
func Segments(pk cipher.PubKey) []string {
	h := pk.Hex()
	out := make([]string, Levels)
	for i := 0; i < Levels; i++ {
		out[i] = h[KeyOffset+i : KeyOffset+i+1]
	}
	return out
}

// Indices returns the per-level child POSITIONS of pk's bucket — what a reader
// hands to an indexed child fetch instead of scanning the level for a name.
// Valid only because the publisher keeps every level dense and treestore
// writes children in sorted name order. A malformed segment yields -1, which
// an indexed fetch treats as a miss.
func Indices(pk cipher.PubKey) []int {
	segs := Segments(pk)
	out := make([]int, len(segs))
	for i, s := range segs {
		n, err := strconv.ParseUint(s, 16, 8)
		if err != nil {
			out[i] = -1
			continue
		}
		out[i] = int(n)
	}
	return out
}

// BucketPath returns the full tree path of pk's bucket, e.g. "a/3".
func BucketPath(pk cipher.PubKey) string {
	return strings.Join(Segments(pk), "/")
}

// BucketPathAt returns the path of the i'th bucket in the dense enumeration
// the publisher materializes, for 0 <= i < BucketCount.
func BucketPathAt(i int) string {
	segs := make([]string, Levels)
	for lvl := Levels - 1; lvl >= 0; lvl-- {
		segs[lvl] = fmt.Sprintf("%x", i%Fanout)
		i /= Fanout
	}
	return strings.Join(segs, "/")
}

// EncodeBucket serializes one bucket's peer records into a leaf body: a JSON
// object keyed by peer public key in sorted order (so unchanged content
// re-encodes to identical bytes), version-framed and gzipped.
func EncodeBucket(peers map[cipher.PubKey]*PeerBindings) ([]byte, error) {
	pks := make([]string, 0, len(peers))
	idx := make(map[string]*PeerBindings, len(peers))
	for pk, b := range peers {
		if b.Empty() {
			continue
		}
		h := pk.Hex()
		pks = append(pks, h)
		idx[h] = b
	}
	sort.Strings(pks)

	payload := make([]byte, 0, 64+len(pks)*256)
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
	return cxoutils.FrameGzip(Version, payload), nil
}

// DecodeBucket parses a bucket leaf body. An unknown version byte yields
// ErrBadVersion so a reader degrades to its fallback path instead of
// misparsing a newer wire shape.
func DecodeBucket(blob []byte) (map[string]*PeerBindings, error) {
	version, body, ok := cxoutils.UnframeGzip(blob)
	if !ok {
		return nil, errors.New("arfeed: empty bucket body")
	}
	if version != Version {
		return nil, fmt.Errorf("%w: got %d want %d", ErrBadVersion, version, Version)
	}
	out := make(map[string]*PeerBindings)
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}
