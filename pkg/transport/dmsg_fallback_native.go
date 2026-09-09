//go:build !js

package transport

// dmsgRelayFallback is whether EnsureBestTransport may mint a DMSG-type
// transport when every direct type failed. A native visor keeps it: behind a
// NAT that defeats stcpr/sudph, the relayed data plane is what keeps its routes
// alive. See dmsg_fallback_js.go for why a browser visor does not.
const dmsgRelayFallback = true
