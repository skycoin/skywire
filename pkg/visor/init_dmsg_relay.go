// Package visor pkg/visor/init_dmsg_relay.go c3-vis-core
//
// dmsg over skynet through this visor (#4484 stage 3). Two halves of one
// mechanism, both on the visor's single dmsg client:
//
//   - RELAY (accept side): a listener on skyenv.DmsgRelayPort over skynet. A
//     whitelisted peer that dials it gets a server-role dmsg session on this
//     client (dmsg.Client.AcceptRelaySession); its stream requests are carried
//     over this visor's own dmsg server sessions under the PEER's key.
//   - CARRIER (dial side): the client's session dialer for skynet:// server
//     addresses. A seeded server entry "skynet://<pk>:70" is dialed as a
//     skywire route to that visor's relay, then wrapped in the usual
//     Noise+yamux dmsg session.
//
// Neither half registers anything in discovery or opens a TCP listener.
package visor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
)

// skynetSessionDialTimeout bounds one relay session dial over skynet.
const skynetSessionDialTimeout = 30 * time.Second

// skynetSessionDialer is the dmsg client's SessionDialer for the skynet
// carrier: it turns "skynet://<pk>:<port>" into a skywire route dial to that
// visor's relay listener. Bounded by skynetSessionDialTimeout: unlike a TCP dial
// this one sets up a route, and at boot that waits on the setup node.
func skynetSessionDialer(ctx context.Context, network, addr string) (net.Conn, error) {
	if network != dmsg.CarrierSkynet {
		return nil, fmt.Errorf("dmsg session dialer: unsupported carrier %q", network)
	}
	pk, port, err := dmsg.ParseSkynetAddr(addr)
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, skynetSessionDialTimeout)
	defer cancel()
	return appnet.DialContext(dialCtx, appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: pk,
		Port:   routing.Port(port),
	})
}

// initDmsgRelay binds the relay listener on skyenv.DmsgRelayPort over skynet
// (once the router's networker exists) and serves every accepted conn as a
// relay session on dmsgC. Admission (relayPeerAllowed) is the peer whitelist
// plus the visors this node manages as their hypervisor, and the key the peer
// proves in the dmsg Noise handshake must be the key the route was set up
// for.
func (v *Visor) initDmsgRelay(ctx context.Context, dmsgC *dmsg.Client) {
	log := v.MasterLogger().PackageLogger("dmsg_relay")
	goServeSkynetMirror(ctx, v.conf.PK, skyenv.DmsgRelayPort, "dmsg_relay", log,
		func(lis net.Listener) {
			go func() {
				<-ctx.Done()
				_ = lis.Close() //nolint:errcheck
			}()
			for {
				conn, err := lis.Accept()
				if err != nil {
					if ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
						log.WithError(err).Warn("dmsg relay accept failed; relay listener stopped")
					}
					return
				}
				routePK, hasRoutePK := remotePK(conn.RemoteAddr())
				if hasRoutePK && !v.relayPeerAllowed(routePK) {
					// Refuse before the handshake: the route already names the
					// peer, so an unlisted one costs no crypto.
					log.WithField("peer", routePK.String()).Debug("dmsg relay: peer not whitelisted")
					_ = conn.Close() //nolint:errcheck
					continue
				}
				go func(conn net.Conn) {
					allow := func(pk cipher.PubKey) bool {
						if hasRoutePK && pk != routePK {
							return false
						}
						return v.relayPeerAllowed(pk)
					}
					if err := dmsgC.AcceptRelaySession(ctx, conn, allow); err != nil {
						log.WithError(err).Debug("dmsg relay session ended with error")
					}
				}(conn)
			}
		})
}

// relayPeerAllowed reports whether pk may attach to this visor's dmsg relay:
// a peer on the peer whitelist (self, configured hypervisors, the pty
// whitelist, anything a hypervisor vouched for), or a visor this node manages
// as its hypervisor. A managed visor already lets us run RPC and a shell on
// it; carrying its dmsg streams when it cannot reach a server itself is the
// hypervisor's side of that relationship, and it needs no configuration —
// the fleet case for stage 3 of #4484.
func (v *Visor) relayPeerAllowed(pk cipher.PubKey) bool {
	if v.peerWhitelist != nil {
		if ok, err := v.peerWhitelist.Get(pk); err == nil && ok {
			return true
		}
	}
	// Lock-free read of remoteVisors, as RemoteVisors/ManagedVisors do.
	for _, conn := range v.remoteVisors {
		if conn.Addr.PK == pk {
			return true
		}
	}
	// A peer we hold a transport to may relay through us: a transport is
	// mutual, and a tab served by this visor has exactly one — to us.
	return v.hasTransportTo(pk)
}

// relayNominationInterval is how often the visor re-derives its relay
// nominees from the transports it currently holds.
const relayNominationInterval = 10 * time.Second

