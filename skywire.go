// Package main skywire.go
/*
skywire + skycoin
*/
package main

import (
	skycoinweb "github.com/skycoin/skycoin/cmd/skycoin-web/commands"

	skycoin "github.com/skycoin/skywire/cmd/skycoin/commands"
	"github.com/skycoin/skywire/cmd/skywire/commands"
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
	// The help screen, over a still frame of the Matrix code rain. Here rather
	// than in InitFlags because InitFlags runs for every binary in this
	// repository, including the services, and this is for the one people read
	// help in. Off for --json/--jq/--shape, for `help -r` and `help -d`, for a
	// pipe or a redirect, and for NO_COLOR or SKYWIRE_NO_HELP_RAIN.
	flags.InitRain(commands.RootCmd)
	// Use/Short/Version and the subcommand tree are set by the package itself
	// (cmd/skycoin/commands), which assembles skycoin's commands on skywire's
	// side rather than importing skycoin's own assembly.
	commands.RootCmd.AddCommand(
		skycoin.RootCmd,
	)
	// Augment the imported `skycoin web` help with skywire-mesh usage
	// (resolving proxy via ENV + dmsg/skynet node URLs). Done here,
	// after the fact of importing skycoin-web, so the vendored command
	// stays transport-agnostic upstream.
	skycoinweb.RootCmd.Long += skywireSkycoinWebHelp
}

func main() {
	commands.Execute()
}
