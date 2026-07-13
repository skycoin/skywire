// Package launcher pkg/app/launcher/registry.go
package launcher

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/skycoin/skywire/pkg/app/appcommon"
)

// AppFunc is an alias for the app function type defined in appcommon.
type AppFunc = appcommon.AppFunc

var appRegistry = make(map[string]AppFunc)

// RegisterApp registers an app function by name.
func RegisterApp(name string, fn AppFunc) {
	if _, exists := appRegistry[name]; exists {
		panic(fmt.Sprintf("app %q already registered", name))
	}
	appRegistry[name] = fn
}

// GetApp returns the app function for the given name, or nil if not found.
func GetApp(name string) (AppFunc, bool) {
	fn, ok := appRegistry[name]
	return fn, ok
}

// httpHandlers holds in-process HTTP handlers published by internal
// (in-process) apps, keyed by app name. A portless-internal app — one that
// runs in the visor process with no TCP port of its own — registers its
// http.Handler here so a same-process consumer (the visor's control surface,
// which backs the hypervisor UI's /skychat/proxy/* routes) can serve it
// directly via ServeHTTP with no loopback dial. When the app instead runs
// externally on its own port, no handler is registered and the consumer falls
// back to an HTTP proxy to that port.
var (
	httpHandlersMu sync.RWMutex
	httpHandlers   = make(map[string]http.Handler)
)

// RegisterHTTPHandler publishes an internal app's in-process HTTP handler.
// It overwrites any previous handler for the same name (an app restart
// re-registers); passing nil clears the entry (call on shutdown).
func RegisterHTTPHandler(name string, h http.Handler) {
	httpHandlersMu.Lock()
	defer httpHandlersMu.Unlock()
	if h == nil {
		delete(httpHandlers, name)
		return
	}
	httpHandlers[name] = h
}

// GetHTTPHandler returns the in-process HTTP handler an internal app published,
// or nil if none is registered (the app runs externally on its own port).
func GetHTTPHandler(name string) http.Handler {
	httpHandlersMu.RLock()
	defer httpHandlersMu.RUnlock()
	return httpHandlers[name]
}
