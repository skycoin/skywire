//go:build !tinygo || (js && wasm)

// Package transport pkg/transport/manager_ar_native.go c2-net-transport
package transport

import "github.com/skycoin/skywire/pkg/transport/network/addrresolver"

// ARClient returns the address resolver client used by this transport manager.
// arClient is stored as `any` (see Manager.arClient) to keep addrresolver — and
// its net/http dependency — out of the TinyGo graph; this native getter recovers
// the concrete type so existing callers are unchanged.
func (tm *Manager) ARClient() addrresolver.APIClient {
	c, _ := tm.arClient.(addrresolver.APIClient)
	return c
}
