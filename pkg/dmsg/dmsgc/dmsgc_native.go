//go:build !js

package dmsgc

// browserRelayOnly is false on native builds: a native visor keeps its server
// sessions and discovery entry whether or not a relay is nominated. See
// dmsgc_js.go.
const browserRelayOnly = false
