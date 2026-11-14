// Package skyobject pkg/cxo/skyobject/delta.go c2-net-cxo
package skyobject

import (
	"sync"
	"time"

	"github.com/skycoin/skycoin/src/cipher"
)

// Bounds of the per-Root deltas a Container remembers. A delta lists the
// objects that were new to this node when it saved or filled a Root, so a
// peer can hand them to a subscriber in one reply instead of one per request.
const (
	DeltaRoots     = 4       // deltas kept per head
	DeltaMaxKeys   = 512     // keys kept per delta, the first ones in walk order
	DeltaTotalKeys = 1 << 16 // keys kept across all heads
)

type deltaHead struct {
	pk    cipher.PubKey
	nonce uint64
}

type delta struct {
	seq  uint64
	keys []cipher.SHA256
}

type deltaEntry struct {
	ds      []delta // ascending seq, at most DeltaRoots
	touched uint64
}

type deltaStore struct {
	mu      sync.Mutex
	m       map[deltaHead]*deltaEntry
	idx     map[cipher.SHA256]int // key -> deltas holding it
	total   int
	clock   uint64
	changed chan struct{} // closed and replaced on every record
}

func (d *deltaStore) changedLocked() chan struct{} {
	if d.changed == nil {
		d.changed = make(chan struct{})
	}
	return d.changed
}

func (d *deltaStore) addLocked(keys []cipher.SHA256) {
	for _, k := range keys {
		d.idx[k]++
	}
	d.total += len(keys)
}

func (d *deltaStore) dropLocked(keys []cipher.SHA256) {
	for _, k := range keys {
		if d.idx[k]--; d.idx[k] <= 0 {
			delete(d.idx, k)
		}
	}
	d.total -= len(keys)
}

// recentElsewhere returns a check whether a key is in a delta of a head
// other than pk/nonce: an object this node got lately for another feed,
// which subscribers of this one may lack. Deltas of the head itself do not
// count, or each delta would carry the previous ones on forever.
func (d *deltaStore) recentElsewhere(pk cipher.PubKey, nonce uint64) func(cipher.SHA256) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.idx) == 0 {
		return func(cipher.SHA256) bool { return false }
	}
	own := make(map[cipher.SHA256]int)
	if e := d.m[deltaHead{pk, nonce}]; e != nil {
		for _, ds := range e.ds {
			for _, k := range ds.keys {
				own[k]++
			}
		}
	}
	return func(k cipher.SHA256) bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.idx[k] > own[k]
	}
}

// RecordDelta remembers keys as the objects new to this node in the Root
// pk/nonce/seq. Only the first DeltaMaxKeys are kept.
func (c *Container) RecordDelta(pk cipher.PubKey, nonce, seq uint64, keys []cipher.SHA256) {
	if len(keys) == 0 {
		return
	}
	if len(keys) > DeltaMaxKeys {
		keys = keys[:DeltaMaxKeys]
	}
	keys = append([]cipher.SHA256(nil), keys...)

	d := &c.deltas
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.m == nil {
		d.m = make(map[deltaHead]*deltaEntry)
		d.idx = make(map[cipher.SHA256]int)
	}
	h := deltaHead{pk, nonce}
	e := d.m[h]
	if e == nil {
		e = new(deltaEntry)
		d.m[h] = e
	}
	d.clock++
	e.touched = d.clock

	i := 0
	for i < len(e.ds) && e.ds[i].seq < seq {
		i++
	}
	if i < len(e.ds) && e.ds[i].seq == seq {
		d.dropLocked(e.ds[i].keys)
		e.ds[i].keys = keys
	} else {
		e.ds = append(e.ds, delta{})
		copy(e.ds[i+1:], e.ds[i:])
		e.ds[i] = delta{seq: seq, keys: keys}
	}
	d.addLocked(keys)
	for len(e.ds) > DeltaRoots {
		d.dropLocked(e.ds[0].keys)
		e.ds = e.ds[1:]
	}

	for d.total > DeltaTotalKeys {
		var (
			oldest deltaHead
			oe     *deltaEntry
		)
		for oh, x := range d.m {
			if x != e && (oe == nil || x.touched < oe.touched) {
				oldest, oe = oh, x
			}
		}
		if oe == nil {
			break
		}
		for _, od := range oe.ds {
			d.dropLocked(od.keys)
		}
		delete(d.m, oldest)
	}

	close(d.changedLocked())
	d.changed = make(chan struct{})
}

// DeltaKeys returns the keys of the deltas of the Roots pk/nonce/since..upto,
// newest Root first, without duplicates and at most limit of them. The bool
// reports whether the delta of upto itself is known.
func (c *Container) DeltaKeys(pk cipher.PubKey, nonce, since, upto uint64, limit int) ([]cipher.SHA256, bool) {
	d := &c.deltas
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.keysLocked(pk, nonce, since, upto, limit)
}

func (d *deltaStore) keysLocked(pk cipher.PubKey, nonce, since, upto uint64, limit int) (keys []cipher.SHA256, found bool) {
	e := d.m[deltaHead{pk, nonce}]
	if e == nil {
		return nil, false
	}
	seen := make(map[cipher.SHA256]struct{})
	for i := len(e.ds) - 1; i >= 0; i-- {
		ds := e.ds[i]
		if ds.seq > upto {
			continue
		}
		if ds.seq < since {
			break
		}
		if ds.seq == upto {
			found = true
		}
		for _, k := range ds.keys {
			if len(keys) >= limit {
				return keys, found
			}
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	return keys, found
}

// WaitDeltaKeys is DeltaKeys that waits up to wait for the delta of upto to
// be recorded, for a Root this node is still filling.
func (c *Container) WaitDeltaKeys(pk cipher.PubKey, nonce, since, upto uint64, limit int, wait time.Duration) ([]cipher.SHA256, bool) {
	if keys, found := c.DeltaKeys(pk, nonce, since, upto, limit); found || wait <= 0 {
		return keys, found
	}
	t := time.NewTimer(wait)
	defer t.Stop()

	d := &c.deltas
	for {
		d.mu.Lock()
		keys, found := d.keysLocked(pk, nonce, since, upto, limit)
		ch := d.changedLocked()
		d.mu.Unlock()
		if found {
			return keys, found
		}
		select {
		case <-ch:
		case <-t.C:
			return c.DeltaKeys(pk, nonce, since, upto, limit)
		}
	}
}
