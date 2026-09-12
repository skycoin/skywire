// Package dmsg pkg/dmsg/dmsg/relay_inproc.go
//
// The IN-PROCESS attach: a second dmsg client in the same process as the first,
// riding the first one's sessions. It is the third and cheapest pipe into the
// relay (skynet in client_relay.go, a unix socket in relay_local.go, an
// in-memory pipe here), and the only one with no I/O under it at all.
//
// What it is for: a visor that hosts a service which must answer under its OWN
// key. The resolving dmsg/skynet proxy is the case that forced it — the survey
// whitelist knows that key, so it cannot rotate, and it is not the visor's.
// Before this the only ways to run it were a second full dmsg client (its own
// sessions to every server, its own discovery entry, re-registered on every
// restart — the identity churn that got the tpviz throwaway client deleted in
// #4500/#4501) or a separate process attached over a unix socket, which is
// machinery a co-located service does not need.
//
// Attached here the service keeps its key, holds exactly one session, publishes
// nothing, and its streams ride the VISOR's sessions and transports. The host
// pays for dmsg once.
//
// Why this is not the thing #4500/#4501 removed: that client registered. It
// posted a discovery entry under a throwaway key and churned 660 identities in
// fourteen days. A guest here is NoRegister by construction — it has no entry to
// churn, and nothing dials it inbound except through its host. If a future
// reader is about to delete this for the same reason that one went, that is the
// difference to check first.
//
// AUTHORIZATION. There is no trust boundary to enforce: both clients are
// constructed by the same process from the same config, so a guest can only
// exist because the operator put its key in that config. The allow callback is
// still threaded through — AcceptRelaySession takes one and the host may want
// to bound which of its own guests attach — but it is a statement about
// configuration, not a gate against an untrusted peer. That is the whole
// difference from relay_local.go, where the socket IS the boundary.
package dmsg

import (
	"context"
	"fmt"
	"net"

	"github.com/skycoin/skywire/pkg/cipher"
)

// InProcessRelayDialer returns a SessionDialer that attaches the caller to
// host's relay over an in-memory pipe.
//
// Each dial makes a net.Pipe, hands the host end to AcceptRelaySession on a
// goroutine and returns the guest end. There is no hello preamble (relay_local
// needs one only because a separate process cannot know the acceptor's key
// before the Noise XK handshake; here the caller was handed the host client
// itself) and no listener (nothing accepts — the dial IS the attach).
//
// The returned dialer refuses any carrier but skynet and any address but the
// host's own, so a guest whose relay peers were repointed elsewhere fails
// loudly instead of silently attaching to the wrong end of its own pipe.
func (ce *Client) InProcessRelayDialer(ctx context.Context, allow func(cipher.PubKey) bool) SessionDialer {
	return func(dialCtx context.Context, network, addr string) (net.Conn, error) {
		if network != CarrierSkynet {
			return nil, fmt.Errorf("dmsg in-process relay: unsupported carrier %q", network)
		}
		pk, _, err := ParseSkynetAddr(addr)
		if err != nil {
			return nil, err
		}
		if pk != ce.pk {
			return nil, fmt.Errorf("dmsg in-process relay: address names %s, host is %s", pk, ce.pk)
		}
		hostEnd, guestEnd := net.Pipe()
		go func() {
			// ctx (the HOST's serving lifetime), not dialCtx: dialCtx is the
			// guest's dial, which returns as soon as the session handshake
			// completes. Tying the accept side to it would tear the session
			// down the moment it came up.
			if err := ce.AcceptRelaySession(ctx, hostEnd, allow); err != nil {
				ce.log.WithError(err).Debug("dmsg in-process relay session ended with error")
			}
			_ = hostEnd.Close() //nolint:errcheck
		}()
		return guestEnd, nil
	}
}

// AttachInProcess points guest at host's relay: guest holds one session, to
// host, carried by an in-memory pipe. port is the relay port the synthetic
// server entry advertises — it is never dialed, but SetRelayPeers builds the
// entry from it and the dialer checks the key it names.
//
// Returns an error rather than attaching when the two clients share a key. Two
// dmsg clients on one key evict each other from every server they share, and a
// guest that is really its host is a configuration mistake worth naming at
// startup instead of debugging later.
func AttachInProcess(ctx context.Context, host, guest *Client, port uint16, allow func(cipher.PubKey) bool) error {
	if host == nil || guest == nil {
		return fmt.Errorf("dmsg in-process relay: nil client")
	}
	if host.pk == guest.pk {
		return fmt.Errorf("dmsg in-process relay: guest shares the host's key %s", host.pk)
	}
	guest.SetSessionDialer(host.InProcessRelayDialer(ctx, allow))
	if !guest.SetRelayPeers([]cipher.PubKey{host.pk}, port) {
		return fmt.Errorf("dmsg in-process relay: host %s was not accepted as a relay peer", host.pk)
	}
	return nil
}
