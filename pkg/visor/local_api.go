// Package visor pkg/visor/local_api.go c3-vis-core
//
// The visor of THIS process, published for the apps that run inside it.
//
// An app launched in internal mode shares the visor's address space, so it can
// reach the same API surface the CLI reaches over net/rpc on `cli_addr` —
// without a socket, a dial, or a gob round trip. Until now such an app had no
// way to ask for it and always dialed TCP, which fails wherever `cli_addr` is
// deliberately empty: on Android every installed app holding INTERNET can
// connect to another app's loopback listener, so the visor there opens no RPC
// port at all, and everything skychat relays through the visor API — pairing,
// group chat, and voice calls — was permanently unavailable as a result.
//
// What a registration means is "the visor runs in this process", not "the RPC
// port is off". An app running as a separate process never sees this variable
// and keeps dialing, so the external-apps deployment is unchanged.
//
// The mirror image of this already exists: an internal app publishes its HTTP
// handler (launcher.RegisterHTTPHandler) and the visor serves it directly
// instead of dialing the app's own port.
package visor

import "sync"

var (
	localAPIMu sync.RWMutex
	localAPIv  API
)

// RegisterLocalAPI publishes api as the visor running in this process,
// replacing any previous registration (a visor restart in the same process
// re-registers, the way the handler registry re-registers on an app restart).
func RegisterLocalAPI(api API) {
	localAPIMu.Lock()
	defer localAPIMu.Unlock()
	localAPIv = api
}

// UnregisterLocalAPI clears the registration only if it still holds api.
//
// Compare-and-clear, not a plain clear: a visor shutting down can finish
// unwinding after a replacement has already registered (tests build visors
// back to back in one process), and an unconditional clear there would take
// the live visor out and leave in-process apps dialing a port nobody serves.
func UnregisterLocalAPI(api API) {
	localAPIMu.Lock()
	defer localAPIMu.Unlock()
	if localAPIv == api {
		localAPIv = nil
	}
}

// LocalAPI returns the visor running in this process, or nil when there is
// none and the caller must dial the RPC.
//
// Close on the returned value is a no-op. Callers treat what they get as an
// RPC *client* and close it when they redial — doing that to the visor itself
// would shut the process down.
func LocalAPI() API {
	localAPIMu.RLock()
	defer localAPIMu.RUnlock()
	if localAPIv == nil {
		return nil
	}
	return localAPIConn{localAPIv}
}

// localAPIConn gives the visor the lifecycle an RPC client has: every call is
// forwarded, except Close, which must not reach it.
type localAPIConn struct{ API }

// Close reports success without touching the visor.
func (localAPIConn) Close() error { return nil }
