//go:build !js

package transport

// DmsgRelayFallback is whether the visor may mint a DMSG-type transport when
// every direct type failed (EnsureBestTransport and the route-setup hook).
// A native visor keeps it: behind a
// NAT that defeats stcpr/sudph, the relayed data plane is what keeps its routes
// alive. See dmsg_fallback_js.go for why a browser visor does not.
const DmsgRelayFallback = true
