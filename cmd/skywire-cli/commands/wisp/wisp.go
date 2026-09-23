// Package cliwisp cmd/skywire-cli/commands/wisp/wisp.go c4-vis-cli
//
// `skywire cli wisp` — both ends of the Wisp protocol.
//
// Wisp multiplexes TCP and UDP sockets over one WebSocket. Browser-based Linux
// emulators use it as the egress for a guest NIC: the page runs the guest's
// TCP/IP stack itself and asks its Wisp backend to open connections by name.
//
//	serve   provide a Wisp backend, carrying its streams over skywire
//	client  consume someone else's Wisp backend, as a local SOCKS5 proxy
//
// The two compose: `client --proxy` reaches a remote backend over a skywire
// route, so a Wisp endpoint published by one visor is usable from another
// without either being on the clearnet.
package cliwisp

import (
	"github.com/spf13/cobra"
)

func init() {
	RootCmd.AddCommand(
		serveCmd,
		clientCmd,
	)
}

// RootCmd is the `wisp` subcommand.
var RootCmd = &cobra.Command{
	Use:   "wisp",
	Short: "Serve or consume the Wisp protocol over skywire",
	Long: `Both ends of the Wisp protocol.

Wisp multiplexes many TCP and UDP sockets over a single WebSocket. It is what
browser Linux emulators use to give a guest kernel a network: the page runs the
guest's TCP/IP stack and asks its backend to open connections by name.

  serve    provide a Wisp backend, carrying its streams over skywire
  client   consume someone else's Wisp backend, as a local SOCKS5 proxy

The two compose. A visor serving wisp on a port it forwards over the mesh is
reachable by another visor's client via --proxy, so the WebSocket itself rides a
skywire route rather than the clearnet.`,
}
