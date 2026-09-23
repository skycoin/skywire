//go:build js

// Package cliwisp cmd/skywire-cli/commands/wisp/serve_js.go c4-vis-cli
//
// Serving Wisp from inside a tab, where the WebSocket half of this command
// cannot exist: websocket.Accept is //go:build !js, and a service worker
// cannot intercept ws:// even if it did. So the session is framed over a
// virtual-loopback conn instead, which page JS reaches with vnet.dial(port).
//
// --path, --tls-cert and --tls-key have no meaning here and are ignored
// rather than made errors: the same command line should work in both places,
// and refusing a flag that is merely irrelevant would break the websh session
// that pasted it from a native example.
package cliwisp

import (
	"fmt"
	"strings"

	"github.com/0magnet/bottle/vnet"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/wisp"
)

// serveWisp binds the virtual-loopback port and blocks, running one session
// per accepted conn.
func serveWisp(cmd *cobra.Command, srv *wisp.Server, egress wisp.Egress) {
	lis, err := vnet.Listen("tcp", wispAddr)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "serving wisp on vnet %s (framed, not websocket)\n", lis.Addr())
	fmt.Fprintf(&b, "  egress: %s\n", egress.Describe())
	fmt.Fprintf(&b, "  buffer: %d packets per stream\n", wispBuffer)
	internal.PrintOutput(cmd.Flags(), b.String(), b.String())

	ctx := cmd.Context()
	for {
		conn, err := lis.Accept()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
			return
		}
		go srv.ServeConn(ctx, conn)
	}
}
