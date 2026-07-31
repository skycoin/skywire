// Package appnet pkg/app/appnet/dial_fallback.go c1-app-net
package appnet

import (
	"context"
	"fmt"
	"net"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// ConnDialer dials a single appnet Addr and returns the resulting conn or an
// error. It abstracts over the concrete dial primitive a caller uses (the RPC
// app.Client, the in-visor appnet.DialContext, a raw dmsg client, …) so the
// ordered-try logic below can be shared regardless of how each network dials.
type ConnDialer func(ctx context.Context, addr Addr) (net.Conn, error)

// DialWithFallback dials pk:port over each network in nets IN ORDER, returning
// the first successful conn and the network that carried it. This is the single
// shared "try skynet, then dmsg" primitive — replacing the per-app copies of the
// same ordered-try loop (skychat filexfer, skysocks-client, …). dmsg is the
// natural last entry: it relays through a dmsg server, so it's the
// always-available baseline when a direct/route network can't reach the peer.
//
// Networks are tried sequentially (not raced): the first is the preferred path,
// later ones are fallbacks tried only after the preferred one fails. Empty net
// entries are skipped. If every dial fails, the last error is returned (wrapped);
// if nets is empty, a descriptive error is returned.
func DialWithFallback(ctx context.Context, dial ConnDialer, pk cipher.PubKey, port routing.Port, nets ...Type) (net.Conn, Type, error) {
	var lastErr error
	for _, n := range nets {
		if n == "" {
			continue
		}
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		default:
		}
		conn, err := dial(ctx, Addr{Net: n, PubKey: pk, Port: port})
		if err == nil {
			return conn, n, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		return nil, "", fmt.Errorf("appnet: no network to dial %s:%d", pk, port)
	}
	return nil, "", fmt.Errorf("appnet: all networks failed to dial %s:%d: %w", pk, port, lastErr)
}

// OtherNet returns the alternate of the two first-class app networks
// (skynet↔dmsg), or "" for any other/unknown value. Small shared helper for the
// "dial the OTHER network" fallback pattern.
func OtherNet(n Type) Type {
	switch n {
	case TypeSkynet:
		return TypeDmsg
	case TypeDmsg:
		return TypeSkynet
	default:
		return ""
	}
}
