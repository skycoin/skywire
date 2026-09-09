//go:build !js

package dmsgc

// browserUnpublished is false on native builds: a native visor publishes its
// discovery entry so peers can reach it over dmsg. See dmsgc_js.go.
const browserUnpublished = false
