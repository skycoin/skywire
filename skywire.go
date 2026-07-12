// Package main skywire.go
/*
skywire + skycoin
*/
package main

import (
	skycoin "github.com/skycoin/skycoin/cmd/skycoin-wallet/commands"
	skycoinweb "github.com/skycoin/skycoin/cmd/skycoin-web/commands"

	"github.com/skycoin/skywire/cmd/skywire/commands"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/flags"
)

// skywireSkycoinWebHelp is appended to the vendored `skycoin web` command's
// Long description when it is imported into skywire. It documents the
// skywire-specific way to reach fibercoin nodes over the mesh: point the
// thin client at a node's dmsg address and let the visor's embedded
// resolving SOCKS5 proxy resolve it (remote DNS), instead of a clearnet URL.
const skywireSkycoinWebHelp = `
Reaching nodes over the skywire mesh
------------------------------------
Enable the visor's embedded resolving proxy (config-gen --dmsgweb, listens on
127.0.0.1:4445; --skynetweb adds .skynet on :4446 and auto-chains) and point
the wallet at it, then use a mesh node URL instead of a clearnet one:

    HTTP_PROXY=socks5://127.0.0.1:4445 HTTPS_PROXY=socks5://127.0.0.1:4445 \
      skywire skycoin web --node-url http://sky.theskywirenetwork.net.<visor-pk>.dmsg

or equivalently pass --socks5-proxy socks5://127.0.0.1:4445. The proxy resolves
.dmsg / .skynet hostnames remotely, so no clearnet DNS or exit is used.

Nodes advertised over the mesh are discoverable as type=coin services in the
service discovery; the wasm-visor wallet auto-addresses them as <pk>:<port>.
Public thin-client nodes:

    sky.theskywirenetwork.net.<visor-pk>.dmsg   skycoin (over the mesh)
    https://sky.theskywirenetwork.net           skycoin (clearnet mirror)
    https://node.skycoin.com                    skycoin (upstream clearnet)

Node URLs are also runtime-reconfigurable from the wallet UI (Settings -> Nodes).
config-gen sets the default node list via SKYCOINWEBNODES.`

func init() {
	flags.InitFlags(commands.RootCmd, true)
	commands.RootCmd.AddCommand(
		skycoin.RootCmd,
	)
	skycoin.RootCmd.Use = "skycoin"
	skycoin.RootCmd.Short = "skycoin daemon & cli"
	if v := buildinfo.DepVersion("github.com/skycoin/skycoin"); v != "" {
		skycoin.RootCmd.Version = v
	}
	// Augment the imported `skycoin web` help with skywire-mesh usage
	// (resolving proxy via ENV + dmsg/skynet node URLs). Done here,
	// after the fact of importing skycoin-web, so the vendored command
	// stays transport-agnostic upstream.
	skycoinweb.RootCmd.Long += skywireSkycoinWebHelp
}

func main() {
	commands.Execute()
}
