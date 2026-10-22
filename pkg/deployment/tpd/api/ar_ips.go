// Package api pkg/deployment/tpd/api/ar_ips.go c4-net-discovery
//
// Public IPs from the address resolver's bindings feed, so the per-visor
// bandwidth day can leave out transports between two visors on one IP. A
// visor marks such transports itself, but only where it learns the peer's
// address: a webrtc conn has none to compare, and visors from before the mark
// send none. The resolver sees every bound visor's public IP, whatever
// transport carries the bytes.
package api

import (
	"net"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cxo/cxosub"
	"github.com/skycoin/skywire/pkg/deployment/ar/arfeed"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
)

// arIPs indexes the bindings feed by visor: public key (hex) to the public
// IPs the resolver holds for it.
type arIPs struct {
	mu      sync.Mutex
	feed    *cxosub.Manager
	builtAt time.Time
	index   map[string][]string
}

// SetARBindingsFeed has the per-visor bandwidth day treat two visors the
// address resolver sees at one public IP as on one network.
func (api *API) SetARBindingsFeed(mgr *cxosub.Manager) {
	api.arIPs.mu.Lock()
	api.arIPs.feed = mgr
	api.arIPs.mu.Unlock()
}

// samePublicIP returns the judge ComputeVisorBW takes, and whether it may
// settle a day now: false while a feed is set but has not synced, since a
// settled day is final. With no feed set it returns nil and true.
func (api *API) samePublicIP() (func(a, b string) bool, bool) {
	idx, ok := api.arIPs.snapshot()
	if !ok {
		return nil, false
	}
	if idx == nil {
		return nil, true
	}
	return func(a, b string) bool {
		for _, x := range idx[a] {
			for _, y := range idx[b] {
				if x == y {
					return true
				}
			}
		}
		return false
	}, true
}

// snapshot returns the current index, rebuilt when the feed has synced since.
func (r *arIPs) snapshot() (map[string][]string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.feed == nil {
		return nil, true
	}
	last := r.feed.LastSync(cxosub.FeedARBindings)
	if last.IsZero() {
		return nil, false
	}
	if r.index == nil || !r.builtAt.Equal(last) {
		index := map[string][]string{}
		r.feed.Walk(cxosub.FeedARBindings, "", func(_ string, body []byte) bool {
			peers, err := arfeed.DecodeBucket(body)
			if err != nil {
				return true
			}
			for pk, b := range peers {
				if ips := bindingIPs(b); len(ips) > 0 {
					index[pk] = ips
				}
			}
			return true
		})
		r.index, r.builtAt = index, last
	}
	return r.index, true
}

// bindingIPs is the distinct public IPs across a visor's bindings.
func bindingIPs(b *arfeed.PeerBindings) []string {
	var out []string
	add := func(addr string) {
		if h, _, err := net.SplitHostPort(addr); err == nil {
			addr = h
		}
		ip := net.ParseIP(addr)
		if ip == nil || (ip.To4() != nil && !netutil.IsPublicIP(ip)) {
			return
		}
		s := ip.String()
		for _, have := range out {
			if have == s {
				return
			}
		}
		out = append(out, s)
	}
	for _, d := range []*addrresolver.VisorData{b.STCPR, b.SUDPH, b.QUIC, b.WT} {
		if d != nil {
			add(d.RemoteAddr)
			add(d.RemoteAddrV6)
		}
	}
	return out
}
