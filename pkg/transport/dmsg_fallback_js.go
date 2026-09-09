//go:build js

package transport

// DmsgRelayFallback is false in a browser: a tab cannot make a direct
// transport to an arbitrary peer, so with the fallback on every on-demand
// dial (a proxy exit probe, a --direct dial) minted one more dmsg-type
// transport that nothing ever released — a desk tab was seen holding 118 of
// them, one per exit its auto-exit loop had tried. A dmsg-type transport
// buys the tab nothing it does not already have through dmsg itself; its
// routes go over the transports it can form (the same-origin link to the
// visor serving it, webtransport, webrtc) or through the host's graph.
const DmsgRelayFallback = false
