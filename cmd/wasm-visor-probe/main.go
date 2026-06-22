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
//	   pkg/transport/network  — the transport client layer (genericClient + dmsg
//	                            carrier). Raw-socket carriers (stcpr/sudph/quic/
//	                            stun/tcp-liveness) + the AR-resolved machinery are
//	                            //go:build !tinygo; ClientFactory.ARClient is `any`
//	                            and EB is a local interface so addrresolver/appevent
//	                            stay out of the graph. TCP tuning is build-tagged.
//
//	⛔ BLOCKED (blocker → fix):
//	   pkg/transport          net/http (TPD client) + net/rpc — net/http-free TPD
//	                          client (FetchOverDmsg) + tag/abstract the net/rpc bits
//	   pkg/router             net/http (routefinder) + net/rpc (cascade_source RSN)
//	   pkg/app/appnet         net/http + net/rpc
//	   pkg/app/appevent       net/rpc — app↔visor event channel
//	   pkg/visor              + os/exec (the app-SUBPROCESS model — needs in-process apps)
//
// See docs/design/wasm-visor-p2p.md §7 for the phased plan.
package main

import (
	"fmt"

	_ "github.com/skycoin/skywire/pkg/routing"
	_ "github.com/skycoin/skywire/pkg/transport/network"
	_ "github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func main() { fmt.Println("wasm-visor-probe: portable visor subset compiled") }
