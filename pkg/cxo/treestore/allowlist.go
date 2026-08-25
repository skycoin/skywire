// Package treestore pkg/cxo/treestore/allowlist.go c2-net-cxo
// access control for Publisher.
//
// A publisher with an empty allowlist accepts subscribe requests from
// any peer (the historical default). A populated allowlist gates the
// CXO node's OnSubscribeRemote callback so only listed PKs may
// subscribe. The check is intentionally conservative: rejection
// returns a generic error so a probing peer can't distinguish "feed
// not found" from "you are not on the allowlist."
//
// The state lives in *allowState so that NewWithDMSG can wire the
// CXO node's OnSubscribeRemote hook BEFORE the node is constructed
// (the hook is read from cfg by node.NewNode), then hand the same
// pointer to the resulting Publisher so SetAllowlist updates the live
// state the hook is consulting.
package treestore

import (
	"errors"
	"sort"
	"sync"
	"time"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/node"
)

// maxDeniedTracked caps the distinct denied-subscriber PKs allowState
// remembers, so a flood of probing peers can't grow the map unbounded.
const maxDeniedTracked = 32

// errSubscribeRejected is the generic error returned by the
// OnSubscribeRemote hook when an allowlist denies a peer. The text
// is intentionally non-specific: a probing peer can't distinguish
// "feed not found" from "you are not on the allowlist."
var errSubscribeRejected = errors.New("subscribe rejected")

// subscribeHook returns an OnSubscribeRemoteFunc bound to the given
// allowState. Used by NewWithDMSG to wire the CXO node's hook before
// the node is constructed.
func subscribeHook(allow *allowState) func(*node.Conn, skycipher.PubKey) error {
	return func(c *node.Conn, _ skycipher.PubKey) error {
		pk := cipher.PubKey(c.PeerID())
		if !allow.permits(pk) {
			allow.recordDenied(pk)
			return errSubscribeRejected
		}
		return nil
	}
}

// DeniedSub records a would-be subscriber the allowlist has turned away:
// its PK, how many times, and when last. Exposed via Publisher.Denied so
// `skywire cli visor state` can show WHY a consuming service (e.g. TPD)
// isn't filling — a denied PK that differs from the one the operator
// expected to allow is the tell (e.g. a service dialing in under a
// different CXO node key than its configured service PK).
type DeniedSub struct {
	PK        cipher.PubKey
	Count     int
	LastNanos int64
}

// allowState is the shared, mutex-protected allowlist. nil-membership
// (set == false) means the gate is disabled and every subscribe is
// permitted. Non-nil membership means only listed PKs are allowed.
type allowState struct {
	mu      sync.RWMutex
	set     bool
	members map[cipher.PubKey]struct{}
	denied  map[cipher.PubKey]*DeniedSub
}

func newAllowState(initial []cipher.PubKey) *allowState {
	a := &allowState{denied: make(map[cipher.PubKey]*DeniedSub)}
	a.replace(initial)
	return a
}

// recordDenied notes that pk was turned away. Cheap and best-effort:
// bumps an existing entry, or adds a new one unless the map is already at
// maxDeniedTracked distinct PKs (then the denial is counted only if pk is
// already tracked). Used purely for the `visor state` diagnostic surface.
func (a *allowState) recordDenied(pk cipher.PubKey) {
	now := time.Now().UnixNano()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.denied == nil {
		a.denied = make(map[cipher.PubKey]*DeniedSub)
	}
	if d, ok := a.denied[pk]; ok {
		d.Count++
		d.LastNanos = now
		return
	}
	if len(a.denied) >= maxDeniedTracked {
		return
	}
	a.denied[pk] = &DeniedSub{PK: pk, Count: 1, LastNanos: now}
}

// deniedList returns a snapshot of denied subscribers, most-recent first.
func (a *allowState) deniedList() []DeniedSub {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.denied) == 0 {
		return nil
	}
	out := make([]DeniedSub, 0, len(a.denied))
	for _, d := range a.denied {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastNanos > out[j].LastNanos })
	return out
}

// replace atomically swaps the allowlist. Passing nil disables the
// gate (open to all). Passing an empty non-nil slice enables the gate
// with zero allowed PKs (closed to all) — useful for staging a feed
// before authorizing any subscribers.
func (a *allowState) replace(pks []cipher.PubKey) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if pks == nil {
		a.set = false
		a.members = nil
		return
	}
	a.set = true
	a.members = make(map[cipher.PubKey]struct{}, len(pks))
	for _, pk := range pks {
		a.members[pk] = struct{}{}
	}
}

// permits reports whether pk may subscribe. Returns true when the
// gate is disabled.
func (a *allowState) permits(pk cipher.PubKey) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.set {
		return true
	}
	_, ok := a.members[pk]
	return ok
}

// list returns a snapshot copy of the current allowed PKs. Returns
// nil when the gate is disabled.
func (a *allowState) list() []cipher.PubKey {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.set {
		return nil
	}
	out := make([]cipher.PubKey, 0, len(a.members))
	for pk := range a.members {
		out = append(out, pk)
	}
	return out
}
