//go:build !tinygo

// Package dmsgclient pkg/dmsg/dmsgclient/local_relay.go c1-net-dmsg
//
// Bootstrap for a standalone dmsg client that ATTACHES to a visor on the same
// host instead of dialing dmsg servers itself (dmsg.Client.AttachLocalRelay,
// and pkg/visor/init_dmsg_relay_local.go for the other end).
//
// The point is the key. Every standalone tool here — dmsgweb, dmsg-socks5,
// dmsghttp, curl — takes a --sk and runs under a fixed identity that something
// else in the deployment already knows: a survey whitelist, a service PK
// written into someone's config, a rewards record. Those keys cannot rotate, so
// the usual answer to "this process should not hold its own server sessions"
// (run it under an ephemeral key, or fold it into the visor) is not available.
// Attaching keeps the key exactly as it is and moves only the transport.
//
// What changes for the process: one session instead of MinSessions (to the
// visor over a unix socket), no discovery entry at all, and its streams
// forwarded over the visor's sessions and transports. What does not change: the
// key it dials under, the key destinations see, and the dmsg API it uses.
package dmsgclient

import (
	"context"
	"fmt"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// LocalRelayIdentity reads the acceptor's hello at network/addr and hangs up,
// returning the visor's public key and the dmsg port its relay is published on.
//
// It creates no dmsg client and therefore no identity: "is there a relay
// acceptor on this socket, and whose?" is a question an operator asks while
// setting the socket up, and answering it must not mint a throwaway key.
func LocalRelayIdentity(ctx context.Context, network, addr string) (cipher.PubKey, uint16, error) {
	return dmsg.LocalRelayDialer{Network: network, Addr: addr}.PubKey(ctx)
}

// StartDmsgLocalRelay builds and serves a dmsg client attached to the local
// visor's relay acceptor at network/addr ("unix" + socket path, or "tcp" + a
// loopback address), and blocks until it holds the relay session or ctx ends.
//
// The client is RelayOnly and NoRegister, which together are what "attached"
// means in practice:
//
//   - RelayOnly: once the relay session is up the serve loop is satisfied by it
//     and stops looking for servers, so this process holds exactly one session
//     — and it is the visor beside it, not a slot on a shared dmsg server.
//   - NoRegister: nothing to publish. A discovery entry names the servers a
//     client sits on so peers can rendezvous there; an attached client sits on
//     none, and peers reach it through the relay. Publishing an entry naming
//     nothing would be worse than publishing none: it is what a peer would
//     resolve and then fail to use.
//
// The discovery client is an EMPTY direct client rather than a real one. It is
// not a stub for something missing — it is the correct discovery for a process
// that neither registers nor resolves: DialStream tries relay sessions before
// any lookup (see client_dial.go's phase 0), so destinations are reached over
// the relay, which does its own resolution with the visor's entry cache and the
// visor's sessions. Handing this client a live discovery instead would make it
// dial dmsg-discovery over the relay on every lookup to answer a question the
// relay already answered.
//
// It returns the client, a stop func, and the visor's public key.
func StartDmsgLocalRelay(ctx context.Context, dlog *logging.Logger, pk cipher.PubKey, sk cipher.SecKey, network, addr string) (*dmsg.Client, func(), cipher.PubKey, error) {
	if dlog == nil {
		return nil, nil, cipher.PubKey{}, fmt.Errorf("nil logger")
	}
	dmsgC := dmsg.NewClient(pk, sk, direct.NewClient(nil, dlog), &dmsg.Config{
		MinSessions: 1,
		RelayOnly:   true,
		NoRegister:  true,
		ClientType:  "attached",
	})
	dmsgC.SetLogger(dlog)

	relayPK, err := dmsgC.AttachLocalRelay(ctx, network, addr)
	if err != nil {
		_ = dmsgC.Close() //nolint:errcheck
		return nil, nil, cipher.PubKey{}, fmt.Errorf("dmsg local relay attach: %w", err)
	}

	go dmsgC.Serve(ctx)

	stop := func() {
		if err := dmsgC.Close(); err != nil {
			dlog.WithError(err).Debug("Disconnected from the local dmsg relay.")
		}
	}
	dlog.WithField("relay", relayPK.String()).WithField("addr", network+"://"+addr).
		Debug("Attaching to the local dmsg relay...")
	select {
	case <-ctx.Done():
		stop()
		return nil, nil, cipher.PubKey{}, ctx.Err()
	case <-dmsgC.Ready():
		dlog.WithField("relay", relayPK.String()).Debug("Attached to the local dmsg relay.")
		return dmsgC, stop, relayPK, nil
	}
}
