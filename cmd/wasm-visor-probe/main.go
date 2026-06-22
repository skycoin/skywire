//go:build js && wasm

// Command wasm-visor-probe measures how much of the visor stack compiles under
// TinyGo for the browser. It is a BUILD-ONLY frontier probe — not a runnable
// visor. `tinygo build -target wasm ./cmd/wasm-visor-probe` succeeding means the
// imported packages are TinyGo/browser-portable; add packages as they are ported
// and the frontier moves.
//
// Frontier as of this commit (measured via `go list -deps -tags tinygo`):
//
//	✅ COMPILES TODAY (imported below):
//	   pkg/routing            — routing rules/types, pure
//	   pkg/visor/visorconfig  — config types + keyring
//
//	⛔ BLOCKED (blocker → fix):
//	   pkg/router             quic-go  → split raw-socket networks (stcp/sudph/quic) behind tags
//	                          net/http → net/http-free routefinder client (dmsgclient.FetchOverDmsg pattern)
//	                          net/rpc  → cascade_source.go RSN oracle (gob-RPC like wasmhv, or tag out)
//	   pkg/transport          quic-go + net/http (TPD client) + net/rpc
//	   pkg/transport/network  quic-go (quic.go/sudph.go/stcpr.go) — keep dmsg.go only for browser
//	   pkg/app/appnet         quic-go + net/http + net/rpc
//	   pkg/app/appevent       net/rpc — app↔visor event channel
//	   pkg/visor              + os/exec (the app-SUBPROCESS model — needs in-process apps)
//
// See docs/design/wasm-visor-p2p.md §7 for the phased plan.
package main

import (
	"fmt"

	_ "github.com/skycoin/skywire/pkg/routing"
	_ "github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func main() { fmt.Println("wasm-visor-probe: portable visor subset compiled") }
