package idxdb

import (
	"sort"
	"sync"
	"time"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
)

// NewMemeoryDB returns a real in-memory IdxDB.
//
// The legacy spelling is preserved (NewMemeoryDB, sic) because every
// caller in the tree spells it this way and renaming is a deferred
// cleanup. The previous implementation was a stub that returned a
// driveDB on a temporary bbolt file with the comment "the memory-db
// is not implemented yet" — which silently turned every
// InMemoryDB:true config (SD / TPD / DMSG-D publishers, plus tests)
// into a bbolt-on-disk store that mmap'd its way to hundreds of MB
// of RSS under steady churn and burned ~64% of CPU in GC scanning
// the bloated heap on production dmsg-discovery.
//
// This implementation is goroutine-safe: every Tx grabs a single
// process-wide mutex around the maps. Tx is non-atomic across
// failure (a partial mutation is NOT rolled back on user-func error)
// because the alternative — snapshot-and-restore the whole map
// pyramid per Tx — is more expensive than the on-disk variant we
// replaced. Callers already retry on Tx error and the cxo cleanup
// path is idempotent, so partial-failure visibility is acceptable.
func NewMemeoryDB() (idx data.IdxDB) {
	return newMemoryDB()
}

func newMemoryDB() *memoryDB {
	return &memoryDB{
		feeds: make(map[cipher.PubKey]*memoryFeed),
	}
}

type memoryDB struct {
	mu    sync.Mutex
	feeds map[cipher.PubKey]*memoryFeed
	// closed is set after Close so subsequent Tx calls fail fast
	// instead of operating on a torn-down map.
	closed bool
}

type memoryFeed struct {
	heads map[uint64]*memoryHead
}

type memoryHead struct {
	roots map[uint64]data.Root // value-typed so the map owns the bytes
}

func newMemoryFeed() *memoryFeed {
	return &memoryFeed{heads: make(map[uint64]*memoryHead)}
}

func newMemoryHead() *memoryHead {
	return &memoryHead{roots: make(map[uint64]data.Root)}
}

// Tx runs txFunc under a single mutex. The Feeds value is bound to
// the live DB state — mutations apply immediately and are visible
// inside the txFunc. If txFunc returns an error the mutations are
// retained (no rollback); see the package doc on NewMemeoryDB for
// why this is intentional.
func (m *memoryDB) Tx(txFunc func(feeds data.Feeds) (err error)) (err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return data.ErrNotFound
	}
	return txFunc(&memoryFeeds{db: m})
}

// Close drops every feed and marks the DB closed so subsequent Tx
// calls return immediately. Idempotent.
func (m *memoryDB) Close() (err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.feeds = nil
	m.closed = true
	return nil
}

// memoryFeeds is the Feeds view of a memoryDB, valid only inside a
// Tx invocation (it borrows the parent's mutex via the Tx wrapper).
type memoryFeeds struct {
	db *memoryDB
}

// Add a feed. No-op if it already exists.
func (f *memoryFeeds) Add(pk cipher.PubKey) (err error) {
	if _, ok := f.db.feeds[pk]; ok {
		return nil
	}
	f.db.feeds[pk] = newMemoryFeed()
	return nil
}

// Del a feed plus every head and root under it.
func (f *memoryFeeds) Del(pk cipher.PubKey) (err error) {
	if _, ok := f.db.feeds[pk]; !ok {
		return data.ErrNoSuchFeed
	}
	delete(f.db.feeds, pk)
	return nil
}

// Iterate over every feed. Snapshots the key set at entry so the
// callback can mutate the feed map without invalidating iteration.
// ErrStopIteration ends the walk cleanly; any other error is
// returned to the caller.
func (f *memoryFeeds) Iterate(iterateFunc data.IterateFeedsFunc) (err error) {
	pks := make([]cipher.PubKey, 0, len(f.db.feeds))
	for pk := range f.db.feeds {
		pks = append(pks, pk)
	}
	sort.Slice(pks, func(i, j int) bool {
		for k := 0; k < len(pks[i]); k++ {
			if pks[i][k] != pks[j][k] {
				return pks[i][k] < pks[j][k]
			}
		}
		return false
	})
	for _, pk := range pks {
		// Skip entries deleted by an earlier callback invocation.
		if _, ok := f.db.feeds[pk]; !ok {
			continue
		}
		if err = iterateFunc(pk); err != nil {
			if err == data.ErrStopIteration {
				err = nil
			}
			return
		}
	}
	return nil
}

func (f *memoryFeeds) Has(pk cipher.PubKey) (ok bool, _ error) {
	_, ok = f.db.feeds[pk]
	return ok, nil
}

func (f *memoryFeeds) Heads(pk cipher.PubKey) (hs data.Heads, err error) {
	feed, ok := f.db.feeds[pk]
	if !ok {
		return nil, data.ErrNoSuchFeed
	}
	return &memoryHeads{feed: feed}, nil
}

func (f *memoryFeeds) Len() (length int) {
	return len(f.db.feeds)
}

