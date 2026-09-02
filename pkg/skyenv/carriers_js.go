//go:build js && !tinygo

// Package skyenv pkg/skyenv/carriers_js.go c0-com-env
package skyenv

// DefaultDmsgCarriers pins freshly generated configs to the two carriers
// a browser runtime can actually dial, WebTransport preferred: wt is
// HTTP/3 (lower overhead than wss-over-TLS-over-TCP) and every fleet
// dmsg server serves it, but the FIRST dial still lands on wss — the
// embedded server seeds carry no WT cert hashes (they rotate), so wt
// only becomes dialable once live discovery supplies the hash, and the
// visor's converge ticker (initDmsg) then re-dials wss sessions over to
// wt. Listing wt ahead of ws is what arms that convergence
// (prefersWTOverWS). Writing the pin into the config keeps the behavior
// visible and editable rather than hidden in the runtime. Gated !tinygo
// because the TinyGo install-page build generates configs for NATIVE
// visors, which must not be pinned to browser carriers.
var DefaultDmsgCarriers = []string{"wt", "ws"}

// DefaultHypervisorHTTPAddr binds the browser visor's hypervisor UI to
// :8001 instead of the native :8000. The two live in different
// namespaces (the page's vnet port table vs the host's real loopback),
// but the nested browser exposes BOTH: a vnet listener shadows the
// real port, and an unclaimed port falls through to the host. Distinct
// defaults keep http://127.0.0.1:8000 meaning "the host's native
// visor" even while a browser visor runs — while an operator who
// EXPLICITLY configures :8000 shadows it, exactly like binding a port.
const DefaultHypervisorHTTPAddr = ":8001"
