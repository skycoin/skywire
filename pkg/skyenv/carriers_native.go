//go:build !js || tinygo

// Package skyenv pkg/skyenv/carriers_native.go c0-com-env
package skyenv

// DefaultDmsgCarriers is the platform default for dmsg.carriers in
// freshly generated configs. Nil on native platforms (and in the
// TinyGo install-page build, which generates configs FOR native
// visors): the dmsg client's own carrier preference applies.
var DefaultDmsgCarriers []string

// DefaultHypervisorHTTPAddr is the platform default bind address for
// the hypervisor web UI.
const DefaultHypervisorHTTPAddr = ":8000"

// DefaultHypervisorDeskAddr is the platform default bind address for the
// desk — the wasm-visor hypervisor UI — served as its own listener beside
// the dashboard's, with the same API behind it. Not :8002: that is
// skycoin-web's default host port (SKYCOINWEBADDR), and it is also the
// port the docs take on the tab's virtual loopback (vnet:8002), so a
// desk there read as the same thing from inside and outside the tab.
const DefaultHypervisorDeskAddr = ":8010"
