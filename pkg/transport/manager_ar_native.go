// Package transport pkg/transport/manager_ar_native.go c2-net-transport
package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// ARClient returns the address resolver client used by this transport manager.
// arClient is stored as `any` (see Manager.arClient) to keep addrresolver — and
// its net/http dependency — out of the TinyGo graph; this native getter recovers
// the concrete type so existing callers are unchanged.
func (tm *Manager) ARClient() addrresolver.APIClient {
	c, _ := tm.arClient.(addrresolver.APIClient)
	return c
}

// ErrNotLocal is EnsureLocalTransport's answer for a peer that is not on this
// visor's local network.
var ErrNotLocal = errors.New("peer is not on the local network")

// Bounds on EnsureLocalTransport. A LAN peer answers in well under a second,
// and a peer it cannot reach must cost a sender little before it takes its
// usual network.
const (
	localResolveTimeout = 5 * time.Second
	localDialTimeout    = 8 * time.Second
)

// How long a peer that failed EnsureLocalTransport is answered ErrNotLocal
// without asking again. One behind the same public IP that did not answer
// costs a dial timeout, as phones behind a carrier NAT do, so it waits longer.
const (
	notLocalBackoff    = time.Minute
	unreachableBackoff = 10 * time.Minute
)

// EnsureLocalTransport makes sure a transport other than dmsg reaches remote
// when remote is on this visor's local network, which the address resolver
// sees behind the same public IP. stcpr and squicr dial its LAN address first.
// A peer elsewhere gets ErrNotLocal and nothing is created.
func (tm *Manager) EnsureLocalTransport(ctx context.Context, remote cipher.PubKey) error {
	if tm.hasDirectTransport(remote) {
		return nil
	}
	if until, ok := tm.localMiss.Load(remote); ok && time.Now().Before(until.(time.Time)) { //nolint:forcetypeassert
		return ErrNotLocal
	}
	backoff, err := tm.makeLocalTransport(ctx, remote)
	switch {
	case err == nil:
		tm.localMiss.Delete(remote)
	case ctx.Err() == nil:
		tm.localMiss.Store(remote, time.Now().Add(backoff))
	}
	return err
}

// makeLocalTransport makes the transport, or says how long to leave remote be.
func (tm *Manager) makeLocalTransport(ctx context.Context, remote cipher.PubKey) (time.Duration, error) {
	ar := tm.ARClient()
	if ar == nil {
		return notLocalBackoff, ErrNotLocal
	}
	rctx, cancel := context.WithTimeout(ctx, localResolveTimeout)
	data, err := ar.Resolve(rctx, string(types.STCPR), remote)
	cancel()
	if err != nil {
		return notLocalBackoff, fmt.Errorf("%w: %w", ErrNotLocal, err)
	}
	if !data.IsLocal && !sameIP(data.RemoteAddr, ar.LocalPublicIP()) {
		return notLocalBackoff, ErrNotLocal
	}
	t := types.STCPR
	if !tm.IsKnownNetwork(t) {
		t = types.QUIC
	}
	if !tm.IsKnownNetwork(t) {
		return notLocalBackoff, ErrNotLocal
	}
	dctx, cancel := context.WithTimeout(ctx, localDialTimeout)
	_, err = tm.SaveTransport(dctx, remote, t, LabelAutomatic)
	cancel()
	if err != nil {
		return unreachableBackoff, fmt.Errorf("%s: %w", t, err)
	}
	return 0, nil
}

// hasDirectTransport reports whether a live transport other than dmsg reaches
// remote, which is what VStreamMux.Dial rides.
func (tm *Manager) hasDirectTransport(remote cipher.PubKey) bool {
	found := false
	tm.WalkTransports(func(tp *ManagedTransport) bool {
		found = tp.Remote() == remote && !tp.IsClosed() && tp.Type() != types.DMSG
		return !found
	})
	return found
}

// sameIP reports whether addr, with or without a port, is the IP ip.
func sameIP(addr, ip string) bool {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}
	return ip != "" && addr == ip
}
