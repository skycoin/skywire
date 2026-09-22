// Package cliwisp cmd/skywire-cli/commands/wisp/serve.go c4-vis-cli
//
// `skywire cli wisp serve` — provide a Wisp backend, carrying every stream it
// is asked for over skywire.
//
// LinuxOnTab (linuxontab.com) reads its backend from a query parameter, so no
// change to the page is needed:
//
//	skywire cli wisp serve
//	# then open https://next.linuxontab.com/?wisp=ws://127.0.0.1:6001/wisp
package cliwisp

import (
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/wisp"
)

var (
	wispAddr    string
	wispPath    string
	wispSocks   string
	wispDirect  bool
	wispBuffer  uint32
	wispTimeout int
	wispTLSCert string
	wispTLSKey  string
)

func init() {
	serveCmd.Flags().StringVarP(&wispAddr, "addr", "a", "127.0.0.1:6001", "address to serve on")
	serveCmd.Flags().StringVarP(&wispPath, "path", "p", "/wisp", "websocket path")
	serveCmd.Flags().StringVarP(&wispSocks, "socks", "s", "127.0.0.1:1080", "SOCKS5 proxy to carry streams over — the local skysocks-client by default")
	serveCmd.Flags().BoolVar(&wispDirect, "direct", false, "dial the host's own network instead of --socks; nothing is carried over skywire")
	serveCmd.Flags().Uint32VarP(&wispBuffer, "buffer", "b", wisp.DefaultBuffer, "per-stream client→server buffer, in packets")
	serveCmd.Flags().IntVar(&wispTimeout, "dial-timeout", 30, "dial timeout in seconds")
	serveCmd.Flags().StringVar(&wispTLSCert, "tls-cert", "", "TLS certificate — serve wss:// instead of ws://")
	serveCmd.Flags().StringVar(&wispTLSKey, "tls-key", "", "TLS key, with --tls-cert")
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve the Wisp protocol over skywire",
	Long: `Serve the Wisp protocol over WebSocket, carrying every stream it is
asked for over skywire via the local SOCKS5 proxy.

Both protocol versions are served. Which one is used is the client's choice: v2
clients open with a Sec-WebSocket-Protocol header and get an INFO exchange
advertising UDP support, v1 clients get the initial CONTINUE straight away.

UDP crosses an exit through SOCKS5 UDP ASSOCIATE, which skysocks relays over
the route, so a guest datagram reaches the same place its TCP does. Against a
proxy that has no association — an older exit, or some other CONNECT-only
SOCKS5 — port 53 still works through a DNS-over-TCP translation, which lets a
guest resolve names without running its own unbound, and every other port is
refused rather than quietly leaked to the clearnet. With --direct, real UDP
sockets are used and nothing is refused.

Examples:
  skywire cli wisp serve                             # ws://127.0.0.1:6001/wisp over skywire
  skywire cli wisp serve --addr 0.0.0.0:6001         # serve a LAN, e.g. from a NAS
  skywire cli wisp serve --direct                    # clearnet egress, for comparison
  skywire cli wisp serve --socks 127.0.0.1:9050      # some other SOCKS5 proxy

  # LinuxOnTab reads its backend from a query parameter:
  https://next.linuxontab.com/?wisp=ws://127.0.0.1:6001/wisp`,
	Run: func(cmd *cobra.Command, _ []string) {
		log := logging.MustGetLogger("wisp")

		var (
			egress wisp.Egress
			err    error
		)
		if wispDirect {
			egress = &wisp.DirectEgress{}
		} else {
			egress, err = wisp.NewSocksEgress(wispSocks)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
		}

		srv, err := wisp.NewServer(wisp.Config{
			Egress:      egress,
			Buffer:      wispBuffer,
			DialTimeout: time.Duration(wispTimeout) * time.Second,
			Log:         log,
		})
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		serveWisp(cmd, srv, egress)
	},
}