// nominateRelayPeers keeps the dmsg client's relay nominees in step with the
// visor's transports: a persistent-transport peer that this visor has a live
// transport to is nominated (dmsg.Client.SetRelayPeers); when the transport
// goes, so does the nomination. No configuration takes part beyond the pin the
// operator already made — a persistent transport says "keep a link to this
// peer", and a desk tab's attach to the visor serving it is exactly one. The
// hypervisor list is deliberately NOT a source: it names the peers that manage
// THIS visor, which on a hypervisor includes the tabs it serves — a host must
// not carry its dmsg through a browser. Pairing (stage 5) will widen this. A nominee that
// has no relay acceptor, or refuses us, fails the dial and is backed off by
// the client; the configured servers stay the bootstrap and the fallback.
func (v *Visor) nominateRelayPeers(ctx context.Context, dmsgC *dmsg.Client, log *logging.Logger) {
	t := time.NewTicker(relayNominationInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			v.scanPendingPairs()
			nominees := v.relayNominees()
			if dmsgC.SetRelayPeers(nominees, skyenv.DmsgRelayPort) {
				log.WithField("nominees", nominees).Info("dmsg relay nominees changed")
			}
		}
	}
}

// relayHubLimit bounds how many co-resident visor+dmsg-server hubs are
// nominated alongside the operator's own pins. Each nominee costs a dial
// attempt, and one working relay is all a visor needs; a handful is enough
// cover for the working one going away.
const relayHubLimit = 4

// relayNominees returns this visor's relay candidates: the persistent-transport
// peers it holds a live transport to, then the hubs it can find.
func (v *Visor) relayNominees() []cipher.PubKey {
	if v.conf == nil {
		return nil
	}
	seen := make(map[cipher.PubKey]struct{})
	out := v.pinnedRelayNominees(seen)
	return append(out, v.hubRelayNominees(seen)...)
}

// hubRelayNominees returns the visors that also run a dmsg server on their own
// key. Those are the hubs: a plain dmsg server has no visor behind it and so no
// relay acceptor to attach to, while a visor that IS a server holds the real
// sessions this visor is trying to stop holding itself.
//
// The signal is the discovery entry carrying BOTH a Client and a Server
// section, which only a co-resident pair produces. No direct transport is
// required: the attach dials skynet://<pk>:70, which resolves over a direct
// transport, a one-hop relay through a shared peer, or a route. A nominee that
// cannot be reached, has no acceptor, or refuses simply fails and is backed off
// by the client, so proposing one costs a dial and nothing else.
//
// Peers already reachable directly come first, being the cheapest path.
func (v *Visor) hubRelayNominees(seen map[cipher.PubKey]struct{}) []cipher.PubKey {
	if v.dmsgServersCache == nil || v.conf == nil {
		return nil
	}
	var direct, remote []cipher.PubKey
	for _, e := range v.dmsgServersCache.All() {
		if e == nil || e.Server == nil || e.Client == nil {
			continue // a plain server, or a plain client: not a hub
		}
		pk := e.Static
		if pk == v.conf.PK {
			continue
		}
		if _, dup := seen[pk]; dup {
			continue
		}
		seen[pk] = struct{}{}
		if v.hasTransportTo(pk) {
			direct = append(direct, pk)
			continue
		}
		remote = append(remote, pk)
	}
	// Stable order: the nominee set is compared for equality every tick, and a
	// set that reshuffles would restart the client's serve pass each time.
	sortPubKeys(direct)
	sortPubKeys(remote)
	out := append(direct, remote...)
	if len(out) > relayHubLimit {
		out = out[:relayHubLimit]
	}
	return out
}

func sortPubKeys(pks []cipher.PubKey) {
	sort.Slice(pks, func(i, j int) bool { return pks[i].Hex() < pks[j].Hex() })
}

// pinnedRelayNominees returns the persistent-transport peers this visor has a
// live transport to.
func (v *Visor) pinnedRelayNominees(seen map[cipher.PubKey]struct{}) []cipher.PubKey {
	if v.tpM == nil || v.conf == nil {
		return nil
	}
	trusted := make(map[cipher.PubKey]struct{}, len(v.conf.PersistentTransports))
	for _, pt := range v.conf.PersistentTransports {
		trusted[pt.PK] = struct{}{}
	}
	if len(trusted) == 0 {
		return nil
	}
	var out []cipher.PubKey
	v.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
		pk := tp.Remote()
		if _, ok := trusted[pk]; !ok || tp.IsClosed() || pk == v.conf.PK {
			return true
		}
		if _, dup := seen[pk]; dup {
			return true
		}
		seen[pk] = struct{}{}
		out = append(out, pk)
		return true
	})
	return out
}

// hasTransportTo reports whether this visor holds a live transport to pk.
func (v *Visor) hasTransportTo(pk cipher.PubKey) bool {
	if v.tpM == nil {
		return false
	}
	found := false
	v.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
		if tp.Remote() == pk && !tp.IsClosed() {
			found = true
			return false
		}
		return true
	})
	return found
}
