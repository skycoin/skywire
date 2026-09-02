//go:build js && wasm && tinygo

// Package network pkg/transport/network/wt_browser_resolve_tinygo.go c2-net-transport
//
// TinyGo browser build: no address-resolver client (addrresolver is !tinygo and
// pulls quic-go), so WT dials come only from the table the page's autoconnect
// populates with the AR-resolved endpoint + cert hash.
package network

import (
	"context"
	"net"

	"github.com/skycoin/skywire/pkg/cipher"
)

func (c *wtClient) dialResolvedWT(_ context.Context, _ cipher.PubKey) (net.Conn, error) {
	return nil, ErrWTEntryNotFound
}
