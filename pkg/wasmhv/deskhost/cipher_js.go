//go:build js && wasm

// Package deskhost pkg/wasmhv/deskhost/cipher_js.go c3-vis-wasm
// The wallet-cipher role of the one skywire command module.
//
// The skycoin-web wallet reaches its cipher only through window.SkycoinCipher
// and window.SkycoinCipherExtras (cipher.provider.ts there), published by
// skycoin-lite/wasmcipher.Register — the same API skycoin's own skycoin-lite
// .wasm publishes, so a page cannot tell which module it came from. The wallet
// bundle instantiates assets/scripts/skycoin-lite.wasm itself, in every window
// it runs in; the servers (pkg/visor wallet_cipher.go, `skywire skycoin web`)
// answer that URL with THIS module and a loader whose `new Go()` runs
// `skywire desk-host --role cipher`, so the instance installs the globals and
// parks. It boots no visor, no dmsg, nothing else.
//
// The desk roles call this too (deskhost.go Run), so the desk page has the
// globals without a second instance — the wallet iframe still loads its own,
// being a separate realm.
package deskhost

import (
	wasmcipher "github.com/skycoin/skycoin/src/skycoin-lite/wasmcipher"
)

// installCipher publishes SkycoinCipher and SkycoinCipherExtras. It does not
// block; the caller parks in keepAlive().
func installCipher() {
	wasmcipher.Register()
}
