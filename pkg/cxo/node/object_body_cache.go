package node

import (
	"sync"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/node/msg"
)

// Large objects are encoded once and the encoded msg.Object body is shared by
// every request for them. Objects are content-addressed, so one encoding is
// right for all requesters, and a send queue then holds a reference instead
// of its own copy. TPD serves ~3.5 MB snapshot objects to hundreds of visors;
// encoding per request left ~300 copies (1 GB) queued behind slow links.
const (
	sharedObjectMinBytes = 64 << 10 // smaller objects are cheaper to encode than to track
	sharedObjectMaxBytes = 64 << 20 // total encoded bytes kept for reuse
)

type objectBodyCache struct {
	mu    sync.Mutex
	items map[cipher.SHA256][]byte
	order []cipher.SHA256 // insertion order, oldest first
	bytes int
}

// body returns the encoded msg.Object body for key, reusing a cached one.
func (oc *objectBodyCache) body(key cipher.SHA256, val []byte) []byte {
	if len(val) < sharedObjectMinBytes {
		return (&msg.Object{Value: val}).Encode()
	}
	oc.mu.Lock()
	if b, ok := oc.items[key]; ok {
		oc.mu.Unlock()
		return b
	}
	oc.mu.Unlock()

	b := (&msg.Object{Value: val}).Encode()

	oc.mu.Lock()
	defer oc.mu.Unlock()
	if prev, ok := oc.items[key]; ok {
		return prev // encoded concurrently; keep the first
	}
	if oc.items == nil {
		oc.items = make(map[cipher.SHA256][]byte)
	}
	oc.items[key] = b
	oc.order = append(oc.order, key)
	oc.bytes += len(b)
	for oc.bytes > sharedObjectMaxBytes && len(oc.order) > 1 {
		old := oc.order[0]
		oc.order = oc.order[1:]
		oc.bytes -= len(oc.items[old])
		delete(oc.items, old)
	}
	return b
}
