// Package visor pkg/visor/cxo_feed_allowlist.go c3-vis-core
//
// Subscriber-allowlist gating for the visor's service-consumed CXO feeds
// (telemetry stats, tp-list, dmsg-discovery registration). These feeds
// were historically PUBLIC — a nil SubscriberAllowlist accepts any
// subscriber, so any peer could subscribe and read the visor's full
// transport set, undermining the transport-setup-node-mediated access
// model. This module composes each feed's allowlist and keeps it in sync
// with the live peer whitelist.
//
// The composed set for a feed is:
//
//	v.peerWhitelist  (configured Hypervisors + Pty.Whitelist + own PK)
//	  ∪ {that feed's consuming-service PK}  (TPD for stats/tp-list,
//	                                          dmsg-discovery for registration)
//
// The consuming-service PK MUST be present or gating breaks that service:
// the OnSubscribeRemote hook fires even for the consumer over the
// visor-initiated announce conn (the check keys off c.PeerID()).
//
// Guard: if BOTH the peer whitelist is empty AND the consumer PK is
// unknown, the feed is left OPEN (nil allowlist) rather than gated to an
// empty set that would lock everyone out. When the consumer PK IS known it
// is always included, even when the peer whitelist is otherwise empty.
package visor

import (
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
)

// gatedCXOFeed pairs a service-consumed publisher with the PK of the
// service that consumes it, so its allowlist can be recomputed against the
// current peer whitelist on every refresh.
type gatedCXOFeed struct {
	pub         *treestore.Publisher
	consumer    cipher.PubKey
	hasConsumer bool
}

// composeFeedAllowlist builds the subscriber allowlist for one feed:
// v.peerWhitelist ∪ {consumer} (when known). Returns nil (OPEN) only when
// the resulting set would be empty — i.e. no peer-whitelist entries and no
// known consumer — so a feed is never gated to an empty allowlist that
// locks out every subscriber.
func composeFeedAllowlist(v *Visor, consumer cipher.PubKey, hasConsumer bool) []cipher.PubKey {
	seen := make(map[cipher.PubKey]struct{})
	var out []cipher.PubKey
	add := func(pk cipher.PubKey) {
		if pk == (cipher.PubKey{}) {
			return
		}
		if _, dup := seen[pk]; dup {
			return
		}
		seen[pk] = struct{}{}
		out = append(out, pk)
	}

	if v.peerWhitelist != nil {
		if all, err := v.peerWhitelist.All(); err == nil {
			for pk := range all {
				add(pk)
			}
		}
	}
	if hasConsumer {
		add(consumer)
	}

	if len(out) == 0 {
		// Both the peer whitelist is empty and the consumer is unknown:
		// leave the feed OPEN rather than gate to an empty set.
		return nil
	}
	return out
}

// registerGatedCXOFeed records a service-consumed publisher and its
// consumer PK so refreshGatedCXOAllowlists can re-apply the composed
// allowlist when the peer whitelist changes. The publisher's initial
// allowlist is set at construction (via PubConfig.SubscriberAllowlist), so
// this only records for future refreshes. nil publishers are ignored (the
// tp-list feed can be nil when the dedicated publisher didn't start).
func (v *Visor) registerGatedCXOFeed(pub *treestore.Publisher, consumer cipher.PubKey, hasConsumer bool) {
	if pub == nil {
		return
	}
	v.gatedCXOFeedsMu.Lock()
	v.gatedCXOFeeds = append(v.gatedCXOFeeds, gatedCXOFeed{
		pub:         pub,
		consumer:    consumer,
		hasConsumer: hasConsumer,
	})
	v.gatedCXOFeedsMu.Unlock()
}

// refreshGatedCXOAllowlists recomputes and re-applies every registered
// feed's subscriber allowlist against the current peer whitelist. Called
// after the peer whitelist is mutated at runtime (AddPtyWhitelist) so a
// newly-trusted hypervisor can immediately subscribe to any of the visor's
// feeds. Cheap: one SetAllowlist per feed (a mutex-guarded map swap).
func (v *Visor) refreshGatedCXOAllowlists() {
	v.gatedCXOFeedsMu.Lock()
	feeds := make([]gatedCXOFeed, len(v.gatedCXOFeeds))
	copy(feeds, v.gatedCXOFeeds)
	v.gatedCXOFeedsMu.Unlock()

	for _, fd := range feeds {
		fd.pub.SetAllowlist(composeFeedAllowlist(v, fd.consumer, fd.hasConsumer))
	}
}
