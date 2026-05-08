// Package dmsg pkg/dmsg/client_sessions.go
package dmsg

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/hashicorp/yamux"
	"github.com/xtaci/smux"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// EnsureAndObtainSession attempts to obtain a session.
// If the session does not exist, we will attempt to establish one.
// It returns an error if the session does not exist AND cannot be established.
//
// sesMx is released across the discovery lookup (getServerEntry) because
// ce.dc may be a dmsgfirst-wrapped client whose primary path dials over
// DMSG itself — DialStream → phase 3 → EnsureAndObtainSession on the
// same goroutine, which would re-acquire sesMx and self-deadlock on a
// non-reentrant mutex. Holding the lock only during the cache check and
// the dialSession step still serializes concurrent dials to the same
// server, preserving the invariant the lock was protecting.
//
// We also resolve the server entry from the local entryCache first when
// possible. If we go to ce.dc here, the dmsgfirst wrapper's primary
// path is dmsghttp — which is itself a DialStream that lands back in
// EnsureAndObtainSession to set up its session, calls getServerEntry,
// and recurses into the same disc.Entry call until the goroutine stack
// blows up. The cache short-circuit removes that loop entirely for any
// server PK we already know about (configured servers are pre-seeded by
// dmsgc.New, so the common case never makes a network lookup; runtime-
// learned servers fall through to the slower disc-client path which
// only recurses if we genuinely don't have the entry yet — and even
// then, the recursion bottoms out as soon as one configured server's
// session is dialed and seeds itself).
func (ce *Client) EnsureAndObtainSession(ctx context.Context, srvPK cipher.PubKey) (ClientSession, error) {
	ce.sesMx.Lock()
	if dSes, ok := ce.clientSession(ce.porter, srvPK); ok {
		ce.sesMx.Unlock()
		return dSes, nil
	}
	ce.sesMx.Unlock()

	srvEntry, err := ce.resolveServerEntry(ctx, srvPK)
	if err != nil {
		return ClientSession{}, err
	}

	ce.sesMx.Lock()
	defer ce.sesMx.Unlock()
	// Re-check after re-locking — another goroutine may have raced
	// us to establish a session for the same server PK while we
	// were doing the discovery lookup. Use it instead of dialing
	// a duplicate.
	if dSes, ok := ce.clientSession(ce.porter, srvPK); ok {
		return dSes, nil
	}
	return ce.dialSession(ctx, srvEntry)
}

// EnsureSession ensures the existence of a session.
// It returns an error if the session does not exist AND cannot be established.
func (ce *Client) EnsureSession(ctx context.Context, entry *disc.Entry) error {
	ce.sesMx.Lock()
	defer ce.sesMx.Unlock()

	// If session with server of pk already exists, skip.
	if _, ok := ce.clientSession(ce.porter, entry.Static); ok {
		ce.log.WithField("remote_pk", entry.Static).Debug("Session already exists...")
		return nil
	}
	entry.Protocol = ce.conf.Protocol
	// Dial session.
	_, err := ce.dialSession(ctx, entry)
	return err
}

// It is expected that the session is created and served before the context cancels, otherwise an error will be returned.
// NOTE: This should not be called directly as it may lead to session duplicates.
// Only `ensureSession` or `EnsureAndObtainSession` should call this function.
func (ce *Client) dialSession(ctx context.Context, entry *disc.Entry) (cs ClientSession, err error) {
	ce.log.WithField("remote_pk", entry.Static).Debug("Dialing session...")

	const network = "tcp"
	var conn net.Conn

	// Trigger dial callback.
	if err := ce.conf.Callbacks.OnSessionDial(network, entry.Server.Address); err != nil {
		return ClientSession{}, fmt.Errorf("session dial is rejected by callback: %w", err)
	}
	defer func() {
		if err != nil {
			// Trigger disconnect callback when dial fails.
			ce.conf.Callbacks.OnSessionDisconnect(network, entry.Server.Address, err)
		}
	}()

	proxyAddr, ok := ctx.Value("socks5_proxy").(string)
	if ok && proxyAddr != "" {
		socksDialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
		if err != nil {
			return ClientSession{}, fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
		}
		conn, err = socksDialer.Dial(network, entry.Server.Address)
		if err != nil {
			return ClientSession{}, fmt.Errorf("failed to dial through SOCKS5 proxy: %w", err)
		}
	} else {
		conn, err = net.DialTimeout(network, entry.Server.Address, DialTimeout)
		if err != nil {
			return ClientSession{}, fmt.Errorf("failed to dial: %w", err)
		}
	}

	dSes, err := makeClientSession(&ce.EntityCommon, ce.porter, conn, entry.Static)
	if err != nil {
		conn.Close() //nolint:errcheck,gosec
		return ClientSession{}, err
	}
	if entry.Protocol == "smux" {
		dSes.sm.smux, err = smux.Client(conn, SmuxConfig())
		if err != nil {
			conn.Close() //nolint:errcheck,gosec
			return ClientSession{}, err
		}
		ce.log.Infof("smux stream session initial for %s", dSes.RemotePK().String())
	} else {
		dSes.sm.yamux, err = yamux.Client(conn, YamuxConfig())
		if err != nil {
			conn.Close() //nolint:errcheck,gosec
			return ClientSession{}, err
		}
		ce.log.Infof("yamux stream session initial for %s", dSes.RemotePK().String())
	}

	if !ce.setSession(ctx, dSes.SessionCommon) {
		_ = dSes.Close() //nolint:errcheck
		return ClientSession{}, errors.New("session already exists")
	}

	ce.wg.Add(1)
	go func() {
		defer ce.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				ce.log.Warnf("recovered panic in session serve goroutine: %v", r)
			}
		}()
		ce.log.WithField("remote_pk", dSes.RemotePK()).Debug("Serving session.")
		err := dSes.serve()
		if !isClosed(ce.done) {
			ce.sesMx.Lock()
			select {
			case ce.errCh <- fmt.Errorf("failed to serve dialed session to %s: %v", dSes.RemotePK(), err):
			default:
			}
			ce.sesMx.Unlock()
			ce.delSession(ctx, dSes.RemotePK())
		}

		// Trigger disconnect callback.
		ce.conf.Callbacks.OnSessionDisconnect(network, entry.Server.Address, err)
	}()

	return dSes, nil
}

// Session obtains an established session.
func (ce *Client) Session(pk cipher.PubKey) (ClientSession, bool) {
	return ce.clientSession(ce.porter, pk)
}
