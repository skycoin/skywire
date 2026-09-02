//go:build js && !tinygo

// Package skyenv pkg/skyenv/carriers_js.go c0-com-env
package skyenv

// DefaultDmsgCarriers pins freshly generated configs to the WebSocket
// carrier under js/wasm: a browser (or node) runtime has no raw TCP,
// so the native tcp-first preference can never connect, while every
// dmsg server in the fleet advertises wss. Writing it into the config
// keeps the behavior visible and editable rather than hidden in the
// runtime. Gated !tinygo because the TinyGo install-page build
// generates configs for NATIVE visors, which must not be pinned to ws.
var DefaultDmsgCarriers = []string{"ws"}

// DefaultHypervisorHTTPAddr binds the browser visor's hypervisor UI to
// :8001 instead of the native :8000. The two live in different
// namespaces (the page's vnet port table vs the host's real loopback),
// but the nested browser exposes BOTH: a vnet listener shadows the
// real port, and an unclaimed port falls through to the host. Distinct
// defaults keep http://127.0.0.1:8000 meaning "the host's native
// visor" even while a browser visor runs — while an operator who
// EXPLICITLY configures :8000 shadows it, exactly like binding a port.
const DefaultHypervisorHTTPAddr = ":8001"
