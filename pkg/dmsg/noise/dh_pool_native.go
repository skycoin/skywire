//go:build !js

// Package noise pkg/dmsg/noise/dh_pool_native.go c1-net-dmsg
package noise

// Pool sizing for a native visor or deployment service. Sized for the
// thundering herd after a deployment restart: hundreds of peers redialing at
// once, each needing an ephemeral keypair for its noise handshake, on a host
// with cores to spare.
const (
	keypairPoolSize   = 512
	keypairGenerators = 4
)
