//go:build js

// Package noise pkg/dmsg/noise/dh_pool_js.go c1-net-dmsg
package noise

// Pool sizing for the browser.
//
// The native sizing (512 keypairs, 4 generators) is wrong here in both terms.
// secp256k1 key generation measures 444us/op under js/wasm against 144us
// native, so filling 512 slots is ~227ms of CPU — and js/wasm is
// GOMAXPROCS=1, so those four generator goroutines do not fill the pool in
// parallel, they take turns with the boot path on the one thread available.
// The pool is a package-level var, so this begins the moment the module
// initializes, competing with everything the visor does to come up.
//
// A browser tab establishes one to three dmsg sessions, not hundreds. Eight
// keypairs is ~3.6ms of work and still covers a reconnect burst; one
// generator refills behind demand without contending for the thread. The
// channel is unchanged, so a consumer that outruns the pool simply blocks
// until the generator catches up, exactly as before.
const (
	keypairPoolSize   = 8
	keypairGenerators = 1
)
