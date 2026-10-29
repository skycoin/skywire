// Package launcher pkg/app/launcher/registry.go c2-vis-appsvc
package launcher

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/skycoin/skywire/pkg/app/appcommon"
)

// AppFunc is an alias for the app function type defined in appcommon.
type AppFunc = appcommon.AppFunc

var (
	appRegistryMu sync.Mutex
	appRegistry   = make(map[string]AppFunc)
)

// RegisterApp registers an app function by name.
//
// Registration is mutex-guarded and idempotent. Guarded because module inits run
// concurrently (visorinit.InitConcurrent), so several embedded-app modules call
// this in parallel — an unsynchronized map write there is a data race that can
// fatally crash the process. Idempotent because a visor Resume re-runs the whole
// module-init graph after Suspend, so each embedded app re-registers its (same,
// deterministic) func; the old hard panic on a duplicate name turned Resume into
// a half-initialized, still-"suspended" state (dmsg_pty/dmsgweb/skynetweb panicked
// on their already-registered names). Re-registering overwrites — the same
// overwrite-on-re-register semantics RegisterHTTPHandler below already uses.
func RegisterApp(name string, fn AppFunc) {
	appRegistryMu.Lock()
	defer appRegistryMu.Unlock()
	appRegistry[name] = fn
}

// GetApp returns the app function for the given name, or nil if not found.
func GetApp(name string) (AppFunc, bool) {
	appRegistryMu.Lock()
	defer appRegistryMu.Unlock()
	fn, ok := appRegistry[name]
	return fn, ok
}

// RegisteredApps returns the sorted names of every app registered in this process.
func RegisteredApps() []string {
	appRegistryMu.Lock()
	names := make([]string, 0, len(appRegistry))
	for name := range appRegistry {
		names = append(names, name)
	}
	appRegistryMu.Unlock()
	sort.Strings(names)
	return names
}

// App kinds reported by the catalog.
const (
	KindServer   = "server"
	KindClient   = "client"
	KindService  = "service"
	KindExternal = "external"
)

// AppInfo is the static catalog metadata for a registered app.
type AppInfo struct {
	Kind          string
	Description   string
	MultiInstance bool
}

var appInfo = map[string]AppInfo{
	"pty":             {KindService, "Remote shell and file access over dmsg for whitelisted keys.", false},
	"skychat":         {KindService, "Peer to peer chat over skywire.", false},
	"skynet":          {KindServer, "Port forwarding server that exposes local ports over skynet.", false},
	"skynet-client":   {KindClient, "Port forwarding client that reaches a remote skynet server.", true},
	"skysocks":        {KindServer, "SOCKS5 proxy exit for other visors.", false},
	"skysocks-client": {KindClient, "SOCKS5 proxy client that routes through a remote skysocks exit.", true},
	"vpn-server":      {KindServer, "VPN exit for other visors.", false},
	"vpn-client":      {KindClient, "VPN client that tunnels this host through a remote vpn-server.", false},
	"vpn-router":      {KindService, "Shares the vpn-client tunnel with LAN or WiFi clients.", false},
	"skydns":          {KindService, "Resolves .dmsg and .skynet names for every app on this device.", false},
	"skycoin-web":     {KindService, "Skycoin thin client web wallet.", false},
	"skydex-market":   {KindServer, "Decentralized exchange market backend.", false},
	"skydex-client":   {KindClient, "Decentralized exchange trading UI.", true},
	"skymail":         {KindService, "Mailbox that sends and receives mail over skywire.", false},
	"dmsgweb":         {KindService, "SOCKS5 proxy that resolves .dmsg names.", false},
	"skynetweb":       {KindService, "SOCKS5 proxy that resolves .skynet names.", false},
}

// AppMetadata returns the catalog metadata for name. Each entry of the
// visor's resolvers config registers as resolver-<name>.
func AppMetadata(name string) (AppInfo, bool) {
	if info, ok := appInfo[name]; ok {
		return info, true
	}
	if strings.HasPrefix(name, "resolver-") {
		return AppInfo{KindService, "Resolving proxy from the visor's resolvers config.", false}, true
	}
	return AppInfo{}, false
}

// httpHandlers holds in-process HTTP handlers published by internal
// (in-process) apps, keyed by app name. An app running in the visor process
// registers its http.Handler here so a same-process consumer (the visor's
// control surface, which backs the hypervisor UI's /skychat/proxy/* routes)
// can serve it directly via ServeHTTP with no loopback dial.
//
// Registration says "this app runs in this process", not "this app has no
// port": an in-process app may also bind a TCP listener for the same mux, and
// the two surfaces coexist. What a missing entry means is that the app runs
// somewhere else — a separate process, whose registry writes this one never
// sees — so the consumer falls back to an HTTP proxy to its address.
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

// ClearHTTPHandler removes name's entry only if it currently holds h.
//
// This is what a shutting-down app should call instead of
// RegisterHTTPHandler(name, nil). An app restart re-registers before the old
// instance finishes unwinding, so a plain delete-by-name races: the old
// instance's cleanup can land after the new one has published, taking a live
// handler out of the registry and leaving the consumer with nothing to serve.
// Compare-and-delete under the write lock closes that window — a Get followed
// by a conditional Register would not, since the two are separate acquisitions.
func ClearHTTPHandler(name string, h http.Handler) {
	httpHandlersMu.Lock()
	defer httpHandlersMu.Unlock()
	if httpHandlers[name] == h {
		delete(httpHandlers, name)
	}
}
