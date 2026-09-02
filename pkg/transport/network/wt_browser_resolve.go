//go:build js && wasm && !tinygo

// Package network pkg/transport/network/wt_browser_resolve.go c2-net-transport
//
// AR-resolved WT dial for the std-Go browser build. The full root binary under
// js/wasm carries a working address-resolver client (dmsg-routed), so a browser
// visor resolves a peer's WebTransport endpoint + pinned cert hash exactly the
// way a native visor does — no pre-populated table required. This is what lets
// the standard autoconnect's WT phase (and `tp add -t wt` in the in-tab CLI)
// form swtr transports from a tab. The explicit table (wt_browser.go) still
// wins when an entry is present. TinyGo keeps the table-only stub
// (wt_browser_resolve_tinygo.go): addrresolver is !tinygo and pulls quic-go.
package network

import (
	"context"
	"fmt"
	"net"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// dialResolvedWT resolves rPK's WT record from the address resolver and dials
// it with the browser WebTransport API. Public endpoints only, v6 before v4
// when both are advertised (the native path's LAN-first same-NAT handling is
// not ported: a browser can dial a LAN https:// endpoint, but the same-NAT
// detection rests on STUN state a browser visor doesn't have).
func (c *wtClient) dialResolvedWT(ctx context.Context, rPK cipher.PubKey) (net.Conn, error) {
	ar, _ := c.ar.(addrresolver.APIClient)
	if ar == nil {
		return nil, ErrWTEntryNotFound
	}
	vd, err := ar.Resolve(ctx, string(types.WT), rPK)
	if err != nil {
		return nil, fmt.Errorf("wt: resolve PK %s: %w", rPK, err)
	}
	if vd.CertHash == "" {
		return nil, ErrWTEntryNotFound
	}
	dialAt := func(hostport string) (net.Conn, error) {
		url := "https://" + hostport + wtPath
		c.log.Debugf("Dialing WT %v @ %s (AR-resolved, browser)", rPK, url)
		return wtDial(ctx, url, vd.CertHash)
	}
	addrV6 := canonicalAddr(vd.RemoteAddrV6, vd.Port)
	addrV4 := canonicalAddr(vd.RemoteAddr, vd.Port)
	if addrV6 != "" {
		if conn, derr := dialAt(addrV6); derr == nil {
			return conn, nil
		}
		if addrV4 == "" {
			return nil, fmt.Errorf("wt: v6 endpoint %s unreachable", addrV6)
		}
	}
	if addrV4 != "" {
		return dialAt(addrV4)
	}
	return nil, fmt.Errorf("wt: no dialable endpoint for %s", rPK)
}
