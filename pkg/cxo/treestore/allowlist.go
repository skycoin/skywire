// Package treestore — pkg/cxo/treestore/allowlist.go: subscriber
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
	"sync"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/node"
)

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
		if !allow.permits(cipher.PubKey(c.PeerID())) {
			return errSubscribeRejected
		}
		return nil
	}
}

// allowState is the shared, mutex-protected allowlist. nil-membership
// (set == false) means the gate is disabled and every subscribe is
// permitted. Non-nil membership means only listed PKs are allowed.
type allowState struct {
	mu      sync.RWMutex
	set     bool
	members map[cipher.PubKey]struct{}
}

func newAllowState(initial []cipher.PubKey) *allowState {
	a := &allowState{}
	a.replace(initial)
	return a
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