type memoryHeads struct {
	feed *memoryFeed
}

func (h *memoryHeads) Roots(nonce uint64) (rs data.Roots, err error) {
	head, ok := h.feed.heads[nonce]
	if !ok {
		return nil, data.ErrNoSuchHead
	}
	return &memoryRoots{head: head}, nil
}

func (h *memoryHeads) Add(nonce uint64) (rs data.Roots, err error) {
	head, ok := h.feed.heads[nonce]
	if !ok {
		head = newMemoryHead()
		h.feed.heads[nonce] = head
	}
	return &memoryRoots{head: head}, nil
}

func (h *memoryHeads) Del(nonce uint64) (err error) {
	if _, ok := h.feed.heads[nonce]; !ok {
		return data.ErrNoSuchHead
	}
	delete(h.feed.heads, nonce)
	return nil
}

func (h *memoryHeads) Has(nonce uint64) (ok bool, _ error) {
	_, ok = h.feed.heads[nonce]
	return ok, nil
}

func (h *memoryHeads) Iterate(iterateFunc data.IterateHeadsFunc) (err error) {
	nonces := make([]uint64, 0, len(h.feed.heads))
	for n := range h.feed.heads {
		nonces = append(nonces, n)
	}
	sort.Slice(nonces, func(i, j int) bool { return nonces[i] < nonces[j] })
	for _, n := range nonces {
		if _, ok := h.feed.heads[n]; !ok {
			continue
		}
		if err = iterateFunc(n); err != nil {
			if err == data.ErrStopIteration {
				err = nil
			}
			return
		}
	}
	return nil
}

func (h *memoryHeads) Len() (length int) {
	return len(h.feed.heads)
}

type memoryRoots struct {
	head *memoryHead
}

// Ascend iterates roots in ascending seq order. driveDB does not
// touch Access during Ascend; we match.
func (r *memoryRoots) Ascend(iterateFunc data.IterateRootsFunc) (err error) {
	return r.walk(false, iterateFunc)
}

// Descend iterates roots in descending seq order. driveDB does not
// touch Access during Descend; we match.
func (r *memoryRoots) Descend(iterateFunc data.IterateRootsFunc) (err error) {
	return r.walk(true, iterateFunc)
}

func (r *memoryRoots) walk(desc bool, iterateFunc data.IterateRootsFunc) (err error) {
	seqs := make([]uint64, 0, len(r.head.roots))
	for s := range r.head.roots {
		seqs = append(seqs, s)
	}
	if desc {
		sort.Slice(seqs, func(i, j int) bool { return seqs[i] > seqs[j] })
	} else {
		sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	}
	for _, s := range seqs {
		stored, ok := r.head.roots[s]
		if !ok {
			continue
		}
		// Hand the callback a copy so it can mutate freely
		// without aliasing the stored Root. driveDB returns a
		// decoded copy too.
		copy := stored
		if err = iterateFunc(&copy); err != nil {
			if err == data.ErrStopIteration {
				err = nil
			}
			return
		}
	}
	return nil
}

// Set stores a Root, or if one already exists at the same seq,
// "touches" it by bumping Access. This mirrors driveRoots.Set
// precisely: on insert Create=Access=now; on touch r.Create and
// r.Access are rewritten to the *stored* values, then the stored
// Access is bumped to now (the caller's r reflects the previous
// state, the DB's row reflects the new access time).
func (r *memoryRoots) Set(in *data.Root) (err error) {
	if err = in.Validate(); err != nil {
		return err
	}
	now := time.Now().UnixNano()
	if stored, ok := r.head.roots[in.Seq]; ok {
		// Touch existing: caller sees old timestamps, stored row
		// advances Access.
		in.Create = stored.Create
		in.Access = stored.Access
		stored.Access = now
		r.head.roots[in.Seq] = stored
		return nil
	}
	in.Create = now
	in.Access = now
	r.head.roots[in.Seq] = *in
	return nil
}

// Del removes a Root. Matches driveRoots.Del: silent no-op if
// the seq is absent (driveDB's bbolt Delete is also a no-op on
// missing keys).
func (r *memoryRoots) Del(seq uint64) (err error) {
	delete(r.head.roots, seq)
	return nil
}

// Get returns the Root at seq and bumps its Access in storage.
// The returned Root carries the previous Access value, matching
// driveRoots.Get's "return previous, store new" contract.
func (r *memoryRoots) Get(seq uint64) (out *data.Root, err error) {
	stored, ok := r.head.roots[seq]
	if !ok {
		return nil, data.ErrNotFound
	}
	prev := stored.Access
	stored.Access = time.Now().UnixNano()
	r.head.roots[seq] = stored
	cp := stored
	cp.Access = prev
	return &cp, nil
}

func (r *memoryRoots) Has(seq uint64) (ok bool, _ error) {
	_, ok = r.head.roots[seq]
	return ok, nil
}

func (r *memoryRoots) Len() int {
	return len(r.head.roots)
}
