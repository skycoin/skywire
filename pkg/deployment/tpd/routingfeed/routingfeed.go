// Package routingfeed pkg/deployment/tpd/routingfeed/routingfeed.go c4-net-discovery
//
// Package routingfeed is the layout of TPD's CXO routing feed and the reader
// that turns it back into the network's transport list.
//
// The feed is a snapshot plus deltas:
//
//	routing/<pk>          gzipped JSON []transport entry: a visor's transports
//	                      (those whose lower-keyed edge it is), as of the
//	                      last snapshot
//	routing/delta/<seq>   gzipped JSON Delta: what changed since the publish
//	                      before it
//
// The snapshot is rewritten only now and then, and the deltas since it are
// deleted in the same publish. Between snapshots its objects keep their
// hashes, so a subscriber fetches only the new delta. A reader that decodes
// every leaf under routing/ as a shard skips the deltas and sees the
// snapshot.
package routingfeed

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// Prefix is the feed's sub-tree; snapshot shards are Prefix + <pk hex>.
const Prefix = "routing/"

// DeltaPrefix holds the deltas since the snapshot.
const DeltaPrefix = Prefix + "delta/"

// DeltaPath is the path of delta seq. The fixed width keeps paths in order.
func DeltaPath(seq int) string { return fmt.Sprintf("%s%012d", DeltaPrefix, seq) }

// Removal names a transport that left.
type Removal struct {
	Edges [2]cipher.PubKey `json:"edges"`
	Type  types.Type       `json:"type"`
}

// Delta is one publish's changes: transports added or changed, and those
// removed.
type Delta struct {
	Changed []*transport.Entry `json:"c,omitempty"`
	Removed []Removal          `json:"r,omitempty"`
}

type key struct {
	edges [2]cipher.PubKey
	typ   types.Type
}

func keyOf(edges [2]cipher.PubKey, typ types.Type) key {
	return key{edges: transport.SortEdges(edges[0], edges[1]), typ: typ}
}

// Reader assembles the feed from its leaves, given in any order.
type Reader struct {
	entries map[key]*transport.Entry
	deltas  map[uint64]Delta
}

// NewReader returns an empty Reader.
func NewReader() *Reader {
	return &Reader{entries: make(map[key]*transport.Entry), deltas: make(map[uint64]Delta)}
}

// Add takes one leaf of the feed. A leaf that does not decode is skipped.
func (r *Reader) Add(path string, body []byte) {
	if rest, ok := strings.CutPrefix(path, DeltaPrefix); ok {
		seq, err := strconv.ParseUint(rest, 10, 64)
		if err != nil {
			return
		}
		var d Delta
		if json.Unmarshal(cxoutils.Gunzip(body), &d) == nil {
			r.deltas[seq] = d
		}
		return
	}
	var shard []*transport.Entry
	if json.Unmarshal(cxoutils.Gunzip(body), &shard) != nil {
		return
	}
	for _, e := range shard {
		if e != nil {
			r.entries[keyOf(e.Edges, e.Type)] = e
		}
	}
}

// Entries returns the transports: the snapshot with every delta applied in
// order, with the t_id the publisher leaves out recomputed.
func (r *Reader) Entries() []*transport.Entry {
	seqs := make([]uint64, 0, len(r.deltas))
	for seq := range r.deltas {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for _, seq := range seqs {
		d := r.deltas[seq]
		for _, rm := range d.Removed {
			delete(r.entries, keyOf(rm.Edges, rm.Type))
		}
		for _, e := range d.Changed {
			if e != nil {
				r.entries[keyOf(e.Edges, e.Type)] = e
			}
		}
	}
	r.deltas = make(map[uint64]Delta)

	out := make([]*transport.Entry, 0, len(r.entries))
	for _, e := range r.entries {
		if e.ID == (uuid.UUID{}) {
			e.ID = transport.MakeTransportID(e.Edges[0], e.Edges[1], e.Type)
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	return out
}
